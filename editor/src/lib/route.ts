// Pure route resolver.
//
// Hash takes precedence when it carries a recognised route — the
// `location.hash = '#/editor'` redirect from the login/signup forms keeps
// working on the same SPA load.
// Pathname is the fallback so direct navigation (`/editor`, `/signup`) and
// the orchestrator's path-based 302s render the right component.
// Anything we don't recognise falls through to 'login'.

export type Route = 'signup' | 'login' | 'editor' | 'admin' | 'guidelines' | 'forgot' | 'account';

const ROUTES: Record<string, Route> = {
  '/signup': 'signup',
  '/login': 'login',
  '/editor': 'editor',
  '/admin': 'admin',
  '/guidelines': 'guidelines',
  '/forgot': 'forgot',
  '/account': 'account'
};

export function parseRoute(pathname: string, hash: string): Route {
  const h = hash.replace(/^#/, '');
  return ROUTES[h] ?? ROUTES[pathname] ?? 'login';
}
