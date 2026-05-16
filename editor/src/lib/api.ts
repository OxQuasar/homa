// REST helpers against the orchestrator's auth endpoints.
// Cookies travel automatically via `credentials: 'include'`.

export interface MeResponse {
  user_id: string;
  email: string;
  preview_url: string;
  // Pinned nous session id; forwarded to the sandbox in the WS Hello so
  // every connect attaches to the same session.
  nous_session_id: string;
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

export interface UploadResponse {
  // Path inside the user's worktree — what the LLM should reference
  // when it reads/uses the file. e.g. "static/uploads/foo.jpg".
  path: string;
  // Browser-facing URL the running site serves it from. e.g. "/uploads/foo.jpg".
  public_path: string;
  size: number;
}

// upload posts a single file as multipart to POST /upload. Returns the
// landed path (which may differ from the local filename if it collided
// with an existing upload). Errors include 413 (over size limit) and
// 401 (cookie missing/stale) — surface them as ApiError with .status.
export async function upload(file: File): Promise<UploadResponse> {
  const form = new FormData();
  form.append('file', file, file.name);
  const resp = await fetch('/upload', {
    method: 'POST',
    credentials: 'include',
    body: form
  });
  const text = await resp.text();
  if (!resp.ok) {
    let msg = resp.statusText;
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed.error === 'string') msg = parsed.error;
    } catch { /* not JSON */ }
    const err = new Error(msg) as ApiError;
    err.status = resp.status;
    throw err;
  }
  return JSON.parse(text) as UploadResponse;
}
