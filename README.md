# Homa

Multi-user LLM-driven website builder. Each user signs up, gets a sandboxed environment (Podman container running nous + a SvelteKit dev server), and builds their site by chatting with the LLM.

## Specifications

- **Spec (executable):** [`~/nous/memories/homa/mvp.md`](../nous/memories/homa/mvp.md)
- **Design rationale:** [`~/nous/memories/homa/homa.md`](../nous/memories/homa/homa.md)
- **Build workflow:** `~/nous/logos/homa-build/LOGOS.yaml`

## Layout (target — see `mvp.md` §4)

```
~/homa/
├── orchestrator/    Go service: auth, user store, sandbox mgr, reverse proxy
├── editor/          Vite + Svelte SPA (built static, served by orchestrator)
├── sandbox/         Containerfile + entrypoint for per-user sandbox
├── site-template/   `main` branch — SvelteKit scaffold users fork from
├── branches/        Worktrees per user (gitignored)
└── data/            SQLite, etc. (gitignored)
```

## Status

Empty. Build proceeds via the `homa-build` LOGOS workflow.
