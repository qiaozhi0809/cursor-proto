# Phase 7n — Local Prompt-Cache Simulator

## Goal

Cursor's real `cache_read_tokens` counter is noisy. Six identical requests
we measured returned four different values (4675 / 4675 / 4240 / 6976 /
4675 / 4675). The server does cache — but only its own internal system
prompt, and it reports opaque, non-monotonic numbers.

Dashboards like CPA and new-api treat `cached_tokens` the way Anthropic
defines it: monotonic, stable across identical prefixes, growing when the
client resends the same content. Phase 7n adds an in-process simulator
so the proxy can present those semantics locally regardless of what
Cursor reports.

## Design

### The stable prefix

For each incoming request the proxy computes a "stable prefix" that is the
same for any two calls that share history:

- All `system` messages, concatenated and trimmed.
- Every message before the *last* user turn, in order.

The last user turn is the fresh input for the current call and is
deliberately excluded — it changes every request, so including it would
guarantee a miss and defeat the whole exercise.

### The store

`executor/simcache/store.go` exposes a small LRU-with-TTL cache:

```go
store := simcache.New(10*time.Minute, 1000)
hit, cachedTokens, ent := store.LookupOrRecord(prefix)
```

- Keyed by `sha256(prefix)`.
- Bounded by `max` entries (default 1000) and per-entry `ttl` (default 10
  minutes — matches Anthropic's ephemeral prompt-cache TTL).
- Thread-safe (single mutex; contention is a non-issue at proxy load).

Semantics:

| State | Return |
| --- | --- |
| First time we see prefix | `hit=false, cachedTokens=0, ent.TokenCount>0` |
| Second call within TTL | `hit=true, cachedTokens=entry.TokenCount, HitCount++` |
| Same prefix past TTL | Entry is evicted; call is treated as a fresh miss |

### Token count estimator

Tokenizer dependencies would balloon the binary and drift from Cursor's
own counting, so we use a rough heuristic:

```go
func estTokens(s string) int {
    ascii, nonAscii := 0, 0
    for _, r := range s {
        if r < 0x80 { ascii++ } else { nonAscii++ }
    }
    return ascii/4 + int(float64(nonAscii)/1.5)
}
```

Empirically within ~10% of Cursor's own token count for English text and
~20% for mixed English/CJK, which is well inside dashboard tolerance.
Exposed as `simcache.EstTokens` for callers that want to log the same
number.

### Wiring into responses

Both OpenAI (`/v1/chat/completions`) and Anthropic (`/v1/messages`)
handlers, streaming and non-streaming, do the same three-step flow:

1. Compute the stable prefix from `system` + history.
2. Call `store.LookupOrRecord(prefix)` — this is a decision object we
   consult later.
3. Run the Cursor call as usual, then rewrite the `Usage` before it
   reaches the OpenAI / Anthropic translator:
   - On a **hit**: `cache_read_tokens = max(real, simulated)`.
   - On a **miss** (Anthropic only): `cache_write_tokens = simulated`
     so the Anthropic-shaped `cache_creation_input_tokens` lifecycle is
     visible.
4. Set `x-cursor-cache-source` response header.

The Usage rewrite happens on the `EventTurnEnded` event, before the
translator serialises it — that way the same code path handles OpenAI's
`prompt_tokens_details.cached_tokens` and Anthropic's
`cache_read_input_tokens` / `cache_creation_input_tokens` without any
per-schema branching.

### `x-cursor-cache-source` header

Three-state, meaningful to dashboards trying to differentiate our
synthesised numbers from Cursor's raw output:

| Value | Meaning |
| --- | --- |
| `real` | Simulator disabled, or a miss — `cached_tokens` is Cursor's raw number |
| `simulated` | Hit — we overrode `cached_tokens`; Cursor's own cache_read was 0 |
| `mixed` | Hit — we overrode `cached_tokens`, AND Cursor's real cache_read was > 0 |

Non-streaming responses buffer their body before flushing, so they can
resolve the three-state after seeing Cursor's real `cache_read`.
Streaming responses commit the header before the stream starts and only
emit `real` (miss) or `simulated` (hit); the `mixed` label is not
available in that mode. This is acceptable — the discriminator between
`simulated` and `mixed` is informational, and the payload's `usage`
object still tells the full story.

## Configuration

All optional; defaults preserve backward compatibility.

| Flag | Env | Default | Notes |
| --- | --- | --- | --- |
| `-simulate-cache` | `CURSOR_PROXY_SIMULATE_CACHE` | `true` | Env value must be a Go-parseable bool; unparseable values fall back to the flag default. |
| `-cache-ttl` | — | `10m` | Any Go `time.ParseDuration` string. |
| `-cache-size` | — | `1000` | Max entries in the LRU. |

When `-simulate-cache=false` (or the env sets it to false), the store is
never constructed and the proxy behaves exactly as it did before Phase
7n: every response carries Cursor's raw `cache_read_tokens` and the
header is a constant `real`.

## Verification

See `phase-7n-verify.log`.
