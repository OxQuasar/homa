// Server-side gate for the MEV research browser.
// See $lib/corpusAuth.ts for the rationale.

import { corpusAuthLoad } from '$lib/corpusAuth';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = (event) => corpusAuthLoad(event);
