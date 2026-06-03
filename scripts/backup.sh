#!/usr/bin/env bash
#
# backup.sh — point-in-time snapshot of the operational database.
#
# Uses SQLite's online-backup API (sqlite3 .backup) which is SAFE on a
# live database — no risk of partial-write corruption, unlike a raw cp.
#
# Output:    ~/homa/backups/homa-YYYYMMDD-HHMMSS.db.gz (primary on /sdb)
# Retention: BACKUP_KEEP most recent (default 14)
# Mirror:    if BACKUP_MIRROR_DIR is set, the primary dir is rsync'd
#            (with --delete) to that path on each run. Use this for
#            on-host redundancy across physical disks. Mirror exactly
#            tracks the primary, including prunes.
#
# Usage:
#   ./scripts/backup.sh                          # primary only
#   BACKUP_MIRROR_DIR=/sda/homa-backups ./scripts/backup.sh
#   systemd timer wraps for daily cron — see scripts/homa-backup.{service,timer}
#
# Off-host (different machine) backup: not built in. Add an rclone /
# rsync-to-remote step here when there's a destination chosen.
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

# On-host redundancy mirror. --delete keeps the mirror an exact copy
# of the primary (pruned files vanish on the mirror too). Non-fatal:
# if the mirror destination is unavailable, the primary backup still
# succeeded.
if [ -n "${BACKUP_MIRROR_DIR:-}" ]; then
  if mkdir -p "$BACKUP_MIRROR_DIR" 2>/dev/null && \
     rsync -a --delete "$DEST_DIR/" "$BACKUP_MIRROR_DIR/" 2>&1; then
    echo "backup.sh: mirrored → $BACKUP_MIRROR_DIR"
  else
    echo "backup.sh: WARN: mirror to $BACKUP_MIRROR_DIR failed" >&2
  fi
fi
