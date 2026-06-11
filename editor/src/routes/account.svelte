<script lang="ts">
  // Account settings — currently just password change. Auth-gated: the
  // page fetches /me on mount; on 401 it bounces to login.
  //
  // On a successful change the server revokes every other session and
  // re-issues THIS browser's cookie, so we stay logged in here but any
  // other device is logged out.

  import { onMount } from 'svelte';
  import { me, changePassword } from '../lib/api';

  const PASSWORD_MIN = 8;

  let email = $state('');
  let loaded = $state(false);

  let current = $state('');
  let next = $state('');
  let confirm = $state('');

  let submitting = $state(false);
  let error = $state<string | null>(null);
  let done = $state(false);

  onMount(async () => {
    try {
      const m = await me();
      email = m.email;
    } catch {
      window.location.hash = '#/login';
      return;
    }
    loaded = true;
  });

  const nextOk = $derived(next.length >= PASSWORD_MIN);
  const matchOk = $derived(next.length > 0 && next === confirm);
  const formOk = $derived(current.length > 0 && nextOk && matchOk);

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    if (!formOk) return;
    submitting = true;
    try {
      await changePassword(current, next);
      done = true;
      current = next = confirm = '';
    } catch (err) {
      error = (err as Error).message;
    } finally {
      submitting = false;
    }
  }
</script>

<div class="page">
  <header>
    <a class="back" href="#/editor">← Editor</a>
    <h1>Account</h1>
  </header>

  {#if !loaded}
    <p class="muted">Loading…</p>
  {:else}
    <p class="email">Signed in as <strong>{email}</strong></p>

    <section class="card">
      <h2>Change password</h2>

      {#if done}
        <div class="success">
          <p>Password changed. Other devices have been logged out.</p>
          <a href="#/editor">Back to the editor →</a>
        </div>
      {:else}
        <form onsubmit={onSubmit}>
          <label>
            Current password
            <input type="password" bind:value={current} autocomplete="current-password" required />
          </label>
          <label>
            New password
            <input type="password" bind:value={next} autocomplete="new-password" required />
            <small class="hint" class:err={next.length > 0 && !nextOk}>
              At least {PASSWORD_MIN} characters
              {#if next.length > 0 && !nextOk}({next.length} so far){/if}
            </small>
          </label>
          <label>
            Confirm new password
            <input type="password" bind:value={confirm} autocomplete="new-password" required />
            {#if confirm.length > 0 && !matchOk}
              <small class="hint err">Passwords don't match</small>
            {/if}
          </label>

          {#if error}<div class="error">{error}</div>{/if}

          <button type="submit" disabled={submitting || !formOk}>
            {submitting ? '…' : 'Change password'}
          </button>
          <p class="note">
            Changing your password logs out every other device.
          </p>
        </form>
      {/if}
    </section>
  {/if}
</div>

<style>
  .page {
    max-width: 520px;
    margin: 2rem auto;
    padding: 0 1.25rem;
    font-family: 'Inter', system-ui, sans-serif;
  }
  header { display: flex; align-items: baseline; gap: 1rem; margin-bottom: 1rem; }
  .back { color: #06f; text-decoration: none; font-size: 0.85rem; }
  .back:hover { text-decoration: underline; }
  h1 { font-size: 1.4rem; margin: 0; }
  .email { color: #555; font-size: 0.9rem; }
  .muted { color: #888; }

  .card {
    border: 1px solid #e0e0e0;
    border-radius: 8px;
    padding: 1.5rem;
    background: #fff;
  }
  h2 { font-size: 1.05rem; margin: 0 0 1rem; }

  form { display: flex; flex-direction: column; gap: 0.85rem; }
  label { display: flex; flex-direction: column; gap: 0.25rem; font-size: 0.85rem; }
  input {
    padding: 0.5rem; border: 1px solid #ccc; border-radius: 4px; font: inherit;
  }
  .hint { color: #888; font-size: 0.75rem; }
  .hint.err { color: #c00; }
  .error { color: #c00; font-size: 0.9rem; }
  button {
    padding: 0.7rem; background: #222; color: white;
    border: none; border-radius: 4px; cursor: pointer; font-size: 0.95rem;
  }
  button:disabled { background: #999; cursor: not-allowed; }
  .note { color: #888; font-size: 0.78rem; margin: 0; }

  .success p { color: #2c7c41; margin: 0 0 0.75rem; }
  .success a { color: #06f; text-decoration: none; }
  .success a:hover { text-decoration: underline; }
</style>
