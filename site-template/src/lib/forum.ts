/**
 * Typed wrappers for the homa forum API (/api/forum/*).
 */

import { api, ApiError } from './api';
export { ApiError };

const BASE = '/api/forum';

export interface Topic {
  id: number | string;
  title: string;
  author_name: string;
  post_count: number;
  created_at?: string;
}

export interface Post {
  id: number | string;
  author_name: string;
  body: string;
  created_at?: string;
}

export const listTopics = () => api<Topic[]>(`${BASE}/topics`);

export const createTopic = (title: string, body: string) =>
  api<Topic>(`${BASE}/topics`, {
    method: 'POST',
    body: JSON.stringify({ title, body }),
  });

export const listPosts = (topicId: string) =>
  api<Post[]>(`${BASE}/topics/${encodeURIComponent(topicId)}/posts`);

export const createPost = (topicId: string, body: string) =>
  api<Post>(`${BASE}/topics/${encodeURIComponent(topicId)}/posts`, {
    method: 'POST',
    body: JSON.stringify({ body }),
  });
