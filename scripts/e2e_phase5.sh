#!/bin/bash
# Phase 5 end-to-end: orchestrator + fake nous upstream + probe through /ws.
#
# Flow:
#   1. Pick free ports for the orchestrator and the fake nous upstream.
#   2. Start the fake upstream FIRST so we know its port.
#   3. Write a config.json that points the orchestrator's stub provisioner at
#      that port (so the first signup's nous_port lands there).
#   4. Start the orchestrator.
#   5. Sign up a user → cookie file.
#   6. GET /me → assert 200 + user_id.
#   7. Run the probe through /ws with the cookie.
#   8. Tear everything down by PID.

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORKDIR=$(mktemp -d -t homa-e2e-XXXXXX)
trap 'cleanup' EXIT

ORCH_PID=
FAKE_PID=
cleanup() {
  [[ -n "$ORCH_PID" ]] && kill "$ORCH_PID" 2>/dev/null || true
  [[ -n "$FAKE_PID" ]] && kill "$FAKE_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$WORKDIR"
}

# ---------------------------------------------------------------------------
echo "==> 1. picking free ports"
ORCH_PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
FAKE_PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
# Note: PORT race is possible — small risk. Acceptable for an MVP e2e.
echo "    orchestrator=$ORCH_PORT  fake-nous=$FAKE_PORT"

# ---------------------------------------------------------------------------
echo "==> 2. building binaries"
cd "$ROOT/orchestrator"
go build -o "$WORKDIR/homa" ./cmd/homa
go build -o "$WORKDIR/fakeupstream" ./internal/proxy/fakeupstream/cmd
( cd "$ROOT/sandbox/probe" && go build -o "$WORKDIR/probe" . )

# ---------------------------------------------------------------------------
echo "==> 3. starting fake upstream on 127.0.0.1:$FAKE_PORT"
setsid "$WORKDIR/fakeupstream" -addr "127.0.0.1:$FAKE_PORT" \
  >"$WORKDIR/fake.log" 2>&1 &
FAKE_PID=$!
sleep 0.5
if ! kill -0 "$FAKE_PID" 2>/dev/null; then
  echo "fake upstream failed to start; log:" >&2
  cat "$WORKDIR/fake.log" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
echo "==> 4. writing orchestrator config"
cat > "$WORKDIR/config.json" <<EOF
{
  "listen_addr": "127.0.0.1:$ORCH_PORT",
  "data_dir": "$WORKDIR/data",
  "branches_dir": "$WORKDIR/branches",
  "cookie_secure": false,
  "provision_host_port_start": $FAKE_PORT
}
EOF

# ---------------------------------------------------------------------------
echo "==> 5. starting orchestrator"
setsid "$WORKDIR/homa" -config "$WORKDIR/config.json" \
  >"$WORKDIR/homa.log" 2>&1 &
ORCH_PID=$!
# Wait for /me (a cheap endpoint) to respond — at most 5s.
for i in $(seq 1 50); do
  if curl -fsS -o /dev/null -w '' "http://127.0.0.1:$ORCH_PORT/me" 2>/dev/null \
     || curl -fsS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$ORCH_PORT/me" 2>/dev/null | grep -q '401\|200'; then
    break
  fi
  sleep 0.1
done
if ! kill -0 "$ORCH_PID" 2>/dev/null; then
  echo "orchestrator failed to start; log:" >&2
  cat "$WORKDIR/homa.log" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
echo "==> 6. /signup"
SIGNUP_RESP=$(curl -sS -c "$WORKDIR/cookies.txt" -X POST \
  -H 'Content-Type: application/json' \
  -d '{"email":"e2e@x.io","password":"hunter22"}' \
  "http://127.0.0.1:$ORCH_PORT/signup")
echo "    signup body: $SIGNUP_RESP"
USER_ID=$(echo "$SIGNUP_RESP" | python3 -c 'import json,sys;print(json.load(sys.stdin)["user_id"])')
[[ -n "$USER_ID" ]] || { echo "no user_id in signup response" >&2; exit 1; }
echo "    user_id=$USER_ID"

# ---------------------------------------------------------------------------
echo "==> 7. /me"
ME_RESP=$(curl -sS -b "$WORKDIR/cookies.txt" "http://127.0.0.1:$ORCH_PORT/me")
echo "    me body: $ME_RESP"
echo "$ME_RESP" | grep -q "\"user_id\":\"$USER_ID\"" \
  || { echo "/me did not echo user_id" >&2; exit 1; }

# ---------------------------------------------------------------------------
echo "==> 8. probing /ws through the proxy (cookie-gated)"
COOKIE_VAL=$(awk '/homa_session/ {print $7}' "$WORKDIR/cookies.txt")
[[ -n "$COOKIE_VAL" ]] || { echo "no homa_session cookie in jar" >&2; exit 1; }
PROBE_OUT=$("$WORKDIR/probe" \
  --addr "ws://127.0.0.1:$ORCH_PORT/ws" \
  --workdir "/workspace" \
  --cookie "$COOKIE_VAL")
echo "    probe output: $PROBE_OUT"
echo "$PROBE_OUT" | grep -q '"session_id":"fake-sess"' \
  || { echo "probe did not see fake-sess session_id" >&2; exit 1; }
echo "$PROBE_OUT" | grep -q '"directory":"/workspace"' \
  || { echo "probe did not see /workspace directory" >&2; exit 1; }

# ---------------------------------------------------------------------------
echo "==> 9. cleanup (trap)"
echo "OK"
