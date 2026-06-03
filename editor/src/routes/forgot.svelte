<script lang="ts">
  // Public password-reset request page. Two states:
  //   1. form — fill email + optional note → submit
  //   2. submitted — confirmation; no info leaked about whether the
  //      email matched.
  //
  // Backend stores a row in password_reset_requests (regardless of
  // match). Admin sees it in /admin and either issues a new password
  // (delivered out-of-band) or dismisses the row.

  import { forgotPassword } from '../lib/api';

  let email = $state('');
  let note = $state('');
  // Honeypot — bots fill, humans don't.
  let website = $state('');
  let error = $state<string | null>(null);
  let submitting = $state(false);
  let submitted = $state(false);

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    submitting = true;
    try {
      await forgotPassword(email, note, website);
      submitted = true;
    } catch (err) {
      error = (err as Error).message;
    } finally {
      submitting = false;
    }
  }
</script>

<div class="auth-page">
  {#if submitted}
    <div class="card done">
      <h1>Request received</h1>
      <p>
        If an account matches the email you entered, an operator will
        reach out with a new password.
      </p>
      <p class="muted">
        <a href="#/login">← back to login</a>
      </p>
    </div>
  {:else}
    <form class="card" onsubmit={onSubmit}>
      <!-- Honeypot input. Visually hidden + aria-hidden + tabindex=-1
           + autocomplete=off so screen readers, keyboards, and
           password managers all skip it. Bots scraping the form fill
           every input; backend silently drops if non-empty. -->
      <div class="hp" aria-hidden="true">
        <label>
          Website
          <input
            type="text"
            name="website"
            autocomplete="off"
            tabindex="-1"
            bind:value={website}
          />
        </label>
      </div>

      <h1>Forgot your password?</h1>
      <p class="intro">
        Enter the email on your account. An operator will review the
        request and send you a new password out-of-band.
      </p>

      <label>
        Email
        <input type="email" bind:value={email} required autocomplete="email" />
      </label>

      <label>
        Note (optional)
        <textarea
          bind:value={note}
          maxlength="500"
          rows="3"
          placeholder="anything helpful for the operator — e.g. 'cleared browser', 'lost authenticator'"
        ></textarea>
      </label>

      {#if error}<div class="error">{error}</div>{/if}
      <button type="submit" disabled={submitting}>
        {submitting ? '…' : 'Submit request'}
      </button>
      <p><a href="#/login">← back to login</a></p>
    </form>
  {/if}
</div>

<style>
  .auth-page { display: flex; justify-content: center; padding: 2rem 1rem; }
  .card {
    display: flex; flex-direction: column; gap: 0.85rem;
    width: 100%; max-width: 480px;
    padding: 1.75rem;
    border: 1px solid #ddd; border-radius: 6px;
    background: #fff;
  }
  h1 { margin: 0; font-size: 1.4rem; }
  .intro { color: #555; font-size: 0.9rem; margin: 0; }
  label { display: flex; flex-direction: column; font-size: 0.85rem; gap: 0.25rem; }
  input, textarea {
    padding: 0.5rem;
    border: 1px solid #ccc; border-radius: 4px;
    font: inherit;
  }
  textarea { resize: vertical; min-height: 4rem; }
  .error { color: #c00; font-size: 0.9rem; }
  button {
    padding: 0.7rem;
    background: #222; color: white;
    border: none; border-radius: 4px;
    cursor: pointer;
    font-size: 0.95rem;
  }
  button:disabled { background: #999; cursor: not-allowed; }
  a { color: #06f; }
  p { margin: 0; }
  .done { padding: 2.5rem 2rem; text-align: center; }
  .done h1 { margin-bottom: 0.75rem; }
  .done p { color: #444; line-height: 1.45; margin: 0.5rem 0; }
  .done .muted { color: #777; font-size: 0.85rem; }

  .hp {
    position: absolute;
    left: -10000px;
    width: 1px;
    height: 1px;
    overflow: hidden;
  }
</style>
