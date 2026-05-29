<script lang="ts">
  // Loading screen shown while the orchestrator's background EnsureRunning
  // goroutine is bringing the user's container up after login. Polls
  // /me/sandbox every POLL_INTERVAL_MS; emits onReady when status flips
  // to "ready", onFailed with the operator-facing message on "failed".
  //
  // Loading screen is shown for at least MIN_DISPLAY_MS even when the
  // first poll returns "ready" — avoids a jarring sub-frame flash and
  // gives the user a beat to register that auth succeeded.

  import { onMount, onDestroy } from 'svelte';

  const POLL_INTERVAL_MS = 1000;
  const MIN_DISPLAY_MS = 1000;

  // Discriminated union matching sandboxstatus.State on the wire.
  type Status =
    | { status: 'starting' }
    | { status: 'ready' }
    | { status: 'failed'; message?: string };

  const { onReady, onFailed }: {
    onReady: () => void;
    onFailed: (msg: string) => void;
  } = $props();

  let phase = $state<'loading' | 'failed'>('loading');
  let failureMessage = $state('');
  let timer: ReturnType<typeof setInterval> | null = null;
  let mountedAt = 0;

  async function poll() {
    let result: Status;
    try {
      const r = await fetch('/me/sandbox', { credentials: 'include' });
      if (!r.ok) return; // network blip — try next tick
      result = (await r.json()) as Status;
    } catch {
      return; // ditto
    }

    if (result.status === 'ready') {
      // Honor the MIN_DISPLAY_MS so the loading screen doesnt flicker
      // away in milliseconds when the container was already up.
      const elapsed = Date.now() - mountedAt;
      const wait = Math.max(0, MIN_DISPLAY_MS - elapsed);
      stopTimer();
      setTimeout(onReady, wait);
    } else if (result.status === 'failed') {
      stopTimer();
      const msg = result.message ?? 'Sandbox failed to start.';
      failureMessage = msg;
      phase = 'failed';
      onFailed(msg);
    }
    // 'starting' → keep polling
  }

  function stopTimer() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  onMount(() => {
    mountedAt = Date.now();
    // Fire one poll immediately so the loading screen doesnt sit idle
    // for the full interval before we know the actual state.
    void poll();
    timer = setInterval(poll, POLL_INTERVAL_MS);
  });

  onDestroy(stopTimer);

  function retry() {
    // A reload re-runs /login on the SPA's auth-redirect path, which
    // triggers a fresh EnsureRunning goroutine on the orchestrator.
    window.location.reload();
  }
</script>

<div class="overlay" aria-live="polite">
  {#if phase === 'loading'}
    <div class="card">
      <div class="spinner" aria-hidden="true"></div>
      <h2>Starting your sandbox…</h2>
      <p>This usually takes a few seconds.</p>
    </div>
  {:else}
    <div class="card failed">
      <div class="x" aria-hidden="true">⚠</div>
      <h2>Sandbox didn't start</h2>
      <p class="msg">{failureMessage}</p>
      <button onclick={retry}>Retry</button>
    </div>
  {/if}
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 100;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #fafafa;
    font-family: 'Inter', system-ui, sans-serif;
  }

  .card {
    background: #fff;
    border: 1px solid #e0e0e0;
    border-radius: 8px;
    padding: 2rem 2.5rem;
    text-align: center;
    max-width: 480px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.06);
  }

  h2 {
    font-size: 1.1rem;
    font-weight: 600;
    margin: 0.75rem 0 0.25rem;
    color: #222;
  }

  p {
    color: #666;
    font-size: 0.9rem;
    margin: 0;
  }

  .msg {
    color: #444;
    margin: 0.5rem 0 1.25rem;
    line-height: 1.45;
  }

  /* Simple CSS spinner — three dim bars rotating, no library cost. */
  .spinner {
    width: 28px;
    height: 28px;
    margin: 0 auto;
    border: 3px solid #e8e8e8;
    border-top-color: #1f6feb;
    border-radius: 50%;
    animation: spin 700ms linear infinite;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  .x {
    font-size: 2rem;
    color: #c44;
    line-height: 1;
  }

  button {
    margin-top: 0.5rem;
    padding: 0.5rem 1.25rem;
    border: 1px solid #ccc;
    border-radius: 4px;
    background: #fff;
    cursor: pointer;
    font-size: 0.9rem;
  }
  button:hover { background: #f5f5f5; }
</style>
