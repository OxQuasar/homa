<script lang="ts">
  import { page } from '$app/stores';
  import { listPosts, listTopics, createPost, ApiError, type Post, type Topic } from '$lib/forum';

  // SvelteKit 2: `$page` store works in runes mode; we read `$page.params.topicId`.
  // (Could also use `$props().data` via a load function later — kept simple here
  // because everything is fetched client-side with credentials.)
  const topicId = $derived($page.params.topicId);

  let topic = $state<Topic | null>(null);
  let posts = $state<Post[] | null>(null);
  let loadError = $state<string | null>(null);
  let unauthenticated = $state(false);

  let replyBody = $state('');
  let submitting = $state(false);
  let submitError = $state<string | null>(null);

  async function load() {
    loadError = null;
    unauthenticated = false;
    try {
      // Parallel: posts for this topic + topics list to derive title.
      // The API exposes no /topics/{id} detail endpoint, so the list is the only
      // way to recover title / author / count for this id.
      const [topicsRes, postsRes] = await Promise.all([listTopics(), listPosts(topicId)]);
      topic = topicsRes.find((t) => String(t.id) === String(topicId)) ?? null;
      posts = postsRes;
    } catch (e) {
      posts = [];
      if (e instanceof ApiError && e.status === 401) {
        unauthenticated = true;
      } else {
        loadError = (e as Error).message;
      }
    }
  }

  $effect(() => {
    // Re-run whenever topicId changes (e.g. client-side nav between topics).
    topicId;
    load();
  });

  async function reply(e: SubmitEvent) {
    e.preventDefault();
    if (!replyBody.trim() || submitting) return;
    submitting = true;
    submitError = null;
    try {
      await createPost(topicId, replyBody.trim());
      replyBody = '';
      await load();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        submitError = 'You must be logged in to reply.';
      } else {
        submitError = (err as Error).message;
      }
    } finally {
      submitting = false;
    }
  }

  function fmtDate(s?: string): string {
    if (!s) return '';
    const d = new Date(s);
    if (isNaN(d.getTime())) return s;
    return d.toLocaleString(undefined, {
      year: 'numeric', month: 'short', day: 'numeric',
      hour: '2-digit', minute: '2-digit',
    });
  }
</script>

<svelte:head>
  <title>{topic?.title ?? 'Topic'} — Forum — Tar Valon</title>
</svelte:head>

<div class="bg" aria-hidden="true"></div>
<div class="scrim" aria-hidden="true"></div>

<a class="back" href="/forum">← Forum</a>

<main>
  <header class="topic-header">
    {#if topic}
      <h1>{topic.title}</h1>
      <p class="meta">
        <span class="author">{topic.author_name}</span>
        <span class="dot">·</span>
        <span>{topic.post_count} {topic.post_count === 1 ? 'post' : 'posts'}</span>
      </p>
    {:else if !loadError && !unauthenticated}
      <h1>Topic</h1>
    {/if}
  </header>

  {#if unauthenticated}
    <p class="notice">
      You must <a href="/login">log in</a> to read this topic.
    </p>
  {:else if loadError}
    <p class="error">Couldn't load — {loadError}</p>
  {:else if posts === null}
    <p class="notice">Loading…</p>
  {:else}
    <ol class="posts">
      {#each posts as p (p.id)}
        <li>
          <article>
            <header class="post-head">
              <span class="author">{p.author_name}</span>
              {#if p.created_at}
                <time datetime={p.created_at}>{fmtDate(p.created_at)}</time>
              {/if}
            </header>
            <div class="body">{p.body}</div>
          </article>
        </li>
      {/each}
      {#if posts.length === 0}
        <p class="notice">No replies yet.</p>
      {/if}
    </ol>

    <form class="reply" onsubmit={reply}>
      <textarea
        bind:value={replyBody}
        placeholder="Write a reply…"
        rows="4"
        required
      ></textarea>
      {#if submitError}
        <p class="error">{submitError}</p>
      {/if}
      <div class="actions">
        <button type="submit" disabled={submitting}>
          {submitting ? 'Posting…' : 'Reply'}
        </button>
      </div>
    </form>
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

  /* Same backdrop pattern as /library: image + scrim behind scrolling content. */
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

  .topic-header {
    margin-bottom: 2.5rem;
    padding-bottom: 1.5rem;
    border-bottom: 1px solid rgba(245, 241, 230, 0.15);
  }
  h1 {
    font-size: clamp(2rem, 5vw, 3rem);
    font-weight: 500;
    letter-spacing: 0.01em;
    margin: 0 0 0.5rem 0;
  }
  .meta {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.85rem;
    opacity: 0.65;
    margin: 0;
    display: flex;
    gap: 0.5rem;
    align-items: baseline;
  }
  .meta .author { font-style: italic; }
  .dot { opacity: 0.5; }

  .posts {
    list-style: none;
    padding: 0;
    margin: 0 0 3rem 0;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .posts article {
    padding: 1.25rem 1.4rem;
    border: 1px solid rgba(245, 241, 230, 0.18);
    border-radius: 6px;
    background: rgba(0, 0, 0, 0.5);
    backdrop-filter: blur(6px);
  }
  .post-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.78rem;
    opacity: 0.7;
    margin-bottom: 0.6rem;
    letter-spacing: 0.02em;
  }
  .post-head .author { font-style: italic; opacity: 0.95; }
  .body {
    font-size: 1.05rem;
    line-height: 1.55;
    white-space: pre-wrap;
    word-wrap: break-word;
  }

  .reply {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    padding: 1.25rem;
    border: 1px solid rgba(245, 241, 230, 0.22);
    border-radius: 6px;
    background: rgba(0, 0, 0, 0.5);
    backdrop-filter: blur(6px);
  }
  .reply textarea {
    font: inherit;
    font-size: 1rem;
    padding: 0.65rem 0.85rem;
    background: rgba(245, 241, 230, 0.06);
    color: #f5f1e6;
    border: 1px solid rgba(245, 241, 230, 0.25);
    border-radius: 4px;
    resize: vertical;
  }
  .reply textarea:focus {
    outline: none;
    border-color: rgba(245, 241, 230, 0.6);
  }
  .reply .actions {
    display: flex;
    justify-content: flex-end;
  }
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
  button[type='submit']:hover:not(:disabled) {
    background: #fff;
    transform: translateY(-1px);
  }
  button[type='submit']:disabled {
    opacity: 0.5;
    cursor: progress;
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
