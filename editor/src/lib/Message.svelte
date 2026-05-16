<script lang="ts">
  import type { ChatMessage } from './types';
  import ToolCard from './ToolCard.svelte';

  const { message }: { message: ChatMessage } = $props();
</script>

<div class="msg msg-{message.role}">
  <div class="role">{message.role}</div>
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
  .role { font-size: 0.75rem; text-transform: uppercase; color: #888; margin-bottom: 0.25rem; }
  .text { white-space: pre-wrap; line-height: 1.45; }
  /* Tools render above the text; spacing applied below so the text sits
     close to the tool stack visually. */
  .tools { margin-bottom: 0.5rem; display: flex; flex-direction: column; gap: 0.25rem; }
</style>
