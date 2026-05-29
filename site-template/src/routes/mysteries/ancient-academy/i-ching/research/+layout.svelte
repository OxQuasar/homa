<script lang="ts">
  // Auth gate for /mysteries/ancient-academy/i-ching/research and all
  // child paths under the catch-all.
  //
  // `authed` comes from +layout.server.ts (which also drives the
  // server-side data skip for anon visitors — see the comment there).
  // Because the auth check is server-resolved, there's no loading
  // state; the gate or the content is in the SSR HTML directly.

  import GuardEncounter from '$lib/GuardEncounter.svelte';
  import type { LayoutProps } from './$types';

  let { data, children }: LayoutProps = $props();

  const gateSpeech =
    'These records are not for passing eyes. ' +
    'Speak your name in the book, or return when you have done so.';
</script>

<svelte:head><title>I Ching Research — Tar Valon</title></svelte:head>

{#if !data.authed}
  <a class="login-link" href="/login">Login</a>
  <GuardEncounter text={gateSpeech}>
    {#snippet actions()}
      <a class="cta primary" href="/signup">Sign Up</a>
      <a class="cta secondary" href="/login">Log In</a>
    {/snippet}
  </GuardEncounter>
{:else}
  {@render children?.()}
{/if}

<style>
  .login-link {
    position: fixed;
    top: 1.25rem;
    right: 1.5rem;
    z-index: 30;
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
  .cta:hover { transform: translateY(-1px); }
  .cta.primary {
    background: #f5f1e6;
    color: #0b1430;
    border: 1px solid #f5f1e6;
  }
  .cta.primary:hover { background: #fff; }
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
