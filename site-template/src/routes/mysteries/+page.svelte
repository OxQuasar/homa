<script lang="ts">
  import MysteryPage from '$lib/MysteryPage.svelte';

  const mysteries: { slug: string; title: string; note?: string }[] = [
    { slug: 'ancient-academy', title: 'Ancient Academy' },
    { slug: 'modern-marvels',  title: 'Modern Marvels'  },
  ];
</script>

<MysteryPage title="The Mysteries" backHref="/enter">
  <header>
    <h1>The Mysteries</h1>
  </header>

  <ul class="items">
    {#each mysteries as m}
      <li>
        <a href={`/mysteries/${m.slug}`}>
          <h2>{m.title}</h2>
          {#if m.note}<p class="note">{m.note}</p>{/if}
        </a>
      </li>
    {/each}
  </ul>
</MysteryPage>
