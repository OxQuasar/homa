<script lang="ts">
  // Root layout — persists across navigations. Runs the auth check
  // once and shows a global Editor pill for logged-in users.
  //
  // The pill hides when already inside /editor (the per-page Back
  // link there handles return navigation).

  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import favicon from '$lib/assets/favicon.svg';
  import { fetchMe, type AuthState } from '$lib/auth';

  let { children } = $props();

  let auth = $state<AuthState | null>(null);

  onMount(async () => {
    auth = await fetchMe();
  });

  let inEditor = $derived(page.url?.pathname?.startsWith('/editor') ?? false);
</script>

<svelte:head>
  <link rel="icon" href={favicon} />
</svelte:head>

{#if auth?.authed && !inEditor}
  <a class="editor-pill" href="/editor" title="Editor">✎ Editor</a>
{/if}

{@render children?.()}

<style>
  .editor-pill {
    position: fixed;
    top: 0.55rem;
    left: 0.55rem;
    z-index: 40;          /* over the Guard overlay (z=20–30) too */
    padding: 0.3rem 0.85rem;
    border-radius: 999px;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.78rem;
    letter-spacing: 0.06em;
    color: #f5f1e6;
    text-decoration: none;
    background: rgba(0, 0, 0, 0.45);
    border: 1px solid rgba(245, 241, 230, 0.35);
    backdrop-filter: blur(6px);
    transition: background 0.18s ease, border-color 0.18s ease;
  }
  .editor-pill:hover {
    background: rgba(0, 0, 0, 0.65);
    border-color: rgba(245, 241, 230, 0.7);
  }
</style>
