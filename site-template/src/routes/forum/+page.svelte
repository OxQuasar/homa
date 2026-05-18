<script lang="ts">
  import { listTopics, createTopic, ApiError, type Topic } from '$lib/forum';

  let topics = $state<Topic[] | null>(null);
  let loadError = $state<string | null>(null);
  let unauthenticated = $state(false);

  let showForm = $state(false);
  let newTitle = $state('');
  let newBody = $state('');
  let submitting = $state(false);
  let submitError = $state<string | null>(null);

  async function load() {
    loadError = null;
    unauthenticated = false;
    try {
      topics = await listTopics();
    } catch (e) {
      topics = [];
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

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!newTitle.trim() || !newBody.trim() || submitting) return;
    submitting = true;
    submitError = null;
    try {
      await createTopic(newTitle.trim(), newBody.trim());
      newTitle = '';
      newBody = '';
      showForm = false;
      await load();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        submitError = 'You must be logged in to post.';
      } else {
        submitError = (err as Error).message;
      }
    } finally {
      submitting = false;
    }
  }
</script>

<svelte:head>
  <title>The Forum — Tar Valon</title>
  <meta name="description" content="The arcaded forum of the White Tower." />
</svelte:head>

<div class="bg" aria-hidden="true"></div>
<div class="scrim" aria-hidden="true"></div>

<a class="back" href="/enter">← Back</a>

<section class="topics">
  <header>
    <h2>Topics</h2>
    <div class="header-actions">
      <a class="users-link" href="/users">All Users</a>
      <button class="new-btn" onclick={() => (showForm = !showForm)}>
        {showForm ? 'Cancel' : '+ New Topic'}
      </button>
    </div>
  </header>

  {#if showForm}
    <form class="new-form" onsubmit={submit}>
      <input
        type="text"
        bind:value={newTitle}
        placeholder="Title"
        maxlength="200"
        required
      />
      <textarea
        bind:value={newBody}
        placeholder="Say something…"
        rows="5"
        required
      ></textarea>
      {#if submitError}
        <p class="error">{submitError}</p>
      {/if}
      <div class="actions">
        <button type="submit" disabled={submitting}>
          {submitting ? 'Posting…' : 'Post topic'}
        </button>
      </div>
    </form>
  {/if}

  {#if unauthenticated}
    <p class="notice">
      You must <a href="/login">log in</a> to see and post topics.
    </p>
  {:else if loadError}
    <p class="error">Couldn't load topics — {loadError}</p>
  {:else if topics === null}
    <p class="notice">Loading…</p>
  {:else if topics.length === 0}
    <p class="notice">No topics yet. Start the first one.</p>
  {:else}
    <ul class="list">
      {#each topics as t (t.id)}
        <li>
          <a href={`/forum/${t.id}`}>
            <span class="title">{t.title}</span>
            <span class="meta">
              <span class="author">{t.author_name}</span>
              <span class="count">{t.post_count} {t.post_count === 1 ? 'post' : 'posts'}</span>
            </span>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  :global(html) {
    background: #05060f;
  }
  :global(body) {
    margin: 0;
    background: transparent;
    color: #f5f1e6;
  }

  /* Photo as fixed backdrop, scrim for legibility, content scrolls on top. */
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

  .topics {
    position: relative;
    z-index: 2;
    max-width: 42rem;
    margin: 0 auto;
    padding: 6rem 1.5rem 6rem;
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
  }

  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 2rem;
    padding-bottom: 1rem;
    border-bottom: 1px solid rgba(245, 241, 230, 0.15);
  }
  h2 {
    font-size: clamp(1.8rem, 4vw, 2.6rem);
    font-weight: 500;
    letter-spacing: 0.01em;
    margin: 0;
  }

  .header-actions {
    display: flex;
    gap: 0.6rem;
    align-items: center;
  }
  .users-link {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.85rem;
    letter-spacing: 0.05em;
    padding: 0.5rem 1.1rem;
    border-radius: 999px;
    color: #f5f1e6;
    text-decoration: none;
    background: transparent;
    border: 1px solid rgba(245, 241, 230, 0.45);
    transition: background 0.18s ease, border-color 0.18s ease;
  }
  .users-link:hover {
    background: rgba(245, 241, 230, 0.08);
    border-color: rgba(245, 241, 230, 0.85);
  }

  .new-btn,
  button[type='submit'] {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.85rem;
    letter-spacing: 0.05em;
    padding: 0.5rem 1.1rem;
    border-radius: 999px;
    background: #f5f1e6;
    color: #0b1430;
    border: 1px solid #f5f1e6;
    cursor: pointer;
    transition: background 0.18s ease, transform 0.15s ease, opacity 0.15s ease;
  }
  .new-btn:hover,
  button[type='submit']:hover:not(:disabled) {
    background: #fff;
    transform: translateY(-1px);
  }
  button[type='submit']:disabled {
    opacity: 0.5;
    cursor: progress;
  }

  .new-form {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    margin-bottom: 2rem;
    padding: 1.25rem;
    border: 1px solid rgba(245, 241, 230, 0.22);
    border-radius: 6px;
    background: rgba(0, 0, 0, 0.5);
    backdrop-filter: blur(6px);
  }
  .new-form input,
  .new-form textarea {
    font: inherit;
    font-size: 1rem;
    padding: 0.65rem 0.85rem;
    background: rgba(245, 241, 230, 0.06);
    color: #f5f1e6;
    border: 1px solid rgba(245, 241, 230, 0.25);
    border-radius: 4px;
    resize: vertical;
  }
  .new-form input:focus,
  .new-form textarea:focus {
    outline: none;
    border-color: rgba(245, 241, 230, 0.6);
  }
  .new-form .actions {
    display: flex;
    justify-content: flex-end;
  }

  .list {
    list-style: none;
    padding: 0;
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .list a {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 1.1rem 1.4rem;
    text-decoration: none;
    color: inherit;
    border: 1px solid rgba(245, 241, 230, 0.18);
    border-radius: 6px;
    background: rgba(0, 0, 0, 0.5);
    backdrop-filter: blur(6px);
    transition: background 0.18s ease, border-color 0.18s ease, transform 0.18s ease;
  }
  .list a:hover {
    background: rgba(0, 0, 0, 0.65);
    border-color: rgba(245, 241, 230, 0.4);
    transform: translateY(-1px);
  }
  .list .title {
    font-size: 1.3rem;
    font-weight: 500;
  }
  .list .meta {
    display: flex;
    gap: 1rem;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.78rem;
    opacity: 0.6;
    letter-spacing: 0.02em;
  }
  .list .author { font-style: italic; opacity: 0.85; }

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
