#!/bin/bash
# Restart-safety test: PortAllocator must rehydrate from the users table
# across daemon restarts so a third user signing up after a restart can't
# collide with the first two users' ports.
#
# Flow:
#   1. Start orchestrator (stub provisioner; use_podman=false).
#   2. Sign up two users → capture nous_port for each from the SQLite DB.
#   3. Kill orchestrator; restart pointing at the same data_dir.
#   4. Sign up a third user → capture nous_port.
#   5. Assert third user's nous_port > max(first two).

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORKDIR=$(mktemp -d -t homa-restart-XXXXXX)
ORCH_PID=
trap 'cleanup' EXIT

cleanup() {
  [[ -n "$ORCH_PID" ]] && kill "$ORCH_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$WORKDIR"
}

# ---------------------------------------------------------------------------
echo "==> building orchestrator + port-dump helper"
cd "$ROOT/orchestrator"
go build -o "$WORKDIR/homa" ./cmd/homa
go build -o "$WORKDIR/portdump-bin" ./cmd/portdump

# ---------------------------------------------------------------------------
ORCH_PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
DATA="$WORKDIR/data"
mkdir -p "$DATA"
cat > "$WORKDIR/config.json" <<EOF
{
  "listen_addr": "127.0.0.1:$ORCH_PORT",
  "data_dir": "$DATA",
  "branches_dir": "$WORKDIR/branches",
  "cookie_secure": false
}
EOF

start_orch() {
  setsid "$WORKDIR/homa" -config "$WORKDIR/config.json" \
    >"$WORKDIR/homa.log" 2>&1 &
  ORCH_PID=$!
  # /me returns 401 when not authenticated; that's the "ready" signal.
  # Drop `-f` so curl exits 0 on 401, then grep the code from stdout.
  for i in $(seq 1 50); do
    code=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$ORCH_PORT/me" 2>/dev/null || echo "000")
    if [[ "$code" == "401" || "$code" == "200" ]]; then
      return 0
    fi
    sleep 0.1
  done
  echo "orchestrator failed to start; log:" >&2
  cat "$WORKDIR/homa.log" >&2
  exit 1
}

stop_orch() {
  [[ -n "$ORCH_PID" ]] && kill "$ORCH_PID" 2>/dev/null || true
  wait "$ORCH_PID" 2>/dev/null || true
  ORCH_PID=
}

signup() {
  local email=$1
  curl -sS -X POST -H 'Content-Type: application/json' \
    -d "{\"email\":\"$email\",\"password\":\"hunter22\"}" \
    "http://127.0.0.1:$ORCH_PORT/signup" >/dev/null
}

# ---------------------------------------------------------------------------
echo "==> 1. start orchestrator (first run)"
start_orch
echo "==> 2. sign up user-1, user-2"
signup u1@x.io
signup u2@x.io
P1=$("$WORKDIR/portdump-bin" -db "$DATA/homa.db" -email u1@x.io)
P2=$("$WORKDIR/portdump-bin" -db "$DATA/homa.db" -email u2@x.io)
echo "    u1.nous_port=$P1  u2.nous_port=$P2"

echo "==> 3. kill orchestrator"
stop_orch

echo "==> 4. restart orchestrator (same data_dir)"
start_orch

echo "==> 5. sign up user-3"
signup u3@x.io
P3=$("$WORKDIR/portdump-bin" -db "$DATA/homa.db" -email u3@x.io)
echo "    u3.nous_port=$P3"

echo "==> 6. assert u3 > max(u1, u2)"
MAX=$(( P1 > P2 ? P1 : P2 ))
if [[ "$P3" -le "$MAX" ]]; then
  echo "FAIL: u3.nous_port=$P3 NOT greater than max(u1=$P1, u2=$P2)=$MAX" >&2
  exit 1
fi
echo "OK: u3=$P3 > max(u1=$P1, u2=$P2)=$MAX"
