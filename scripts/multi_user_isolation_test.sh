#!/bin/bash
# Multi-user e2e: two concurrent signups against a real orchestrator
# (stub provisioner) must produce distinct user_ids and distinct
# preview_urls. Runs against the same code path as production minus
# podman.

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORKDIR=$(mktemp -d -t homa-isolation-XXXXXX)
ORCH_PID=
trap 'cleanup' EXIT

cleanup() {
  [[ -n "$ORCH_PID" ]] && kill "$ORCH_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$WORKDIR"
}

echo "==> 1. picking free port"
ORCH_PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
echo "    orchestrator=$ORCH_PORT"

echo "==> 2. building orchestrator"
cd "$ROOT/orchestrator"
go build -o "$WORKDIR/homa" ./cmd/homa

echo "==> 3. writing config (stub provisioner; preview_base_url set so /me returns a URL)"
cat > "$WORKDIR/config.json" <<EOF
{
  "listen_addr": "127.0.0.1:$ORCH_PORT",
  "data_dir": "$WORKDIR/data",
  "branches_dir": "$WORKDIR/branches",
  "cookie_secure": false,
  "preview_base_url": "https://homa.test.local"
}
EOF

echo "==> 4. starting orchestrator"
setsid "$WORKDIR/homa" -config "$WORKDIR/config.json" \
  >"$WORKDIR/homa.log" 2>&1 &
ORCH_PID=$!
for i in $(seq 1 50); do
  code=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$ORCH_PORT/me" 2>/dev/null || echo "000")
  if [[ "$code" == "401" || "$code" == "200" ]]; then
    break
  fi
  sleep 0.1
done
if ! kill -0 "$ORCH_PID" 2>/dev/null; then
  echo "orchestrator failed to start; log:" >&2
  cat "$WORKDIR/homa.log" >&2
  exit 1
fi

echo "==> 5. concurrent signups for a@x.io + b@x.io"
signup() {
  local email=$1
  local cookies=$2
  local out=$3
  curl -sS -c "$cookies" -X POST -H 'Content-Type: application/json' \
    -d "{\"email\":\"$email\",\"password\":\"hunter22\"}" \
    "http://127.0.0.1:$ORCH_PORT/signup" > "$out"
}
signup a@x.io "$WORKDIR/cookies-a.txt" "$WORKDIR/signup-a.json" &
PID_A=$!
signup b@x.io "$WORKDIR/cookies-b.txt" "$WORKDIR/signup-b.json" &
PID_B=$!
wait "$PID_A"
wait "$PID_B"

UID_A=$(python3 -c 'import json,sys;print(json.load(sys.stdin)["user_id"])' < "$WORKDIR/signup-a.json")
UID_B=$(python3 -c 'import json,sys;print(json.load(sys.stdin)["user_id"])' < "$WORKDIR/signup-b.json")
echo "    a.user_id=$UID_A"
echo "    b.user_id=$UID_B"

echo "==> 6. fetching /me for each"
ME_A=$(curl -sS -b "$WORKDIR/cookies-a.txt" "http://127.0.0.1:$ORCH_PORT/me")
ME_B=$(curl -sS -b "$WORKDIR/cookies-b.txt" "http://127.0.0.1:$ORCH_PORT/me")
echo "    a /me: $ME_A"
echo "    b /me: $ME_B"

PV_A=$(python3 -c 'import json,sys;print(json.load(sys.stdin)["preview_url"])' <<<"$ME_A")
PV_B=$(python3 -c 'import json,sys;print(json.load(sys.stdin)["preview_url"])' <<<"$ME_B")

echo "==> 7. assertions"
if [[ -z "$UID_A" || -z "$UID_B" ]]; then
  echo "FAIL: empty user_id (A=$UID_A B=$UID_B)" >&2
  exit 1
fi
if [[ "$UID_A" == "$UID_B" ]]; then
  echo "FAIL: user_ids collided ($UID_A)" >&2
  exit 1
fi
if [[ -z "$PV_A" || -z "$PV_B" ]]; then
  echo "FAIL: empty preview_url (A=$PV_A B=$PV_B)" >&2
  exit 1
fi
if [[ "$PV_A" == "$PV_B" ]]; then
  echo "FAIL: preview_urls collided ($PV_A)" >&2
  exit 1
fi
echo "OK: user_ids and preview_urls are pairwise distinct"
echo "    a → user_id=$UID_A preview_url=$PV_A"
echo "    b → user_id=$UID_B preview_url=$PV_B"
