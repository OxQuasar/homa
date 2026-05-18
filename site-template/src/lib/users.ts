/**
 * Typed wrapper for the homa users API (/api/users).
 *
 * Response shape per the People Directory contract:
 *   [{ user_id: "77b4cf0e", username: "quasar", created_at: 1778868569 }, ...]
 *
 * - Sorted by `created_at` ascending (server-side).
 * - Rows with empty `username` are excluded server-side.
 * - No email, no freeform name. Username is the public identity.
 */

import { api } from './api';

export interface User {
  user_id: string;
  username: string;
  created_at: number; // unix seconds UTC
}

export const listUsers = () => api<User[]>('/api/users');

export function displayName(u: User): string {
  return u.username || `User ${u.user_id}`;
}
