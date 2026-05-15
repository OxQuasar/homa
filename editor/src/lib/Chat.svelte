<script lang="ts">
  import type { ChatMessage, Streaming } from './types';
  import Message from './Message.svelte';
  import Input from './Input.svelte';

  const { messages, streaming, status, onSend }: {
    messages: ChatMessage[];
    streaming: Streaming | null;
    status: 'idle' | 'running';
    onSend: (text: string) => void;
  } = $props();

  // Pseudo-message for the in-flight assistant turn, so it renders identically
  // to a completed assistant message.
  const liveMessage = $derived<ChatMessage | null>(
    streaming
      ? { role: 'assistant', text: streaming.text, tools: streaming.tools }
      : null
  );
</script>

<div class="chat">
  <div class="messages">
    {#each messages as m, i (i)}
      <Message message={m} />
    {/each}
    {#if liveMessage}
      <Message message={liveMessage} />
    {/if}
  </div>
  <Input onSubmit={onSend} disabled={status === 'running'} />
</div>

<style>
  .chat { display: flex; flex-direction: column; height: 100%; }
  .messages { flex: 1; overflow-y: auto; }
</style>
