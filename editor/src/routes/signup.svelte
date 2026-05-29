<script lang="ts">
  import { signup } from '../lib/api';

  let email = $state('');
  let password = $state('');
  let username = $state('');
  let name = $state('');
  // Application essay fields — required, captured for operator's
  // manual approval review.
  let joinReason = $state('');
  let mysteryInterest = $state('');
  let background = $state('');
  let error = $state<string | null>(null);
  let submitting = $state(false);
  // After successful submit, swap from form to the pending-approval
  // confirmation screen. Cookie was NOT set — the operator must run
  // `homa approve <userid>` before this user can log in.
  let submittedPending = $state(false);

  // Strict ASCII pattern mirroring orchestrator's usernamePattern. Browser
  // `pattern` attribute gives instant feedback before submit; server still
  // validates (canonical source of truth).
  const usernameRegex = '^[a-z0-9_]{3,32}$';
  const ESSAY_MIN = 20;

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    submitting = true;
    try {
      const r = await signup(
        email,
        password,
        username,
        {
          join_reason: joinReason,
          mystery_interest: mysteryInterest,
          background,
        },
        name || undefined,
      );
      // Pending-approval gate: no cookie, no redirect. Show the
      // confirmation screen instead. `r.pending` is always true under
      // the current flow; future direct-approve operator paths could
      // flip it false and route to /editor.
      if (r.pending) {
        submittedPending = true;
      } else {
        window.location.hash = '#/editor';
      }
    } catch (err) {
      error = (err as Error).message;
    } finally {
      submitting = false;
    }
  }
</script>

<div class="auth-page">
  {#if submittedPending}
    <div class="card pending">
      <h1>Application submitted</h1>
      <p>
        Your application is under review. You'll be granted access once an
        operator has read your essays.
      </p>
      <p class="muted">
        Try <a href="#/login">logging in</a> later to see if you've been
        approved. Until then, login will tell you the application is pending.
      </p>
    </div>
  {:else}
  <form class="card" onsubmit={onSubmit}>
    <h1>Apply to the White Tower</h1>
    <p class="intro">
      Your application will be reviewed before access is granted.
      Please answer thoughtfully.
    </p>

    <fieldset>
      <legend>Account</legend>
      <label>Email <input type="email" bind:value={email} required autocomplete="email" /></label>
      <label>Password <input type="password" bind:value={password} required minlength="8" autocomplete="new-password" /></label>
      <label>
        Username
        <input
          type="text"
          bind:value={username}
          required
          pattern={usernameRegex}
          minlength="3"
          maxlength="32"
          autocomplete="username"
          title="3-32 chars; lowercase a-z, digits, underscore"
          placeholder="e.g. alice_42"
        />
        <small>Shown on forum posts. 3-32 chars: a-z, 0-9, _</small>
      </label>
      <label>Name (optional) <input type="text" bind:value={name} autocomplete="name" /></label>
    </fieldset>

    <fieldset>
      <legend>Application</legend>
      <label>
        Why are you interested in joining the White Tower?
        <textarea
          bind:value={joinReason}
          required
          minlength={ESSAY_MIN}
          rows="4"
          placeholder="Speak plainly."
        ></textarea>
      </label>
      <label>
        What mystery are you interested in investigating?
        <textarea
          bind:value={mysteryInterest}
          required
          minlength={ESSAY_MIN}
          rows="4"
          placeholder="A question, a problem, a thread you want to pull on."
        ></textarea>
      </label>
      <label>
        What is your background?
        <textarea
          bind:value={background}
          required
          minlength={ESSAY_MIN}
          rows="4"
          placeholder="What you've done, what you study, what tools you wield."
        ></textarea>
      </label>
    </fieldset>

    {#if error}<div class="error">{error}</div>{/if}
    <button type="submit" disabled={submitting}>{submitting ? '…' : 'Submit application'}</button>
    <p>Have an account? <a href="#/login">Log in</a></p>
  </form>
  {/if}
</div>

<style>
  .auth-page { display: flex; justify-content: center; padding: 2rem 1rem; }
  .card {
    display: flex; flex-direction: column; gap: 1rem;
    width: 100%; max-width: 540px;
    padding: 1.75rem;
    border: 1px solid #ddd; border-radius: 6px;
    background: #fff;
  }
  h1 { margin: 0; font-size: 1.5rem; }
  .intro { color: #555; font-size: 0.9rem; margin: 0; }
  fieldset {
    border: 0; padding: 0; margin: 0;
    display: flex; flex-direction: column; gap: 0.75rem;
  }
  legend {
    font-size: 0.78rem; text-transform: uppercase;
    letter-spacing: 0.05em; color: #888;
    padding: 0 0 0.25rem;
  }
  label { display: flex; flex-direction: column; font-size: 0.85rem; gap: 0.25rem; }
  small { color: #666; font-size: 0.72rem; }
  input { padding: 0.5rem; border: 1px solid #ccc; border-radius: 4px; }
  textarea {
    padding: 0.6rem;
    border: 1px solid #ccc; border-radius: 4px;
    font: inherit; resize: vertical;
    min-height: 5rem;
  }
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
  .pending { padding: 2.5rem 2rem; text-align: center; }
  .pending h1 { margin-bottom: 0.75rem; }
  .pending p { color: #444; line-height: 1.45; margin: 0.5rem 0; }
  .pending .muted { color: #777; font-size: 0.85rem; }
</style>
