# Phase 7f — Cursor plugin follow-up plan

This document tracks the remaining work needed to graduate the CPA
plugin (`plugin/cursor/`) from "register + parse" to a fully functional
executor that CPA can route real chat traffic through.

## Current state (2026-07-10)

- The plugin builds as a c-shared library (`cursor.dylib` on macOS,
  `cursor.so` on Linux) and exports all four ABI symbols the host
  looks for: `cliproxy_plugin_init`, `cliproxyPluginCall`,
  `cliproxyPluginFree`, `cliproxyPluginShutdown`.
- The following ABI methods work end-to-end (unit tests in
  `plugin/cursor/handlers_test.go`):
  - `plugin.register`, `plugin.reconfigure`, `plugin.shutdown`
  - `auth.identifier`, `auth.parse`
  - `auth.refresh` (passthrough — see below)
  - `executor.identifier`
  - `model.static`, `model.register`, `model.for_auth`
- The following ABI methods return `not_implemented` right now:
  - `executor.execute`, `executor.execute_stream`
  - `executor.count_tokens`
  - `auth.login.start`, `auth.login.poll`

## Missing piece #1 — executor.execute_stream

The plugin advertises `executor_input_formats: ["openai", "claude"]`
and `executor_output_formats: ["openai", "claude"]`. On the wire that
means CPA hands us:

- `ExecutorRequest.Format` == `"openai"` or `"claude"`
- `ExecutorRequest.Payload` == an OpenAI-shaped chat/completions body
  (or Anthropic-shaped messages body)
- `ExecutorRequest.Stream` == true for `execute_stream`

To close the loop we need to:

1. **Parse the incoming payload.** Reuse `translator/openai.go` and
   `translator/anthropic.go` from this repo — they already implement
   the request-to-Cursor-`ChatRequest` mapping (with `UserMessage`,
   `SystemPrompt`, `PureMode`, `AutoStopOnTurnEnd`).
2. **Rebuild an `auth.Account` from `ExecutorRequest.StorageJSON`.**
   Use `cpaformat.Unmarshal` + `AuthFile.ToAccount` + `Account.FillSessionDefaults`.
   The plugin also needs to cache one `*executor.Client` per Auth so
   the checksum + machine id + client key are stable across requests.
3. **Invoke `executor.Client.RunChat`.** That returns a
   `<-chan Event`. Each event needs to be mapped back to the caller's
   requested output format (OpenAI SSE frames or Anthropic
   `message_delta` blocks).
4. **Stream the frames back to the host** via the `host.stream.emit`
   callback (see `pluginabi.MethodHostStreamEmit` +
   `internal/pluginhost/stream_bridge.go` on the CPA side).
   The plugin cgo bridge in `plugin/cursor/main.go` already stores the
   host API pointer; wiring `callHost("host.stream.emit", …)` and
   `callHost("host.stream.close", …)` gives us the "one chunk at a
   time" behaviour CPA needs.
5. **Close the stream** with `host.stream.close` on `RunChat`'s exit,
   forwarding any error into the `Err` field of the terminal chunk.

### Estimated shape of the executor handler

```go
func handleExecuteStream(payload []byte) ([]byte, int) {
    // 1. Decode the ABI request struct (mirror of pluginapi.ExecutorRequest).
    // 2. Rebuild *auth.Account from req.StorageJSON.
    // 3. Get-or-create *executor.Client keyed by AuthID.
    // 4. Translate req.Payload -> executor.ChatRequest via the format hint.
    // 5. Start RunChat, spawn a goroutine that copies events -> host.stream.emit.
    // 6. Return a synchronous OK envelope + StreamID; the goroutine keeps
    //    running until RunChat closes its channel.
}
```

## Missing piece #2 — executor.count_tokens

Cursor's protocol does not expose an isolated "count tokens" endpoint;
the token count comes back in the streaming response. The two options
are:

- **Cheap fallback**: use `tiktoken-go` (already indirectly available
  via any OpenAI-compatible library the plugin can vendor) and count
  locally.
- **Passthrough**: proxy to `POST /v1/count-tokens` on the OpenAI-side
  wire format that CPA already speaks; CPA's `executor.count_tokens`
  contract allows returning any provider-shaped body.

Either is a small addition once #1 lands.

## Missing piece #3 — real auth.refresh

`handleAuthRefresh` currently bumps `LastRefresh` and returns the
existing tokens. Cursor's refresh flow lives in `auth/oauth.go`
(`RefreshAccessToken` is not implemented in cursor-proto yet either —
the login flow uses device polling only). Options:

- Implement `RefreshAccessToken(refreshToken string)` inside
  `auth/oauth.go`, hitting the same token endpoint the OAuth poll
  uses, and have `handleAuthRefresh` call it.
- Alternatively, mark `auth.refresh` as a no-op and rely on Cursor's
  long-lived access tokens (empirically they stay valid for weeks).
  In that case, adjust the plugin's `NextRefreshAfter` to something
  like 24h so CPA does not spin refresh calls.

## Missing piece #4 — auth.login.start / poll

Right now `cmd/cursor-login` is the only login path. CPA's management
UI exposes a "sign in" button that invokes `auth.login.start` +
`auth.login.poll` via the plugin. Wiring these up requires:

- Exporting the OAuth device-flow helpers from `auth/oauth.go` (they
  are internal to `cmd/cursor-login` at the moment).
- Returning the verification URL from `auth.login.start`, then polling
  Cursor's token endpoint from `auth.login.poll` until we receive an
  access token.
- On success, packaging the result into an `AuthData` identical to
  what `auth.parse` builds from a converted JSON.

## Missing piece #5 — model.for_auth vs. model.static

Static ships a hardcoded list (`knownCursorModels` in
`plugin/cursor/handlers.go`). Once `handleExecuteStream` has a working
`executor.Client` cache, `model.for_auth` can lazily call
`Client.ListModels` for the specific Auth and return that instead —
which matches how Gemini/Claude behave in CPA.

## Testing plan when D2 lands

1. Unit tests: extend `plugin/cursor/handlers_test.go` to cover
   `handleExecuteStream` with a fake host that captures
   `host.stream.emit` calls.
2. Integration test: run CPA locally with the plugin loaded, drop a
   converted `cursor-<email>.json` into its auth dir, and issue an
   OpenAI-shaped `/v1/chat/completions` against CPA. Confirm the
   stream tokens arrive in-order.
3. Confirm the plugin still builds on Linux + macOS with
   `CGO_ENABLED=1`.

## Non-goals for this follow-up

- Building any Windows-specific machine-id support (still scaffolded
  but untested in cursor-proto proper).
- Exposing Cursor's Agent / BidiAppend flow — the OpenAI/Anthropic
  translation only needs `RunChat`. Agent flows can be a later
  extension when CPA introduces first-class tool routing.
