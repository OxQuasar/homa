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

<script lang="ts" module>
  // Show the "thinking..." placeholder only between request-sent and the
  // first stream event. Once any text/tool delta arrives, liveMessage is
  // non-null and the rendered Message itself signals activity.
  function shouldShowThinking(status: 'idle' | 'running', live: ChatMessage | null): boolean {
    return status === 'running' && live === null;
  }
</script>

<div class="chat">
  <div class="messages">
    {#each messages as m, i (i)}
      <Message message={m} />
    {/each}
    {#if liveMessage}
      <Message message={liveMessage} />
    {/if}
    {#if shouldShowThinking(status, liveMessage)}
      <div class="thinking" aria-live="polite">
        <span class="dot"></span>
        <span class="dot"></span>
        <span class="dot"></span>
      </div>
    {/if}
  </div>
  <Input onSubmit={onSend} disabled={status === 'running'} />
</div>

<style>
  .chat { display: flex; flex-direction: column; height: 100%; }
  .messages { flex: 1; overflow-y: auto; }

  /* Three pulsing dots while waiting for the first stream event. Replaces
     the dead-air gap between Send-click and first text_delta / tool_start. */
  .thinking {
    display: inline-flex; gap: 0.3rem;
    padding: 0.5rem 0.75rem;
    align-items: center;
  }
  .dot {
    width: 0.45rem; height: 0.45rem; border-radius: 50%;
    background: #888;
    animation: bounce 1.2s ease-in-out infinite;
  }
  .dot:nth-child(2) { animation-delay: 0.18s; }
  .dot:nth-child(3) { animation-delay: 0.36s; }
  @keyframes bounce {
    0%, 80%, 100% { opacity: 0.25; transform: translateY(0); }
    40%           { opacity: 1;    transform: translateY(-2px); }
  }
</style>
