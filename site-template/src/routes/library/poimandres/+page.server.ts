// Server load for the Poimandres reader.
//
// The text lives on a read-only bind at /library/texts/poimandres.txt
// (host-managed, outside the repo). We read it once per request and
// hand the raw string to the page for client-side parsing.

import { error } from '@sveltejs/kit';
import fs from 'node:fs/promises';
import type { PageServerLoad } from './$types';

const SOURCE = '/library/texts/poimandres.txt';

export const load: PageServerLoad = async () => {
  try {
    const raw = await fs.readFile(SOURCE, 'utf-8');
    return { raw };
  } catch {
    error(404, 'Poimandres text not found');
  }
};
