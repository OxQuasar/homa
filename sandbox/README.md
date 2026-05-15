# homa-sandbox

Per-user container image: Node 22 + git + a baked `nous` binary + entrypoint.
One container per user; one user worktree bind-mounted at `/workspace`. See
`~/nous/memories/homa/mvp.md` §8 and §13 for the spec.

## Build

```bash
bash ~/homa/sandbox/build.sh
```

What it does:
1. Compiles `~/nous` as a CGO-disabled `linux/amd64` ELF into `./nous`.
2. Captures `git -C $NOUS_REPO rev-parse --short HEAD` as `NOUS_REV` and bakes
   it into the image as the OCI revision label.
3. Runs `podman build -t homa-sandbox:latest` (if podman is installed).

Environment overrides:
- `NOUS_REPO=...` — path to the nous source tree (default `~/nous`).
- `IMAGE_TAG=...` — image tag (default `homa-sandbox:latest`).

## Run (single sandbox, by hand)

```bash
USERID=test-phase3
WORKTREE=$HOME/homa/branches/$USERID
NOUS_PORT=49000
PREVIEW_PORT=45173

podman run -d --rm \
  --name homa-user-$USERID \
  -v $WORKTREE:/workspace:Z \
  -p 127.0.0.1:$NOUS_PORT:9000 \
  -p 127.0.0.1:$PREVIEW_PORT:5173 \
  -e ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  --memory=2g --cpus=2 \
  homa-sandbox:latest
```

When the orchestrator is launched with `use_podman: true` (see
`~/homa/RUNTIME.md`), signup orchestrates this `podman run` automatically.
To run a single sandbox by hand, prepare a worktree first:
`git -C ~/homa/site-template worktree add ~/homa/branches/$USERID -b user/$USERID main`.

Rootless Podman is the intended mode (mvp.md §2). The `:Z` mount option
relabels for SELinux and is a no-op on hosts without SELinux. If the
container can't write to the bind-mounted worktree because of UID mismatch
between host owner and the in-container `node` user, pass `--userns=keep-id`
on `podman run`. We deliberately do not bake a custom UID — that would force
host-side `chown` dances and a rebuild per host.

## Logs

```bash
podman logs -f homa-user-$USERID
```

Both Vite ("Local: http://0.0.0.0:5173/") and the nous daemon ("daemon
listening on ...") write to stdout/stderr.

## Cleanup

```bash
podman stop homa-user-$USERID
git -C ~/homa/site-template worktree remove ~/homa/branches/$USERID
```

The `--rm` on `podman run` removes the container on stop. The worktree
`remove` deletes the user's branch checkout but leaves the branch ref in the
template repo (delete that separately if you want a true tear-down).

## Probe

`~/homa/sandbox/probe/` is a small Go program that opens a WS, sends `Hello`,
and asserts the first event is an `EventSessionState` snapshot. Used by the
e2e script (with the `--cookie` flag, against the orchestrator's `/ws`) and
handy as a standalone health check against a bare sandbox:

```bash
go run ~/homa/sandbox/probe --addr ws://localhost:49000/ --workdir /workspace
```

## What's NOT here

- No memory/RAG sidecar config — the website-builder loop doesn't need it and
  the BGE-M3 embedder is heavy. If you need recall across sessions, add a
  `memory` block to `nous.config.json` and bake the rag_server.py + venv in.
- No baked secrets. `$ANTHROPIC_API_KEY` is resolved at daemon startup by
  nous's `resolveEnvVars`; the entrypoint fails fast if it's unset.
- No `node_modules` baked in. First container start runs `npm install`.
