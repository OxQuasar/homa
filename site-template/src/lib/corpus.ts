// Server-side helpers for browsing a markdown corpus mounted under
// /library/*. Used by I Ching research and MEV research routes.
//
// Each corpus is a read-only directory tree of markdown (+ a few svg/txt
// assets). The route uses a [...path] catch-all and resolves one of three
// kinds:
//   - 'dir' : list children (filtered) + optional directory.md readme
//   - 'md'  : single markdown file (rendered client-side via marked)
//   - 'raw' : svg/txt asset rendered as-is
//
// Paths are sanitized against traversal. The `.md` extension is hidden
// from URLs: `/.../deep/open-questions` resolves to `deep/open-questions.md`.

import { error } from '@sveltejs/kit';
import fs from 'node:fs/promises';
import path from 'node:path';

// File extensions hidden from directory listings (analysis artifacts,
// not reading material). Adjust later if we want a "show all" toggle.
const HIDDEN_EXTS = new Set(['.py', '.npz', '.json', '.tsv', '.csv', '.parquet']);

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
 * Resolve a user-supplied relative path to an absolute filesystem path
 * under `root`. Throws 400 on traversal attempts.
 */
function resolveSafe(root: string, rel: string): string {
  const segments = rel.split('/').filter(Boolean);
  if (segments.some((s) => s === '..' || s === '.' || s.startsWith('.'))) {
    error(400, 'invalid path');
  }
  const joined = path.join(root, ...segments);
  // Belt-and-suspenders: ensure the resolved path is inside root.
  if (joined !== root && !joined.startsWith(root + path.sep)) {
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

/**
 * Load a path under `root`, returning either a directory listing, a
 * markdown doc, or a raw asset. If `authed` is false, returns an empty
 * directory listing — caller's layout should render a gate.
 *
 * @param root      absolute filesystem root (e.g. /library/iching)
 * @param relPath   url-supplied relative path (params.path)
 * @param authed    whether the visitor is logged in
 */
export async function loadCorpus(
  root: string,
  relPath: string | undefined,
  authed: boolean,
): Promise<LoadResult> {
  if (!authed) {
    return { kind: 'dir', path: '', segments: [], entries: [] };
  }

  const rel = (relPath ?? '').replace(/\/+$/, '');
  const segments = rel ? rel.split('/') : [];
  const abs = resolveSafe(root, rel);

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
    // at the top of the listing.
    let readme: string | undefined;
    for (const candidate of ['directory.md', 'README.md', 'readme.md']) {
      try {
        readme = await fs.readFile(path.join(abs, candidate), 'utf-8');
        break;
      } catch { /* try next */ }
    }

    return { kind: 'dir', path: rel, segments, entries, readme };
  }

  // 2. Try exact file (e.g. svg, txt referenced by URL).
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
}
