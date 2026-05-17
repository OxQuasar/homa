<script lang="ts">
  // Tab strip across the top of the chat pane:
  //   [ AI ] [ alice (3) ] [ bob ] … [ + N ]
  //
  // AI is always present and pinned-left. DM tabs follow, in
  // most-recently-opened order. The "+" button opens the picker
  // (handled by parent — this component just notifies onPickerOpen).
  //
  // Each DM tab carries an unread badge (count of unread messages in
  // that thread). The "+" button carries an aggregate badge of unread
  // messages from peers NOT currently represented as open tabs.
  //
  // Tabs have a small × close button that appears on hover; closing a
  // tab is purely a UI op (clears state via onCloseDm) — server state
  // is untouched.

  import type { ActiveTab, DmTab } from './types';

  const {
    tabs,
    active,
    aiHasNew,
    dmUnread,
    pickerBadge,
    onSelect,
    onCloseDm,
    onPickerOpen
  }: {
    tabs: DmTab[];
    active: ActiveTab;
    aiHasNew: boolean; // optional ping on AI tab when a run completes while on a DM
    dmUnread: Record<string, number>;
    pickerBadge: number; // count of unread from peers NOT in `tabs`
    onSelect: (t: ActiveTab) => void;
    onCloseDm: (peerId: string) => void;
    onPickerOpen: () => void;
  } = $props();

  function isActive(kind: 'ai' | 'dm', peerId?: string): boolean {
    if (kind === 'ai') return active.kind === 'ai';
    return active.kind === 'dm' && active.peerId === peerId;
  }

  // B6: keep the active tab visible. When tabs overflow the strip
  // (many open) and the user activates one off-screen via the picker,
  // scroll it into view. queueMicrotask defers until Svelte has
  // applied the active-class update, so the element exists with its
  // post-update layout.
  let stripEl: HTMLDivElement | undefined = $state();
  $effect(() => {
    active; // dep — re-run when active tab changes
    if (typeof window === 'undefined' || !stripEl) return;
    queueMicrotask(() => {
      const sel =
        active.kind === 'ai'
          ? '.tab.ai'
          : `[data-peer="${CSS.escape(active.peerId)}"]`;
      const el = stripEl?.querySelector(sel) as HTMLElement | null;
      el?.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'nearest' });
    });
  });
</script>

<div class="strip" bind:this={stripEl}>
  <button
    class="tab ai"
    class:active={isActive('ai')}
    onclick={() => onSelect({ kind: 'ai' })}
    title="LLM in this sandbox"
  >
    AI
    {#if aiHasNew && !isActive('ai')}
      <span class="dot" aria-label="new activity"></span>
    {/if}
  </button>

  {#each tabs as t (t.peerId)}
    <div class="tab-wrap" class:active={isActive('dm', t.peerId)}>
      <button
        class="tab dm"
        data-peer={t.peerId}
        onclick={() => onSelect({ kind: 'dm', peerId: t.peerId })}
        title="DM {t.username}"
      >
        {t.username}
        {#if (dmUnread[t.peerId] ?? 0) > 0 && !isActive('dm', t.peerId)}
          <span class="badge">{dmUnread[t.peerId]}</span>
        {/if}
      </button>
      <button
        class="close"
        onclick={(e) => {
          e.stopPropagation();
          onCloseDm(t.peerId);
        }}
        title="Close tab"
        aria-label="Close DM with {t.username}"
      >×</button>
    </div>
  {/each}

  <button
    class="picker"
    onclick={onPickerOpen}
    title="New message"
  >
    +{pickerBadge > 0 ? ` ${pickerBadge}` : ''}
  </button>
</div>

<style>
  .strip {
    display: flex; align-items: stretch; gap: 0.15rem;
    padding: 0.25rem 0.4rem 0;
    border-bottom: 1px solid #ddd;
    background: #fafafa;
    overflow-x: auto;
    /* small font; tab strip is dense by nature */
    font-size: 0.82rem;
  }
  .tab, .picker, .close {
    background: transparent; border: 1px solid transparent;
    cursor: pointer; padding: 0.25rem 0.55rem;
    border-radius: 4px 4px 0 0;
    color: #555;
    line-height: 1.2;
  }
  .tab:hover, .picker:hover { background: #f0f0f0; color: #222; }
  .tab.ai { font-weight: 600; }
  /* Active tab visually attaches to the chat area below by hiding its
     bottom border — overlap with the strip's underline. */
  .tab-wrap.active .tab, .tab.ai.active {
    background: #fff; border-color: #ddd; border-bottom-color: #fff;
    color: #222; font-weight: 600;
  }
  .tab-wrap { display: flex; align-items: stretch; }
  /* × button appears beside its tab; visible-on-hover keeps the strip tidy. */
  .close {
    padding: 0.25rem 0.35rem; font-size: 1rem; color: #aaa;
    visibility: hidden;
  }
  .tab-wrap:hover .close { visibility: visible; }
  .close:hover { color: #c00; background: transparent; }

  .badge {
    display: inline-block; margin-left: 0.3rem;
    padding: 0 0.35rem; border-radius: 9px;
    background: #1f6feb; color: #fff;
    font-size: 0.7rem; line-height: 1.4;
    font-weight: 600;
  }
  .dot {
    display: inline-block; width: 6px; height: 6px;
    margin-left: 0.3rem; border-radius: 50%;
    background: #1f6feb; vertical-align: middle;
  }

  .picker {
    margin-left: auto; font-weight: 600;
    border-color: #ddd; background: #fff;
  }
</style>
