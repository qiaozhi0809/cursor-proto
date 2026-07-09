# Phase 7l — Prompt-Cache Passthrough

## Goal

Cursor's `TurnEnded` message on every response carries prompt-cache counters
(`cache_read_tokens`, `cache_write_tokens`, `reasoning_tokens`). We already
lift them into `translator.Usage` in `events.go`, but the OpenAI / Anthropic
writers were dropping them on the floor. Phase 7l wires them through so
downstream clients see the standard fields in both HTTP shapes.

## What Cursor gives us

Observed on `composer-2.5` (personal-Pro account, no client-side caching):

| Field | Typical value | Notes |
| --- | --- | --- |
| `input_tokens` | 13000ish | Total input, including cached reads |
| `output_tokens` | model-dependent | |
| `cache_read_tokens` | ~4675 (warms up to ~13k) | Cursor auto-caches its own system prompt |
| `cache_write_tokens` | 0 | Server does not accept client-directed caching |
| `reasoning_tokens` | 0 for non-thinking; non-zero for thinking models | |

Numbers verified live — see `phase-7l-verify.log`.

## Mapping

### OpenAI Chat Completion

Both streaming and non-streaming responses expose the same `usage` object:

```json
"usage": {
  "prompt_tokens": <Usage.InputTokens>,
  "completion_tokens": <Usage.OutputTokens>,
  "total_tokens": <Usage.InputTokens + Usage.OutputTokens>,
  "prompt_tokens_details": {
    "cached_tokens": <Usage.CacheReadTokens>
  },
  "completion_tokens_details": {
    "reasoning_tokens": <Usage.ReasoningTokens>
  }
}
```

- OpenAI's `prompt_tokens` is **total input including cached**, unlike
  Anthropic. `prompt_tokens_details.cached_tokens` is the sub-count. We
  preserve `prompt_tokens = Usage.InputTokens` as-is.
- The two `_details` objects are always emitted, with 0 defaults, so
  clients that key on them don't need presence checks.
- No `cache_write` equivalent in OpenAI's schema, so `CacheWriteTokens`
  is dropped here (it's always 0 anyway).

### OpenAI streaming — `stream_options.include_usage`

When the request body carries `stream_options: {"include_usage": true}`,
the writer emits an additional final chunk **before** `data: [DONE]`:

```
data: {"id":"chatcmpl-...","object":"chat.completion.chunk","choices":[],"usage":{...}}

data: [DONE]
```

- `choices` is `[]` on this frame (per OpenAI's contract for the usage
  chunk).
- The frame is suppressed when `include_usage` is false / absent — no
  backward-compat break.
- Implementation: `openaiChatRequest.StreamOptions` is parsed on the way
  in; the streaming handler sets `OpenAIStreamWriter.IncludeUsage=true`
  and calls `writer.FinalUsageFrame()` after the last content chunk.

### Anthropic Messages

Both streaming and non-streaming responses expose:

```json
"usage": {
  "input_tokens": <max(0, InputTokens - CacheReadTokens)>,
  "output_tokens": <Usage.OutputTokens>,
  "cache_read_input_tokens": <Usage.CacheReadTokens>,
  "cache_creation_input_tokens": <Usage.CacheWriteTokens>
}
```

- Anthropic's `input_tokens` is the **non-cached** portion of the input
  (their pricing model bills cached reads separately). Cursor gives us
  the pre-subtraction total, so we subtract `cache_read_tokens` before
  emitting. Clamps at 0.
- `cache_creation_input_tokens` maps to `CacheWriteTokens` and is always
  0 today (Cursor doesn't accept client-directed writes).
- Streaming: the `message_delta` event's `usage` object carries the same
  four fields — the reader only sees them on this final delta, which is
  the standard place per Anthropic's docs.

## Backward compatibility

- OpenAI streaming without `stream_options` → identical wire shape as
  before Phase 7l. No extra frames, no extra keys.
- OpenAI non-streaming previously emitted the three-key `usage`
  (prompt/completion/total). We now always add the two `_details`
  objects. Existing clients that ignore unknown keys are unaffected.
- Anthropic previously emitted `input_tokens` / `output_tokens` only, and
  passed `input_tokens` as the raw Cursor total. Phase 7l:
    - subtracts cache_read from `input_tokens` (matches Anthropic's real
      shape)
    - always adds `cache_read_input_tokens` and
      `cache_creation_input_tokens` (0 defaults)

  Clients that compared `input_tokens` between our proxy and native
  Anthropic previously saw a mismatch; they'll now see aligned values.

## Files touched

- `translator/openai.go` — added `IncludeUsage`, `LastUsage`,
  `FinalUsageFrame`; introduced `buildOpenAIUsage`; used it from both
  streaming and `NonStreamingAccumulator.Response`.
- `translator/anthropic.go` — introduced exported `BuildAnthropicUsage`;
  called it from `EventTurnEnded` streaming path.
- `translator/translator_test.go` — added 5 tests covering both
  non-streaming and streaming shapes across both providers, plus the
  `include_usage=false` opt-out.
- `cmd/cursor-proxy/main.go` — parsed `stream_options` on the OpenAI
  chat request; propagated `IncludeUsage` into the writer; called
  `FinalUsageFrame` before `[DONE]`; delegated Anthropic non-streaming
  usage to `translator.BuildAnthropicUsage`.

## Test evidence

`go test ./translator/... ./cmd/cursor-proxy/...` — green.
`phase-7l-verify.log` — real curl + response for all four scenarios.
