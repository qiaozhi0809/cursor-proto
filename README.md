# cursor-proto

A Go client + reverse-engineered protocol library for Cursor 3.10.x IDE, plus
an OpenAI- and Anthropic-compatible HTTP proxy so any existing OpenAI/Anthropic
client can talk to Cursor's backend.

Built by rebuilding Cursor 3.10.20's private wire protocol from the shipped
`workbench.desktop.main.js` (39 MB) — proto schemas, checksum algorithm,
machine-id derivation, session identifiers, and header set.

## Status

| Phase | Content | Status |
|---|---|---|
| 1 | Cursor 3.10 proto schema extraction | ✅ 823 messages + 52 enums |
| 2 | IDE traffic capture / header baseline | ✅ 75 real requests, 25 required headers |
| 3 | Go proto codegen | ✅ `gen/cursor/cursor.pb.go` (2.5 MB) |
| 4 | Auth module (checksum + machine id + OAuth) | ✅ macMachineID byte-perfect vs IDE |
| 5 | Executor (chat / agent RunSSE + BidiAppend) | ✅ end-to-end AI reply |
| 6 | Translator (OpenAI + Anthropic SSE) | ✅ HTTP proxy live |
| 7 | Integration / packaging | 🚧 in progress |

## Repository layout

```
cursor-proto/
├── auth/            Checksum, machine-id, OAuth device flow, account JSON
├── executor/        HTTP client, RunSSE + BidiAppend, header assembly
├── translator/      Cursor events → OpenAI / Anthropic wire format
├── proto/           cursor.proto (source of gen/*)
├── gen/cursor/      Generated Go protobuf types (cursor package = cursorpb)
├── cmd/
│   ├── cursor-proxy/   OpenAI + Anthropic HTTP endpoint backed by Cursor
│   ├── cursor-login/   Interactive OAuth CLI, writes account JSON
│   ├── test-chat/      End-to-end RunSSE dumper
│   ├── test-connect/   Unary AvailableModels verifier
│   ├── test-features/  SystemPrompt / PureMode / AutoStop toggles
│   └── test-*          Various diagnostic tools
├── docs/            Reverse-engineering reports (schema-3.10, checksum, pitfalls)
├── captures/        raw schema JSON, mitmproxy traffic samples
├── reference/       Original JS sources (CursorGateway) used during RE
└── scripts/         Python extractors: extract_schema.py, gen_proto.py
```

## Prerequisites

- Go 1.24+
- Cursor 3.10.20 installed and signed in (the tools read your access token from
  Cursor's SQLite storage: `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb`)
- macOS (Linux + Windows machine-id support is scaffolded but untested)
- CGO enabled (for `mattn/go-sqlite3` — the SQLite reader used by `test-connect`)

## Quick start

```bash
# 1. Build the OpenAI/Anthropic bridge
CGO_ENABLED=1 go build -o cursor-proxy ./cmd/cursor-proxy

# 2. Start it (reads your Cursor IDE token automatically on macOS)
./cursor-proxy -addr 127.0.0.1:8317

# With API key protection
CURSOR_PROXY_API_KEYS=sk-mypersonalkey ./cursor-proxy -addr 127.0.0.1:8317
curl -H "Authorization: Bearer sk-mypersonalkey" http://127.0.0.1:8317/v1/models
# (equivalent flag form: ./cursor-proxy -addr 127.0.0.1:8317 -api-keys sk-mypersonalkey,sk-second)

# 3. Talk to it with any OpenAI client
curl -N http://127.0.0.1:8317/v1/chat/completions \
  -H "content-type: application/json" \
  -d '{
    "model":"composer-2.5",
    "messages":[
      {"role":"system","content":"Reply in pirate speak."},
      {"role":"user","content":"say hello"}
    ],
    "stream":true
  }'

# Or with any Anthropic-compatible client
curl -N http://127.0.0.1:8317/v1/messages \
  -H "content-type: application/json" \
  -d '{
    "model":"composer-2.5",
    "system":"Reply in pirate speak.",
    "messages":[{"role":"user","content":"say hello"}],
    "stream":true,
    "max_tokens":100
  }'
```

Endpoints exposed by `cursor-proxy`:

- `GET  /v1/models` — full Cursor model catalog
- `POST /v1/chat/completions` — OpenAI Chat Completion (streaming + non-streaming)
- `POST /v1/messages` — Anthropic Messages (streaming + non-streaming)

## Which models work?

Cursor gates model access by region. As of 2026-07-10 (China Mainland IP):

| Model family | Works? |
|---|---|
| `composer-2.5`, `composer-2.5-fast` | ✅ |
| `grok-4.5-*` | ✅ |
| `gpt-5.5-*` | ⚠️ mixed |
| `default` | ✅ (uses your default in Cursor settings) |
| `claude-opus-4-8-*`, `claude-fable-5-*` | ❌ "This model provider is not supported in your region" |

`GET /v1/models` returns all 156 models the server advertises, but only a
subset are actually callable from any given IP.

## Non-obvious findings

Documented in `docs/`:

- **checksum-algorithm.md** — JS's 32-bit shift oddities recovered, the
  timestamp is snapshot-once-per-session, `machineID = SHA-256(IOPlatformUUID)`,
  `macMachineID = SHA-256(first MAC address)`.
- **phase-2-report.md** — full list of the 25 required headers, incl. the
  four that CursorGateway missed (`x-client-key`, `x-cursor-config-version`,
  `x-cursor-client-layout: unifiedAgent`, `x-new-onboarding-completed`).
- **phase-5-report.md** — RunSSE lives on api2 not api3; the "AgentClientMessage"
  envelope is a bare `{ field 1: AgentRunRequest }`; `ConversationState.mode`
  is required.
- **phase-6-report.md** — `custom_system_prompt` field is server-rejected
  (regardless of harness); assistant final text is in a KV blob rather than
  `text_delta`; the auto-stop heuristic needs to wait for the assistant blob.

## Tools

| Binary | What it does |
|---|---|
| `cursor-proxy` | The HTTP bridge (OpenAI + Anthropic) |
| `cursor-login` | Runs the OAuth device flow, writes an account JSON file |
| `test-chat` | Streams a single chat and dumps every server event |
| `test-connect` | Sanity-checks `/v1/models` end-to-end |
| `test-features` | Toggle SystemPrompt / PureMode / Harness / AutoStop |
| `test-kv` | Dumps KV blob contents for reverse engineering |
| `test-sniff` | Prints blob head bytes for the auto-stop heuristic |

## Regenerating the proto

If Cursor ships a new release:

```bash
# 1. Point at the new workbench.desktop.main.js
cp "/Applications/Cursor.app/Contents/Resources/app/out/vs/workbench/workbench.desktop.main.js" \
   captures/wb-latest.js

# 2. Extract schema
python3 scripts/extract_schema.py > captures/schema-latest.raw.json

# 3. Regenerate .proto (core mode = chat + agent essentials)
python3 scripts/gen_proto.py --mode core

# 4. protoc → Go
protoc --proto_path=proto --go_out=gen --go_opt=paths=source_relative proto/cursor.proto

# 5. Bump the release hash constant if it changed
grep -oE '[a-f0-9]{64}' /Applications/Cursor.app/Contents/Resources/app/product.json  # or via IDE main.log update URL
# then update auth.KnownReleaseHash_3_10_20
```

## Docker

Prebuilt image workflow — see `Dockerfile` and `docker-compose.yml`.

```bash
# 1. Put an account JSON where compose can see it.
#    Generate one with `cursor-login` (writes cursor-<email>.json), then:
mkdir -p accounts
cp cursor-you@example.com.json accounts/current.json

# 2. Start the proxy (localhost-only bind by default)
docker compose up -d --build

# 3. Smoke-test
curl http://127.0.0.1:8317/v1/models
```

Environment variables:

- `CURSOR_PROXY_API_KEYS` — comma-separated allowlist for the proxy's future
  `-api-keys` gate (empty = unauthenticated; do not leave empty on a public
  network).
- `CURSOR_PROXY_ACCOUNT_FILE` — absolute path inside the container to the
  account JSON. `docker-compose.yml` mounts `./accounts` at `/data/accounts:ro`
  and defaults this to `/data/accounts/current.json`.

The compose file binds the port to `127.0.0.1:8317` by default. To expose it
on all interfaces (only behind a trusted network), change `ports:` to
`"8317:8317"`.

Prebuilt binaries are published from tag pushes (`v*.*.*`) via
`.github/workflows/release.yml`:

- `linux/amd64`, `linux/arm64`, `darwin/arm64`

The release workflow does not push a Docker image yet — that is a future
improvement.

## Warning

This is reverse-engineered code. Cursor's Terms of Service explicitly prohibit
programmatic access outside their sanctioned CLI. Use only for personal
research, keep the repository private, and expect the protocol to shift under
you — bumping to a new Cursor release usually requires updating headers,
release hash, and proto schema (see the last section).

## License

Not yet decided. Everything under `reference/` retains its upstream license
(CursorGateway's MIT). Everything else is currently unlicensed and private.
