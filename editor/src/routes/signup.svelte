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

  // Two-step UX: applicant reads the manifesto first, clicks 'I'm in'
  // to reach the form. State machine across all screens:
  //
  //   manifesto  →  form  →  submittedPending
  //
  // 'manifesto' is the default so a fresh /signup load lands on it
  // every time (no localStorage persistence — operator wants every
  // applicant to read it).
  type Step = 'manifesto' | 'form';
  let step = $state<Step>('manifesto');

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
  {:else if step === 'manifesto'}
    <!--
      Manifesto step — applicant lands here on /signup. Edit the body
      directly: replace placeholder paragraphs with the actual White
      Tower manifesto. Keep it readable; the "I'm in" button advances
      to the form.
    -->
    <article class="card manifesto">
      <h1>The White Tower</h1>

      <section class="m-body">
        <p class="lede">
          Do you feel the world is hardly what it seems? 
          That lost ancient knowledge goes far deeper than what is told? 
          Do you feel the world is disintegrating? 
          That the leaders are corrupt and unfeeling? 
          That work is divorced from meaning
          And action from heart from mind? 
          
          Do you feel we are on the cusp of great change, 
          The turning of an era, 
          That champions are needed, 
          To unite our actions,
          unite our minds, 
          unite our hearts? 
        </p>

        <p>
          We prepare to surf the brewing chaos. We use the latest
          technologies to self-direct our research. We research the past —
          learnings and wisdom from the great lost civilizations. We
          research the present — the structure of the financial markets
          and the modern system. We fund ourselves and direct ourselves.
          We direct the future.
        </p>

        <p class="m-close">
          Are you one of the worthy? Will you give yourself to serve the
          greatest of causes? Will you join us?
        </p>
      </section>

      <div class="m-cta">
        <button class="primary" onclick={() => (step = 'form')}>I'm in — apply</button>
        <a class="secondary" href="#/login">I already have an account</a>
      </div>
    </article>
  {:else}
  <form class="card" onsubmit={onSubmit}>
    <button
      type="button"
      class="back-to-manifesto"
      onclick={() => (step = 'manifesto')}
    >← back to the manifesto</button>
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

  /* Manifesto step — reads like a short essay, not a form. Wider
     comfortable measure than the form card; serif body for reading
     ergonomics; the CTA hits hard at the bottom. */
  .manifesto {
    max-width: 640px;
    padding: 2.5rem 2.75rem;
    font-family: 'Cormorant Garamond', Georgia, serif;
    line-height: 1.65;
  }
  .manifesto h1 {
    font-size: 2.3rem;
    font-weight: 500;
    text-align: center;
    margin: 0 0 1.75rem;
    letter-spacing: 0.01em;
  }
  .m-body { color: #333; font-size: 1.05rem; }
  .m-body p { margin: 0 0 1.25rem; }
  /* Lede: the hand-broken lines are intentional (read as
     incantation). pre-line preserves newlines while still collapsing
     runs of spaces; italic + center keeps the poetic frame. Blank
     lines in the source render as visual breath between strophes. */
  .m-body .lede {
    font-size: 1.15rem;
    color: #222;
    font-style: italic;
    text-align: center;
    white-space: pre-line;
    margin-bottom: 2rem;
  }
  /* Closing question — the moment the reader decides. Display serif
     (Cinzel, Roman-inscription style), heavier weight, wider tracking,
     centered. Visually weightier than the body so the eye stops here
     and the 'I'm in' CTA below answers it. */
  .m-body .m-close {
    margin-top: 2.25rem;
    padding-top: 1.75rem;
    border-top: 1px solid #ddd;
    font-family: 'Cinzel', 'Cormorant Garamond', Georgia, serif;
    font-weight: 500;
    font-size: 1.15rem;
    line-height: 1.6;
    letter-spacing: 0.04em;
    color: #1a1a1a;
    text-align: center;
  }
  .m-cta {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.6rem;
    margin-top: 2rem;
    padding-top: 1.5rem;
    border-top: 1px solid #eee;
  }
  .m-cta button.primary {
    padding: 0.75rem 2rem;
    border: none;
    border-radius: 4px;
    background: #222;
    color: white;
    cursor: pointer;
    font: inherit;
    font-size: 1rem;
    font-weight: 500;
    letter-spacing: 0.02em;
  }
  .m-cta button.primary:hover { background: #000; }
  .m-cta .secondary {
    font-family: 'Inter', system-ui, sans-serif;
    font-size: 0.85rem;
    color: #888;
  }

  /* Back-to-manifesto link at the top of the form card. Sans-serif
     so it doesn't blend with the form labels visually. */
  .back-to-manifesto {
    align-self: flex-start;
    padding: 0;
    border: none;
    background: transparent;
    color: #888;
    cursor: pointer;
    font-size: 0.8rem;
    margin-bottom: 0.5rem;
  }
  .back-to-manifesto:hover { color: #222; }
</style>
