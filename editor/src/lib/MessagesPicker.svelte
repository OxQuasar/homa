<script lang="ts">
  // Picker dropdown for the tab strip's "+" button. Two sections:
  //
  //   Recent conversations
  //     - peers the user has DM history with
  //     - each shows username + last_preview + unread badge
  //     - click to open/focus that tab
  //
  //   Find a user
  //     - search input filters all registered users (/api/users)
  //     - exclude self + already-contacted (those are in the recent list)
  //     - click to start a new conversation
  //
  // Parent owns the open/close state + the data; this component just
  // renders + notifies onPick / onClose.

  import type { DmConversation } from './types';

  interface UserSummary {
    user_id: string;
    username: string;
    created_at: number;
  }

  const {
    conversations,
    allUsers,
    selfUserId,
    onPick,
    onClose
  }: {
    conversations: DmConversation[];
    allUsers: UserSummary[];
    selfUserId: string;
    onPick: (peerId: string, username: string) => void;
    onClose: () => void;
  } = $props();

  let query = $state('');

  // Users available to start a NEW conversation with: registered, not
  // self, not already in conversations. Filtered by the search query
  // (case-insensitive substring against username).
  const candidates = $derived.by(() => {
    const contacted = new Set(conversations.map((c) => c.peer_id));
    const q = query.trim().toLowerCase();
    return allUsers
      .filter((u) => u.user_id !== selfUserId && !contacted.has(u.user_id))
      .filter((u) => !q || u.username.toLowerCase().includes(q))
      .slice(0, 20);
  });
</script>

<!-- backdrop: click outside the panel closes the picker -->
<div
  class="backdrop"
  onclick={onClose}
  onkeydown={(e) => e.key === 'Escape' && onClose()}
  role="button"
  tabindex="-1"
  aria-label="Close picker"
></div>

<div class="picker" role="dialog" aria-label="Start or open a message">
  {#if conversations.length > 0}
    <section>
      <h4>Recent</h4>
      <ul>
        {#each conversations as c (c.peer_id)}
          <li>
            <button
              class="row"
              onclick={() => { onPick(c.peer_id, c.peer_username); onClose(); }}
            >
              <span class="who">
                {c.peer_username}
                {#if c.unread_count > 0}<span class="badge">{c.unread_count}</span>{/if}
              </span>
              <span class="preview">{c.last_preview}</span>
            </button>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  <section>
    <h4>Find user</h4>
    <input
      type="text"
      bind:value={query}
      placeholder="search by username…"
      autofocus
    />
    {#if candidates.length === 0}
      <p class="empty">{query ? 'No matches.' : 'No other users available.'}</p>
    {:else}
      <ul>
        {#each candidates as u (u.user_id)}
          <li>
            <button
              class="row"
              onclick={() => { onPick(u.user_id, u.username); onClose(); }}
            >
              <span class="who">{u.username}</span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; z-index: 50;
    background: rgba(0,0,0,0.15);
    cursor: default;
  }
  .picker {
    position: absolute; top: 2.4rem; right: 0.4rem; z-index: 51;
    width: 320px; max-height: 70vh; overflow-y: auto;
    background: #fff; border: 1px solid #bbb; border-radius: 6px;
    box-shadow: 0 4px 16px rgba(0,0,0,0.15);
    padding: 0.5rem;
    font-size: 0.85rem;
  }
  section + section { margin-top: 0.75rem; padding-top: 0.5rem; border-top: 1px solid #eee; }
  h4 { margin: 0 0 0.3rem; font-size: 0.7rem; text-transform: uppercase; color: #888; font-weight: 600; }
  ul { list-style: none; margin: 0; padding: 0; }
  li { padding: 0.05rem 0; }
  .row {
    width: 100%; display: flex; flex-direction: column; gap: 0.1rem;
    text-align: left; padding: 0.4rem 0.5rem;
    background: transparent; border: 0; border-radius: 4px;
    cursor: pointer; font: inherit; color: inherit;
  }
  .row:hover { background: #f5f7fb; }
  .who { font-weight: 600; }
  .badge {
    display: inline-block; margin-left: 0.4rem;
    padding: 0 0.35rem; border-radius: 9px;
    background: #1f6feb; color: #fff;
    font-size: 0.65rem; line-height: 1.5;
  }
  .preview { color: #888; font-size: 0.78rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  input { width: 100%; padding: 0.4rem 0.5rem; border: 1px solid #ccc; border-radius: 4px; font: inherit; }
  .empty { color: #888; padding: 0.4rem 0.5rem; margin: 0.3rem 0 0; }
</style>
