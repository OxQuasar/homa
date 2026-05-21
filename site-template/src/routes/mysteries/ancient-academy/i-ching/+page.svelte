<script lang="ts">
  import MysteryPage from '$lib/MysteryPage.svelte';
</script>

<!-- Bagua diagram backdrop, inlined as SVG so it can't be cached
     incorrectly. Rendered as a top-level sibling of MysteryPage at
     z:1 — above the scrim, below main.mp-content's text. -->
<svg
  class="bagua-back"
  viewBox="0 0 400 400"
  xmlns="http://www.w3.org/2000/svg"
  text-anchor="middle"
  font-size="24"
  aria-hidden="true"
>
  <g stroke="currentColor" fill="currentColor">
    <path fill="none" stroke-width="2"
      d="m61,143v114l82 82h114l82-82v-114l-82-82h-114zM108,162v76l54 54h76l54-54v-76l-54-54h-76zM61,143l47,19m184,76 47 19m0-114-47,19m-184,76-47,19m82,82 19-47m76-184 19-47m-114,0 19,47m76,184 19,47" />
    <path stroke-width="6"
      d="m185,19h12m6 0h12m-30,12h30m-30,12h12m6,0h12m-159,22h30m228,0h30m-288 12h30m228,0h12m6,0h12m-288,12h30m228,0h12m6,0h12m-328 96h12m6,0h12m308,0h12m6,0h12m-368,12h30m308,0h12m6 0h12m-368,12h30m308,0h30m-328,102h12m6,0h12m228,0h30m-288 12h12m6,0h12m228,0h30m-288,12h12m6,0h12m228,0h12m6 0h12m-159,20h30m-30,12h12m6,0h12m-30,12h30" />
  </g>
  <text x="200" y="213" font-size="46">八卦</text>
  <text x="200" y="93">坎</text>
  <text x="120" y="127">乾<tspan x="280">艮</tspan></text>
  <text x="85" y="205">兌<tspan x="316">震</tspan></text>
  <text x="120" y="292">坤<tspan x="280">巽</tspan></text>
  <text x="200" y="325">離</text>
</svg>

<MysteryPage
  title="I Ching 易經"
  backHref="/mysteries/ancient-academy"
>
  <header>
    <h1>I Ching <span class="han">易經</span></h1>
    <p class="subtitle">Book of Changes</p>
  </header>

  <ul class="items">
    <li>
      <a href="/mysteries/ancient-academy/i-ching/research">
        <h2>Research</h2>
      </a>
    </li>
  </ul>
</MysteryPage>

<style>
  /* Inline SVG, white ink via currentColor, sized to viewport with
     a max cap. Fixed-positioned at z:1 so it sits between the scrim
     (z:1, but DOM-earlier) and main.mp-content (z:2, transparent). */
  .bagua-back {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(80vmin, 700px);
    height: min(80vmin, 700px);
    z-index: 1;
    pointer-events: none;
    color: #f5f1e6;       /* drives currentColor in the SVG */
    opacity: 0.43;
  }

  /* The inline Chinese accent inside the h1. */
  :global(.mp-content h1 .han) {
    margin-left: 0.6rem;
    font-weight: 400;
    opacity: 0.85;
  }
</style>
