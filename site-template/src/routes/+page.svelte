<script lang="ts">
  import { goto } from '$app/navigation';
  import Hero from '$lib/Hero.svelte';
  import GuardEncounter from '$lib/GuardEncounter.svelte';

  let started = $state(false);

  const intro =
    'Welcome to the White Tower. Here is learning and building. ' +
    'You may choose to visit our grounds and library. Will you enter?';
</script>

<svelte:head>
  <title>Welcome to Tar Valon</title>
  <meta name="description" content="The shining city of Tar Valon — home of the White Tower." />
</svelte:head>

<!--
  Login link in the top-right corner. For now it points at the SPA's login
  page; once we move the auth UI inline into this site, this hook stays
  put and we swap its target / replace it with a logged-in-state indicator.
-->
<a class="login-link" href="/login">Login</a>

<Hero
  image="/uploads/whitetower.jpg"
  alt="The White Tower"
  title="The White Tower"
  titleY="22%"
  ctaLabel="Knock"
  ctaY="68%"
  onCta={() => (started = true)}
  ctaHidden={started}
  nightTint
/>

{#if started}
  <GuardEncounter text={intro} onDone={() => goto('/enter')} />
{/if}

<style>
  .login-link {
    position: fixed;
    top: 1.25rem;
    right: 1.5rem;
    z-index: 10;
    padding: 0.45rem 1.1rem;
    border-radius: 999px;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.85rem;
    letter-spacing: 0.05em;
    color: #f5f1e6;
    text-decoration: none;
    background: rgba(0, 0, 0, 0.25);
    border: 1px solid rgba(245, 241, 230, 0.45);
    backdrop-filter: blur(6px);
    transition: background 0.18s ease, border-color 0.18s ease;
  }
  .login-link:hover {
    background: rgba(0, 0, 0, 0.45);
    border-color: rgba(245, 241, 230, 0.85);
  }
</style>
