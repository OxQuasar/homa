#!/usr/bin/env bash
#
# refresh.sh — rebuild + reload after editing source.
#
# Detects what changed in the working tree (since last build) and runs
# only the steps needed. Always safe to re-run; idempotent.
#
# Editor SPA changes (editor/) → rebuild dist + rebuild Go + restart orch
# Orchestrator Go changes      → rebuild Go + restart orch
# Site-template changes        → no-op (Vite HMRs from homa-main)
# Library content changes      → no-op (homa-main RO bind sees live)
#
# After this script: hard-refresh your browser (Ctrl+Shift+R) to bust
# any cached SPA bundle.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

# Detect what's been touched since the most recent build artifact.
ORCH_BIN=$HOME/homa/homa
SPA_DIST=orchestrator/internal/static/dist

editor_dirty=0
orch_dirty=0

if [ ! -f "$ORCH_BIN" ]; then
  orch_dirty=1
  editor_dirty=1
else
  # Editor: any source file newer than the embedded dist's index?
  if find editor/src editor/index.html -newer "$SPA_DIST/index.html" 2>/dev/null | grep -q .; then
    editor_dirty=1
  fi
  # Orchestrator: any .go newer than the binary?
  if find orchestrator -name '*.go' -newer "$ORCH_BIN" 2>/dev/null | grep -q .; then
    orch_dirty=1
  fi
fi

if [ "$editor_dirty" = "1" ]; then
  echo "» rebuilding editor SPA"
  (cd editor && bash build.sh) | tail -3
  orch_dirty=1   # SPA is embedded in orch binary; force orch rebuild
fi

if [ "$orch_dirty" = "1" ]; then
  echo "» rebuilding orchestrator"
  (cd orchestrator && go build -o "$ORCH_BIN" ./cmd/homa)
  echo "» restarting daemon"
  XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user restart homa
  sleep 1
fi

if [ "$editor_dirty" = "0" ] && [ "$orch_dirty" = "0" ]; then
  echo "» nothing to do (no source changes since last build)"
fi

echo "» done. Hard-refresh your browser (Ctrl+Shift+R) if you edited the SPA."
