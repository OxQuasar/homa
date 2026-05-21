<script lang="ts">
  import MysteryPage from '$lib/MysteryPage.svelte';

  const items = [
    { slug: 'i-ching', title: 'I Ching', accent: '易經' },
    // Ἑλληνικά — what the ancient Greeks called their own language.
    { slug: 'greek',   title: 'Greek',   accent: 'Ἑλληνικά' },
  ];
</script>

<MysteryPage
  title="Ancient Academy"
  backHref="/mysteries"
  bgImage="/images/ancient-academy-bg.jpg"
>
  <header>
    <h1>Ancient Academy</h1>
  </header>

  <ul class="items">
    {#each items as it}
      <li>
        <a href={`/mysteries/ancient-academy/${it.slug}`}>
          <h2>
            {it.title}
            {#if it.accent}<span class="accent">{it.accent}</span>{/if}
          </h2>
        </a>
      </li>
    {/each}
  </ul>
</MysteryPage>
