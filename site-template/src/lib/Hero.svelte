<script lang="ts">
  interface Props {
    image: string;
    alt: string;
    title?: string;
    ctaHref?: string;
    ctaLabel?: string;
    /** Vertical position of the CTA button as CSS top value. Default 50% (centered). */
    ctaY?: string;
    /** Vertical position of the title as CSS top value. Default 50% (centered). */
    titleY?: string;
    /** Apply a bluer/darker scrim to fake nighttime mood on a daytime photo. */
    nightTint?: boolean;
    /** 'cover' fills the viewport (may crop); 'contain' shows the whole image (may letterbox). */
    fit?: 'cover' | 'contain';
    /** Focal point for cropping under object-fit: cover. Default '50% 50%'. */
    objectPosition?: string;
    /** Flat black overlay opacity (0–1). 0 = none, 0.5 = halve image brightness. */
    dim?: number;
  }

  let {
    image,
    alt,
    title,
    ctaHref,
    ctaLabel,
    ctaY = '50%',
    titleY = '50%',
    nightTint = false,
    fit = 'cover',
    objectPosition = '50% 50%',
    dim = 0,
  }: Props = $props();
</script>

<section class="hero">
  <img class="bg" src={image} {alt} style="object-fit: {fit}; object-position: {objectPosition}" />
  <div class="scrim" class:night={nightTint}></div>
  {#if dim > 0}
    <div class="dim" style="background: rgba(0,0,0,{dim})"></div>
  {/if}
  {#if title}
    <h1 style="top: {titleY}">{title}</h1>
  {/if}
  {#if ctaHref && ctaLabel}
    <a class="btn" href={ctaHref} style="top: {ctaY}">{ctaLabel}</a>
  {/if}
</section>

<style>
  :global(html, body) {
    margin: 0;
    background: #05060f;
    color: #f5f1e6;
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
  }

  .hero {
    position: relative;
    height: 100dvh;
    min-height: 600px;
    width: 100%;
    overflow: hidden;
    isolation: isolate;
  }

  .bg {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    z-index: -2;
  }

  .scrim {
    position: absolute;
    inset: 0;
    z-index: -1;
    background:
      radial-gradient(ellipse at 50% 60%, rgba(0,0,0,0.55), rgba(0,0,0,0) 60%),
      linear-gradient(180deg, rgba(0,0,0,0.35) 0%, rgba(0,0,0,0) 30%, rgba(0,0,0,0.55) 100%);
  }

  .dim {
    position: absolute;
    inset: 0;
    z-index: -1;
    pointer-events: none;
  }

  .scrim.night {
    background:
      radial-gradient(ellipse at 50% 60%, rgba(0,0,20,0.4), rgba(0,0,20,0) 65%),
      linear-gradient(180deg, rgba(5,10,30,0.7) 0%, rgba(10,20,50,0.45) 50%, rgba(5,10,30,0.75) 100%);
  }

  h1, .btn {
    position: absolute;
    left: 50%;
    transform: translate(-50%, -50%);
    text-align: center;
    margin: 0;
  }

  h1 {
    font-size: clamp(2.6rem, 7vw, 5.5rem);
    line-height: 1.05;
    font-weight: 500;
    letter-spacing: 0.01em;
    text-shadow: 0 2px 30px rgba(0,0,0,0.85);
    width: max-content;
    max-width: 90vw;
  }

  .btn {
    display: inline-block;
    padding: 0.85rem 2rem;
    border-radius: 999px;
    text-decoration: none;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.95rem;
    letter-spacing: 0.05em;
    background: #f5f1e6;
    color: #0b1430;
    border: 1px solid #f5f1e6;
    box-shadow: 0 8px 30px rgba(0,0,0,0.5);
    transition: transform 0.15s ease, background 0.2s ease;
  }

  .btn:hover {
    transform: translate(-50%, -50%) translateY(-1px);
    background: #fff;
  }
</style>
