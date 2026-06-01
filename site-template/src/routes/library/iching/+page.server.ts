// Server load for the I Ching reader.
//
// The text lives on the read-only bind at /library/texts/iching.json
// (host-managed, outside the repo). The JSON was extracted from
// duyijing.cn's data bundle — 64 hexagrams, each with:
//
//   id          - 6-bit binary (yang=1, yin=0), bottom line first
//   name        - 卦名 (e.g. 乾)
//   gua_ci      - 卦辞 (judgment)
//   tuan_ci     - 彖传 (judgment commentary)
//   da_xiang    - 大象 (image commentary)
//   yao_ci      - 爻辞 (line texts; 6 lines, 7 for 乾/坤 with 用九/用六)
//   xiao_xiang  - 小象 (per-line commentary, paired 1:1 with yao_ci)
//   symbol      - Unicode hexagram glyph (e.g. ䷀)

import { error } from '@sveltejs/kit';
import fs from 'node:fs/promises';
import type { PageServerLoad } from './$types';

const SOURCE = '/library/texts/iching.json';

export type Hexagram = {
  id: string;
  name: string;
  gua_ci: string;
  tuan_ci: string;
  da_xiang: string;
  yao_ci: string[];
  xiao_xiang: string[];
  symbol: string;
};

export const load: PageServerLoad = async () => {
  try {
    const raw = await fs.readFile(SOURCE, 'utf-8');
    const hexagrams = JSON.parse(raw) as Hexagram[];
    return { hexagrams };
  } catch {
    error(404, 'I Ching text not found');
  }
};
