# homa editor SPA

Vite + Svelte 5 (runes-only) frontend for the homa orchestrator. Two-pane
editor with chat on the left, iframe preview on the right. No SvelteKit —
this is a plain SPA, embedded into the Go binary at build time.

## Build

```bash
bash build.sh   # → orchestrator/internal/static/dist/
```

`vite.config.ts` points `build.outDir` at
`../orchestrator/internal/static/dist` so that `//go:embed all:dist` in
`orchestrator/internal/static/static.go` picks up the result on the next
`go build`. This means:

1. SPA changes are picked up by re-running `bash editor/build.sh` then
   re-running `go build ./...` in `orchestrator/`.
2. A stub `dist/index.html` is committed under
   `orchestrator/internal/static/dist/` so `go build` works *before* the
   SPA has ever been built (e.g. on a fresh CI checkout).

## Dev

```bash
npm install
npm run dev          # http://localhost:5180/
```

Vite's `server.proxy` forwards `/signup`, `/login`, `/logout`, `/me`, and
`/ws` (with `ws: true`) to `http://localhost:8080` — run the orchestrator
there separately for a hot-reload dev experience.

## Routes

- `/#/signup` — signup form
- `/#/login` — login form (default landing)
- `/#/editor` — two-pane chat + iframe (requires cookie)

Hash routing avoids HTML5 history complications behind the orchestrator's
catch-all routes. Real users land at `/signup`, `/login`, `/editor` on the
orchestrator (which serves `index.html`); the SPA reads `location.hash` to
pick the route.

## House rules

- Svelte 5 runes only (`$state`, `$derived`, `$props`, `$effect`).
- No `export let`, no `$:`.
- All time UTC.
