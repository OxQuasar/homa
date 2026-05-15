# homa runtime activation

> **When all five live verifications (T1–T5) pass** on the operator's host:
> edit `~/nous/memories/homa/progress.md` and add `MVP_COMPLETE` as the
> first line. This is the operator's call — the build workflow does not
> mark it. All code paths are tested and gated behind `use_podman`; this
> sentinel keeps "all unit tests green" from being mistaken for "shipped".

Phases 4 + 6 + 7 are **code-complete + mocked**. The real provisioner (git
worktree + Podman + Tailscale Serve), the idle-sandbox GC, and the
multi-user isolation guarantees live behind interfaces in
`orchestrator/internal/{sandbox,tsserve,worktree,provision,lifecycle,integration}`.
Unit + integration tests cover argv shapes, ordering, tear-down, GC tick
logic, and multi-user cookie/proxy scoping without needing podman /
tailscale on the test host.

The **live** acceptance — actually starting a sandbox container, observing
idle GC kill it, observing login bring it back — is **BLOCKED** until podman
is installed. The steps below flip everything on.

---

## 0. Initialise the site template (once, after fresh clone)

`site-template/` is the canonical `main` branch every user's worktree
forks from. It ships as plain files; turn it into a git repo:

```bash
cd ~/homa/site-template
git init -b main
git add .
git -c user.email=homa@local -c user.name=homa commit -m "template: initial SvelteKit scaffold"
```

`git worktree add` (used by the provisioner) requires this; without it,
the first signup fails with "not a git repository".

## 1. Install podman + rootless prerequisites

```bash
sudo apt-get update
sudo apt-get install -y podman uidmap slirp4netns fuse-overlayfs
```

Optional (for working `/me preview_url`): `sudo apt-get install -y tailscale`
and `tailscale up`.

## 2. Build the sandbox image

```bash
bash ~/homa/sandbox/build.sh
```

Compiles `~/nous` as a static `linux/amd64` ELF, runs
`podman build -t homa-sandbox:latest …`. The image carries the nous git
short-rev as `org.opencontainers.image.revision`.

## 3. Configure the orchestrator

`~/homa/config.json`:

```json
{
  "use_podman": true,
  "site_template_dir": "site-template",
  "image_ref": "homa-sandbox:latest",
  "anthropic_api_key": "$ANTHROPIC_API_KEY",
  "preview_base_url": "https://homa.tailnet.ts.net",
  "container_memory": "2g",
  "container_cpus": "2"
}
```

- **`$ANTHROPIC_API_KEY`** is expanded at startup via `config.ExpandSecret`
  (matches nous's pattern). Missing → empty → sandbox API calls fail until
  set. Log warns at startup.
- **`idle_after_minutes`** (default `30`) and **`gc_interval_seconds`**
  (default `60`) match mvp.md §16. Set negative to disable GC entirely
  (tests + debugging).
- **Restart safety**: on startup, the orchestrator reads every `nous_port`,
  `preview_port`, and `preview_serve_port` from the users table and seeds
  the PortAllocator past the max. No collisions on second-user-after-restart.
  Verified by `scripts/restart_safety_test.sh`.

Restart `homa`. Log lines:

```
level=INFO msg="port allocator seeded" users_in_db=N max_host_port_seen=… max_serve_port_seen=…
level=INFO msg=provisioner kind=podman …
level=INFO msg="lifecycle gc started" idle_after=30m0s interval=1m0s
```

## 4. T1 acceptance — provisioning

```bash
curl -sS -c cookies.txt -X POST -H 'Content-Type: application/json' \
  -d '{"email":"t1@x.io","password":"hunter22"}' \
  http://localhost:8080/signup
```

Verify:
- `podman ps` shows `homa-user-<userid>` running.
- `~/homa/branches/<userid>/` exists.
- SQLite `users` row has `nous_port`, `preview_port`, `preview_serve_port` populated.
- `tailscale serve status` lists the user's HTTPS port (if tailscale is up).

Any failure during signup runs reverse-order tear-down automatically
(tailscale unregister → podman stop → git worktree remove). The error
returned to the client is the root cause; `errors.Is` reaches it.

## 5. T4 acceptance — GC + respawn (Phase 6)

Add to `config.json` for a fast cycle:

```json
{
  "idle_after_minutes": 1,
  "gc_interval_seconds": 5
}
```

Sign up a user. Wait ~70s without opening the editor. Then:

```bash
podman ps          # → homa-user-<userid> NOT in the list (GC stopped it)
podman ps -a       # → still absent (run --rm cleans it up)
```

Log into the same account:

```bash
curl -sS -c cookies.txt -X POST -H 'Content-Type: application/json' \
  -d '{"email":"t1@x.io","password":"hunter22"}' \
  http://localhost:8080/login
podman ps          # → homa-user-<userid> back in the list
```

Login calls `EnsureRunning`, which is idempotent (no port reallocation —
the worktree, tsserve mapping, and user row all persist).

## 6. T5 acceptance — two-user isolation (Phase 7)

This is the user-visible proof that the per-user sandbox boundary holds —
two users editing concurrently never see each other's work.

1. Sign up two users via the editor SPA (or curl):

   ```bash
   curl -sS -c a.txt -X POST -H 'Content-Type: application/json' \
     -d '{"email":"a@your.com","password":"hunter22"}' \
     http://localhost:8080/signup
   curl -sS -c b.txt -X POST -H 'Content-Type: application/json' \
     -d '{"email":"b@your.com","password":"hunter22"}' \
     http://localhost:8080/signup
   ```

2. Two distinct containers + non-overlapping ports:

   ```bash
   podman ps --format '{{.Names}}\t{{.Ports}}'
   # → homa-user-<id-a>   127.0.0.1:40000->9000/tcp, 127.0.0.1:40001->5173/tcp
   # → homa-user-<id-b>   127.0.0.1:40002->9000/tcp, 127.0.0.1:40003->5173/tcp
   ```

3. Two worktrees on disk:

   ```bash
   ls ~/homa/branches/
   # → <id-a>  <id-b>
   ```

4. Open user A's editor. Ask the LLM:

   > "Change the homepage title to `Hello A`."

   Wait for the file to be saved and Vite HMR to update A's iframe.

5. Open user B's preview URL **in an incognito window** (so B's cookie
   scope is separate). Confirm the title is still the SvelteKit default
   — **NOT** `Hello A`.

If step 5 shows `Hello A` in user B's preview, the per-user sandbox
boundary is broken and Phase 7's isolation guarantees regressed; do not
mark `MVP_COMPLETE`.

## Why this is gated rather than auto-detected

Phase 4/6 ship behind `use_podman` rather than `if podman --version succeeds`
so that:

1. Phase 5 e2e (`scripts/e2e_phase5.sh`) keeps using the deterministic
   `StubProvisioner` regardless of host state.
2. An accidentally-installed podman doesn't silently start spawning
   containers on every signup; the flip is explicit and reviewable.
3. GC is a destructive operation (`podman stop`). Tying it to the same
   flag means you opt in to both at once.
