import { describe, expect, it } from 'vitest';
import { parseRoute, type Route } from './route';

// 5 pathnames × 5 hashes = 25 cases.
const PATHNAMES = ['/', '/signup', '/login', '/editor', '/unknown'];
const HASHES = ['', '#/signup', '#/login', '#/editor', '#/unknown'];

// Expected matrix. Hash wins when it names a known route; else pathname.
// Unknown anything → 'login'.
const EXPECTED: Record<string, Route> = {
  // pathname=/ — pathname has no signal, hash decides
  '/|':              'login',
  '/|#/signup':      'signup',
  '/|#/login':       'login',
  '/|#/editor':      'editor',
  '/|#/unknown':     'login',
  // pathname=/signup
  '/signup|':              'signup',
  '/signup|#/signup':      'signup',
  '/signup|#/login':       'login',  // hash wins
  '/signup|#/editor':      'editor', // hash wins
  '/signup|#/unknown':     'signup',
  // pathname=/login
  '/login|':              'login',
  '/login|#/signup':      'signup', // hash wins
  '/login|#/login':       'login',
  '/login|#/editor':      'editor', // hash wins (post-form redirect path)
  '/login|#/unknown':     'login',
  // pathname=/editor
  '/editor|':              'editor',
  '/editor|#/signup':      'signup', // hash wins
  '/editor|#/login':       'login',  // hash wins
  '/editor|#/editor':      'editor',
  '/editor|#/unknown':     'editor',
  // pathname=/unknown
  '/unknown|':              'login',
  '/unknown|#/signup':      'signup',
  '/unknown|#/login':       'login',
  '/unknown|#/editor':      'editor',
  '/unknown|#/unknown':     'login'
};

describe('parseRoute', () => {
  for (const pathname of PATHNAMES) {
    for (const hash of HASHES) {
      const key = `${pathname}|${hash}`;
      const want = EXPECTED[key];
      it(`pathname=${JSON.stringify(pathname)} hash=${JSON.stringify(hash)} → ${want}`, () => {
        expect(parseRoute(pathname, hash)).toBe(want);
      });
    }
  }

  it('hash without leading "#" still resolves', () => {
    // Defensive: some callers might already strip the '#'. The function
    // tolerates either form.
    expect(parseRoute('/login', '/editor')).toBe('editor');
  });
});
