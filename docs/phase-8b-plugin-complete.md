# Phase 8b — Cursor CPA plugin executor complete

The Cursor CPA plugin now implements every executor ABI method it
advertises in `plugin.register`. This document summarises what
landed and what is intentionally left for a follow-up.

## Implemented methods

| ABI method              | Behaviour                                                                                                                  |
|-------------------------|-----------------------------------------------------------------------------------------------------------------------------|
| `plugin.register`       | Advertises `executor_input_formats` and `executor_output_formats` = `["openai", "claude"]`, plus every existing capability. |
| `plugin.reconfigure`    | Same as `plugin.register` (no plugin-scoped config today).                                                                  |
| `plugin.shutdown`       | Empty OK envelope.                                                                                                          |
| `auth.identifier`       | `"cursor"`.                                                                                                                 |
| `auth.parse`            | Unchanged; parses on-disk `AuthFile` into `AuthData`.                                                                       |
| `auth.refresh`          | Unchanged passthrough; bumps `LastRefresh`.                                                                                 |
| `executor.identifier`   | `"cursor"`.                                                                                                                 |
| `executor.execute`      | Parses OpenAI/Claude request, rebuilds `*auth.Account`, invokes `Client.RunChat`, returns a full response body.             |
| `executor.execute_stream` | Same setup, then streams SSE frames back through `host.stream.emit` / `host.stream.close`.                                |
| `executor.count_tokens` | Local heuristic (`len(ascii)/4 + len(cjk)/1.5`); returns an OpenAI-shaped `usage` block.                                    |
| `model.static` / `model.register` / `model.for_auth` | Static list of `knownCursorModels`.                                                            |

## Package layout

The plugin's logic moved into `plugin/cursor/kernel/`. `plugin/cursor/main.go`
is now a thin cgo shim that:

1. Exposes the four ABI symbols (`cliproxy_plugin_init`,
   `cliproxyPluginCall`, `cliproxyPluginFree`,
   `cliproxyPluginShutdown`).
2. Stores the host API pointer and forwards the `host.stream.emit` /
   `host.stream.close` callbacks to `kernel.callHost`.
3. Delegates every plugin ABI call to `kernel.Dispatch`.

Splitting the code lets tests and the E2E harness drive the same
dispatch surface without going through cgo. A Go binary that
`dlopen`s another Go binary is a known deadlock/crash pattern (two
Go runtimes fighting for a shared address space) — the split
avoids that entirely.

## Streaming shape

The plugin returns from `executor.execute_stream` synchronously with
an empty `chunks` slice (`rpcExecutorStreamResponse.Chunks: nil`) —
that instructs CPA's host to read chunks off the stream bridge.
The plugin then, in a background goroutine, emits every SSE frame
through `host.stream.emit` and closes the stream with
`host.stream.close`. `host.stream.close` is always called on every
exit path (defer), even if `RunChat` fails mid-stream. Errors
propagate through the `error` field on the close request so CPA's
host translates them into a channel error on the caller's side.

The wire shape of each emitted chunk is the standard OpenAI Chat
Completion SSE frame (or Anthropic Messages SSE frame when the
`Format` field is `"claude"`), matching what `cmd/cursor-proxy`
serves over plain HTTP.

## Testing

`plugin/cursor/kernel/executor_test.go` exercises:

- `executor.execute` for both `openai` and `claude` formats using a
  scripted `chatRunner` that emits fake `AgentServerMessage` events.
- `executor.execute_stream` with a fake host emitter that captures
  every chunk; asserts the SSE terminator is `data: [DONE]` and the
  usage frame carries token counts.
- `executor.count_tokens` on a small chat payload, plus a targeted
  CJK-branch check for `isCJK`.
- Parser edge cases (`no user message`, Claude array-form content).

`cmd/plugin-e2e/main.go` is a live end-to-end harness. It reads a
real Cursor account from the IDE's SQLite storage, marshals it into
the CPA on-disk format, calls `kernel.Dispatch("plugin.register",
…)` and `kernel.Dispatch("executor.execute_stream", …)`, and prints
every chunk that flows through `host.stream.emit`. Sample output is
in `phase-8b-verify.log`.

## Known limitations / follow-ups

- **`max_tokens` / `temperature`**: the plugin ignores these fields.
  Cursor's protocol does not accept a raw `max_tokens` — the
  effective limit is fixed per model. When we grow tighter control
  over generation we'll route those through `AgentRunRequest`.
- **`tool_result` forwarding**: when the caller sends a
  `tool_result` message (Anthropic) or a `tool` role message
  (OpenAI), the parser drops it. Full multi-round tool loops require
  `BidiAppend` — see `docs/phase-7a-mcp.md`.
- **Multi-round tool loops in general**: today `AutoStopOnToolCall`
  fires as soon as the model calls a tool. Completing the loop
  ("model calls tool → caller returns result → model continues")
  needs a plugin-side session that pairs one `executor.execute_stream`
  call with subsequent `BidiAppend` posts. That would probably live
  behind a plugin config flag.
- **Real `auth.refresh`**: currently a passthrough. Cursor's OAuth
  refresh endpoint is not wired into cursor-proto yet.
- **`auth.login.start` / `auth.login.poll`**: still stubbed. Users
  must run `cmd/cursor-login` + `cmd/cursor-to-cpa` to bootstrap.
- **Live model discovery**: `model.for_auth` returns the static list.
  Once we cache a client per auth we can lazily call `Client.ListModels`
  to produce per-account model lists (matches Gemini/Claude plugins).
- **Token counting accuracy**: the char heuristic is intentionally
  fast and coarse. Users needing tight accuracy should not rely on
  the plugin's estimate. A tiktoken port would tighten this at the
  cost of a new dependency.
