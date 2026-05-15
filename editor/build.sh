#!/bin/bash
# Build the editor SPA into the orchestrator's embed source.
#
# Output dir is wired in vite.config.ts (ORCH_DIST → ../orchestrator/internal/static/dist).
# After this completes, `cd ~/homa/orchestrator && go build ./...` will
# embed the freshly built SPA via //go:embed.
#
# Idempotent. Safe to run repeatedly.

set -euo pipefail
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$SCRIPT_DIR"

if [[ ! -d node_modules ]]; then
  npm install --no-audit --no-fund
fi
npm run build
