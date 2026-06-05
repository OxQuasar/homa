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
  // Honeypot field — bound to a visually-hidden input that humans
  // don't see (and browser password managers / autofill skip too).
  // Bots scraping the form fill every input; the backend rejects any
  // signup with website != ''. Stays $state for reactivity, but in
  // normal use it never changes from '' so the server passes the check.
  let website = $state('');
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
  const ESSAY_MAX = 4000;
  const PASSWORD_MIN = 8;

  // Live validity per field — surfaced inline so a too-short essay
  // shows "4 / 20 minimum" in red as the user types, instead of the
  // browser's easily-missed native popup that only fires on submit.
  // attemptedSubmit gates the harshest visual cues (red borders) so a
  // pristine form doesn't look angry on first load.
  let attemptedSubmit = $state(false);
  const usernameOk = $derived(/^[a-z0-9_]{3,32}$/.test(username));
  const passwordOk = $derived(password.length >= PASSWORD_MIN);
  const emailOk = $derived(/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email));
  const joinOk = $derived(joinReason.length >= ESSAY_MIN);
  const mysteryOk = $derived(mysteryInterest.length >= ESSAY_MIN);
  const backgroundOk = $derived(background.length >= ESSAY_MIN);
  const formOk = $derived(
    emailOk && passwordOk && usernameOk && joinOk && mysteryOk && backgroundOk,
  );

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    attemptedSubmit = true;
    // Client-side guard: keep the user on the form with red borders +
    // counters until everything's valid. The server's the canonical
    // validator (and would return a precise 400 message), but the
    // client check spares the round-trip + makes the failure visible
    // exactly where it occurred.
    if (!formOk) {
      submitting = false;
      return;
    }
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
        website,
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
          We forge a new way forward.
        </p>

        <p class="m-close">
          Are you one of the worthy? Will you give yourself to serve a
          great cause? Will you apply to join us?
        </p>
      </section>

      <div class="m-cta">
        <button class="primary" onclick={() => (step = 'form')}>I'm in — apply</button>
        <a class="secondary" href="#/login">I already have an account</a>
      </div>
    </article>
  {:else}
  <!--
    novalidate disables the browser's native HTML5 validation popup
    so our custom red borders + counters + banner are the only error
    surface. Without novalidate, the browser intercepts the submit
    event before our onSubmit handler can run — user clicks Submit
    and gets a tiny native tooltip that disappears on next click,
    while our attemptedSubmit flag never flips to true.

    The HTML5 attributes (required, minlength, pattern) stay on the
    inputs — they're still useful for autofill + browser hints — but
    they no longer GATE submission.
  -->
  <form class="card" onsubmit={onSubmit} novalidate>
    <!--
      Honeypot. Visually hidden + aria-hidden + tabindex=-1 + autocomplete
      off so screen-readers, keyboards, and password managers all skip it.
      Bots scraping the form fill every input; backend silently drops any
      signup with website != ''. The wrapper div is what hides; the input
      itself stays visible to bot DOM scrapers.
    -->
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
      <label>
        Email
        <input
          type="email"
          bind:value={email}
          required
          autocomplete="email"
          class:invalid={attemptedSubmit && !emailOk}
        />
        {#if attemptedSubmit && !emailOk}
          <small class="hint err">Enter a valid email address.</small>
        {/if}
      </label>
      <label>
        Password
        <input
          type="password"
          bind:value={password}
          required
          minlength={PASSWORD_MIN}
          autocomplete="new-password"
          class:invalid={attemptedSubmit && !passwordOk}
        />
        <small class="hint" class:err={attemptedSubmit && !passwordOk}>
          At least {PASSWORD_MIN} characters
          {#if password.length > 0 && password.length < PASSWORD_MIN}
            ({password.length} so far)
          {/if}
        </small>
      </label>
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
          class:invalid={attemptedSubmit && !usernameOk}
        />
        <small class="hint" class:err={attemptedSubmit && !usernameOk}>
          Shown on forum posts. 3-32 chars: a-z, 0-9, _
        </small>
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
          maxlength={ESSAY_MAX}
          rows="4"
          placeholder="Speak plainly."
          class:invalid={attemptedSubmit && !joinOk}
        ></textarea>
        <small class="hint" class:err={attemptedSubmit && !joinOk}>
          {joinReason.length} / {ESSAY_MIN} minimum
        </small>
      </label>
      <label>
        What mystery are you interested in investigating?
        <textarea
          bind:value={mysteryInterest}
          required
          minlength={ESSAY_MIN}
          maxlength={ESSAY_MAX}
          rows="4"
          placeholder="A question, a problem, a thread you want to pull on."
          class:invalid={attemptedSubmit && !mysteryOk}
        ></textarea>
        <small class="hint" class:err={attemptedSubmit && !mysteryOk}>
          {mysteryInterest.length} / {ESSAY_MIN} minimum
        </small>
      </label>
      <label>
        What is your background?
        <textarea
          bind:value={background}
          required
          minlength={ESSAY_MIN}
          maxlength={ESSAY_MAX}
          rows="4"
          placeholder="What you've done, what you study, what tools you wield."
          class:invalid={attemptedSubmit && !backgroundOk}
        ></textarea>
        <small class="hint" class:err={attemptedSubmit && !backgroundOk}>
          {background.length} / {ESSAY_MIN} minimum
        </small>
      </label>
    </fieldset>

    {#if error}<div class="error">{error}</div>{/if}
    {#if attemptedSubmit && !formOk}
      <div class="error">Please fix the highlighted fields above.</div>
    {/if}
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
  /* Hints sit just under inputs. .err class flips them red when the
     user has attempted submit + the field is invalid. Counts (e.g.
     "4 / 20 minimum") update live as the user types so the threshold
     is visible without trial and error. */
  .hint {
    color: #888;
    font-size: 0.75rem;
    margin-top: 0.25rem;
  }
  .hint.err { color: #c00; }
  /* Red ring on invalid inputs after a submit attempt. Subtle until
     the user clicks Submit; sharp afterwards so it's unambiguous
     which fields need attention. */
  input.invalid, textarea.invalid {
    border-color: #c00;
    background: #fff5f5;
  }
  input.invalid:focus, textarea.invalid:focus {
    outline-color: #c00;
  }
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
  /* Body paragraph (sits between lede + close). Bumped up for reading
     presence; class-scoped selectors for .lede and .m-close win over
     this rule, so they keep their own sizes. */
  .m-body p {
    font-size: 1.2rem;
    line-height: 1.55;
    margin: 0 0 1.25rem;
  }
  /* Lede: the hand-broken lines are intentional (read as
     incantation). pre-line preserves newlines while still collapsing
     runs of spaces; italic + center keeps the poetic frame. Blank
     lines in the source render as visual breath between strophes. */
  .m-body .lede {
    /* Lede is the heart of the manifesto — bumped up so it carries
       the page. Hierarchy: title (2.3) > lede (1.4) > body/close (1.05). */
    font-size: 1.4rem;
    line-height: 1.45;
    color: #222;
    font-style: italic;
    text-align: center;
    white-space: pre-line;
    margin-bottom: 2.25rem;
  }
  /* Closing question — the moment the reader decides. GFS Didot
     (Greek Font Society's classical-revival Didot, the typeface of
     modern Greek scholarship). Centered, with a thin top rule for a
     quiet pause. Deliberately RESTRAINED — same size as body, normal
     weight, subtle tracking — so it doesn't outshine the lede above.
     The intensity comes from typeface character + visual breathing
     room, not heft. */
  .m-body .m-close {
    margin-top: 2.25rem;
    padding-top: 1.5rem;
    border-top: 1px solid #e0e0e0;
    font-family: 'GFS Didot', 'Cormorant Garamond', Georgia, serif;
    font-weight: 400;
    font-size: 1.05rem;
    line-height: 1.6;
    letter-spacing: 0.015em;
    color: #333;
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

  /* Honeypot wrapper — visually removed but kept in the DOM so naive
     bots that read input names still see it. position:absolute + offset
     keeps it out of flow without display:none (which some bot frameworks
     skip). aria-hidden + tabindex=-1 + autocomplete=off keep real users
     from ever stumbling on it. */
  .hp {
    position: absolute;
    left: -10000px;
    width: 1px;
    height: 1px;
    overflow: hidden;
  }
</style>
