# Homa Ops Cheatsheet

Single-operator reference. Architectural background lives in `README.md`.

## Start / restart

Two ways to run homa: ad-hoc foreground (good for iterating on code) or as a user-level systemd service (production / always-on).

### Ad-hoc foreground

```bash
# Run / restart
cd ~/homa && ./homa -config config.json

# Stop foreground instance
pkill -f "homa -config"

# What's running?
ps -o pid,etime,lstart,cmd -p $(pgrep -f "homa -config")
stat -c "binary built: %y" ~/homa/homa
```

### Systemd (recommended for daily use)

One-time install:

```bash
bash ~/homa/systemd/install.sh
# - Copies homa.service into ~/.config/systemd/user/
# - Enables it (auto-start)
# - Calls `sudo loginctl enable-linger $USER` so it survives logout/reboot
# - Stops any foreground homa instance and starts the systemd-managed one
```

Daily ops:

```bash
systemctl --user status  homa             # is it up? since when?
systemctl --user restart homa             # after a rebuild
systemctl --user stop    homa             # take it down (won't auto-restart on clean stop)
systemctl --user start   homa
journalctl   --user -u   homa -f          # live logs
journalctl   --user -u   homa --since '10 min ago'
```

Behavior:
- `Restart=on-failure` — auto-respawns after crashes, not after clean stop
- 20s graceful shutdown window (orchestrator's `shutdownGrace` is 10s)
- Logs to the journal — no log file rotation to manage

After rebuilding the binary (`cd orchestrator && go build -o ~/homa/homa ./cmd/homa`), just `systemctl --user restart homa`.

### Either way

Orchestrator restart does NOT restart sandbox containers — they're managed independently by Podman. Restart them explicitly when needed (see "Pick up a new image").

## URLs (tailnet)

| Path | Serves |
|---|---|
| `https://gandiva.kingfisher-celsius.ts.net/` | Public site (= `site-template/main`, the `homa-main` container's vite) |
| `https://gandiva…/login`, `/signup` | SPA auth pages |
| `https://gandiva…/editor` | LLM editor (chat + iframe of your worktree's preview) |
| `https://gandiva…:10001/` | Direct preview of user `77b4cf0e`'s worktree |
| `https://gandiva…:10002/` | Direct preview of user `af416dcd`'s worktree |

## Promote a user's edits to public

```bash
./homa merge <userid>
```

Auto-commits everything in `branches/<userid>/` (uncommitted LLM edits + new files) under the user's email, then `git merge --no-ff user/<userid>` into `main`. `homa-main`'s vite HMRs; visitors see new content within ~2s.

Conflicts surface as a non-zero git exit. Resolve by hand in `~/homa/site-template/` and `git commit`. Retry the merge command — auto-commit is idempotent on a clean tree.

## Edit a user's nous config

```bash
$EDITOR ~/homa/data/configs/<userid>/config.json
podman stop homa-user-<userid>
# Next login or /ws hit respawns the container with the new config.
```

Per-user file is bind-mounted read-only at `/usr/local/bin/config.json` inside the container, shadowing the image default. LLM can read but not write.

Common fields: `default_model`, `effort`, `thinking`, `web_search`, `bash.foreground_timeout`. Full schema in `~/nous/internal/config/config.go`.

To change the **default for new users**, edit `~/homa/sandbox/nous.config.json` + `bash sandbox/build.sh` (existing users keep their per-user file).

## Inspect

```bash
# Users
sqlite3 ~/homa/data/homa.db '
  select id, email, container_name,
    datetime(created_at, "unixepoch")     as created,
    datetime(last_active_at, "unixepoch") as ws_tick,
    datetime(last_message_at, "unixepoch") as last_msg
  from users order by created_at'

# Running containers (user + main)
podman ps --filter name=homa-

# Persistent state per user
podman volume ls --filter name=homa-user-          # nous data (chat history)
ls ~/homa/branches/                                # worktrees (site source)
ls ~/homa/data/configs/                            # per-user nous configs

# Tailscale serve mappings
tailscale serve status

# Per-user nous logs (only while container is running)
podman logs homa-user-<userid> 2>&1 | tail -50

# Public-site logs
podman logs homa-main 2>&1 | tail -50
```

## Force a user's container to restart

```bash
podman stop homa-user-<userid>
# --rm cleans up the container; volume preserved; next /ws or /login respawns.
```

## Pick up a new sandbox image

```bash
# After editing sandbox/Containerfile, entrypoint.sh, or nous source:
bash ~/homa/sandbox/build.sh

# Force all existing users onto the new image:
podman stop $(podman ps -q --filter name=homa-user-)
# (homa-main respawns automatically via the orchestrator watchdog)
```

## Delete a user (full teardown)

```bash
ID=<userid>
podman stop homa-user-$ID 2>/dev/null
podman volume rm homa-user-$ID-nous 2>/dev/null       # ⚠️ destroys chat history
tailscale serve --https=$(sqlite3 ~/homa/data/homa.db \
    "select preview_serve_port from users where id='$ID'") off
git -C ~/homa/site-template worktree remove --force ~/homa/branches/$ID
git -C ~/homa/site-template branch -D user/$ID 2>/dev/null
rm -rf ~/homa/data/configs/$ID
sqlite3 ~/homa/data/homa.db \
  "delete from web_sessions where user_id='$ID'; delete from users where id='$ID';"
```

## Backup

Three independent surfaces:

```bash
# 1. SQLite + per-user configs
tar czf homa-meta-$(date +%F).tgz -C ~/homa data/

# 2. Per-user worktrees (the actual sites + uncommitted LLM edits)
tar czf homa-branches-$(date +%F).tgz -C ~/homa branches/ site-template/

# 3. Per-user nous data volumes (chat history)
for u in $(podman volume ls --filter name=homa-user- --format '{{.Name}}'); do
    podman volume export "$u" > "${u}-$(date +%F).tar"
done
```

Site-template's git history covers itself; backing up `branches/` is mostly for uncommitted in-flight LLM work.

## Idle lifecycle: tuning

Default: at 60 min since user's last message, force-disconnect browser → compact (if PromptTokens > 50k) → stop container. Editor shows a banner at T-60s.

```json
// ~/homa/config.json
{
  "idle_after_minutes": 60,         // 0 → default 60; negative disables lifecycle
  "gc_interval_seconds": 60,        // ticker cadence
  "idle_warning_seconds": 60,       // lead time for the warning banner
  "compact_timeout_seconds": 90,    // bound on the full_compact round-trip
  "compact_min_tokens": 50000       // skip compaction below this; 0 disables gate
}
```

For testing the lifecycle quickly: `idle_after_minutes: 2, idle_warning_seconds: 30, gc_interval_seconds: 5, compact_min_tokens: 0`. Restart orchestrator.

## Troubleshooting

**Editor shows "closed" / WS won't connect**
```bash
# Is the user's container up?
podman ps --filter name=homa-user-<id>
# Did orchestrator dial succeed? Check stderr for "upstream dial failed".
# If container is down, just /login should respawn it.
```

**Public site is "spa fallback" (the login page) instead of main**
```bash
# homa-main vite is down or warming up. Check it:
podman ps --filter name=homa-main
podman logs homa-main 2>&1 | tail -20
# Watchdog re-Ensures every 30s; restart by hand if it's stuck:
podman rm -f homa-main; sleep 1   # watchdog re-spawns on next tick
```

**Merge fails with "untracked working tree files would be overwritten"**
```bash
# A generated artifact landed in site-template/ uncommitted (most commonly
# package-lock.json from a manual npm install). Clear it:
rm ~/homa/site-template/<offending-file>
./homa merge <userid>
```

**"Idle compaction in Ns" banner appears unexpectedly**
The user's `last_message_at` is older than `idle_after_minutes - idle_warning_seconds`. WS keepalives don't reset the clock — only actual messages do. Sending any message clears the banner.

**OAuth token expired (Anthropic 401 inside sandbox)**
```bash
# Host-side Claude Code refreshes ~/.claude/.credentials.json automatically.
# If it's stale, force a refresh:
claude --help > /dev/null   # any claude command nudges the refresh
# Existing containers see the refreshed file via bind-mount; nous's
# OnAuthError callback re-reads on the next 401. No restart needed.
```

**Need to see what the orchestrator is actually doing**
```bash
# It logs to stderr. If foreground, scroll up. If you backgrounded it:
journalctl --user -u homa -f          # if running under systemd-user
# Otherwise re-run foreground for live logs.
```

## File layout reminder

```
~/homa/
├── orchestrator/    Go source. `go build -o ~/homa/homa ./cmd/homa`
├── editor/          Svelte SPA. `bash build.sh` rebuilds + drops into orchestrator's embed
├── sandbox/         Containerfile + entrypoint + nous.config.json. `bash build.sh` rebuilds image
├── site-template/   `main` branch = public site. Edited via ./homa merge from user branches
├── branches/<id>/   Per-user worktrees (gitignored). User sandbox bind-mounts as /workspace
├── data/
│   ├── homa.db      SQLite — users + web_sessions
│   └── configs/<id>/config.json   Per-user nous configs (bind-mounted RO)
├── config.json      Orchestrator config (gitignored)
└── homa             Built binary (gitignored)
```

## Repos

```
~/homa             OxQuasar/homa  (main)
~/nous             OxQuasar/nous  (homa branch — adds WS transport + session_id pinning)
```

After any nous source change, rebuild the sandbox image (which bundles the nous binary): `bash ~/homa/sandbox/build.sh`.
