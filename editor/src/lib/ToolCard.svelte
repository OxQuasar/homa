<script lang="ts">
  import type { ToolCall } from './types';

  const { tool }: { tool: ToolCall } = $props();
  let open = $state(false);
</script>

<div class="tool" class:err={tool.isError}>
  <button class="hdr" onclick={() => (open = !open)}>
    <span class="caret">{open ? '▾' : '▸'}</span>
    <span class="name">{tool.name}</span>
    {#if tool.isError}<span class="badge">error</span>{/if}
  </button>
  {#if open}
    <div class="body">
      <div class="label">input</div>
      <pre>{tool.input}</pre>
      {#if tool.output !== undefined}
        <div class="label">output</div>
        <pre>{tool.output}</pre>
      {/if}
    </div>
  {/if}
</div>

<style>
  .tool { font-size: 0.85rem; border: 1px solid #ddd; border-radius: 4px; }
  .tool.err { border-color: #d33; }
  .hdr {
    display: flex; align-items: center; gap: 0.5rem;
    width: 100%; padding: 0.35rem 0.5rem;
    background: #fafafa; border: none; cursor: pointer; text-align: left;
  }
  .caret { color: #888; }
  .name { font-family: ui-monospace, monospace; }
  .badge { margin-left: auto; color: #d33; font-size: 0.75rem; }
  .body { padding: 0.5rem; background: #fff; }
  .label { font-size: 0.7rem; color: #888; text-transform: uppercase; margin-top: 0.25rem; }
  pre { margin: 0.25rem 0; padding: 0.4rem; background: #f4f4f7; overflow-x: auto; }
</style>
