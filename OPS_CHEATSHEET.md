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

> **Don't `pkill` if you're running under systemd.** A SIGTERM is a clean
> exit, which `Restart=on-failure` does NOT respawn — you'll silently
> stop the service. Use `systemctl --user restart homa` instead.

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

From a non-interactive shell (cron, scripts, ssh `-c` invocations) the
user systemd bus isn't auto-discovered — set XDG_RUNTIME_DIR explicitly:

```bash
XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user restart homa
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

Two paths: **direct merge** (whole user branch) or **PR merge** (staged
work on a `pr/<userid>/<topic>` branch — for when you want review
separation per topic instead of merging everything-since-last-publish).

### Direct merge

```bash
./homa merge <userid>
```

Auto-commits everything in `branches/<userid>/` (uncommitted LLM edits + new files) under the user's email, then `git merge --no-ff user/<userid>` into `main`. `homa-main`'s vite HMRs; visitors see new content within ~2s.

Conflicts surface as a non-zero git exit. Resolve by hand in `~/homa/site-template/` and `git commit`. Retry the merge command — auto-commit is idempotent on a clean tree.

### PR-style merge

LLM (or user) creates a `pr/<userid>/<topic>` branch from their work:

```bash
# inside the user's sandbox (LLM via bash):
cd /workspace
git checkout -b pr/77b4cf0e/dark-mode    # off user/77b4cf0e
# ... commits ...
git checkout user/77b4cf0e               # back to user branch
```

Operator review + merge from host:

```bash
homa pr list                              # see all pr/* branches with stats
homa pr show pr/77b4cf0e/dark-mode        # commits + file changes vs main
homa pr merge pr/77b4cf0e/dark-mode       # git merge --no-ff into main
# or
homa pr close pr/77b4cf0e/dark-mode       # delete without merging
```

Branch naming `pr/<userid>/<topic>` is the convention; anything else is
not picked up by `homa pr list`. Topic charset: `[a-zA-Z0-9._-]+` (URL-safe,
no slashes — nested topics aren't supported in v1).

Phase 1 is purely git-native (no metadata DB). Phase 2 (later) adds a
`pull_requests` table for titles, descriptions, comments, and a UI
surface in the editor.

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
# Users — pretty table via the python venv (sqlite3 CLI not installed):
~/nous/.venv/bin/python - <<'PY'
import sqlite3, datetime
c = sqlite3.connect('/home/quasar/homa/data/homa.db')
print(f"{'id':<10} {'email':<25} {'created':<17} {'last_msg':<17} {'nous':<5} {'preview':<8} {'code':<5}")
print('-' * 92)
for r in c.execute('''SELECT id, email, created_at, last_message_at,
                             nous_port, preview_serve_port, code_server_serve_port
                      FROM users ORDER BY created_at'''):
    fmt = lambda t: datetime.datetime.fromtimestamp(t, tz=datetime.timezone.utc).strftime('%Y-%m-%d %H:%M') if t else '-'
    print(f"{r[0]:<10} {r[1]:<25} {fmt(r[2]):<17} {fmt(r[3]):<17} {r[4]:<5} {r[5]:<8} {r[6]:<5}")
PY

# Or raw SQL if sqlite3 CLI is installed:
# sqlite3 ~/homa/data/homa.db 'SELECT id, email, datetime(created_at,"unixepoch") FROM users'

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

## VS Code in the browser (code-server)

Each user's sandbox runs a code-server instance, accessed via tailscale
serve on a per-user HTTPS port. The "Open VS Code" button in the editor
header opens it in a new tab.

**Auth (Phase 1 — tailnet-only)**: code-server runs with `--auth none`.
The gate is tailscale-serve: only nodes on the operator's tailnet can
reach the per-user `:1000X` port. Anyone on the tailnet who guesses
the right port number can reach any user's code-server. **Acceptable
for single-operator deployments; not acceptable for shared tailnets
or public exposure** — Phase 2 (memories/homa/codeserver.md) replaces
this with an orchestrator reverse-proxy that validates the homa
session cookie + matches it against the requested user.

**Master secret** at `~/homa/data/code_server_secret` (mode 0600, 32
bytes) — auto-generated. Currently only governs whether the feature
turns ON for a user; in Phase 2 it'll derive per-user passwords the
proxy injects into upstream code-server calls.

```bash
# Disable feature: in ~/homa/config.json:
{
  "code_server_enabled": false
}

# Rotate every user's password:
rm ~/homa/data/code_server_secret
systemctl --user restart homa
# A new secret is generated; passwords change. Each user's code-server
# remembers OLD session cookies, so they may need to log out + re-click
# Open VS Code in the homa editor to pick up the new password.

# Extensions / settings persistence: each user has a podman volume
# `homa-user-<userid>-codeserver` mounted at /root/.local/share/code-server
# inside their container. Survives container --rm.
```

Image size note: code-server adds ~250 MB. Sandbox image is now ~620 MB total.

## People directory

```
GET /api/users  → [{user_id, username, created_at}, …]
```

Auth-required + CORS-wrapped (same posture as the forum). Sorted by
created_at ascending. Users with empty username (pre-backfill legacy
rows) are filtered out. **No emails in the response** — directory is
a public-name-only view.

Example page lives at `site-template/src/routes/users/+page.svelte`;
the forum index links to it via the "All users →" button.

## Forum API

Shared multi-tenant forum lives at orchestrator endpoints. All four
require an authenticated cookie (both reads and writes).

```
GET  /api/forum/topics
       → [{id, title, author_id, author_name, created_at, post_count}, …]
       newest first

POST /api/forum/topics
       body: {"title": "..."}
       → created topic JSON

GET  /api/forum/topics/<id>/posts
       → [{id, topic_id, author_id, author_name, content, created_at}, …]
       newest first

POST /api/forum/topics/<id>/posts
       body: {"content": "..."}
       → created post JSON
```

Author_name is the user's username (from `users.username`). All
endpoints CORS-enabled for the configured PreviewBaseURL host (any
port) so user-iframe-rendered pages can call them cross-origin with
`credentials: 'include'`.

Example fetch from a SvelteKit page (see `site-template/src/routes/forum/`
for the full reference implementation):

```ts
const r = await fetch('https://gandiva.kingfisher-celsius.ts.net/api/forum/topics', {
  credentials: 'include'
});
if (r.status === 401) { /* show login prompt */ }
const topics = await r.json();
```

Wiring your own styled forum page to the API: prompt your LLM in
`/editor` something like:

> Wire the existing /forum page's "create topic" form to
> POST https://gandiva.kingfisher-celsius.ts.net/api/forum/topics
> with credentials:'include' and body {title}. Show topics from
> GET /api/forum/topics. Use the API's author_name field where the
> design references the author.

The shared SQLite owns the data — posts from any logged-in user land
in the same place and become visible to all other users.

## Browser errors → LLM

A `<script>` baked into `site-template/src/app.html` captures
`window.onerror` + `unhandledrejection` events in the user's site
iframe and posts them up to the editor (cross-origin postMessage).
Editor buffers + dedupes them, surfaces an amber *"⚠ N browser errors"*
badge above the chat input, and **prepends the buffer to the user's
next prompt** so the LLM sees the same failures the user does. Cleared
on send (or `✕` on the badge).

(SvelteKit's dev pipeline bypasses vite's `transformIndexHtml` hook,
so a plugin-injected version doesn't fire — `app.html` is SvelteKit's
actual extension point.)

Auto-on for any user whose `app.html` includes the script — new
signups inherit it from the template. To roll it forward to an
existing user's branch:

```bash
cp ~/homa/site-template/src/app.html ~/homa/branches/<userid>/src/app.html
# Next ./homa merge auto-commits it.
```

**Disable** (operator or LLM, per-site): delete the `<script>` block
in the user's `src/app.html`. Inert outside an iframe so the public
main site never posts anything.

Errors that *don't* flow through this loop today: vite build / HMR
errors (when the user's code has a syntax error, vite shows its red
overlay and no JS runs in the iframe). console.error / console.warn.
CSS-only failures. The LLM sees runtime JS errors and unhandled
promise rejections.

## Get a user file (photo, etc.) into their site

Three paths:

1. **Editor upload button** (📎 in the input bar) — pick a file from
   the local machine. Lands at `branches/<userid>/static/uploads/<name>`
   (auto-renamed if it collides). Editor pre-pends `[uploaded: <path>]`
   to the chat input so the next prompt names the file directly.
   Default size cap: 10 MB. Tune with `upload_max_bytes` in
   `config.json` (0 = default).

2. **scp from another machine** — bypass the upload UI:
   ```bash
   scp ~/Pictures/foo.jpg gandiva:~/homa/branches/<userid>/static/images/
   ```
   Then in the editor: *"I added static/images/foo.jpg. Use it as …"*

3. **Ask the LLM to fetch from a URL** — `curl` is available inside the
   sandbox: *"Download https://example.com/foo.jpg to
   static/images/foo.jpg and use it as …"*

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

## Where state lives (post-move)

```
~/homa/data       → symlink → /sdb/homa/data    (mounted: 11TB ext4 on sdb1)
~/homa/backups    → symlink → /sdb/homa/backups
~/homa/branches   → symlink → /sdb/homa/branches
```

All three stateful dirs moved to `/sdb` for storage headroom (library + library-branches + per-user worktrees dominate growth). The symlinks keep every path inside the codebase, container mounts, git worktree refs, and backup.sh valid — no config change needed.

Only `~/homa/site-template/` stays on `/` since it's the curated main of a separate git repo (small, slow-growth, regularly pushed to GitHub).

To move `data/` between disks safely:

```bash
systemctl --user stop homa
podman stop -t 5 homa-main $(podman ps -aq --filter name=homa-user-)
rsync -aHX ~/homa/data/  /new/path/homa/data/   # verify with diff -rq before next step
rm -rf ~/homa/data
ln -s /new/path/homa/data ~/homa/data
systemctl --user start homa
```

**Recovery if data/ is lost**: SQLite DB from `~/homa/backups/`, library from `git@github.com:OxQuasar/homa-library.git`. `library-branches/` and `configs/` are recreated lazily; `code_server_secret` regenerated on startup.
