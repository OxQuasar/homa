<script lang="ts">
  // Full I Ching on one page. All 64 hexagrams sequentially with:
  //   - top index grid (8×8) for jump nav
  //   - per-hexagram: glyph, number, name, 卦辞, 彖传, 大象,
  //     then 爻辞 lines each paired with its 小象 commentary.
  //
  // Anchors: #h1 … #h64 for direct linking.

  import type { PageProps } from './$types';

  let { data }: PageProps = $props();
  let hexagrams = $derived(data.hexagrams);

  // Pair line text + small-image commentary for rendering. The data is
  // already 1:1 indexed; we just zip them once.
  function lines(h: typeof hexagrams[number]) {
    return h.yao_ci.map((text, i) => ({
      text,
      commentary: h.xiao_xiang[i] ?? '',
    }));
  }
</script>

<svelte:head>
  <title>I Ching 易經 — Library — Tar Valon</title>
  <meta name="description" content="易經 — Book of Changes. All 64 hexagrams with judgment (卦辞), 彖, 大象, and line texts (爻辞) paired with 小象 commentary." />
</svelte:head>

<a class="back" href="/library">← Library</a>

<main>
  <article>
    <header class="title">
      <h1 class="han">易經</h1>
      <p class="english">Book of Changes</p>
      <p class="meta">六十四卦 · the sixty-four hexagrams</p>
    </header>

    <!-- Top index: 8×8 grid, glyph + number, links to anchors. -->
    <nav class="index" aria-label="hexagram index">
      {#each hexagrams as h, i}
        <a href={`#h${i + 1}`} title={h.name}>
          <span class="glyph">{h.symbol}</span>
          <span class="num">{i + 1}</span>
        </a>
      {/each}
    </nav>

    {#each hexagrams as h, i}
      <section class="hexagram" id={`h${i + 1}`}>
        <header class="hx-head">
          <span class="hx-num">{i + 1}</span>
          <span class="hx-glyph" aria-hidden="true">{h.symbol}</span>
          <h2 class="hx-name">{h.name}</h2>
        </header>

        <p class="gua-ci">{h.gua_ci}</p>

        <section class="commentary">
          <h3>彖</h3>
          <p>{h.tuan_ci}</p>
        </section>

        <section class="commentary">
          <h3>象</h3>
          <p>{h.da_xiang}</p>
        </section>

        <section class="yao">
          <h3>爻辭</h3>
          <ol class="lines">
            {#each lines(h) as line}
              <li>
                <p class="yao-ci">{line.text}</p>
                {#if line.commentary}
                  <p class="xiao-xiang">象：{line.commentary}</p>
                {/if}
              </li>
            {/each}
          </ol>
        </section>

        <a class="top-link" href="#top">↑ index</a>
      </section>
    {/each}
  </article>
</main>

<style>
  :global(html) { scroll-behavior: smooth; }
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
    max-width: 44rem;
    margin: 0 auto;
  }

  /* Title block. */
  .title {
    text-align: center;
    margin-bottom: 3rem;
    padding-bottom: 2rem;
    border-bottom: 1px solid rgba(245, 241, 230, 0.15);
  }
  .title h1.han {
    font-size: clamp(2.4rem, 6vw, 3.6rem);
    font-weight: 500;
    letter-spacing: 0.08em;
    margin: 0 0 0.4rem;
    color: #f5f1e6;
  }
  .title .english {
    font-style: italic;
    opacity: 0.7;
    font-size: 1.15rem;
    margin: 0 0 0.4rem;
  }
  .title .meta {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.78rem;
    letter-spacing: 0.15em;
    text-transform: lowercase;
    opacity: 0.5;
    margin: 0;
  }

  /* Index grid: dense 8×8. */
  .index {
    display: grid;
    grid-template-columns: repeat(8, 1fr);
    gap: 0.35rem;
    margin: 0 0 4rem 0;
  }
  .index a {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 0.55rem 0.2rem;
    gap: 0.1rem;
    text-decoration: none;
    color: #f5f1e6;
    border: 1px solid rgba(245, 241, 230, 0.12);
    border-radius: 4px;
    background: rgba(5, 6, 15, 0.4);
    transition: background 0.15s ease, border-color 0.15s ease;
  }
  .index a:hover {
    background: rgba(245, 241, 230, 0.08);
    border-color: rgba(245, 241, 230, 0.4);
  }
  .index .glyph {
    font-size: 1.35rem;
    line-height: 1;
  }
  .index .num {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.65rem;
    letter-spacing: 0.05em;
    opacity: 0.55;
  }

  /* Per-hexagram section. */
  .hexagram {
    margin: 0 0 4.5rem 0;
    padding-top: 1rem;
    scroll-margin-top: 1.5rem;
  }
  .hx-head {
    display: flex;
    align-items: center;
    gap: 0.9rem;
    margin: 0 0 1.5rem;
    padding-bottom: 0.8rem;
    border-bottom: 1px solid rgba(245, 241, 230, 0.12);
  }
  .hx-num {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.72rem;
    letter-spacing: 0.18em;
    color: rgba(245, 241, 230, 0.45);
    padding: 0.2rem 0.55rem;
    border: 1px solid rgba(245, 241, 230, 0.25);
    border-radius: 3px;
  }
  .hx-glyph {
    font-size: 2.4rem;
    line-height: 1;
    color: #f5f1e6;
  }
  .hx-name {
    font-size: 1.7rem;
    font-weight: 500;
    margin: 0;
    letter-spacing: 0.04em;
  }

  /* Judgment (卦辞) — slightly larger, set apart. */
  .gua-ci {
    font-size: 1.2rem;
    line-height: 1.6;
    color: #f5f1e6;
    margin: 0 0 2rem;
    padding: 0.6rem 0;
  }

  /* Each commentary section: 彖, 象, 爻辭. */
  .commentary, .yao {
    margin: 0 0 1.4rem;
  }
  .commentary h3, .yao h3 {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.72rem;
    font-weight: 500;
    letter-spacing: 0.18em;
    text-transform: lowercase;
    color: rgba(245, 241, 230, 0.45);
    margin: 0 0 0.5rem;
  }
  .commentary p {
    font-size: 1.02rem;
    line-height: 1.65;
    color: rgba(245, 241, 230, 0.85);
    margin: 0;
  }

  /* Line texts (爻辞) + their 小象 commentary. */
  .lines {
    list-style: none;
    counter-reset: none;
    padding: 0;
    margin: 0;
  }
  .lines li {
    margin: 0 0 1rem;
    padding: 0 0 0 1rem;
    border-left: 2px solid rgba(245, 241, 230, 0.12);
  }
  .lines li:last-child { margin-bottom: 0; }
  .yao-ci {
    font-size: 1.02rem;
    line-height: 1.55;
    color: #f5f1e6;
    margin: 0;
  }
  .xiao-xiang {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.83rem;
    line-height: 1.5;
    color: rgba(245, 241, 230, 0.5);
    margin: 0.25rem 0 0;
  }

  /* Small "back to index" link after each hexagram. */
  .top-link {
    display: inline-block;
    margin-top: 1.5rem;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.72rem;
    letter-spacing: 0.12em;
    text-transform: lowercase;
    color: rgba(245, 241, 230, 0.4);
    text-decoration: none;
    border-bottom: 1px dotted rgba(245, 241, 230, 0.25);
  }
  .top-link:hover {
    color: #f5f1e6;
    border-bottom-color: rgba(245, 241, 230, 0.7);
  }

  /* Fixed back-to-library pill (same pattern as Poimandres). */
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

  /* Mobile: looser index, narrower padding. */
  @media (max-width: 520px) {
    main { padding: 5rem 1rem 5rem; }
    .index { grid-template-columns: repeat(4, 1fr); }
  }
</style>
