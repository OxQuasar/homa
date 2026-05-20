<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import Hero from '$lib/Hero.svelte';
  import GuardEncounter from '$lib/GuardEncounter.svelte';
  import { fetchMe, type AuthState } from '$lib/auth';

  let started = $state(false);
  let speech = $state('');
  // When set, GuardEncounter auto-navigates after typing finishes (no
  // Enter button). Used for the logged-in flow.
  let autoMs = $state<number | undefined>(undefined);
  let auth = $state<AuthState | null>(null);

  // Resolve auth in the background. /me is fast and same-origin; by the
  // time a visitor reads the page and clicks Knock, this has almost
  // certainly settled. We don't block the UI on it.
  onMount(async () => {
    auth = await fetchMe();
  });

  // Anonymous visitor — the original gatekeeper speech.
  const anonIntro =
    'Welcome to the White Tower. ' +
    'You may choose to visit our grounds and library. Will you enter?';

  // Logged-in visitor — short recognition, then auto-advance.
  function authedIntro(a: AuthState): string {
    const name = a.username ? ` ${a.username}` : '';
    return `Welcome Back${name}.`;
  }

  // Snapshot speech + mode at click time, not reactively. If we passed
  // a $derived to GuardEncounter, a late /me response would mutate the
  // text prop mid-typewriter and restart it (GuardEncounter's $effect
  // depends on `text`). Capturing here makes the encounter stable.
  //
  // Two flows:
  //   anon   — full greeting, manual Enter button (autoMs undefined)
  //   authed — short greeting, 1s pause, auto-navigate
  function knock() {
    if (auth?.authed) {
      speech = authedIntro(auth);
      autoMs = 1000;
    } else {
      speech = anonIntro;
      autoMs = undefined;
    }
    started = true;
  }
</script>

<svelte:head>
  <title>Welcome to Tar Valon</title>
  <meta name="description" content="The shining city of Tar Valon — home of the White Tower." />
</svelte:head>

<!--
  Login pill, anonymous visitors only. Hidden while /me is in flight to
  prevent a flash for returning visitors. Logout lives elsewhere.
-->
{#if auth && !auth.authed}
  <a class="login-link" href="/login">Login</a>
{/if}

<Hero
  image="/uploads/whitetower.jpg"
  alt="The White Tower"
  title="The White Tower"
  titleY="22%"
  ctaLabel="Knock"
  ctaY="68%"
  onCta={knock}
  ctaHidden={started}
  nightTint
/>

{#if started}
  <GuardEncounter
    text={speech}
    onDone={() => goto('/enter')}
    autoAdvanceMs={autoMs}
  />
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
