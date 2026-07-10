#!/usr/bin/env bash
# scripts/phase-8c-verify.sh
#
# Real-run verification of the phase-8c batch tooling:
#   1. Build every new binary.
#   2. Import a synthetic CSV with two fake JWTs (validation skipped —
#      the tokens are not real Cursor tokens, so /GetMe would 401).
#   3. Snapshot cursor-pool status output.
#   4. Round-trip cursor-to-cpa -> cursor-from-cpa and diff the pools.
#   5. Simulate cursor-login-batch by pointing at a fake OAuth server
#      (poll endpoint returns 200 immediately with a canned payload).
#
# This is a self-contained script; it writes to phase-8c-verify.log and
# a scratch dir under /tmp. Idempotent: safe to re-run.

set -euo pipefail

REPO=$(git rev-parse --show-toplevel)
cd "$REPO"

LOG="$REPO/phase-8c-verify.log"
: >"$LOG"
exec > >(tee -a "$LOG") 2>&1

BIN=$(mktemp -d)
POOL_A=$(mktemp -d)
POOL_CPA=$(mktemp -d)
POOL_B=$(mktemp -d)
POOL_LOGIN=$(mktemp -d)

echo "===================================================================="
echo " Phase 8c batch tooling — verification run"
echo " repo   : $REPO"
echo " bin dir: $BIN"
echo " date   : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "===================================================================="

echo
echo "== 1. build =="
for pkg in cursor-login-batch cursor-batch-import cursor-to-cpa cursor-from-cpa cursor-pool; do
  echo "-- $pkg"
  go build -o "$BIN/$pkg" "./cmd/$pkg"
done
ls -la "$BIN"

echo
echo "== 2. cursor-batch-import from a synthetic CSV =="
python3 - <<'PY' > /tmp/phase-8c-tokens.csv
import base64, json, sys, time
def jwt(iat, exp):
    def b64(o):
        return base64.urlsafe_b64encode(json.dumps(o).encode() if isinstance(o, dict) else o).decode().rstrip('=')
    head = b64({"alg":"none"})
    body = b64({"iat":iat, "exp":exp, "sub":"user_fake"})
    sig = b64(b"sig")
    return f"{head}.{body}.{sig}"
now = int(time.time())
print("email,access_token,refresh_token")
print(f"alice_batch@example.com,{jwt(now, now+58*86400)},r_alice")
print(f"bob_batch@example.com,{jwt(now, now+12*86400)},r_bob")
print(f"carol_batch@example.com,{jwt(now-86400, now-3600)},")  # expired, no refresh
PY
cat /tmp/phase-8c-tokens.csv

"$BIN/cursor-batch-import" \
  --csv /tmp/phase-8c-tokens.csv \
  --out "$POOL_A" \
  --skip-validate \
  --force

echo
echo "== 3. cursor-pool status (table) =="
"$BIN/cursor-pool" status --dir "$POOL_A"

echo
echo "== 4. cursor-pool status --json (first 40 lines) =="
"$BIN/cursor-pool" status --dir "$POOL_A" --json | head -n 40

echo
echo "== 5. cursor-to-cpa batch =="
"$BIN/cursor-to-cpa" --dir-in "$POOL_A" --dir "$POOL_CPA"
ls -la "$POOL_CPA"

echo
echo "== 6. cursor-from-cpa reverse batch =="
"$BIN/cursor-from-cpa" --dir-in "$POOL_CPA" --out "$POOL_B"
ls -la "$POOL_B"

echo
echo "== 7. round-trip diff =="
if diff -r "$POOL_A" "$POOL_B" >/dev/null; then
  echo "OK: $POOL_A and $POOL_B are byte-identical"
else
  echo "FAIL: pools differ"
  diff -r "$POOL_A" "$POOL_B" || true
  exit 1
fi

echo
echo "== 8. cursor-login-batch (mocked OAuth server) =="
MOCK_PORT=18641
python3 - "$MOCK_PORT" <<'PY' &
import http.server, socketserver, sys, json, base64, time
port = int(sys.argv[1])
def jwt(iat, exp):
    def b64(o):
        return base64.urlsafe_b64encode(json.dumps(o).encode() if isinstance(o, dict) else o).decode().rstrip('=')
    return f"{b64({'alg':'none'})}.{b64({'iat':iat,'exp':exp,'sub':'user_mock'})}.{b64(b'sig')}"
now = int(time.time())
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        payload = json.dumps({
            "accessToken": jwt(now, now+30*86400),
            "refreshToken": "r_mock",
            "authId": "auth0|user_mock",
            "authType": "Auth_0",
        }).encode()
        self.send_response(200)
        self.send_header("content-type","application/json")
        self.send_header("content-length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
    def log_message(self, *a): pass
socketserver.TCPServer.allow_reuse_address = True
srv = socketserver.TCPServer(("127.0.0.1", port), H)
srv.serve_forever()
PY
MOCK_PID=$!
sleep 1

CURSOR_POLL_URL_OVERRIDE="http://127.0.0.1:$MOCK_PORT/auth/poll" \
  "$BIN/cursor-login-batch" \
  --emails "mockuser1@example.com,mockuser2@example.com" \
  --out "$POOL_LOGIN" \
  --no-browser \
  --interval 500ms \
  --timeout 10s \
  --force

kill $MOCK_PID 2>/dev/null || true
wait 2>/dev/null || true

ls -la "$POOL_LOGIN"

echo
echo "== 9. cursor-pool status against the mock-login pool =="
"$BIN/cursor-pool" status --dir "$POOL_LOGIN"

echo
echo "===================================================================="
echo " Verification complete. Log written to $LOG"
echo "===================================================================="
