# House Rules

- Use Svelte 5 runes (`$state`, `$derived`, `$props`). Do NOT use `export let` or `$:` reactive statements.
- Dev server is running with HMR; just save files.
- Do not modify `vite.config.ts`, `svelte.config.js`, or `package.json` dependencies unless explicitly asked.
- All time UTC.

# Working in this sandbox

This is `/workspace`, a git worktree of homa's `site-template` repo.
Your branch is `user/<userid>`. The `.git` is shared with `main` (the
public site), so all branches are visible.

To get your userid programmatically:

```bash
git branch --show-current | sed 's|^user/||'
```

## Committing your work

Your work goes on `user/<userid>`. Just commit normally:

```bash
git add -A
git commit -m "<descriptive message>"
```

Author identity is already set in the git config.

## Opening a PR for review

The operator reviews + merges PRs into `main` via the `homa pr` CLI.
You stage work for review by creating a branch named:

```
pr/<userid>/<topic>
```

where `<topic>` is `[a-zA-Z0-9._-]+` (URL-safe, no spaces, no slashes).

### Recipe

```bash
USERID=$(git branch --show-current | sed 's|^user/||')
TOPIC="dark-mode"   # name your PR — short, kebab-or-snake-case

# 1. Commit to user/<userid> first (canonical home for your work)
git add -A
git commit -m "<descriptive message>"

# 2. Snapshot HEAD into a PR branch — same commits, different ref
git branch pr/$USERID/$TOPIC
```

Stay on `user/<userid>` after — that's home base.

### Why commit to user/<userid> first

The user branch is the canonical home for all your work. The PR branch
is a named pointer to a commit. Committing to user first keeps user /
main alignment clean across multiple PRs.

### What the operator does next

You don't need to do anything once you've created the PR branch. The
operator runs `homa pr list` (sees yours), `homa pr show <branch>`
(reviews the diff), and `homa pr merge <branch>` (promotes to main).

## Site structure

SvelteKit + TS + Svelte 5. Public routes:

- `/` — homepage (entrance gate, hero)
- `/enter` — courtyard for authenticated visitors
- `/forum`, `/forum/[topicId]` — discussion threads
- `/users` — people directory
- `/library`, `/library/[text]` — long-form content

Helpers in `src/lib/`:
- `Hero.svelte` — fullscreen image + title + CTA pattern
- `GuardEncounter.svelte` — typewriter-speech overlay (cloaked sentinel),
  used for narrative entry + auth gates
- `forum.ts`, `messages.ts`, `users.ts`, `auth.ts` — API client wrappers
  for the orchestrator's `/api/...` endpoints (all same-origin, cookie-authed)

## Auth-gated pages convention

A page that requires login wraps itself via its directory's
`+layout.svelte`, using `fetchMe()` from `$lib/auth` to check, and
rendering `GuardEncounter` with sign-up / log-in actions for anonymous
visitors.

Canonical example: `src/routes/forum/+layout.svelte`. Copy that file
into any directory you want to gate (`library/+layout.svelte`, etc.) —
change the speech text + adjust action labels as needed.

`/signup` and `/login` are SPA routes served by the orchestrator (same
origin), so anchor tags like `<a href="/signup">` route there directly.

## What NOT to do

- Don't merge anything into `main` yourself — main is operator-controlled.
  Stage via PR branches.
- Don't `git checkout main` — locked by another worktree; git refuses.
- Don't `git push` — no remote configured. All operations are local.
- Don't add binary files larger than ~10MB without operator approval
  (shared git repo).

## API endpoints (same-origin, no CORS needed)

From SvelteKit pages, use relative URLs with `credentials: 'include'`:

```
GET  /me                              auth check
POST /signup                          create user (SPA flow)
POST /login                           log in
POST /logout                          log out

GET  /api/forum/topics                list forum topics
POST /api/forum/topics                create topic     {title}
GET  /api/forum/topics/{id}/posts     thread of replies
POST /api/forum/topics/{id}/posts     reply            {content}

GET  /api/users                       people directory

GET  /api/messages/conversations      DM conversation list
GET  /api/messages/unread-count       unread badge
GET  /api/messages/with/{userId}      thread (oldest first; marks read)
POST /api/messages/with/{userId}      send DM          {content}
```

All cookie-required except `/signup` + `/login`.
