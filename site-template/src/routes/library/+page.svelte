<script lang="ts">
  const texts = [
    {
      slug: 'poimandres',
      title: 'Poimandres',
      author: 'Hermes Trismegistus',
      note: 'Corpus Hermeticum I — Greek with English translation.'
    },
    {
      slug: 'iching',
      title: 'I Ching 易經',
      author: 'anonymous (Zhou dynasty)',
      note: 'Book of Changes — all 64 hexagrams with judgment, 彖, 大象, and line texts.'
    }
  ];
</script>

<svelte:head>
  <title>The Library — Tar Valon</title>
  <meta name="description" content="The library of the White Tower." />
</svelte:head>

<div class="bg" aria-hidden="true"></div>
<div class="scrim" aria-hidden="true"></div>

<a class="back" href="/enter">← Back</a>

<main>
  <header>
    <h1>The Library</h1>
  </header>

  <ul class="texts">
    {#each texts as t}
      <li>
        <a href={`/library/${t.slug}`}>
          <h2>{t.title}</h2>
          <p class="author">{t.author}</p>
          <p class="note">{t.note}</p>
        </a>
      </li>
    {/each}
  </ul>
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

  .bg {
    position: fixed;
    inset: 0;
    z-index: 0;
    background-image: url('/images/library-bg.jpg');
    background-size: cover;
    background-position: 50% 40%;
  }
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 1;
    background:
      radial-gradient(ellipse at 50% 40%, rgba(0,0,0,0.05), rgba(0,0,0,0.2) 80%),
      linear-gradient(180deg, rgba(5,6,15,0.05) 0%, rgba(5,6,15,0.15) 100%);
  }

  main {
    min-height: 100dvh;
    padding: 6rem 1.5rem 6rem;
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
    position: relative;
    z-index: 2;
  }

  header {
    max-width: 42rem;
    margin: 0 auto 3rem;
    text-align: center;
  }
  h1 {
    font-size: clamp(2.4rem, 6vw, 4rem);
    font-weight: 500;
    letter-spacing: 0.01em;
    margin: 0;
  }

  .texts {
    list-style: none;
    padding: 0;
    margin: 0 auto;
    max-width: 42rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .texts a {
    display: block;
    padding: 1.5rem 1.75rem;
    text-decoration: none;
    color: inherit;
    border: 1px solid rgba(245, 241, 230, 0.22);
    border-radius: 6px;
    background: rgba(5, 6, 15, 0.55);
    backdrop-filter: blur(6px);
    transition: background 0.18s ease, border-color 0.18s ease, transform 0.18s ease;
  }
  .texts a:hover {
    background: rgba(5, 6, 15, 0.75);
    border-color: rgba(245, 241, 230, 0.5);
    transform: translateY(-1px);
  }
  .texts h2 {
    font-size: 1.65rem;
    font-weight: 500;
    margin: 0 0 0.15rem 0;
  }
  .texts .author {
    font-style: italic;
    font-size: 0.95rem;
    opacity: 0.7;
    margin: 0 0 0.5rem 0;
  }
  .texts .note {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.8rem;
    opacity: 0.55;
    margin: 0;
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
