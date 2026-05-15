#!/bin/bash
# Per-user sandbox entrypoint.
#
# Boot order:
#   1. Validate that *some* Anthropic auth is reachable — either
#      ANTHROPIC_API_KEY env var OR a non-empty Claude OAuth credentials
#      file bind-mounted at /root/.claude/.credentials.json. Either is
#      sufficient; nous's resolveAuth chain prefers the OAuth file when
#      present.
#   2. Lazy `npm install` on first run (node_modules not baked into image).
#   3. Vite dev server in background — unsupervised; if it dies the iframe
#      goes blank but nous (the actual orchestration target) keeps running.
#      Acceptable for MVP.
#   4. exec nous daemon in foreground so it owns container lifecycle (PID 1
#      semantics via exec). nous reads its config.json from nousDir
#      (/usr/local/bin/config.json — baked into the image), so no runtime
#      config-copy is needed and the user's worktree stays uncluttered.

set -euo pipefail

# Auth precondition: either env var or a usable OAuth credentials file.
if [[ -z "${ANTHROPIC_API_KEY:-}" && ! -s /root/.claude/.credentials.json ]]; then
  echo "homa-sandbox: no Anthropic auth available — set ANTHROPIC_API_KEY or" >&2
  echo "  mount a non-empty .credentials.json at /root/.claude/.credentials.json." >&2
  exit 1
fi

if [[ ! -d /workspace/node_modules ]]; then
  echo "homa-sandbox: installing node dependencies (first run)..." >&2
  (cd /workspace && npm install --no-audit --no-fund)
fi

# Vite in background. Use `&` not `exec` so the shell stays around to launch
# nous next. --strictPort keeps us deterministic — fail loud if 5173 is busy.
(cd /workspace && exec npm run dev -- --host 0.0.0.0 --port 5173 --strictPort) &

# nous in foreground (PID 1 semantics). cwd = /workspace so it operates on
# the user's site files; config comes from /usr/local/bin/config.json.
cd /workspace
exec nous daemon
