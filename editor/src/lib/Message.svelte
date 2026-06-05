<script lang="ts">
  import type { ChatMessage } from './types';
  import { formatMessageTime, formatMessageTimeISO } from './timestamp';
  import ToolCard from './ToolCard.svelte';

  const { message }: { message: ChatMessage } = $props();

  // Derived so format updates as time passes (long-lived chat). Cheap;
  // recomputes only on $state changes that touch message.createdAt.
  const ts = $derived(formatMessageTime(message.createdAt));
  const tsISO = $derived(formatMessageTimeISO(message.createdAt));
</script>

<div class="msg msg-{message.role}">
  <div class="header">
    <span class="role">
      {#if message.role === 'system_error'}
        ⚠ Error
      {:else}
        {message.displayLabel ?? message.role}
      {/if}
    </span>
    {#if ts}
      <time class="ts" title={tsISO}>{ts}</time>
    {/if}
  </div>
  {#if message.tools && message.tools.length > 0}
    <div class="tools">
      {#each message.tools as t (t.id)}
        <ToolCard tool={t} />
      {/each}
    </div>
  {/if}
  {#if message.text}
    <div class="text">{message.text}</div>
  {/if}
</div>

<style>
  .msg { padding: 0.5rem 0.75rem; border-bottom: 1px solid #eee; }
  .msg-user { background: #f7f7fa; }
  /* System error bubble — tinted red, bordered, monospace for the
     copy-pastable raw error. Used when nous emits run_done with an
     error (e.g. provider 429, OAuth refresh failure). Without this,
     the chat just stopped responding with no signal. */
  .msg-system_error {
    background: #fff5f5;
    border-left: 3px solid #c33;
    margin: 0.5rem 0;
  }
  .msg-system_error .role { color: #c33; font-weight: 500; }
  .msg-system_error .text {
    font-family: ui-monospace, Menlo, Consolas, monospace;
    font-size: 0.82rem;
    color: #831a1a;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .header {
    display: flex; align-items: baseline; gap: 0.5rem;
    margin-bottom: 0.25rem;
  }
  .role { font-size: 0.75rem; text-transform: uppercase; color: #888; }
  /* Timestamp visually subordinate — same dim color as the role label,
     monospace digits so the column doesn't shift when the clock ticks.
     Hover reveals full ISO via title attr. */
  .ts {
    font-size: 0.72rem; color: #aaa;
    font-family: ui-monospace, Menlo, Consolas, monospace;
    cursor: default;
  }
  .text { white-space: pre-wrap; line-height: 1.45; }
  /* Tools render above the text; spacing applied below so the text sits
     close to the tool stack visually. */
  .tools { margin-bottom: 0.5rem; display: flex; flex-direction: column; gap: 0.25rem; }
</style>
