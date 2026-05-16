<script lang="ts">
  // Status-aware input. While idle, the button is a Send; while running,
  // the same slot becomes a Stop that cancels the in-flight run. Keeps
  // the textarea disabled-but-prefillable so users can compose their next
  // prompt while waiting (and submit once the run finishes).
  const { onSubmit, onStop, status }: {
    onSubmit: (text: string) => void;
    onStop?: () => void;
    status: 'idle' | 'running';
  } = $props();

  let text = $state('');
  const running = $derived(status === 'running');

  function submit() {
    if (running) return;
    const trimmed = text.trim();
    if (!trimmed) return;
    onSubmit(trimmed);
    text = '';
  }

  function onButton() {
    if (running) {
      onStop?.();
      return;
    }
    submit();
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  }
</script>

<div class="input">
  <textarea
    bind:value={text}
    onkeydown={onKeydown}
    placeholder={running ? 'Compose your next prompt…' : 'Ask homa to change your site…'}
    rows="3"
    disabled={running}
  ></textarea>
  <button onclick={onButton} class:stop={running}>
    {running ? 'Stop' : 'Send'}
  </button>
</div>

<style>
  .input { display: flex; gap: 0.5rem; padding: 0.5rem; border-top: 1px solid #ddd; background: #fafafa; }
  textarea {
    flex: 1; font-family: inherit; font-size: 0.95rem;
    padding: 0.5rem; border: 1px solid #ccc; border-radius: 4px; resize: vertical;
  }
  textarea:disabled { background: #f0f0f0; }
  button {
    padding: 0 1rem; border: 1px solid #444; background: #222; color: white;
    border-radius: 4px; cursor: pointer; min-width: 5rem;
  }
  /* Stop variant — distinct from Send so the action is obvious. Red
     ring + slightly darker fill; not enough contrast change to be jarring. */
  button.stop {
    background: #b30000; border-color: #b30000;
  }
  button.stop:hover { background: #d10000; }
</style>
