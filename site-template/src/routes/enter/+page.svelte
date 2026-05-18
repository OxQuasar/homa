<script lang="ts">
  import Hero from '$lib/Hero.svelte';
</script>

<svelte:head>
  <title>The Courtyard — Tar Valon</title>
  <meta name="description" content="A moonlit courtyard within the White Tower." />
</svelte:head>

<a class="side-link left" href="/library">Library</a>
<a class="side-link right" href="/forum">Forum</a>

<Hero
  image="/images/moonlit-courtyard.jpg"
  alt="A stone courtyard with arches at night, lit by the moon"
  objectPosition="50% 70%"
/>

<style>
  .side-link {
    position: fixed;
    top: 50%;
    transform: translate(-50%, -50%);
    z-index: 10;
    padding: 1.1rem 2.4rem;
    border-radius: 999px;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 1.25rem;
    letter-spacing: 0.05em;
    color: #f5f1e6;
    text-decoration: none;
    background: rgba(0, 0, 0, 0.35);
    border: 1px solid rgba(245, 241, 230, 0.5);
    backdrop-filter: blur(6px);
    transition: background 0.2s ease, border-color 0.2s ease, transform 0.15s ease;
  }
  .side-link.left  { left: 25%; }
  .side-link.right { left: 55%; }
  .side-link:hover {
    transform: translate(-50%, -50%) translateY(-1px);
    background: rgba(0, 0, 0, 0.55);
    border-color: rgba(245, 241, 230, 0.9);
  }
</style>
