// REST helpers against the orchestrator's auth endpoints.
// Cookies travel automatically via `credentials: 'include'`.

export interface MeResponse {
  user_id: string;
  email: string;
  preview_url: string;
}

export interface ApiError extends Error {
  status: number;
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'include' };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(path, init);
  const text = await resp.text();
  if (!resp.ok) {
    let msg = resp.statusText;
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed.error === 'string') msg = parsed.error;
    } catch { /* not JSON; fall back to statusText */ }
    const err = new Error(msg) as ApiError;
    err.status = resp.status;
    throw err;
  }
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

export function signup(email: string, password: string, name?: string) {
  return call<{ user_id: string }>('POST', '/signup', { email, password, name });
}

export function login(email: string, password: string) {
  return call<{ user_id: string }>('POST', '/login', { email, password });
}

export function logout() {
  return call<void>('POST', '/logout');
}

export function me() {
  return call<MeResponse>('GET', '/me');
}
