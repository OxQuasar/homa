#!/usr/bin/env bash
# Install + enable the homa.service user-level systemd unit.
#
# After running this, homa starts on boot (assuming linger is on) and
# restarts on crash. Manage with:
#
#   systemctl --user status  homa
#   systemctl --user restart homa
#   journalctl   --user -u   homa  -f

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
src="$here/homa.service"
dst="$HOME/.config/systemd/user/homa.service"

if [[ ! -f "$src" ]]; then
  echo "missing $src" >&2
  exit 1
fi

mkdir -p "$(dirname "$dst")"
cp -f "$src" "$dst"
echo "installed: $dst"

systemctl --user daemon-reload
systemctl --user enable homa.service
echo "enabled: homa.service"

# linger lets user services keep running after logout / across reboots.
# Idempotent — no error if already enabled.
if ! loginctl show-user "$USER" 2>/dev/null | grep -q "Linger=yes"; then
  echo "enabling linger (requires sudo) so homa survives logout + reboot..."
  sudo loginctl enable-linger "$USER"
fi

# Stop any foreground instance the operator might be running, so the
# systemd-managed one can claim the port + socket without collision.
if pgrep -f "homa -config" > /dev/null 2>&1; then
  echo "stopping foreground homa process(es) so systemd can take over..."
  pkill -f "homa -config" || true
  sleep 1
fi

systemctl --user start homa.service
sleep 2
systemctl --user --no-pager status homa.service | head -15
