<script lang="ts">
  // I Ching research — surfaces the operator's research collection from
  // ~/homa/data/docs/iching/ (served read-only via /api/library/iching/*).
  //
  // v1 shape: directory.md as the human intro at top, then the full
  // subdir listing as a grid of cards. Each card links to the API
  // listing for that subdir for now — the LLM can evolve this into
  // a richer browser (per-subdir pages, file viewers, etc.) over time.

  import { onMount } from 'svelte';
  import MysteryPage from '$lib/MysteryPage.svelte';

  interface Entry {
    name: string;
    is_dir: boolean;
    size?: number;
  }

  let entries = $state<Entry[]>([]);
  let intro = $state(''); // raw markdown text of directory.md
  let loaded = $state(false);
  let error = $state<string | null>(null);

  const API = '/api/library/iching/';

  async function load() {
    try {
      const listP = fetch(API, { credentials: 'include' });
      const introP = fetch(API + 'directory.md', { credentials: 'include' });
      const [listR, introR] = await Promise.all([listP, introP]);
      if (!listR.ok) throw new Error(`listing: ${listR.status}`);
      entries = (await listR.json()) as Entry[];
      if (introR.ok) intro = await introR.text();
      // intro is optional — if directory.md is missing, just omit
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loaded = true;
    }
  }

  // Subdirs only — directory.md and other root files render separately.
  const dirs = $derived(entries.filter((e) => e.is_dir));
  const rootFiles = $derived(
    entries.filter((e) => !e.is_dir && e.name !== 'directory.md')
  );

  onMount(load);
</script>

<MysteryPage
  title="I Ching Research"
  backHref="/mysteries/ancient-academy/i-ching"
>
  <header>
    <h1>Research</h1>
    <p class="subtitle">Notes on the Book of Changes</p>
  </header>

  {#if !loaded}
    <p class="status">Loading…</p>
  {:else if error}
    <p class="status err">Failed to load: {error}</p>
  {:else}
    {#if intro}
      <!-- directory.md rendered as preformatted text. No markdown
           library yet — readable as-is and keeps the page light. -->
      <section class="intro">
        <pre>{intro}</pre>
      </section>
    {/if}

    {#if dirs.length > 0}
      <section>
        <h2>Threads ({dirs.length})</h2>
        <ul class="grid">
          {#each dirs as d (d.name)}
            <li>
              <a href={API + encodeURIComponent(d.name) + '/'}>
                <span class="name">{d.name}</span>
              </a>
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if rootFiles.length > 0}
      <section>
        <h2>Top-level documents</h2>
        <ul class="files">
          {#each rootFiles as f (f.name)}
            <li>
              <a href={API + encodeURIComponent(f.name)}>{f.name}</a>
              {#if f.size}<span class="size">{(f.size / 1024).toFixed(1)} KB</span>{/if}
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if dirs.length === 0 && rootFiles.length === 0 && !intro}
      <p class="status">The records are still being gathered.</p>
    {/if}
  {/if}
</MysteryPage>

<style>
  h1 { font-family: 'Cormorant Garamond', serif; font-size: 2.5rem; font-weight: 500; }
  .subtitle { color: rgba(245, 241, 230, 0.55); font-style: italic; margin-top: -0.25rem; }
  .status { color: rgba(245, 241, 230, 0.5); margin-top: 1.5rem; }
  .status.err { color: #ec8585; }

  .intro {
    margin: 1.5rem 0 2.5rem;
    padding: 1.25rem 1.5rem;
    background: rgba(0, 0, 0, 0.25);
    border-left: 2px solid rgba(245, 241, 230, 0.25);
    border-radius: 2px;
  }
  .intro pre {
    margin: 0;
    white-space: pre-wrap;
    font-family: ui-monospace, 'SF Mono', Menlo, monospace;
    font-size: 0.78rem;
    line-height: 1.55;
    color: rgba(245, 241, 230, 0.82);
    overflow-x: auto;
  }

  h2 {
    font-family: 'Cormorant Garamond', serif;
    font-weight: 400;
    font-size: 1.4rem;
    margin-top: 2rem;
    color: rgba(245, 241, 230, 0.85);
  }

  .grid {
    list-style: none; padding: 0; margin: 1rem 0 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 0.6rem;
  }
  .grid a {
    display: block;
    padding: 0.7rem 0.9rem;
    background: rgba(245, 241, 230, 0.06);
    border: 1px solid rgba(245, 241, 230, 0.15);
    border-radius: 4px;
    color: rgba(245, 241, 230, 0.9);
    text-decoration: none;
    transition: background 0.15s ease, border-color 0.15s ease;
  }
  .grid a:hover {
    background: rgba(245, 241, 230, 0.12);
    border-color: rgba(245, 241, 230, 0.4);
  }
  .name { font-weight: 500; font-family: 'Cormorant Garamond', serif; font-size: 1.05rem; }

  .files { list-style: none; padding: 0; margin: 1rem 0 0; }
  .files li {
    padding: 0.4rem 0.6rem;
    border-bottom: 1px solid rgba(245, 241, 230, 0.1);
    display: flex; justify-content: space-between; align-items: baseline;
  }
  .files a { color: rgba(245, 241, 230, 0.9); text-decoration: none; }
  .files a:hover { text-decoration: underline; }
  .size { color: rgba(245, 241, 230, 0.45); font-size: 0.75rem; }
</style>
