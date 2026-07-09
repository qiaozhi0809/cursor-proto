# Phase 7e — Docker & Release Packaging

Runbook for shipping `cursor-proxy` as a container image and a set of prebuilt
release binaries. Scope is intentionally narrow: no image push, no signing, no
Homebrew tap, no Windows.

## What ships

| Artefact | Where it comes from | Purpose |
|---|---|---|
| `Dockerfile` | multi-stage build, `golang:1.24-alpine` -> `alpine:3.20` | Local `docker compose up`, future registry publish |
| `.dockerignore` | this phase | Keeps captures/, docs/, reference/, test-* out of the build context |
| `docker-compose.yml` | this phase | Single-service definition, localhost-only bind |
| `.github/workflows/ci.yml` | this phase | gofmt + `go build ./...` + `go test ./...` on push/PR |
| `.github/workflows/release.yml` | this phase | Native-runner matrix build for `v*.*.*` tags |
| `-token-file` flag on `cursor-proxy` | additive change to `cmd/cursor-proxy/main.go` | Load account JSON in environments without Cursor.app (containers, CI) |

## Runbook

### Local build

```bash
# From the repo root
docker build -t cursor-proxy:local .
docker compose config    # validates docker-compose.yml
```

### Local run

```bash
# 1. Produce an account JSON on a machine that has Cursor.app + cursor-login.
go run ./cmd/cursor-login   # writes cursor-<email>.json to $HOME (default)

# 2. Drop it into ./accounts/current.json (the compose mount).
mkdir -p accounts
cp cursor-you@example.com.json accounts/current.json

# 3. Bring it up.
export CURSOR_PROXY_API_KEYS="sk-local-1"
docker compose up -d --build
curl -s http://127.0.0.1:8317/v1/models | head
```

### Cutting a release

```bash
git tag -a v0.1.0 -m "First tagged release"
git push origin v0.1.0
```

The workflow builds three artefacts (`linux-amd64`, `linux-arm64`,
`darwin-arm64`), then `softprops/action-gh-release` publishes them with
auto-generated notes. `contents: write` is granted to the workflow via the
`permissions:` block, so no PAT is needed as long as the maintainer keeps
"Read and write permissions" enabled in Settings -> Actions -> General.

### CI

`.github/workflows/ci.yml` runs on `ubuntu-latest`, installs
`libsqlite3-dev`, then runs `gofmt -l .`, `go build ./...`, `go test ./...`
with `CGO_ENABLED=1`. Module cache is cached via `actions/setup-go@v5`.

## Trust boundary notes

- **Account JSON is a bearer credential.** It contains the raw Cursor access
  token, refresh token, and derived machine identifiers. Anyone with the file
  can impersonate the user. Consequences:
  - The container mounts it read-only (`:ro`).
  - `.dockerignore` excludes `.env`, `*.token`, `accounts/`, `auths/`, so the
    file cannot accidentally end up baked into an image layer.
  - The default compose bind is `127.0.0.1:8317` — do not change it to
    `0.0.0.0` on an untrusted network until `CURSOR_PROXY_API_KEYS` is being
    enforced by every route (that gate is being added in a sibling branch).
- **Machine ID coupling.** The account JSON pins `machine_id` and
  `mac_machine_id`. Running the container on a different physical host does
  not re-derive them — Cursor's checksum stays consistent, which is why the
  proxy works in a container at all, but it also means a leaked JSON is
  usable anywhere.
- **Non-root runtime.** The runtime image runs as UID 1000; the account
  directory is `chown`ed to that UID. There is no writable state in the
  image; all runtime state (logs) goes to stdout.
- **CGO surface.** `mattn/go-sqlite3` is compiled into the binary. In the
  container we still `apk add sqlite-libs` for `libsqlite3.so` at runtime.
  The proxy does not open Cursor's on-disk SQLite when `-token-file` /
  `CURSOR_PROXY_ACCOUNT_FILE` is set — the SQLite path is only exercised on
  macOS hosts.
- **Release binaries are unsigned.** No notarisation for the darwin build,
  no `cosign` for the linux ones. Users run them at their own risk. Add
  signing before making the repo public.

## Known limitations / follow-ups

- Linux `arm64` builds run on `ubuntu-24.04-arm`. Public repos have this
  runner available for free; private repos need it enabled in the org's
  Actions runner group. If unavailable, drop `linux/arm64` from the matrix
  and note it in the release description.
- No `docker buildx` / registry push in `release.yml`. When a registry
  target is chosen (GHCR is the obvious pick, following CLIProxyAPI's
  precedent), add a `docker/build-push-action@v6` job gated on the same
  tag trigger.
- `CURSOR_PROXY_API_KEYS` is passed through but not yet enforced by every
  route — the gate is being added in `feat/api-auth`. Compose already sets
  the env var so the switchover is a no-op once merged.
