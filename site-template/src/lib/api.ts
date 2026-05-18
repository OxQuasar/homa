/**
 * Shared HTTP helper for the homa backend.
 *
 * - Always uses `credentials: 'include'` (cookie session auth).
 * - Throws `ApiError` on non-2xx so call sites can branch on `.status`
 *   (e.g. 401 → prompt to log in).
 */

export const API_ORIGIN = 'https://gandiva.kingfisher-celsius.ts.net';

export class ApiError extends Error {
  constructor(public status: number, public body: string) {
    super(`API ${status}${body ? `: ${body}` : ''}`);
    this.name = 'ApiError';
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(`${API_ORIGIN}${path}`, {
    credentials: 'include',
    ...init,
    headers,
  });
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new ApiError(res.status, body);
  }
  // Some POST endpoints may return 204 with no body; treat as null.
  if (res.status === 204) return null as T;
  return (await res.json()) as T;
}
