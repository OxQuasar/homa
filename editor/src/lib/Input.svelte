<script lang="ts">
  // Status-aware input. While idle, the button is a Send; while running,
  // the same slot becomes a Stop that cancels the in-flight run. Keeps
  // the textarea disabled-but-prefillable so users can compose their next
  // prompt while waiting (and submit once the run finishes).
  //
  // The 📎 button on the left handles file uploads — multipart POST to
  // /upload via api.upload(). On success, the worktree-relative path is
  // pre-pended to the chat input so the user's next prompt names the
  // file directly.

  import { upload, type ApiError } from './api';

  // text + onTextChange are owned by the parent so the input draft can
  // be per-tab (per-recipient in the DM case). Without this lift, the
  // same Input instance would carry typed text across tab switches —
  // risking sending a message intended for one recipient to another.
  // After submit, parent clears via onTextChange('').
  const { text, onTextChange, onSubmit, onStop, status }: {
    text: string;
    onTextChange: (v: string) => void;
    onSubmit: (text: string) => void;
    onStop?: () => void;
    status: 'idle' | 'running';
  } = $props();

  // Upload UI state. uploadStatus drives the button label + disabled
  // state without leaking back into the parent component.
  let uploadStatus = $state<'idle' | 'busy' | 'error'>('idle');
  let uploadError = $state('');
  let fileInput: HTMLInputElement | undefined = $state();

  const running = $derived(status === 'running');
  const uploadDisabled = $derived(running || uploadStatus === 'busy');

  function submit() {
    if (running) return;
    const trimmed = text.trim();
    if (!trimmed) return;
    onSubmit(trimmed);
    // Parent clears via onTextChange('') in its onSend handler — Input
    // doesn't manipulate its own text now that it's a prop.
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

  function onAttach() {
    fileInput?.click();
  }

  async function onFile(e: Event) {
    const t = e.target as HTMLInputElement;
    const file = t.files?.[0];
    t.value = ''; // reset so selecting the same filename twice still fires change
    if (!file) return;

    uploadStatus = 'busy';
    uploadError = '';
    try {
      const r = await upload(file);
      // Prepend a path hint to whatever the user was already typing.
      // The user adds context: "I uploaded ... use it as the hero".
      const hint = `[uploaded: ${r.path}] `;
      onTextChange(hint + text);
      uploadStatus = 'idle';
    } catch (err) {
      const e = err as ApiError;
      uploadError = e.message || 'upload failed';
      uploadStatus = 'error';
      // Auto-clear the error after a few seconds so it doesn't linger.
      setTimeout(() => {
        if (uploadStatus === 'error') uploadStatus = 'idle';
      }, 4000);
    }
  }
</script>

<div class="input">
  <button
    class="attach"
    onclick={onAttach}
    disabled={uploadDisabled}
    title={uploadStatus === 'busy' ? 'Uploading…' : 'Attach a file'}
  >
    {uploadStatus === 'busy' ? '⏳' : '📎'}
  </button>

  <!-- accept="" → any file; image/* would be tighter but the LLM can
       use non-image files too (.md notes, .json fixtures, etc). -->
  <input
    bind:this={fileInput}
    type="file"
    onchange={onFile}
    style="display: none"
  />

  <!--
    Controlled component: value + oninput rather than bind:value. Lets
    parent own the text state (per-tab drafts) without needing
    $bindable plumbing through Chat.svelte too.
  -->
  <textarea
    value={text}
    oninput={(e) => onTextChange((e.currentTarget as HTMLTextAreaElement).value)}
    onkeydown={onKeydown}
    placeholder={running ? 'Compose your next prompt…' : 'Ask homa to change your site…'}
    rows="3"
    disabled={running}
  ></textarea>

  <button onclick={onButton} class:stop={running}>
    {running ? 'Stop' : 'Send'}
  </button>
</div>

{#if uploadStatus === 'error'}
  <div class="upload-err" role="alert">⚠ Upload failed: {uploadError}</div>
{/if}

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
  /* Attach button — smaller than Send/Stop; visually distinct so the
     paperclip doesn't compete with the primary action. */
  .attach {
    min-width: 2.5rem; padding: 0;
    background: #fff; color: #333; border-color: #ccc;
    font-size: 1.1rem;
  }
  .attach:disabled { background: #eee; cursor: not-allowed; opacity: 0.6; }
  .attach:not(:disabled):hover { background: #f0f0f0; border-color: #888; }
  /* Stop variant — distinct from Send so the action is obvious. */
  button.stop {
    background: #b30000; border-color: #b30000;
  }
  button.stop:hover { background: #d10000; }

  .upload-err {
    padding: 0.4rem 0.75rem;
    background: #ffe0e0; color: #a00;
    font-size: 0.85rem;
    border-top: 1px solid #f8b0b0;
  }
</style>
