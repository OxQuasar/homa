<script lang="ts">
  // Auth gate for /forum and all child routes (/forum/[topicId]).
  //
  // Convention: any route directory that wraps its children in this
  // pattern (auth-check + Guard overlay) is auth-gated. Future authed
  // pages can copy this layout file OR (when there are several) get
  // promoted into a SvelteKit (authed) route group with a single
  // shared layout.
  //
  // Client-side check: fetchMe() hits /me; cookie set on the same
  // domain travels with the request. Renders three states:
  //   loading — brief skeleton (avoids flashing the gate then content)
  //   anon    — GuardEncounter with Sign up / Log in actions
  //   authed  — renders {@render children()} (the forum pages)

  import { onMount } from 'svelte';
  import GuardEncounter from '$lib/GuardEncounter.svelte';
  import { fetchMe, type AuthState } from '$lib/auth';

  // children is the always-on snippet SvelteKit passes for nested
  // routes — typed as a Snippet so $derived/conditionals work.
  let { children } = $props();

  let auth = $state<AuthState | null>(null);

  onMount(async () => {
    auth = await fetchMe();
  });

  // Speech for the gate. Stays in the White Tower voice from the
  // homepage sentinel; addresses the unrecognized visitor.
  const gateSpeech =
    'You are a stranger to the White Tower. ' +
    'Speak your name in the book of names, or return with the one you wrote before.';
</script>

<svelte:head><title>The Forum — Tar Valon</title></svelte:head>

{#if auth === null}
  <!-- Loading state — brief skeleton. Prevents flash of gate before
       the cookie check resolves. The dark backdrop matches the
       eventual GuardEncounter so the transition feels seamless. -->
  <div class="loading" aria-hidden="true"></div>
{:else if !auth.authed}
  <!--
    Anonymous visitor — render the gate. Login pill stays at top-right
    (always-on, per the design); Guard's Sign up + Log in actions are
    the primary CTAs. Both navigate to the SPA routes; after success
    there, the user returns to /forum and the gate resolves to authed.
  -->
  <a class="login-link" href="/login">Login</a>
  <GuardEncounter text={gateSpeech}>
    {#snippet actions()}
      <a class="cta primary" href="/signup">Sign Up</a>
      <a class="cta secondary" href="/login">Log In</a>
    {/snippet}
  </GuardEncounter>
{:else}
  {@render children()}
{/if}

<style>
  .loading {
    position: fixed;
    inset: 0;
    background: #06060e;
  }

  .login-link {
    position: fixed;
    top: 1.25rem;
    right: 1.5rem;
    z-index: 30; /* over GuardEncounter's overlay (z=20) */
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

  /* CTAs in the gate — same shape as the homepage Enter button so
     visual language is consistent. Primary = ivory, secondary = outlined. */
  .cta {
    padding: 0.85rem 2.4rem;
    border-radius: 999px;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.95rem;
    letter-spacing: 0.06em;
    text-decoration: none;
    box-shadow: 0 10px 36px rgba(0, 0, 0, 0.6);
    cursor: pointer;
    transition: transform 0.15s ease, background 0.2s ease;
  }
  .cta:hover {
    transform: translateY(-1px);
  }
  .cta.primary {
    background: #f5f1e6;
    color: #0b1430;
    border: 1px solid #f5f1e6;
  }
  .cta.primary:hover {
    background: #fff;
  }
  .cta.secondary {
    background: transparent;
    color: #f5f1e6;
    border: 1px solid rgba(245, 241, 230, 0.65);
  }
  .cta.secondary:hover {
    border-color: #f5f1e6;
    background: rgba(255, 255, 255, 0.08);
  }
</style>
