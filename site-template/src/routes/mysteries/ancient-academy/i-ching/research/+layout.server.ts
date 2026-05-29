// Server-side gate for the research browser.
//
// Why this exists (and the forum gate doesn't have an equivalent):
// research/[...path]/+page.server.ts reads markdown from /library/iching
// during SSR and embeds it in the page payload. A purely client-side
// gate (the +layout.svelte) would still ship that data to anonymous
// visitors in the SSR HTML — they'd see the GuardEncounter on screen
// while view-source revealed the corpus. This load runs first, sets
// `authed`, and the child +page.server.ts skips its filesystem read
// when authed is false.

import { API_ORIGIN } from '$lib/api';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async ({ request, fetch }) => {
  // Forward the visitor's cookie through to /me. event.fetch keeps
  // cookies for same-origin only; the orchestrator lives at a
  // different port in dev so we attach the header explicitly.
  const cookie = request.headers.get('cookie') ?? '';
  let authed = false;
  try {
    const res = await fetch(`${API_ORIGIN}/me`, { headers: { cookie } });
    authed = res.ok;
  } catch {
    /* network / CORS — treat as anonymous */
  }
  return { authed };
};
