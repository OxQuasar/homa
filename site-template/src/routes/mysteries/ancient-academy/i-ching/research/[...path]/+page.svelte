<script lang="ts">
  import { marked } from 'marked';
  import MysteryPage from '$lib/MysteryPage.svelte';
  import type { PageProps } from './$types';

  let { data }: PageProps = $props();

  // URL prefix all internal links and breadcrumbs share.
  const PREFIX = '/mysteries/ancient-academy/i-ching/research';

  // Title: last segment if any, else "Research".
  let title = $derived(
    data.segments.length ? data.segments[data.segments.length - 1] : 'Research',
  );

  // For directory listings with a readme, parse it once. For .md docs,
  // parse the content. Marked is synchronous; the content is trusted
  // (read-only mount), so {@html} is acceptable here.
  let renderedHtml = $derived.by(() => {
    if (data.kind === 'md') return marked.parse(data.content) as string;
    if (data.kind === 'dir' && data.readme) return marked.parse(data.readme) as string;
    return '';
  });

  // Breadcrumb segments → cumulative hrefs.
  let crumbs = $derived(
    data.segments.map((seg, i) => ({
      name: seg,
      href: `${PREFIX}/${data.segments.slice(0, i + 1).join('/')}`,
    })),
  );

  // Back link target: parent path, or the I Ching landing if at root.
  let backHref = $derived(
    data.segments.length === 0
      ? '/mysteries/ancient-academy/i-ching'
      : data.segments.length === 1
        ? PREFIX
        : `${PREFIX}/${data.segments.slice(0, -1).join('/')}`,
  );
</script>

<MysteryPage {title} {backHref}>
  <!-- Breadcrumbs. Always show "Research" as root, then any segments. -->
  <nav class="crumbs" aria-label="breadcrumb">
    <a href={PREFIX}>Research</a>
    {#each crumbs as c}
      <span class="sep">/</span>
      <a href={c.href}>{c.name}</a>
    {/each}
  </nav>

  {#if data.kind === 'md'}
    <!-- Trusted source: read-only research corpus on disk. -->
    <article class="doc">{@html renderedHtml}</article>
  {:else if data.kind === 'dir'}
    {#if data.readme}
      <article class="doc readme">{@html renderedHtml}</article>
    {/if}

    <ul class="listing">
      {#each data.entries as e}
        <li class="entry entry--{e.kind}">
          <a href={`${PREFIX}/${e.href}`}>
            <span class="icon" aria-hidden="true">
              {#if e.kind === 'dir'}▸{:else if e.kind === 'asset'}◆{:else}·{/if}
            </span>
            <span class="name">{e.name}</span>
            {#if e.ext && e.kind === 'asset'}
              <span class="ext">{e.ext}</span>
            {/if}
          </a>
        </li>
      {/each}
    </ul>
  {:else if data.kind === 'raw'}
    <!-- Non-markdown assets (svg, txt). Render as preformatted text;
         for images we'd inline them, but they're rare here. -->
    {#if data.ext === '.svg'}
      <div class="asset svg">{@html data.content}</div>
    {:else}
      <pre class="asset raw">{data.content}</pre>
    {/if}
  {/if}
</MysteryPage>

<style>
  .crumbs {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.82rem;
    letter-spacing: 0.04em;
    opacity: 0.75;
    margin: 0 auto 2rem;
    max-width: 56rem;
    text-align: center;
  }
  .crumbs a {
    color: #f5f1e6;
    text-decoration: none;
    border-bottom: 1px dotted rgba(245, 241, 230, 0.35);
  }
  .crumbs a:hover { border-bottom-color: rgba(245, 241, 230, 0.9); }
  .crumbs .sep { margin: 0 0.5rem; opacity: 0.5; }

  /* Listing: simple monospace column. */
  .listing {
    list-style: none;
    margin: 0 auto;
    padding: 0;
    max-width: 48rem;
    font-family: 'Inter', system-ui, sans-serif;
  }
  .entry {
    border-bottom: 1px solid rgba(245, 241, 230, 0.08);
  }
  .entry a {
    display: flex;
    align-items: baseline;
    gap: 0.7rem;
    padding: 0.55rem 0.4rem;
    color: #f5f1e6;
    text-decoration: none;
    transition: background 0.12s ease;
  }
  .entry a:hover { background: rgba(245, 241, 230, 0.05); }
  .entry .icon {
    width: 1rem;
    text-align: center;
    opacity: 0.55;
    font-size: 0.78rem;
  }
  .entry--dir .name { font-weight: 500; }
  .entry .name { flex: 1; font-size: 0.95rem; }
  .entry .ext {
    font-size: 0.72rem;
    letter-spacing: 0.06em;
    opacity: 0.5;
    text-transform: lowercase;
  }

  /* Document body: minimal serif styling. The corpus is the content;
     we just need it readable. */
  .doc {
    max-width: 48rem;
    margin: 0 auto;
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
    font-size: 1.08rem;
    line-height: 1.65;
    color: rgba(245, 241, 230, 0.92);
  }
  .doc.readme {
    margin-bottom: 3rem;
    padding-bottom: 2.5rem;
    border-bottom: 1px solid rgba(245, 241, 230, 0.1);
  }

  /* Markdown element styling — all scoped via :global because marked
     emits raw HTML. Just enough to be legible. */
  .doc :global(h1),
  .doc :global(h2),
  .doc :global(h3),
  .doc :global(h4) {
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
    font-weight: 500;
    margin: 2.2rem 0 0.8rem;
    line-height: 1.25;
  }
  .doc :global(h1) { font-size: 2rem; }
  .doc :global(h2) { font-size: 1.55rem; }
  .doc :global(h3) { font-size: 1.25rem; }
  .doc :global(h4) { font-size: 1.05rem; letter-spacing: 0.02em; }

  .doc :global(p) { margin: 0.9rem 0; }
  .doc :global(a) {
    color: #f5f1e6;
    border-bottom: 1px dotted rgba(245, 241, 230, 0.45);
    text-decoration: none;
  }
  .doc :global(a:hover) { border-bottom-color: rgba(245, 241, 230, 0.95); }

  .doc :global(code) {
    font-family: 'JetBrains Mono', ui-monospace, monospace;
    font-size: 0.88em;
    padding: 0.12em 0.35em;
    background: rgba(245, 241, 230, 0.08);
    border-radius: 4px;
  }
  .doc :global(pre) {
    overflow-x: auto;
    padding: 1rem 1.2rem;
    background: rgba(0, 0, 0, 0.35);
    border: 1px solid rgba(245, 241, 230, 0.1);
    border-radius: 6px;
    font-size: 0.85rem;
    line-height: 1.5;
  }
  .doc :global(pre code) {
    background: none;
    padding: 0;
    font-size: 1em;
  }

  .doc :global(blockquote) {
    margin: 1rem 0;
    padding: 0.2rem 1rem;
    border-left: 3px solid rgba(245, 241, 230, 0.4);
    opacity: 0.82;
  }

  .doc :global(ul),
  .doc :global(ol) {
    padding-left: 1.5rem;
    margin: 0.8rem 0;
  }
  .doc :global(li) { margin: 0.3rem 0; }

  .doc :global(table) {
    border-collapse: collapse;
    margin: 1.2rem 0;
    font-size: 0.92rem;
  }
  .doc :global(th),
  .doc :global(td) {
    border: 1px solid rgba(245, 241, 230, 0.18);
    padding: 0.4rem 0.7rem;
  }
  .doc :global(th) {
    background: rgba(245, 241, 230, 0.06);
    font-weight: 500;
  }

  .doc :global(hr) {
    border: 0;
    border-top: 1px solid rgba(245, 241, 230, 0.18);
    margin: 2rem 0;
  }

  /* Raw assets. */
  .asset.raw {
    max-width: 56rem;
    margin: 0 auto;
    padding: 1rem 1.2rem;
    background: rgba(0, 0, 0, 0.35);
    border: 1px solid rgba(245, 241, 230, 0.1);
    border-radius: 6px;
    color: rgba(245, 241, 230, 0.85);
    font-family: 'JetBrains Mono', ui-monospace, monospace;
    font-size: 0.82rem;
    line-height: 1.55;
    white-space: pre-wrap;
  }
  .asset.svg {
    max-width: 36rem;
    margin: 0 auto;
    text-align: center;
  }
  .asset.svg :global(svg) {
    max-width: 100%;
    height: auto;
    filter: invert(0.95);
  }
</style>
