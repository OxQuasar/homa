#!/bin/bash
# Per-user sandbox entrypoint.
#
# Boot order:
#   1. Validate ANTHROPIC_API_KEY is present (fail fast — nous would die at
#      first LLM call anyway, but with a less obvious message).
#   2. Seed /workspace/.nous/config.json from the baked YOLO template if the
#      worktree doesn't already carry one. -n leaves any user-supplied
#      config in place across container restarts.
#   3. Lazy `npm install` on first run (node_modules not baked into image).
#   4. Vite dev server in background — unsupervised; if it dies the iframe
#      goes blank but nous (the actual orchestration target) keeps running.
#      Acceptable for MVP.
#   5. exec nous daemon in foreground so it owns container lifecycle (PID 1
#      semantics via exec).

set -euo pipefail

if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "homa-sandbox: ANTHROPIC_API_KEY is unset — refusing to start." >&2
  echo "Pass it with: podman run -e ANTHROPIC_API_KEY=... ..." >&2
  exit 1
fi

mkdir -p /workspace/.nous
cp -n /etc/homa/nous.config.json /workspace/.nous/config.json

if [[ ! -d /workspace/node_modules ]]; then
  echo "homa-sandbox: installing node dependencies (first run)..." >&2
  (cd /workspace && npm install --no-audit --no-fund)
fi

# Vite in background. Use `&` not `exec` so the shell stays around to launch
# nous next. --strictPort keeps us deterministic — fail loud if 5173 is busy.
(cd /workspace && exec npm run dev -- --host 0.0.0.0 --port 5173 --strictPort) &

cd /workspace
exec nous daemon
