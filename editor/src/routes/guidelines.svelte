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

  <nav class="toc" aria-label="table of contents">
    <a href="#welcome">Welcome</a>
    <a href="#the-tower">The White Tower</a>
    <a href="#editor">Using the editor</a>
    <a href="#sandbox">Your AI sandbox</a>
    <a href="#pr">Pull requests &amp; merging</a>
    <a href="#forum">Forum &amp; messages</a>
    <a href="#library">The library</a>
    <a href="#etiquette">Etiquette</a>
  </nav>

  <section id="welcome" class="g-section">
    <h2>Welcome</h2>
    <p>[ TODO: write the welcome — 1–2 short paragraphs.  Who is this for, what they're stepping into, the spirit of the place. ]</p>
  </section>

  <section id="the-tower" class="g-section">
    <h2>What is the White Tower?</h2>
    <p>[ TODO: the high-level frame. What the project is. What members do. What success looks like. ]</p>
  </section>

  <section id="editor" class="g-section">
    <h2>Using the editor</h2>
    <h3>The chat panel</h3>
    <p>[ TODO: what the AI tab is, how to message it, what conversations to expect. ]</p>
    <h3>The DM tabs</h3>
    <p>[ TODO: clicking + opens a DM picker; tabs persist across reload; etc. ]</p>
    <h3>The preview / iframe</h3>
    <p>[ TODO: what the right pane shows, that it's your live site, navigation works. ]</p>
    <h3>Opening VS Code</h3>
    <p>[ TODO: the Open VS Code button, what they can do there. ]</p>
  </section>

  <section id="sandbox" class="g-section">
    <h2>Your AI sandbox</h2>
    <p>[ TODO: explain that the LLM has access to your worktree, can read/write files, can run shell commands. What kinds of tasks to give it. ]</p>
    <h3>What to ask for</h3>
    <ul>
      <li>[ example task 1 ]</li>
      <li>[ example task 2 ]</li>
      <li>[ example task 3 ]</li>
    </ul>
    <h3>What it can read</h3>
    <p>[ TODO: <code>/library/</code> reference content, your worktree, project context (CLAUDE.md). ]</p>
  </section>

  <section id="pr" class="g-section">
    <h2>Pull requests &amp; merging</h2>
    <p>[ TODO: PR branches naming, how to ask the AI to create one, how an operator reviews + merges. ]</p>
    <pre class="code">git checkout -b pr/&lt;userid&gt;/&lt;topic&gt;
# commit work...
</pre>
  </section>

  <section id="forum" class="g-section">
    <h2>Forum &amp; messages</h2>
    <h3>Forum threads</h3>
    <p>[ TODO: how to post, what topics are encouraged, link to /forum. ]</p>
    <h3>Direct messages</h3>
    <p>[ TODO: where DMs live (chat panel tabs), how to reach another user. ]</p>
  </section>

  <section id="library" class="g-section">
    <h2>The library</h2>
    <p>[ TODO: <code>/library</code> on the public site, <code>/library</code> inside the sandbox for the AI. Pointers to subject collections (e.g. i-ching). ]</p>
  </section>

  <section id="etiquette" class="g-section">
    <h2>Etiquette</h2>
    <p>[ TODO: tone, respect, no harassment, scope of acceptable use, what gets accounts rejected. ]</p>
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
  .g-section h3 {
    font-size: 0.95rem;
    font-weight: 600;
    margin: 1.5rem 0 0.5rem;
    color: #444;
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
