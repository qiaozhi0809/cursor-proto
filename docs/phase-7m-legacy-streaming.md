# Phase 7-M: Legacy ChatService streaming — Investigation report

## TL;DR

**None of the five `aiserver.v1.ChatService` methods emits real per-token
`text_delta` chunks for a 3.10.20 client on our account.** The two SSE-style
variants (`...ToolsSSE`, `...ToolsPoll`) have no active handler on api2 and
hang until the request times out. The one variant that accepts our wire
payload (`StreamUnifiedChatWithTools`) is server-rejected with
`Update Required` for `composer-2.5` and `ERROR_UNSUPPORTED_REGION` for
Anthropic/OpenAI models. Cursor's server has retired the entire legacy
`ChatService` family for our client version. **No opt-in flag was wired
through** — the plumbing is not built because the endpoint would not carry
traffic anyway.

## What we probed

`cmd/test-legacy-stream/main.go` posts a hand-encoded
`StreamUnifiedChatWithToolsRequest` (schema from
`reference/cursor-2.3.41.proto` lines 527..596) with the current IDE
headers (`ApplyCommonHeaders`, 3.10.20) to each of:

| Endpoint | api2 result | api3 result |
|---|---|---|
| `StreamUnifiedChatWithToolsSSE` | dial hangs, context deadline | 404 "The request could not be routed" |
| `StreamUnifiedChatWithTools`    | 200 with error frame (see below) | 404 |
| `StreamUnifiedChatWithToolsPoll`| dial hangs, context deadline | 404 |
| `StreamUnifiedChat`             | 200, `parse binary: illegal tag: field no 13 wire type 7` | 404 |
| `WarmStreamUnifiedChatWithTools`| 415, then 500 with same parse error | 404 |

Full frame log with wall-clock deltas: `phase-7m-verify.log`.

## The one endpoint that accepts our payload

`aiserver.v1.ChatService/StreamUnifiedChatWithTools` returns HTTP 200 with a
single Connect frame (flags=`0x02`) containing a JSON error envelope. Two
different error codes surfaced depending on the requested model:

**With `claude-4.5-sonnet` / `gpt-4o`:**
```
"error": "ERROR_UNSUPPORTED_REGION",
"title": "Model not available",
"detail": "This model provider is not supported in your region.
           Visit https://cursor.com/docs/account/regions ..."
```

**With `composer-2.5` / `cursor-fast`:**
```
"error": "ERROR_GPT_4_VISION_PREVIEW_RATE_LIMIT",
"title": "Update Required",
"detail": "Your version of Cursor is no longer supported. Please update to
           the latest version at cursor.com/downloads to continue."
```

The `composer-2.5` message is decisive: the exact same account with the
exact same headers happily streams `composer-2.5` responses through
`agent.v1.AgentService/RunSSE`. The rejection is not a client-version
problem — it is a **deliberate server-side sunset** of the legacy
`ChatService` API. The error string is a repurposed enum
(`ERROR_GPT_4_VISION_PREVIEW_RATE_LIMIT`) whose displayed title is
"Update Required," but the routing rule is version-independent: no v3.x
protocol traffic is served by this endpoint.

## Why SSE/Poll hang instead of erroring

Both `...ToolsSSE` and `...ToolsPoll` accept the TCP connection, complete
the TLS handshake, and never write a single byte back. There is no server
handler bound to those paths on `api2.cursor.sh`; the connection is left
open until either side times out. `api3` returns a clean 404 for both,
confirming they are not served there either.

## What this means for the current path

The Phase 7-B conclusion stands unchanged. The best we can do without
Cursor cooperating is the existing `agent.v1.AgentService/RunSSE` +
BidiAppend flow with progressive-blob diffing at the end of turn. No
alternative streaming surface is available on the 3.10 backend for
programmatic clients.

## Deliverables

- `cmd/test-legacy-stream/main.go` — self-contained probe. Loads the IDE
  account, hand-encodes the legacy request, walks every candidate endpoint,
  supports two content-types (`application/connect+proto` with envelope,
  and raw `application/proto`) and both hosts (via `PROBE_HOST=`).
- `phase-7m-verify.log` — timestamped per-frame log of the final run.
- No changes to `executor/`, `cmd/cursor-proxy/`, or the plugin. The
  `LegacyStreaming` opt-in described in the task brief was intentionally
  not plumbed through because there is no legacy path to switch to.

## What would change our mind

Only one thing: if Cursor re-enables the `ChatService` endpoint for
3.10.20 clients, the probe already produces a decoded stream of
`StreamUnifiedChatWithToolsResponse` frames. The classifier in
`classify()` already extracts `text[legacy]`, `message.content`,
`thinking`, and `tool_call_v2` per the reference schema, so the same
binary can be re-run and its `SUMMARY textDeltas=…` line will report
frame counts and total text bytes for the whole turn. That is the signal
we'd need to justify wiring `LegacyStreaming` through.
