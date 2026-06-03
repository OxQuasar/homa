#!/usr/bin/env bash
#
# backup.sh — point-in-time snapshot of the operational database.
#
# Uses SQLite's online-backup API (sqlite3 .backup) which is SAFE on a
# live database — no risk of partial-write corruption, unlike a raw cp.
#
# Output: ~/homa/backups/homa-YYYYMMDD-HHMMSS.db.gz
# Retention: keeps the most recent BACKUP_KEEP files, prunes the rest.
#
# Usage:
#   ./scripts/backup.sh                # run once
#   systemd timer wraps for daily cron (see scripts/homa-backup.{service,timer})
#
# Off-host backup: this script writes locally. If gandiva's disk dies,
# the backups die with it. For real off-host safety, add a follow-up
# step (rclone / rsync to remote, or push to a private git repo).
set -euo pipefail

ROOT="${HOMA_ROOT:-$HOME/homa}"
DB="$ROOT/data/homa.db"
DEST_DIR="$ROOT/backups"
KEEP="${BACKUP_KEEP:-14}"

if [ ! -f "$DB" ]; then
  echo "backup.sh: $DB does not exist" >&2
  exit 1
fi

mkdir -p "$DEST_DIR"
STAMP=$(date -u +%Y%m%d-%H%M%S)
TMP="$DEST_DIR/.homa-${STAMP}.db.tmp"
FINAL="$DEST_DIR/homa-${STAMP}.db.gz"

# Online backup. SQLite reads page-by-page with locks short enough that
# concurrent writers see no impact.
sqlite3 "$DB" ".backup '$TMP'"
gzip -c "$TMP" > "$FINAL"
rm -f "$TMP"

# Prune anything older than the most recent $KEEP backups.
mapfile -t OLD < <(ls -1t "$DEST_DIR"/homa-*.db.gz 2>/dev/null | tail -n +$((KEEP + 1)))
for f in "${OLD[@]:-}"; do
  [ -n "$f" ] && rm -f "$f"
done

echo "backup.sh: $FINAL ($(stat -c%s "$FINAL") bytes; ${#OLD[@]} pruned; $KEEP retained)"
