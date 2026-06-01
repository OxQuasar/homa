<script lang="ts">
  import MysteryPage from '$lib/MysteryPage.svelte';

  const items = [
    { slug: 'mev', title: 'MEV', accent: 'Maximal Extractable Value' },
  ];
</script>

<MysteryPage
  title="Financial Markets"
  backHref="/mysteries/modern-marvels"
  tone="dark"
>
  <header>
    <h1>Financial Markets</h1>
  </header>

  <ul class="items">
    {#each items as it}
      <li>
        <a href={`/mysteries/modern-marvels/financial-markets/${it.slug}`}>
          <h2>
            {it.title}
            {#if it.accent}<span class="accent">{it.accent}</span>{/if}
          </h2>
        </a>
      </li>
    {/each}
  </ul>
</MysteryPage>
