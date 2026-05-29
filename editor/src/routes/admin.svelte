<script lang="ts">
  // Admin dashboard. Visible only to users with is_admin=true. Reachable
  // via /admin (the orchestrator's static handler serves the SPA, which
  // routes via parseRoute → 'admin'). The page itself re-checks /me on
  // mount and bounces non-admins.

  import { onMount } from 'svelte';
  import {
    me,
    adminListUsers,
    adminApprove,
    adminReject,
    type AdminUserRow,
  } from '../lib/api';

  let users = $state<AdminUserRow[]>([]);
  let loaded = $state(false);
  let error = $state<string | null>(null);
  let phase = $state<'checking' | 'forbidden' | 'ready'>('checking');
  // Per-row "in flight" tracker so the buttons go to a disabled spinner
  // state while their HTTP call is pending. Keyed by user_id.
  let acting = $state<Record<string, boolean>>({});
  // Per-row expand toggle for the essays. Default collapsed for tidy list.
  let expanded = $state<Record<string, boolean>>({});

  // Filter chips at the top — clicking switches the visible subset.
  type Filter = 'all' | 'pending' | 'approved' | 'rejected';
  let filter = $state<Filter>('pending'); // default to what needs attention

  const visible = $derived(
    users.filter((u) => filter === 'all' || u.status === filter),
  );

  // Counts to label the filter chips.
  const counts = $derived({
    all: users.length,
    pending: users.filter((u) => u.status === 'pending').length,
    approved: users.filter((u) => u.status === 'approved').length,
    rejected: users.filter((u) => u.status === 'rejected').length,
  });

  async function load() {
    try {
      users = await adminListUsers();
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loaded = true;
    }
  }

  async function approve(userId: string) {
    acting = { ...acting, [userId]: true };
    try {
      const updated = await adminApprove(userId);
      users = users.map((u) => (u.user_id === userId ? updated : u));
    } catch (e) {
      error = (e as Error).message;
    } finally {
      acting = { ...acting, [userId]: false };
    }
  }

  async function reject(userId: string) {
    acting = { ...acting, [userId]: true };
    try {
      const updated = await adminReject(userId);
      users = users.map((u) => (u.user_id === userId ? updated : u));
    } catch (e) {
      error = (e as Error).message;
    } finally {
      acting = { ...acting, [userId]: false };
    }
  }

  function toggleExpand(userId: string) {
    expanded = { ...expanded, [userId]: !expanded[userId] };
  }

  function fmtDate(unixSec: number): string {
    return new Date(unixSec * 1000).toISOString().slice(0, 10);
  }

  onMount(async () => {
    try {
      const m = await me();
      if (!m.is_admin) {
        phase = 'forbidden';
        return;
      }
      phase = 'ready';
      await load();
    } catch (err) {
      // Not authenticated — bounce to login.
      window.location.hash = '#/login';
    }
  });
</script>

<svelte:head><title>Admin — homa</title></svelte:head>

{#if phase === 'checking'}
  <div class="page"><p class="status">…</p></div>
{:else if phase === 'forbidden'}
  <div class="page">
    <h1>Forbidden</h1>
    <p class="status">This page is admin-only. <a href="#/editor">Back to editor</a>.</p>
  </div>
{:else}
  <div class="page">
    <header>
      <h1>Applications</h1>
      <a class="back" href="#/editor">← back to editor</a>
    </header>

    {#if error}<div class="error">{error}</div>{/if}

    {#if !loaded}
      <p class="status">Loading…</p>
    {:else}
      <div class="filters">
        {#each ['pending', 'approved', 'rejected', 'all'] as f}
          <button
            class:active={filter === f}
            onclick={() => (filter = f as Filter)}
          >{f} ({counts[f as Filter]})</button>
        {/each}
      </div>

      {#if visible.length === 0}
        <p class="status">No {filter === 'all' ? '' : filter} applications.</p>
      {:else}
        <ul class="users">
          {#each visible as u (u.user_id)}
            <li class="user user-{u.status}">
              <div class="row">
                <div class="who">
                  <div class="primary">
                    <span class="username">{u.username}</span>
                    {#if u.is_admin}<span class="badge admin">admin</span>{/if}
                    <span class="badge status-{u.status}">{u.status}</span>
                  </div>
                  <div class="secondary">
                    {u.email}
                    <span class="meta">· joined {fmtDate(u.created_at)}</span>
                    {#if u.name}<span class="meta">· {u.name}</span>{/if}
                  </div>
                </div>
                <div class="actions">
                  <button class="expand" onclick={() => toggleExpand(u.user_id)}>
                    {expanded[u.user_id] ? '▾' : '▸'} essays
                  </button>
                  {#if u.status === 'pending'}
                    <button
                      class="approve"
                      disabled={acting[u.user_id]}
                      onclick={() => approve(u.user_id)}
                    >Approve</button>
                    <button
                      class="reject"
                      disabled={acting[u.user_id]}
                      onclick={() => reject(u.user_id)}
                    >Reject</button>
                  {:else if u.status === 'rejected'}
                    <button
                      class="approve"
                      disabled={acting[u.user_id]}
                      onclick={() => approve(u.user_id)}
                      title="Change your mind: flip rejected → approved"
                    >Approve anyway</button>
                  {:else if u.status === 'approved' && !u.is_admin}
                    <button
                      class="reject"
                      disabled={acting[u.user_id]}
                      onclick={() => reject(u.user_id)}
                      title="Revoke: flip approved → rejected"
                    >Reject</button>
                  {/if}
                </div>
              </div>

              {#if expanded[u.user_id]}
                <div class="essays">
                  <section>
                    <h3>Why joining the White Tower</h3>
                    <p>{u.join_reason}</p>
                  </section>
                  <section>
                    <h3>Mystery to investigate</h3>
                    <p>{u.mystery_interest}</p>
                  </section>
                  <section>
                    <h3>Background</h3>
                    <p>{u.background}</p>
                  </section>
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </div>
{/if}

<style>
  .page {
    max-width: 880px;
    margin: 2rem auto;
    padding: 0 1.25rem;
    font-family: 'Inter', system-ui, sans-serif;
  }
  header {
    display: flex; align-items: baseline; justify-content: space-between;
    margin-bottom: 1.5rem;
  }
  h1 { margin: 0; font-size: 1.5rem; font-weight: 600; }
  .back { color: #666; text-decoration: none; font-size: 0.85rem; }
  .back:hover { text-decoration: underline; }
  .status { color: #888; }
  .error {
    padding: 0.75rem 1rem; background: #fee;
    border-left: 3px solid #c44;
    color: #800; margin-bottom: 1rem;
  }

  .filters { display: flex; gap: 0.5rem; margin-bottom: 1.5rem; }
  .filters button {
    padding: 0.4rem 0.85rem;
    border: 1px solid #ddd; border-radius: 999px;
    background: #fafafa; color: #555;
    cursor: pointer;
    font-size: 0.82rem;
    text-transform: capitalize;
  }
  .filters button.active {
    background: #1f6feb; color: #fff; border-color: #1f6feb;
  }
  .filters button:hover:not(.active) { background: #eee; }

  ul.users { list-style: none; padding: 0; margin: 0; }
  .user {
    border: 1px solid #e6e6e6;
    border-radius: 6px;
    margin-bottom: 0.6rem;
    background: #fff;
  }
  .user-pending  { border-left: 3px solid #d4a017; }
  .user-approved { border-left: 3px solid #2c7c41; }
  .user-rejected { border-left: 3px solid #b34a4a; background: #fcfafa; }

  .row {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.7rem 1rem;
    gap: 1rem;
  }
  .who { min-width: 0; }
  .primary { display: flex; align-items: center; gap: 0.5rem; }
  .username { font-weight: 600; }
  .secondary {
    color: #666; font-size: 0.82rem; margin-top: 0.15rem;
  }
  .meta { color: #999; }

  .badge {
    font-size: 0.65rem; padding: 0.1rem 0.45rem;
    border-radius: 9px;
    text-transform: uppercase; letter-spacing: 0.05em;
  }
  .badge.status-pending  { background: #fef3d4; color: #88660b; }
  .badge.status-approved { background: #e3f3e7; color: #185a26; }
  .badge.status-rejected { background: #f6e1e1; color: #893030; }
  .badge.admin           { background: #1f6feb; color: #fff; }

  .actions { display: flex; gap: 0.4rem; flex-shrink: 0; }
  .actions button {
    padding: 0.35rem 0.8rem;
    border: 1px solid #ddd; border-radius: 4px;
    background: #fff; cursor: pointer;
    font-size: 0.8rem;
  }
  .actions button.approve {
    background: #2c7c41; color: #fff; border-color: #2c7c41;
  }
  .actions button.approve:hover:not(:disabled) { background: #266b38; }
  .actions button.reject {
    background: #fff; color: #b34a4a; border-color: #d8a5a5;
  }
  .actions button.reject:hover:not(:disabled) {
    background: #b34a4a; color: #fff; border-color: #b34a4a;
  }
  .actions button.expand { color: #666; }
  .actions button:disabled { opacity: 0.5; cursor: wait; }

  .essays {
    padding: 0.25rem 1rem 1rem; border-top: 1px solid #f0f0f0;
    background: #fafafa;
  }
  .essays section { margin-top: 0.85rem; }
  .essays h3 {
    font-size: 0.72rem; text-transform: uppercase;
    letter-spacing: 0.05em; color: #888;
    margin: 0 0 0.25rem;
  }
  .essays p { margin: 0; line-height: 1.5; color: #333; white-space: pre-wrap; }
</style>
