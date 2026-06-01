// Shared auth gate for corpus-browser layouts.
//
// Why this exists: the catch-all +page.server.ts reads markdown from
// /library/* during SSR and embeds it in the page payload. A purely
// client-side gate would still ship that data to anonymous visitors
// in the SSR HTML — they'd see the GuardEncounter on screen while
// view-source revealed the corpus. This load runs first, sets
// `authed`, and the child +page.server.ts skips its filesystem read
// when authed is false.

import { API_ORIGIN } from '$lib/api';
import type { ServerLoadEvent } from '@sveltejs/kit';

export async function corpusAuthLoad(
  event: Pick<ServerLoadEvent, 'request' | 'fetch'>,
): Promise<{ authed: boolean }> {
  // Forward the visitor's cookie through to /me. event.fetch keeps
  // cookies for same-origin only; the orchestrator lives at a
  // different port in dev so we attach the header explicitly.
  const cookie = event.request.headers.get('cookie') ?? '';
  let authed = false;
  try {
    const res = await event.fetch(`${API_ORIGIN}/me`, { headers: { cookie } });
    authed = res.ok;
  } catch {
    /* network / CORS — treat as anonymous */
  }
  return { authed };
}
