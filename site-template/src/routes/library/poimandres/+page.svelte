<script lang="ts">
  import raw from '$lib/library/poimandres.txt?raw';

  type Block =
    | { type: 'title'; greek: string; english: string }
    | { type: 'verse'; ref: string }
    | { type: 'pair'; greek: string; english: string; note?: string };

  function parse(text: string): Block[] {
    const blocks = text.split(/\n\s*\n+/).map(b => b.trim()).filter(Boolean);
    return blocks.map((b, i): Block => {
      const lines = b.split('\n').map(l => l.trim()).filter(Boolean);
      if (lines.length === 1 && /^\d+\.\d+$/.test(lines[0])) {
        return { type: 'verse', ref: lines[0] };
      }
      const greek = lines[0];
      const english = lines[1] ?? '';
      const note = lines.slice(2).join(' ') || undefined;
      if (i === 0) return { type: 'title', greek, english };
      return { type: 'pair', greek, english, note };
    });
  }

  const blocks = parse(raw);
</script>

<svelte:head>
  <title>Poimandres — Library — Tar Valon</title>
  <meta name="description" content="Poimandres of Hermes Trismegistus (Corpus Hermeticum I) — Greek with English translation." />
</svelte:head>

<a class="back" href="/library">← Library</a>

<main>
  <article>
    {#each blocks as block}
      {#if block.type === 'title'}
        <header class="title">
          <h1 class="greek">{block.greek}</h1>
          <p class="english">{block.english}</p>
        </header>
      {:else if block.type === 'verse'}
        <h2 class="verse" id={`v${block.ref}`}>{block.ref}</h2>
      {:else}
        <section class="pair">
          <p class="greek">{block.greek}</p>
          <p class="english">{block.english}</p>
          {#if block.note}
            <p class="note">{block.note}</p>
          {/if}
        </section>
      {/if}
    {/each}
  </article>
</main>

<style>
  :global(html, body) {
    margin: 0;
    background: #05060f;
    color: #f5f1e6;
  }

  main {
    min-height: 100dvh;
    padding: 6rem 1.5rem 6rem;
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
  }

  article {
    max-width: 42rem;
    margin: 0 auto;
  }

  .title {
    text-align: center;
    margin-bottom: 4rem;
    padding-bottom: 2rem;
    border-bottom: 1px solid rgba(245, 241, 230, 0.15);
  }
  .title h1.greek {
    font-size: clamp(1.6rem, 3.5vw, 2.3rem);
    font-weight: 500;
    letter-spacing: 0.04em;
    margin: 0 0 0.6rem 0;
    color: #f5f1e6;
  }
  .title .english {
    font-style: italic;
    opacity: 0.7;
    font-size: 1.1rem;
    margin: 0;
  }

  .verse {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.75rem;
    letter-spacing: 0.15em;
    font-weight: 500;
    color: rgba(245, 241, 230, 0.45);
    margin: 2.5rem 0 1rem;
    padding: 0;
  }

  .pair {
    margin: 0 0 1.5rem 0;
  }
  .pair .greek {
    font-size: 1.25rem;
    line-height: 1.5;
    color: #f5f1e6;
    margin: 0;
  }
  .pair .english {
    font-size: 1rem;
    line-height: 1.5;
    color: rgba(245, 241, 230, 0.7);
    margin: 0.25rem 0 0 0;
    font-style: italic;
  }
  .pair .note {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.8rem;
    color: rgba(245, 241, 230, 0.45);
    margin: 0.4rem 0 0 0;
    padding-left: 1rem;
    border-left: 2px solid rgba(245, 241, 230, 0.15);
  }

  .back {
    position: fixed;
    top: 1.25rem;
    left: 1.5rem;
    z-index: 10;
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
