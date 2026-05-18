<script lang="ts">
  import { listUsers, displayName, type User } from '$lib/users';
  import { ApiError } from '$lib/api';

  let users = $state<User[] | null>(null);
  let loadError = $state<string | null>(null);
  let unauthenticated = $state(false);

  async function load() {
    loadError = null;
    unauthenticated = false;
    try {
      users = await listUsers();
    } catch (e) {
      users = [];
      if (e instanceof ApiError && e.status === 401) {
        unauthenticated = true;
      } else {
        loadError = (e as Error).message;
      }
    }
  }

  $effect(() => {
    load();
  });

  // API returns unix seconds (UTC). Date() takes milliseconds → * 1000.
  function fmtDate(seconds: number): string {
    const d = new Date(seconds * 1000);
    if (isNaN(d.getTime())) return '';
    return d.toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    });
  }
</script>

<svelte:head>
  <title>All Users — Tar Valon</title>
  <meta name="description" content="All registered users of Tar Valon." />
</svelte:head>

<div class="bg" aria-hidden="true"></div>
<div class="scrim" aria-hidden="true"></div>

<a class="back" href="/forum">← Forum</a>

<main>
  <header>
    <h1>All Users</h1>
  </header>

  {#if unauthenticated}
    <p class="notice">
      You must <a href="/login">log in</a> to see users.
    </p>
  {:else if loadError}
    <p class="error">Couldn't load users — {loadError}</p>
  {:else if users === null}
    <p class="notice">Loading…</p>
  {:else if users.length === 0}
    <p class="notice">No users yet.</p>
  {:else}
    <ul class="list">
      {#each users as u (u.user_id)}
        <li>
          <article>
            <span class="name">{displayName(u)}</span>
            <span class="joined">joined {fmtDate(u.created_at)}</span>
          </article>
        </li>
      {/each}
    </ul>
  {/if}
</main>

<style>
  :global(html) {
    background: #05060f;
  }
  :global(body) {
    margin: 0;
    background: transparent;
    color: #f5f1e6;
  }

  /* Reuse forum-bg backdrop for visual continuity from forum → users. */
  .bg {
    position: fixed;
    inset: 0;
    z-index: 0;
    background-image: url('/images/forum-bg.jpg');
    background-size: cover;
    background-position: 50% 50%;
  }
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 1;
    background: rgba(0, 0, 0, 0.5);
  }

  main {
    position: relative;
    z-index: 2;
    max-width: 42rem;
    margin: 0 auto;
    padding: 6rem 1.5rem 6rem;
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
  }

  header {
    margin-bottom: 2rem;
    padding-bottom: 1rem;
    border-bottom: 1px solid rgba(245, 241, 230, 0.15);
  }
  h1 {
    font-size: clamp(1.8rem, 4vw, 2.6rem);
    font-weight: 500;
    letter-spacing: 0.01em;
    margin: 0;
  }

  .list {
    list-style: none;
    padding: 0;
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .list article {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 1rem;
    padding: 0.9rem 1.2rem;
    border: 1px solid rgba(245, 241, 230, 0.18);
    border-radius: 6px;
    background: rgba(0, 0, 0, 0.5);
    backdrop-filter: blur(6px);
  }
  .name {
    font-size: 1.15rem;
    font-style: italic;
  }
  .joined {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.75rem;
    opacity: 0.6;
    letter-spacing: 0.02em;
  }

  .notice,
  .error {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.9rem;
    opacity: 0.7;
    margin: 1.5rem 0;
  }
  .error { color: #f6c0c0; opacity: 0.9; }
  .notice a { color: #f5f1e6; }

  .back {
    position: fixed;
    top: 1.25rem;
    left: 1.5rem;
    z-index: 20;
    padding: 0.45rem 1.1rem;
    border-radius: 999px;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.85rem;
    letter-spacing: 0.05em;
    color: #f5f1e6;
    text-decoration: none;
    background: rgba(0, 0, 0, 0.35);
    border: 1px solid rgba(245, 241, 230, 0.45);
    backdrop-filter: blur(6px);
    transition: background 0.18s ease, border-color 0.18s ease;
  }
  .back:hover {
    background: rgba(0, 0, 0, 0.55);
    border-color: rgba(245, 241, 230, 0.85);
  }
</style>
