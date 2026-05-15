<script lang="ts">
  import { signup } from '../lib/api';

  let email = $state('');
  let password = $state('');
  let name = $state('');
  let error = $state<string | null>(null);
  let submitting = $state(false);

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    submitting = true;
    try {
      await signup(email, password, name || undefined);
      window.location.hash = '#/editor';
    } catch (err) {
      error = (err as Error).message;
    } finally {
      submitting = false;
    }
  }
</script>

<div class="auth-page">
  <form class="card" onsubmit={onSubmit}>
    <h1>Create account</h1>
    <label>Email <input type="email" bind:value={email} required autocomplete="email" /></label>
    <label>Password <input type="password" bind:value={password} required minlength="8" autocomplete="new-password" /></label>
    <label>Name (optional) <input type="text" bind:value={name} autocomplete="name" /></label>
    {#if error}<div class="error">{error}</div>{/if}
    <button type="submit" disabled={submitting}>{submitting ? '…' : 'Sign up'}</button>
    <p>Have an account? <a href="#/login">Log in</a></p>
  </form>
</div>

<style>
  .auth-page { display: flex; justify-content: center; padding: 4rem 1rem; }
  .card { display: flex; flex-direction: column; gap: 0.75rem; width: 100%; max-width: 360px; padding: 1.5rem; border: 1px solid #ddd; border-radius: 6px; background: #fff; }
  h1 { margin: 0 0 0.5rem; font-size: 1.4rem; }
  label { display: flex; flex-direction: column; font-size: 0.85rem; gap: 0.25rem; }
  input { padding: 0.5rem; border: 1px solid #ccc; border-radius: 4px; }
  .error { color: #c00; font-size: 0.9rem; }
  button { padding: 0.6rem; background: #222; color: white; border: none; border-radius: 4px; cursor: pointer; }
  button:disabled { background: #999; }
  a { color: #06f; }
</style>
