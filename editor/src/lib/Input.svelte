<script lang="ts">
  const { onSubmit, disabled = false }: {
    onSubmit: (text: string) => void;
    disabled?: boolean;
  } = $props();

  let text = $state('');

  function submit() {
    const trimmed = text.trim();
    if (!trimmed) return;
    onSubmit(trimmed);
    text = '';
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
    placeholder="Ask homa to change your site…"
    rows="3"
    {disabled}
  ></textarea>
  <button onclick={submit} {disabled}>Send</button>
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
    border-radius: 4px; cursor: pointer;
  }
  button:disabled { background: #999; cursor: not-allowed; }
</style>
