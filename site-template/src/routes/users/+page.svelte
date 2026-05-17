<script lang="ts">
  // People directory — every registered user with their username.
  // Same auth + CORS posture as the forum: must be logged in.

  import { onMount } from 'svelte';

  const API_ORIGIN = 'https://gandiva.kingfisher-celsius.ts.net';

  interface UserSummary {
    user_id: string;
    username: string;
    created_at: number;
  }

  let users = $state<UserSummary[]>([]);
  let loaded = $state(false);
  let authed = $state(true);
  let error = $state<string | null>(null);

  async function load() {
    try {
      const r = await fetch(`${API_ORIGIN}/api/users`, { credentials: 'include' });
      if (r.status === 401) { authed = false; loaded = true; return; }
      if (!r.ok) throw new Error(await r.text());
      users = await r.json();
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loaded = true;
    }
  }

  function fmt(ts: number): string {
    return new Date(ts * 1000).toLocaleDateString();
  }

  onMount(load);
</script>

<svelte:head><title>People — Forum</title></svelte:head>

<main>
  <p><a href="/forum">← back to forum</a></p>
  <h1>People</h1>

  {#if !loaded}
    <p>Loading…</p>
  {:else if !authed}
    <p>You need to <a href={`${API_ORIGIN}/login`}>log in</a> to see the directory.</p>
  {:else if error}
    <p class="error">{error}</p>
  {:else if users.length === 0}
    <p class="empty">No users yet.</p>
  {:else}
    <ul>
      {#each users as u (u.user_id)}
        <li>
          <span class="username">{u.username}</span>
          <span class="meta">joined {fmt(u.created_at)}</span>
        </li>
      {/each}
    </ul>
  {/if}
</main>

<style>
  main { max-width: 720px; margin: 2rem auto; padding: 0 1rem; font-family: system-ui; }
  h1 { font-weight: 400; }
  .error { color: #a00; }
  .empty { color: #888; }
  ul { list-style: none; padding: 0; }
  ul li {
    display: flex; align-items: baseline; gap: 0.75rem;
    padding: 0.6rem 0; border-bottom: 1px solid #eee;
  }
  .username { font-weight: 600; color: #333; }
  .meta { color: #888; font-size: 0.85rem; }
</style>
