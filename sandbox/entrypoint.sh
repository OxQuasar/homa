#!/bin/bash
# Sandbox entrypoint. Two roles, branched on HOMA_ROLE env var:
#
#   HOMA_ROLE=user   (default)
#     Per-user editor sandbox. Boots:
#       1. Auth precondition (ANTHROPIC_API_KEY env OR Claude OAuth file)
#       2. Lazy `npm install` on first run
#       3. Vite dev server in background
#       4. nous daemon in foreground (owns container lifecycle, PID 1)
#
#   HOMA_ROLE=main
#     The public-facing site (one per orchestrator). Vite-only — no nous,
#     no auth precondition. Orchestrator reverse-proxies / to this
#     container.

set -euo pipefail

ROLE="${HOMA_ROLE:-user}"

# Shared first-run install — both roles serve a SvelteKit site from /workspace.
if [[ ! -d /workspace/node_modules ]]; then
  echo "homa-sandbox[$ROLE]: installing node dependencies (first run)..." >&2
  (cd /workspace && npm install --no-audit --no-fund)
fi

if [[ "$ROLE" == "main" ]]; then
  # vite in foreground — it owns the container, no nous backing it.
  cd /workspace
  exec npm run dev -- --host 0.0.0.0 --port 5173 --strictPort
fi

# --- user role from here down ---

# Auth precondition: either env var or a usable OAuth credentials file.
if [[ -z "${ANTHROPIC_API_KEY:-}" && ! -s /root/.claude/.credentials.json ]]; then
  echo "homa-sandbox: no Anthropic auth available — set ANTHROPIC_API_KEY or" >&2
  echo "  mount a non-empty .credentials.json at /root/.claude/.credentials.json." >&2
  exit 1
fi

# Vite in background. `&` (not exec) so the shell stays around to launch
# nous next. --strictPort keeps us deterministic — fail loud if 5173 is busy.
(cd /workspace && exec npm run dev -- --host 0.0.0.0 --port 5173 --strictPort) &

# nous in foreground (PID 1 semantics). cwd = /workspace so it operates on
# the user's site files; config comes from /usr/local/bin/config.json.
cd /workspace
exec nous daemon
