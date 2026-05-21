<script lang="ts">
  import type { Snippet } from 'svelte';

  // Shared chrome for /mysteries and descendants:
  //   - fixed backdrop (photo or dark gradient)
  //   - scrim tuned per `tone`
  //   - top-left back link
  //   - centered <main> with serif typography
  //
  // Pages pass their own <header> + content as children, so each page
  // keeps full control over its title markup (e.g. inline Chinese
  // characters in the I Ching page).

  type Tone = 'balanced' | 'ambient' | 'dark';

  interface Props {
    title: string;
    backHref: string;
    bgImage?: string;
    tone?: Tone;
    children: Snippet;
  }

  let {
    title,
    backHref,
    bgImage,
    // Default: if there's a photo, darken it moderately; if not, use
    // the hub's minimal scrim over the dark gradient.
    tone = bgImage ? 'balanced' : 'dark',
    children,
  }: Props = $props();
</script>

<svelte:head>
  <title>{title} — Tar Valon</title>
</svelte:head>

{#if bgImage}
  <div class="bg" style:background-image={`url('${bgImage}')`} aria-hidden="true"></div>
{:else}
  <div class="bg bg--dark" aria-hidden="true"></div>
{/if}
<div class="scrim scrim--{tone}" aria-hidden="true"></div>

<a class="back" href={backHref}>← Back</a>

<main class="mp-content">
  {@render children()}
</main>

<style>
  :global(html) { background: #05060f; }
  :global(body) { margin: 0; background: transparent; color: #f5f1e6; }

  .bg {
    position: fixed;
    inset: 0;
    z-index: 0;
    background-size: cover;
    background-position: 50% 50%;
  }
  .bg--dark {
    background:
      radial-gradient(ellipse at 50% 30%, rgba(20, 18, 50, 0.55), transparent 70%),
      linear-gradient(180deg, #07080f 0%, #05060f 100%);
  }

  .scrim {
    position: fixed;
    inset: 0;
    z-index: 1;
  }
  /* Minimal haze — only used over the dark gradient on the hub. */
  .scrim--dark {
    background:
      radial-gradient(ellipse at 50% 40%, rgba(0,0,0,0.05), rgba(0,0,0,0.25) 80%);
  }
  /* Moderate darkening for bright photos (e.g. B&W temple columns).
     These values match the Ancient Academy tuning. */
  .scrim--balanced {
    background:
      radial-gradient(ellipse at 50% 40%, rgba(0,0,0,0.20), rgba(0,0,0,0.31) 80%),
      linear-gradient(180deg, rgba(5,6,15,0.18) 0%, rgba(5,6,15,0.25) 100%);
  }
  /* Light haze for already-dark photos (e.g. night cityscape) — just
     enough to keep titles readable without dulling the lights. */
  .scrim--ambient {
    background:
      radial-gradient(ellipse at 50% 30%, rgba(0,0,0,0.15), rgba(0,0,0,0.4) 80%);
  }

  main {
    min-height: 100dvh;
    padding: 6rem 1.5rem 6rem;
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
    position: relative;
    z-index: 2;
  }

  /* Typography for content rendered via the {children} snippet. Using
     `.mp-content :global(...)` keeps these styles scoped to our wrapper
     so they don't leak to other pages. */
  .mp-content :global(header) {
    max-width: 42rem;
    margin: 0 auto 3rem;
    text-align: center;
  }
  .mp-content :global(h1) {
    font-size: clamp(2.4rem, 6vw, 4rem);
    font-weight: 500;
    letter-spacing: 0.01em;
    margin: 0;
  }
  .mp-content :global(.subtitle) {
    margin: 0.35rem 0 0;
    font-style: italic;
    font-size: 1.05rem;
    opacity: 0.65;
  }
  .mp-content :global(.empty) {
    max-width: 42rem;
    margin: 0 auto;
    text-align: center;
    font-style: italic;
    font-size: 1.15rem;
    opacity: 0.6;
  }

  /* Card list — used on the hub and any sub-hub. */
  .mp-content :global(.items) {
    list-style: none;
    padding: 0;
    margin: 0 auto;
    max-width: 42rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .mp-content :global(.items a) {
    display: block;
    padding: 1.5rem 1.75rem;
    text-decoration: none;
    color: inherit;
    border: 1px solid rgba(245, 241, 230, 0.22);
    border-radius: 6px;
    background: rgba(5, 6, 15, 0.55);
    backdrop-filter: blur(6px);
    transition: background 0.18s ease, border-color 0.18s ease, transform 0.18s ease;
  }
  .mp-content :global(.items a:hover) {
    background: rgba(5, 6, 15, 0.75);
    border-color: rgba(245, 241, 230, 0.5);
    transform: translateY(-1px);
  }
  .mp-content :global(.items h2) {
    font-size: 1.65rem;
    font-weight: 500;
    margin: 0 0 0.5rem 0;
  }
  /* Inline accent next to an item title (e.g. Chinese characters). */
  .mp-content :global(.items .accent) {
    margin-left: 0.6rem;
    font-size: 1.35rem;
    opacity: 0.7;
    font-weight: 400;
  }
  .mp-content :global(.items .note) {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.8rem;
    opacity: 0.55;
    margin: 0;
  }

  .back {
    position: fixed;
    top: 1.25rem;
    left: 1.5rem;
    z-index: 10;
    padding: 0.45rem 1.1rem;
    border-radius: 999px;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.85rem;
    letter-spacing: 0.05em;
    color: #f5f1e6;
    text-decoration: none;
    background: rgba(0, 0, 0, 0.35);
    border: 1px solid rgba(245, 241, 230, 0.45);
    backdrop-filter: blur(6px);
    transition: background 0.18s ease, border-color 0.18s ease;
  }
  .back:hover {
    background: rgba(0, 0, 0, 0.55);
    border-color: rgba(245, 241, 230, 0.85);
  }
</style>
