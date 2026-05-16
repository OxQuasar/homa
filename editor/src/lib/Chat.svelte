<script lang="ts" module>
  // Follow-mode threshold (px). Anything within this of the bottom counts
  // as "pinned" — gives a little slack so subpixel scroll jitter doesn't
  // unpin during streaming.
  const PIN_TOLERANCE_PX = 32;
</script>

<script lang="ts">
  import type { ChatMessage, Streaming } from './types';
  import Message from './Message.svelte';
  import Input from './Input.svelte';

  const { messages, streaming, status, onSend, onStop }: {
    messages: ChatMessage[];
    streaming: Streaming | null;
    status: 'idle' | 'running';
    onSend: (text: string) => void;
    onStop?: () => void;
  } = $props();

  // Pseudo-message for the in-flight assistant turn, so it renders identically
  // to a completed assistant message.
  const liveMessage = $derived<ChatMessage | null>(
    streaming
      ? { role: 'assistant', text: streaming.text, tools: streaming.tools }
      : null
  );

  // --- auto-scroll / follow mode ---------------------------------------
  //
  // Rule: scroll to bottom on any change IF either
  //   (a) the user just sent a message (last message has role=user), or
  //   (b) the user was already pinned to the bottom (within tolerance).
  // If the user has scrolled up to read history, streaming/new messages
  // do NOT yank them back — pinned stays false until they manually return.
  //
  // pinned is a plain (non-reactive) variable updated by onscroll, so the
  // effect reads it without forming a write-loop with itself.

  let scrollEl: HTMLDivElement | undefined = $state();
  let pinned = true;

  function onScroll() {
    if (!scrollEl) return;
    pinned = scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight < PIN_TOLERANCE_PX;
  }

  function snapToBottom() {
    if (!scrollEl) return;
    // rAF so layout has settled before we read scrollHeight.
    requestAnimationFrame(() => {
      if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
    });
  }

  $effect(() => {
    // Read every reactive dep that should trigger a scroll check.
    const len = messages.length;
    const _ = streaming?.text;            // streaming text deltas
    const __ = streaming?.tools.length;   // streaming tool calls
    void _; void __;

    if (!scrollEl) return;
    const lastIsUser = len > 0 && messages[len - 1].role === 'user';
    if (lastIsUser || pinned) snapToBottom();
  });

  // Re-pin on container size changes (splitter drag, window resize, any
  // layout reflow). Without this, a wider chat pane → fewer wrapped lines
  // → shorter scrollHeight → unchanged scrollTop scrolls off the bottom,
  // hiding the in-flight assistant bubble and the thinking-dots.
  $effect(() => {
    if (!scrollEl) return;
    const ro = new ResizeObserver(() => {
      if (pinned) snapToBottom();
    });
    ro.observe(scrollEl);
    return () => ro.disconnect();
  });
</script>

<div class="chat">
  <div class="messages" bind:this={scrollEl} onscroll={onScroll}>
    {#each messages as m, i (i)}
      <Message message={m} />
    {/each}
    {#if liveMessage}
      <Message message={liveMessage} />
    {/if}
    {#if status === 'running'}
      <!--
        Dots show throughout the whole run, not just the request→first-delta
        gap. Between bursts (e.g. text-then-tool, then waiting for tool
        result, then more text), liveMessage is non-null but the LLM is
        still working — without this indicator the chat looks frozen.
      -->
      <div class="thinking" aria-live="polite">
        <span class="dot"></span>
        <span class="dot"></span>
        <span class="dot"></span>
      </div>
    {/if}
  </div>
  <Input onSubmit={onSend} {onStop} {status} />
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
