<script lang="ts">
  // Forum topic page — shows replies (newest first) + reply form.
  // Mirrors the auth/origin model in /forum/+page.svelte.

  import { onMount } from 'svelte';
  import { page } from '$app/state';

  const API_ORIGIN = 'https://gandiva.kingfisher-celsius.ts.net';

  interface Post {
    id: number;
    topic_id: number;
    author_id: string;
    author_name: string;
    content: string;
    created_at: number;
  }

  const topicId = $derived(parseInt(page.params.topicId, 10));
  let posts = $state<Post[]>([]);
  let loaded = $state(false);
  let authed = $state(true);
  let notFound = $state(false);
  let error = $state<string | null>(null);
  let newContent = $state('');
  let submitting = $state(false);

  async function load() {
    try {
      const r = await fetch(`${API_ORIGIN}/api/forum/topics/${topicId}/posts`, {
        credentials: 'include'
      });
      if (r.status === 401) { authed = false; loaded = true; return; }
      if (r.status === 404) { notFound = true; loaded = true; return; }
      if (!r.ok) throw new Error(await r.text());
      posts = await r.json();
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loaded = true;
    }
  }

  async function createPost(e: SubmitEvent) {
    e.preventDefault();
    if (!newContent.trim()) return;
    submitting = true;
    error = null;
    try {
      const r = await fetch(`${API_ORIGIN}/api/forum/topics/${topicId}/posts`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: newContent.trim() })
      });
      if (!r.ok) throw new Error(await r.text());
      const created: Post = await r.json();
      posts = [created, ...posts];
      newContent = '';
    } catch (e) {
      error = (e as Error).message;
    } finally {
      submitting = false;
    }
  }

  // ISO-ish display in user's local time. UTC stored server-side.
  function fmt(ts: number): string {
    return new Date(ts * 1000).toLocaleString();
  }

  onMount(load);
</script>

<svelte:head><title>Topic — Forum</title></svelte:head>

<main>
  <p><a href="/forum">← all topics</a></p>

  {#if !loaded}
    <p>Loading…</p>
  {:else if !authed}
    <p>You need to <a href={`${API_ORIGIN}/login`}>log in</a> to read this topic.</p>
  {:else if notFound}
    <p>Topic not found.</p>
  {:else}
    <form onsubmit={createPost}>
      <textarea
        bind:value={newContent}
        placeholder="Write a reply…"
        rows="3"
        disabled={submitting}
      ></textarea>
      <button type="submit" disabled={submitting || !newContent.trim()}>
        {submitting ? '…' : 'Reply'}
      </button>
    </form>
    {#if error}<div class="error">{error}</div>{/if}

    {#if posts.length === 0}
      <p class="empty">No replies yet — be the first.</p>
    {:else}
      <ul class="posts">
        {#each posts as p (p.id)}
          <li>
            <header>
              <span class="author">{p.author_name}</span>
              <time>{fmt(p.created_at)}</time>
            </header>
            <div class="body">{p.content}</div>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</main>

<style>
  main { max-width: 720px; margin: 2rem auto; padding: 0 1rem; font-family: system-ui; }
  form { display: flex; flex-direction: column; gap: 0.5rem; margin: 1rem 0 1.5rem; }
  textarea { padding: 0.5rem; font-size: 1rem; font-family: inherit; }
  button { padding: 0.5rem 1rem; align-self: flex-start; }
  .error { color: #a00; padding: 0.5rem 0; }
  .empty { color: #888; }
  ul.posts { list-style: none; padding: 0; }
  ul.posts li { padding: 0.8rem 0; border-bottom: 1px solid #eee; }
  ul.posts header { display: flex; gap: 0.75rem; align-items: baseline; font-size: 0.85rem; color: #666; margin-bottom: 0.3rem; }
  ul.posts .author { font-weight: 600; color: #333; }
  ul.posts .body { white-space: pre-wrap; }
</style>
