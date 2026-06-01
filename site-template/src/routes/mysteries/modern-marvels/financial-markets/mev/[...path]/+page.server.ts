// Server load for the MEV research browser. Thin wrapper around
// $lib/corpus.ts's loadCorpus, pinned to /library/mev.

import { loadCorpus, type LoadResult } from '$lib/corpus';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ params, parent }): Promise<LoadResult> => {
  const { authed } = await parent();
  return loadCorpus('/library/mev', params.path, authed);
};
