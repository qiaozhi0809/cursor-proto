# Phase 7g — Usage Snapshot

## Goal

Expose Cursor's dashboard/usage data so downstream systems (CPA account list,
`/v1/usage` HTTP endpoint, Prometheus scrapers) can display spend, slow-pool
status, and short-window rate-limit info at a glance.

Cursor does NOT use Claude-Code-style "5h / 7d rolling windows". Its model:

1. **Billing cycles** (typically monthly) with a hard $ spend limit.
2. **Usage events** aggregatable by any date range.
3. **Slow pool** — when included quota is exceeded, requests get downgraded.
4. **Short-window rate limits** — separate from monthly quota, reset every few
   minutes/hours; `reset_at_ms` + `reset_days_remaining` fields track them.

## RPCs consumed

All on `https://api2.cursor.sh`, all unary, all identified as living on
`aiserver.v1.DashboardService` (confirmed by decompiling `workbench.desktop.main.js`
3.10.20 — the initial task brief said some were on `AiService`, but every
usage-related RPC is actually on `DashboardService`).

| RPC | Purpose | Team-scope? |
|---|---|---|
| `DashboardService/GetCurrentPeriodUsage` | current cycle spend/included/remaining/limit | optional team_id |
| `DashboardService/GetCurrentBillingCycle` | start/end epoch millis of current cycle | optional team_id |
| `DashboardService/GetAggregatedUsageEvents` | aggregate cost cents by any [start_date,end_date] window | team_id + optional user_id |
| `DashboardService/GetUsageBasedPremiumRequests` | premium-request flag | required team_id |
| `DashboardService/GetHardLimit` | hard $ cap | optional team_id |
| `DashboardService/GetUsageLimitStatusAndActiveGrants` | slow-pool state + `reset_at_ms` + `reset_days_remaining` + active credit grants | none |
| `DashboardService/GetMe` | account identity — email, country, sign-up type, created_at | optional team_id |

The Aggregated call is invoked three times in parallel with different windows
(24h, 7d, 30d) to fill `spend_24h_cents`, `spend_7d_cents`, `spend_30d_cents`.

## Wire format & code generation

The `gen/cursor/cursor.pb.go` file in this repo is scoped to core chat/agent
messages (823 messages, transitive closure from `CORE_ROOTS`) and does NOT
include the DashboardService message types. Rather than modify the generated
file, we hand-wrote `proto/cursor_usage.proto` with the exact field layouts
extracted from the JS bundle (see `scripts/extract_schema.py` for the
extraction primitives) and generated `usage/pb/cursor_usage.pb.go` under a
separate Go package (`usagepb`). Wire format matches Cursor 3.10.20.

## Snapshot JSON

Example output from `GET /v1/usage`:

```json
{
  "period_start": "2026-07-09T13:16:30Z",
  "period_end": "2026-08-09T13:16:30Z",
  "total_spend_cents": 79,
  "included_spend_cents": 79,
  "remaining_cents": 1921,
  "limit_cents": 2000,
  "spend_24h_cents": 79,
  "spend_7d_cents": 79,
  "spend_30d_cents": 79,
  "in_slow_pool": false,
  "hard_limit_cents": 0,
  "no_usage_based_allowed": true,
  "usage_based_premium_requests_enabled": false,
  "email": "user@example.com",
  "country": "CN",
  "created_at": "2026-07-09T13:12:29.439Z",
  "sign_up_type": "non-professional",
  "fetched": {
    "current_period_usage": true,
    "billing_cycle": true,
    "aggregated_24h": true,
    "aggregated_7d": true,
    "aggregated_30d": true,
    "slow_pool_status": true,
    "hard_limit": true,
    "premium_requests": true,
    "me": true
  }
}
```

Fields:

- **Money values** are cents (`int64`) so callers never see floating-point
  rounding. Convert with `usage.FormatCents()`.
- **`fetched`** flags which RPC groups succeeded, so a `0` value can be
  distinguished from "the endpoint was skipped or denied".
- **`errors`** (map) is present only when at least one RPC failed. Entries are
  keyed by group name (`hard_limit`, `me`, etc.) and hold the raw error text.
  Use `usage.IsPermissionDenied(err)` for classification.
- **`rate_limit_reset_at`** and **`rate_limit_reset_days_remaining`** appear
  when Cursor is enforcing a short-window rate limit (separate from the
  monthly quota). They live in `UsageLimitPolicyStatus.reset_at_ms` /
  `.reset_days_remaining` on the wire.
- **`last_rate_limit_error`** / **`last_rate_limit_title`** are RESERVED for
  callers that want to plug the last observed 429 title/detail into the
  snapshot before rendering. This package doesn't populate them; the surface
  is exposed so chat code can set them via `snap.LastRateLimitTitle = ...` on
  a shared Snapshot before the next fetch.

## Endpoints

```
GET /v1/usage             JSON Snapshot
GET /v1/usage/prometheus  Prometheus text-format metrics (gauge values)
```

Prom metrics emitted include:

```
cursor_usage_total_spend_cents
cursor_usage_included_spend_cents
cursor_usage_remaining_cents
cursor_usage_limit_cents
cursor_usage_hard_limit_cents
cursor_usage_spend_24h_cents
cursor_usage_spend_7d_cents
cursor_usage_spend_30d_cents
cursor_usage_in_slow_pool                      (0 or 1)
cursor_usage_no_usage_based_allowed            (0 or 1)
cursor_usage_premium_requests_enabled          (0 or 1)
cursor_usage_slowness_ms
cursor_usage_rate_limit_reset_days_remaining
cursor_usage_rate_limit_reset_at_seconds       (when reset is scheduled)
cursor_usage_period_start_seconds
cursor_usage_period_end_seconds
```

## How CPA can consume this

Two options:

1. **HTTP**: hit `GET /v1/usage` on the proxy for each Cursor account. The
   response is a stable JSON contract (snake_case field names, `_cents`
   suffixes for money).
2. **Go import**: `import "github.com/router-for-me/cursor-proto/usage"` and
   call `usage.New(exec).Fetch(ctx)` directly, sharing whatever `executor.Client`
   the caller has already wired up.

The `Country` field is the model-gating hint CPA needs: `"US"` accounts can
access the full model roster; `"CN"` accounts are typically restricted to
composer/grok/gpt.

## CLI

```
$ cursor-usage                         # loads Cursor IDE state.vscdb token
$ cursor-usage -account acc.json       # loads from account JSON
$ cursor-usage -format table           # human-readable
$ cursor-usage -timeout 30s
```

## Known blockers

None. All nine RPC groups succeed against a live personal-Pro account; see
`phase-7g-verify.log` for the recorded output.

Should a personal account ever get `permission_denied` on a team-scoped
endpoint (currently observed: none), that error is captured in `errors[...]`
and the rest of the snapshot is returned as usual.

## Files added

- `proto/cursor_usage.proto` — hand-crafted proto for 15 usage messages.
- `usage/pb/cursor_usage.pb.go` — generated bindings.
- `usage/client.go` — parallel fetcher.
- `usage/client_test.go` — happy-path + permission-denied fake-server tests.
- `cmd/cursor-usage/main.go` — CLI.
- `cmd/cursor-proxy/usage_handler.go` — HTTP handlers.
- `cmd/cursor-proxy/main.go` — 2 new `mux.HandleFunc` lines (conflict-friendly).
- `docs/phase-7g-usage.md` — this doc.
- `phase-7g-verify.log` — real curl output.
