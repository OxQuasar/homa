// Client-side auth helpers for the public site. Same-origin with the
// orchestrator (gandiva.tailnet/), so /me / /signup / /login are all
// relative fetches with no CORS gymnastics.
//
// Pages that need auth wrap themselves via their directory's
// +layout.svelte calling fetchMe() and gating the children — see
// src/routes/forum/+layout.svelte for the canonical example.

export interface AuthState {
  authed: boolean;
  username?: string;
  user_id?: string;
}

// fetchMe returns the caller's auth state via /me. 200 → authed=true
// with identity fields; anything else (401, network, 5xx) → authed=false.
// Treats all failures as anonymous so the gate UI is uniform.
export async function fetchMe(): Promise<AuthState> {
  try {
    const r = await fetch('/me', { credentials: 'include' });
    if (r.ok) {
      const m = (await r.json()) as { username?: string; user_id?: string };
      return { authed: true, username: m.username, user_id: m.user_id };
    }
  } catch {
    /* network failure — treat as anonymous */
  }
  return { authed: false };
}
