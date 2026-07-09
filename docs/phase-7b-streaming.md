# Phase 7-B: Real-time streaming — Investigation report

## Summary

Real per-token streaming from Cursor's backend is **not currently possible**
with the data the server actually emits. This report documents the empirical
findings and the pragmatic fallback we ship.

## What the server sends per turn

Measured on `composer-2.5` responding with a 300-word essay:

| Signal | Count | Timing | Content |
|---|---|---|---|
| `InteractionUpdate.text_delta` | ~48 | spread over 8s | sparse fragments: punctuation, partial tokens (`":"`, `"**"`, `","`, `"tag"`, …) |
| `InteractionUpdate.token_delta` | ~286 | continuous | just token counters, no text |
| `InteractionUpdate.heartbeat` | 3–5 | every ~2s | keepalive |
| `KvServerMessage.SetBlobArgs` with `{"role":"assistant"…}` | **1** | at end-of-turn | complete final response as one blob |
| `InteractionUpdate.turn_ended` | 1 | end-of-turn | with usage counts |

## Why per-token streaming isn't feasible

- `text_delta` fragments **do not form a clean prefix** of the final blob text
  (some are formatting metadata that ends up nested inside the response, not
  concatenated).
- Intermediate KV blobs during the turn are protobuf-wrapped internal state
  snapshots, not text.
- Only the terminal assistant-role JSON blob contains the complete answer.

Any client that tries to emit text as `text_delta` arrives will produce
misordered or duplicated output relative to the final blob.

## Fallback ("progressive final blob")

Our current implementation waits for the terminal JSON blob and then
diff-suffix-streams it once. This produces **one large content chunk** at
end-of-turn rather than incremental deltas.

Perceived latency is dominated by Cursor's own server-side generation time
(4–15 s on typical prompts), not by our proxy — so improving this requires
either:

1. Cursor server-side changes (out of our control).
2. Reverse-engineering a different endpoint that streams tokens
   (`StreamUnifiedChatWithToolsSSE` on aiserver.v1.ChatService looks promising
   — see docs/schema-3.10.md — but it's the older chat protocol, not the
   3.10 agent runtime).

## Recommendation

Keep the current blob-based streaming. Add a `first_byte_flush` optimisation:
send a heartbeat SSE frame ("thinking…") within 500ms of the request so
clients can render a loading UI. This is a **UX** improvement, not a protocol
one — real token streaming requires deeper reverse-engineering that is out
of scope for phase 7.
