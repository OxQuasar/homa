<script lang="ts">
  // Forum index — lists topics, lets logged-in users create new ones.
  // Auth-required for both read + write; renders an "log in" prompt
  // when /api/forum/topics returns 401.
  //
  // API origin: HARDCODED to the orchestrator. This page is loaded from
  // either the public main site (same-origin) or a user's iframe-served
  // preview (cross-origin). Using an absolute URL works for both; CORS
  // is handled by the orchestrator. Replace API_ORIGIN below for your
  // own deployment.

  import { onMount } from 'svelte';

  const API_ORIGIN = 'https://gandiva.kingfisher-celsius.ts.net';

  interface Topic {
    id: number;
    title: string;
    author_id: string;
    author_name: string;
    created_at: number;
    post_count: number;
  }

  let topics = $state<Topic[]>([]);
  let loaded = $state(false);
  let authed = $state(true);  // optimistic; flips to false on 401
  let error = $state<string | null>(null);
  let newTitle = $state('');
  let submitting = $state(false);

  async function load() {
    try {
      const r = await fetch(`${API_ORIGIN}/api/forum/topics`, { credentials: 'include' });
      if (r.status === 401) { authed = false; loaded = true; return; }
      if (!r.ok) throw new Error(await r.text());
      topics = await r.json();
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loaded = true;
    }
  }

  async function createTopic(e: SubmitEvent) {
    e.preventDefault();
    if (!newTitle.trim()) return;
    submitting = true;
    error = null;
    try {
      const r = await fetch(`${API_ORIGIN}/api/forum/topics`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: newTitle.trim() })
      });
      if (!r.ok) throw new Error(await r.text());
      const created: Topic = await r.json();
      topics = [created, ...topics];
      newTitle = '';
    } catch (e) {
      error = (e as Error).message;
    } finally {
      submitting = false;
    }
  }

  onMount(load);
</script>

<svelte:head><title>Forum</title></svelte:head>

<main>
  <h1>Forum</h1>

  {#if !loaded}
    <p>Loading…</p>
  {:else if !authed}
    <p>You need to <a href={`${API_ORIGIN}/login`}>log in</a> to see the forum.</p>
  {:else}
    <form onsubmit={createTopic}>
      <input
        type="text"
        bind:value={newTitle}
        placeholder="Start a new topic…"
        required
        disabled={submitting}
      />
      <button type="submit" disabled={submitting || !newTitle.trim()}>
        {submitting ? '…' : 'Create'}
      </button>
    </form>

    {#if error}<div class="error">{error}</div>{/if}

    {#if topics.length === 0}
      <p class="empty">No topics yet — start one!</p>
    {:else}
      <ul class="topics">
        {#each topics as t (t.id)}
          <li>
            <a href={`/forum/${t.id}`}>{t.title}</a>
            <span class="meta">
              by {t.author_name} • {t.post_count} {t.post_count === 1 ? 'reply' : 'replies'}
            </span>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</main>

<style>
  main { max-width: 720px; margin: 2rem auto; padding: 0 1rem; font-family: system-ui; }
  h1 { font-weight: 400; }
  form { display: flex; gap: 0.5rem; margin: 1.5rem 0; }
  input { flex: 1; padding: 0.5rem; font-size: 1rem; }
  button { padding: 0.5rem 1rem; }
  .error { color: #a00; padding: 0.5rem 0; }
  .empty { color: #888; }
  ul.topics { list-style: none; padding: 0; }
  ul.topics li { padding: 0.6rem 0; border-bottom: 1px solid #eee; }
  ul.topics a { font-weight: 600; color: #1f6feb; text-decoration: none; }
  .meta { color: #888; font-size: 0.85rem; margin-left: 0.5rem; }
</style>
