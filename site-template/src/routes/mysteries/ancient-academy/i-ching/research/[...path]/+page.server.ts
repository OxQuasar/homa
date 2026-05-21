// Server load for the I Ching research browser.
//
// Reads from /library/iching (a read-only bind mount). One catch-all
// route handles three cases via `params.path`:
//   - empty  → landing: render `directory.md` + list root entries
//   - dir    → list children (filtered)
//   - file   → return raw content (markdown rendered client-side)
//
// URLs hide the `.md` extension: `/research/deep/open-questions` resolves
// to `/library/iching/deep/open-questions.md`. Other extensions (.svg,
// .png, .txt) match by exact filename.

import { error } from '@sveltejs/kit';
import fs from 'node:fs/promises';
import path from 'node:path';
import type { PageServerLoad } from './$types';

const ROOT = '/library/iching';

// File extensions hidden from directory listings (analysis artifacts,
// not reading material). Adjust later if we want a "show all" toggle.
const HIDDEN_EXTS = new Set(['.py', '.npz', '.json', '.tsv']);

// File extensions to surface in listings as readable docs.
// PNG omitted: this route returns text content, not binary. Figures
// referenced by markdown won't resolve until a separate static endpoint
// is added. Out of scope for the plumbing pass.
const VISIBLE_EXTS = new Set(['.md', '.txt', '.svg']);

export type Entry = {
  name: string;        // base name
  href: string;        // relative URL slug (no .md extension)
  kind: 'dir' | 'doc' | 'asset';
  ext?: string;        // for non-md docs/assets, surface the extension
};

export type LoadResult =
  | { kind: 'dir'; path: string; segments: string[]; entries: Entry[]; readme?: string }
  | { kind: 'md';  path: string; segments: string[]; content: string }
  | { kind: 'raw'; path: string; segments: string[]; content: string; ext: string };

/**
 * Resolve a user-supplied path to an absolute filesystem path under ROOT.
 * Throws 400 on traversal attempts.
 */
function resolveSafe(rel: string): string {
  const segments = rel.split('/').filter(Boolean);
  if (segments.some((s) => s === '..' || s === '.' || s.startsWith('.'))) {
    error(400, 'invalid path');
  }
  const joined = path.join(ROOT, ...segments);
  // Belt-and-suspenders: ensure the resolved path is inside ROOT.
  if (joined !== ROOT && !joined.startsWith(ROOT + path.sep)) {
    error(400, 'invalid path');
  }
  return joined;
}

async function listDir(absPath: string, urlBase: string): Promise<Entry[]> {
  const dirents = await fs.readdir(absPath, { withFileTypes: true });
  const entries: Entry[] = [];

  for (const d of dirents) {
    if (d.name.startsWith('.')) continue; // hidden files

    if (d.isDirectory()) {
      entries.push({
        name: d.name,
        href: urlBase ? `${urlBase}/${d.name}` : d.name,
        kind: 'dir',
      });
      continue;
    }

    const ext = path.extname(d.name).toLowerCase();
    if (HIDDEN_EXTS.has(ext)) continue;
    if (!VISIBLE_EXTS.has(ext)) continue;

    if (ext === '.md') {
      const stem = d.name.slice(0, -3);
      entries.push({
        name: stem,
        href: urlBase ? `${urlBase}/${stem}` : stem,
        kind: 'doc',
      });
    } else {
      entries.push({
        name: d.name,
        href: urlBase ? `${urlBase}/${d.name}` : d.name,
        kind: 'asset',
        ext,
      });
    }
  }

  // Sort: directories first, then by name (case-insensitive).
  entries.sort((a, b) => {
    if (a.kind === 'dir' && b.kind !== 'dir') return -1;
    if (a.kind !== 'dir' && b.kind === 'dir') return 1;
    return a.name.localeCompare(b.name);
  });

  return entries;
}

export const load: PageServerLoad = async ({ params }): Promise<LoadResult> => {
  const rel = (params.path ?? '').replace(/\/+$/, '');
  const segments = rel ? rel.split('/') : [];
  const abs = resolveSafe(rel);

  // 1. Try as a directory.
  let stat;
  try {
    stat = await fs.stat(abs);
  } catch {
    stat = null;
  }

  if (stat?.isDirectory()) {
    const entries = await listDir(abs, rel);

    // If this directory has a directory.md (or readme.md), preview it
    // at the top of the listing. The root /library/iching/directory.md
    // is the canonical TOC.
    let readme: string | undefined;
    for (const candidate of ['directory.md', 'README.md', 'readme.md']) {
      try {
        readme = await fs.readFile(path.join(abs, candidate), 'utf-8');
        break;
      } catch { /* try next */ }
    }

    return { kind: 'dir', path: rel, segments, entries, readme };
  }

  // 2. Try exact file (e.g. svg, png referenced by URL).
  if (stat?.isFile()) {
    const ext = path.extname(abs).toLowerCase();
    if (ext === '.md') {
      const content = await fs.readFile(abs, 'utf-8');
      return { kind: 'md', path: rel, segments, content };
    }
    if (VISIBLE_EXTS.has(ext)) {
      const content = await fs.readFile(abs, 'utf-8');
      return { kind: 'raw', path: rel, segments, content, ext };
    }
    error(404, 'not a readable file');
  }

  // 3. Try with .md appended (the common case — hidden extension).
  try {
    const mdAbs = abs + '.md';
    const content = await fs.readFile(mdAbs, 'utf-8');
    return { kind: 'md', path: rel, segments, content };
  } catch {
    error(404, 'not found');
  }
};
