<script lang="ts">
  // First-time user guidelines. Edit the section bodies directly —
  // each <section class="g-section"> is one section, intentionally
  // simple HTML so you can paste markdown-rendered or hand-written
  // copy without fighting the layout.
  //
  // No fetch / no auth check. Page is meant to be readable always.
  // Reachable via the editor header link or by direct navigation
  // to /guidelines (orchestrator serves the SPA index for that path).
</script>

<svelte:head><title>Guidelines — homa</title></svelte:head>

<div class="page">
  <header>
    <h1>Guidelines</h1>
    <a class="back" href="#/editor">← back to editor</a>
  </header>

  <section id="editor" class="g-section">
    <h2>Using the editor</h2>

    <p>
      This is a living website that you change and build as projects continue.
      Ask the AI to make changes and build the website. You may also use the
      VS Code tab in the editor to directly change the source code.
    </p>

    <h3>The chat panel</h3>
    <p>
      The Admin funds the tokens in the AI chat. Use it only for White Tower
      purposes. It can be used to:
    </p>
    <ul>
      <li>Modify and build the website.</li>
      <li>Summarize and explain research files.</li>
      <li>Do research.</li>
      <li>Be creative with it.</li>
    </ul>
    <p>
      When finished with a change ask the AI to commit and make a PR for merge.
    </p>

    <h3>The DM tabs</h3>
    <p>Send DMs to other White Tower members.</p>

    <h3>Opening VS Code</h3>
    <p>You may view and directly change the source code of the site in VS Code mode.</p>
  </section>

  <section id="sandbox" class="g-section">
    <h2>Your AI sandbox</h2>
    <p>
      Your session runs in a sandbox which is a branch of the main site.
      The LLM has access to your worktree, can read and write research files,
      and can run shell commands. Use it to build and change the website and
      to carry out project research.
    </p>
  </section>

  <section id="projects" class="g-section">
    <h2>Projects</h2>
    <p>Our open projects:</p>
    <ul>
      <li>
        <strong>Financial Markets Research.</strong> We've done extensive
        research on crypto markets trading and macro strategies and have a
        trading platform coded up. Contact the admin (0xquasar) if interested
        in joining.
      </li>
      <li>
        <strong>Ancient Philosophy Research.</strong> Quite a bit has been
        lost in translation over 2000+ years. We are using AI to directly
        translate Ancient Greek and Classical Chinese texts. In particular,
        mathematical characterization is ongoing on the I Ching.
      </li>
    </ul>
    <p>
      Want to join an active project? Want to start up a new one? Discuss
      a plan with the admin (0xquasar). 
      
      Use the AI to push the boundaries of what was possible. The AI chat is designed 
      to read and edit research files. Ask it to summarize and explain research, ask it 
      to do research. 
    </p>
  </section>

  <footer class="g-foot">
    <a class="back" href="#/editor">← back to editor</a>
  </footer>
</div>

<style>
  .page {
    max-width: 760px;
    margin: 2.5rem auto;
    padding: 0 1.5rem 4rem;
    font-family: 'Inter', system-ui, sans-serif;
    color: #222;
    line-height: 1.55;
  }
  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 1.75rem;
    border-bottom: 1px solid #eee;
    padding-bottom: 1rem;
  }
  h1 {
    font-family: 'Cormorant Garamond', Georgia, serif;
    font-size: 2.2rem;
    font-weight: 500;
    margin: 0;
  }
  .back {
    color: #777;
    text-decoration: none;
    font-size: 0.85rem;
  }
  .back:hover { color: #222; text-decoration: underline; }

  /* Sticky table-of-contents at the top of the body. Anchors target
     each section's id; smooth scroll handled by `scroll-behavior`. */
  :global(html) { scroll-behavior: smooth; }
  .toc {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem 0.85rem;
    padding: 0.85rem 1rem;
    background: #fafafa;
    border: 1px solid #eee;
    border-radius: 6px;
    margin-bottom: 2.5rem;
    font-size: 0.82rem;
  }
  .toc a {
    color: #1f6feb;
    text-decoration: none;
  }
  .toc a:hover { text-decoration: underline; }

  .g-section {
    margin-bottom: 2.5rem;
    scroll-margin-top: 1.5rem; /* offset for #anchor jumps */
  }
  .g-section h2 {
    font-family: 'Cormorant Garamond', Georgia, serif;
    font-size: 1.55rem;
    font-weight: 500;
    margin: 0 0 0.85rem;
    padding-bottom: 0.35rem;
    border-bottom: 1px solid #eee;
  }
  /* Subsection headers — same serif language as h1/h2 for visual
     rhyme; smaller than section h2 (1.55) but clearly above body
     (1.05) so the hierarchy is unambiguous. Heavier weight (600 vs
     h2's 500) compensates for the small size gap. Extra top margin
     gives each subsection breathing room. */
  .g-section h3 {
    font-family: 'Cormorant Garamond', Georgia, serif;
    font-size: 1.25rem;
    font-weight: 600;
    margin: 2rem 0 0.4rem;
    color: #222;
  }
  /* › guillemet pointer before each subsection header. Pure CSS via
     ::before — no markup change, so any future h3 inherits it
     automatically. Muted color + lighter weight so it gestures
     without competing with the heading text. */
  .g-section h3::before {
    content: '›';
    margin-right: 0.4em;
    color: #999;
    font-weight: 400;
  }
  .g-section p, .g-section ul, .g-section ol {
    margin: 0.6rem 0;
    color: #333;
  }
  .g-section ul, .g-section ol { padding-left: 1.4rem; }
  .g-section li { margin: 0.3rem 0; }
  .g-section code {
    font-family: ui-monospace, 'SF Mono', Menlo, monospace;
    font-size: 0.86em;
    background: #f3f3f3;
    padding: 0.1em 0.35em;
    border-radius: 3px;
  }
  .g-section pre.code {
    background: #f6f6f6;
    border: 1px solid #eee;
    border-radius: 4px;
    padding: 0.8rem 1rem;
    overflow-x: auto;
    font-family: ui-monospace, 'SF Mono', Menlo, monospace;
    font-size: 0.82rem;
    line-height: 1.5;
  }

  .g-foot {
    margin-top: 3rem;
    padding-top: 1.25rem;
    border-top: 1px solid #eee;
    text-align: center;
  }
</style>
