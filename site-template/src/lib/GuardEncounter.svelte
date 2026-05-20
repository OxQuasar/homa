<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    text: string;
    /** Default action when typewriter finishes — renders an "Enter" button.
     *  Ignored if `actions` snippet is provided. */
    onDone?: () => void;
    /** Optional override for the post-typewriter call-to-action region.
     *  When set, replaces the default Enter button — e.g. an auth gate
     *  can render Sign up + Log in buttons instead. */
    actions?: Snippet;
    /** ms per character */
    speed?: number;
    /** ms before typing starts (lets figure & bubble fade in first) */
    startDelay?: number;
    /** When set, the encounter auto-advances: after typing finishes,
     *  wait this many ms, then call `onDone()`. Suppresses the Enter
     *  button. Ignored if `actions` is provided (manual choice). */
    autoAdvanceMs?: number;
  }

  let {
    text,
    onDone,
    actions,
    speed = 38,
    startDelay = 900,
    autoAdvanceMs,
  }: Props = $props();

  let typed = $state('');
  let done = $state(false);

  $effect(() => {
    let interval: ReturnType<typeof setInterval> | undefined;
    const startId = setTimeout(() => {
      let i = 0;
      interval = setInterval(() => {
        i++;
        typed = text.slice(0, i);
        if (i >= text.length) {
          clearInterval(interval);
          done = true;
        }
      }, speed);
    }, startDelay);

    return () => {
      clearTimeout(startId);
      if (interval) clearInterval(interval);
    };
  });

  // Auto-advance: once typing finishes, optionally fire onDone after a
  // pause. Skipped when caller supplies `actions` (manual choice region)
  // or when no onDone is provided. Cleanup cancels the timer if the
  // overlay unmounts mid-pause.
  $effect(() => {
    if (!done) return;
    if (actions) return;
    if (autoAdvanceMs === undefined) return;
    if (!onDone) return;
    const id = setTimeout(onDone, autoAdvanceMs);
    return () => clearTimeout(id);
  });
</script>

<div class="overlay" role="dialog" aria-live="polite">
  <!-- hooded sentinel standing at the door -->
  <div class="figure">
    <svg viewBox="0 0 140 320" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      <!-- staff -->
      <rect x="118" y="28" width="3" height="280" fill="#15152a" />
      <circle cx="119.5" cy="26" r="5.5" fill="#15152a" />
      <!-- cloaked body -->
      <path d="
        M 60 8
        C 42 8, 28 22, 26 46
        C 24 64, 30 78, 38 88
        C 28 92, 16 106, 12 136
        C 6 176, 6 270, 10 320
        L 110 320
        C 114 270, 114 176, 108 136
        C 104 106, 92 92, 82 88
        C 90 78, 96 64, 94 46
        C 92 22, 78 8, 60 8
        Z
      " fill="#06060e" />
      <!-- hood shadow hint -->
      <ellipse cx="60" cy="50" rx="13" ry="17" fill="#15152a" opacity="0.55" />
    </svg>
  </div>

  <!-- speech bubble -->
  <div class="bubble">
    <p>
      {typed}{#if !done}<span class="cursor" aria-hidden="true">▍</span>{/if}
    </p>
    <span class="tail"></span>
  </div>

  {#if done}
    {#if actions}
      <!-- Override: caller supplies its own CTAs (auth gate uses
           Sign up + Log in here). Default Enter button below is
           skipped when this snippet is provided. -->
      <div class="actions">{@render actions()}</div>
    {:else if onDone && autoAdvanceMs === undefined}
      <!-- Manual button hidden in auto-advance mode — the timer will
           call onDone itself, so showing a button you don't need to
           press would be confusing. -->
      <button class="enter-btn" onclick={onDone}>Enter</button>
    {/if}
  {/if}
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 20;
    background: rgba(0, 0, 0, 0.55);
    animation: fade-bg 400ms ease forwards;
  }

  @keyframes fade-bg {
    from { background: rgba(0, 0, 0, 0); }
    to   { background: rgba(0, 0, 0, 0.55); }
  }

  .figure {
    position: absolute;
    left: 50%;
    bottom: 6vh;
    transform: translateX(-50%);
    height: 42vh;
    max-height: 540px;
    opacity: 0;
    filter: drop-shadow(0 12px 30px rgba(0, 0, 0, 0.8));
    animation: rise 700ms ease 250ms forwards;
  }
  .figure svg {
    height: 100%;
    width: auto;
  }

  @keyframes rise {
    from { opacity: 0; transform: translate(-50%, 24px); }
    to   { opacity: 1; transform: translate(-50%, 0); }
  }

  .bubble {
    position: absolute;
    left: 50%;
    top: 18%;
    transform: translateX(-50%);
    max-width: min(34rem, 86vw);
    padding: 1.1rem 1.5rem 1.2rem;
    background: #f5f1e6;
    color: #0b1430;
    border-radius: 14px;
    font-family: 'Cormorant Garamond', 'Iowan Old Style', Georgia, serif;
    font-size: clamp(1.1rem, 2.2vw, 1.45rem);
    line-height: 1.45;
    box-shadow: 0 18px 50px rgba(0, 0, 0, 0.55);
    opacity: 0;
    animation: bubble-in 500ms ease 700ms forwards;
  }
  .bubble p {
    margin: 0;
    min-height: 1.45em;
  }

  @keyframes bubble-in {
    from { opacity: 0; transform: translate(-50%, -8px); }
    to   { opacity: 1; transform: translate(-50%, 0); }
  }

  .tail {
    position: absolute;
    left: 50%;
    bottom: -10px;
    transform: translateX(-50%);
    width: 0;
    height: 0;
    border-left: 12px solid transparent;
    border-right: 12px solid transparent;
    border-top: 12px solid #f5f1e6;
  }

  .cursor {
    display: inline-block;
    margin-left: 2px;
    animation: blink 1s steps(2) infinite;
    color: #0b1430;
    opacity: 0.7;
  }
  @keyframes blink {
    50% { opacity: 0; }
  }

  .enter-btn {
    position: absolute;
    left: 50%;
    bottom: 8vh;
    transform: translateX(-50%);
    padding: 0.85rem 2.4rem;
    border-radius: 999px;
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.95rem;
    letter-spacing: 0.06em;
    background: #f5f1e6;
    color: #0b1430;
    border: 1px solid #f5f1e6;
    box-shadow: 0 10px 36px rgba(0, 0, 0, 0.6);
    cursor: pointer;
    opacity: 0;
    animation: enter-in 500ms ease 200ms forwards;
    transition: transform 0.15s ease, background 0.2s ease;
  }
  .enter-btn:hover {
    transform: translateX(-50%) translateY(-1px);
    background: #fff;
  }

  /* `actions` slot wrapper — same positioning as the default Enter
     button so multiple-button CTAs (auth gate's Sign up + Log in)
     line up where users expect. Inherits the fade-in animation. */
  .actions {
    position: absolute;
    left: 50%;
    bottom: 8vh;
    transform: translateX(-50%);
    display: flex;
    gap: 0.85rem;
    opacity: 0;
    animation: enter-in 500ms ease 200ms forwards;
  }

  @keyframes enter-in {
    from { opacity: 0; transform: translate(-50%, 8px); }
    to   { opacity: 1; transform: translate(-50%, 0); }
  }
</style>
