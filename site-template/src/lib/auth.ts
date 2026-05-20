// Client-side auth helpers for the public site.
//
// In production the orchestrator serves SvelteKit + APIs on the same
// origin; in dev (vite) the SvelteKit page lives on a different origin
// than the orchestrator. To make /me work in both contexts we route it
// through src/lib/api.ts, which prepends API_ORIGIN and includes
// credentials — the same path used by forum.ts / users.ts.
//
// Pages that need auth wrap themselves via their directory's
// +layout.svelte calling fetchMe() and gating the children — see
// src/routes/forum/+layout.svelte for the canonical example.

import { api } from './api';

export interface AuthState {
  authed: boolean;
  username?: string;
  user_id?: string;
}

// fetchMe returns the caller's auth state via /me. 200 → authed=true
// with identity fields; anything else (401, network, 5xx, CORS, etc.)
// → authed=false. Treats all failures as anonymous so the gate UI is
// uniform.
export async function fetchMe(): Promise<AuthState> {
  try {
    const m = await api<{ username?: string; user_id?: string }>('/me');
    return { authed: true, username: m.username, user_id: m.user_id };
  } catch {
    /* 401 / network / parse — treat as anonymous */
  }
  return { authed: false };
}
