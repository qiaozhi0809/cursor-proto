# Phase 8d — Cursor plugin pool status admin API

## Goal

Expose rich per-account status to CPA's admin panel through the plugin
ABI's `management.handle` method so operators can see the state of the
Cursor pool without shelling into each host.

## What shipped

- `plugin/cursor/status.go` — an `AccountStatus` struct that carries
  identity, token expiry, plan/quota, slow-pool state, rate limits,
  and a Country-derived model compatibility hint. Backed by an
  `authRegistry` singleton with a 30-second in-memory TTL so a full
  pool listing does not fan out `/GetMe` + `/GetCurrentPeriodUsage`
  on every browser refresh.
- `plugin/cursor/management.go` — the `management.register` and
  `management.handle` ABI wiring. Advertises five routes to CPA under
  `/v0/management/cli-proxy-api/cursor/…`:
  - `GET /accounts` — list every registered account (menu entry: "Cursor accounts")
  - `GET /account?email=<addr>` — one account's detailed status
  - `POST /account/refresh?email=<addr>` — force a JWT refresh
  - `POST /account/probe?email=<addr>` — bust cache and re-fetch
  - `GET /pool-summary` — aggregate view for the admin dashboard
- `plugin/cursor/main.go` — dispatch cases added for
  `management.register` and `management.handle`. `auth.parse` now also
  registers the parsed account with the pool-status registry so the
  admin panel sees accounts the moment CPA hands them to us. The
  executor.* case blocks are untouched (owned by the sibling worktree).
- `plugin/cursor/handlers.go` — `plugin.register` capabilities list now
  advertises `management_api: true`.
- `cmd/cursor-pool-summary/main.go` — a small CLI that speaks HTTP to
  a running CPA and prints the same summary in table form.
- `plugin/cursor/status_test.go` — unit tests: fake usage.Snapshot ⇒
  AccountStatus mapping, cache TTL/probe/invalidate behaviour, on-disk
  auth loader, and end-to-end dispatch through `management.handle`.

## Route shape choice

CPA's `pluginhost/management.go` route table rejects paths that
contain `:` or `*`, so path-parameter style (`/accounts/{email}`) is
not viable. The plugin therefore uses `?email=` query parameters
throughout. This matches CPA's own `/v0/management/config/*` style.

## Cache & data source

Each `AccountStatus` field beyond identity + token expiry comes from
`usage.Client.Fetch(ctx)`, which fans out six unary RPCs against
`api2.cursor.sh`. The fetch is expensive enough that we cache the
result for 30 seconds per account. Cache invalidation:

- `POST /account/probe` invalidates the cache and re-fetches synchronously.
- `POST /account/refresh` invalidates the cache after refreshing the JWT.
- `Register()` (called from `auth.parse` and `LoadFromDisk`) invalidates
  the cache for the affected email.

## Plan derivation

Cursor's `Snapshot` does not expose `stripe_subscription_status`
directly. `derivePlan()` uses this heuristic:

- `sign_up_type ∈ {business, team, enterprise}` → `Team`
- `usage_based_premium_requests_enabled` **or** `limit > 0` → `Pro`
- `current_period_usage` fetched with `limit == 0` → `Free`
- otherwise → `unknown`

## Claude compatibility

`CanCallClaude` uses an explicit country allowlist (see
`claudeCountryAllowlist` in `status.go`). Everyone else is downgraded
to composer + gemini + gpt in the `Models` hint. The list is
documented inline so operators can audit and edit it in one place.

## Verification

- `go test ./plugin/cursor/ ./...` — all packages pass.
- `CGO_ENABLED=1 go build -buildmode=c-shared -o cursor.dylib ./plugin/cursor` — builds cleanly.
- `go build -o cursor-pool-summary ./cmd/cursor-pool-summary` — builds cleanly.
- Curl demo of each admin endpoint (proxied `AccountStatus` fixture):
  `phase-8d-verify.log`.

## Non-goals for this pass

- `LastRequestAt`, `RequestCount`, `FailureCount`, `LastErrorCode`
  are exposed in the JSON shape but not filled in — that instrumentation
  belongs in the executor recording path (owned by the sibling worktree).
- The refresh endpoint currently goes through the existing
  `handleAuthRefresh` passthrough. When the real refresh path lands,
  this endpoint will benefit automatically.
