<script lang="ts">
  import Hero from '$lib/Hero.svelte';
</script>

<svelte:head>
  <title>Welcome to Tar Valon</title>
  <meta name="description" content="The shining city of Tar Valon — home of the White Tower." />
</svelte:head>

<Hero
  image="/images/white-tower-night.jpg"
  alt="A white stone tower with a wooden door, evoking the White Tower of Tar Valon"
  title="The White Tower"
  titleY="22%"
  ctaHref="/enter"
  ctaLabel="Enter"
  ctaY="68%"
  nightTint
/>
