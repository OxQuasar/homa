<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { codeURL, me, logout } from '../lib/api';
  import { openSession, type Session } from '../lib/ws';
  import type {
    BufferedError,
    ChatMessage,
    Event as NousEvent,
    Streaming,
    ToolCall
  } from '../lib/types';
  import { hydrateMessages } from '../lib/history';
  import {
    addToBuffer,
    augmentPrompt,
    originOf,
    parseBeaconMessage
  } from '../lib/iframe_errors';
  import Chat from '../lib/Chat.svelte';

  // Spec: §11 state shape.
  const session = $state({
    messages: [] as ChatMessage[],
    streaming: null as Streaming | null,
    status: 'idle' as 'idle' | 'running',
    previewUrl: '' as string
  });

  let userEmail = $state('');
  let ws: Session | null = null;
  let wsStatus = $state<'connecting' | 'open' | 'closed'>('connecting');
  let workDir = $state('/workspace');
  let sessionId = $state('');
  // wsWasOpen flips true the first time we see status='open'. Drives the
  // auto-logout-on-close behavior: if we never got open we want to leave
  // the user on /editor showing the closed status (so an initial-connect
  // failure doesn't bounce-loop main → login → /editor → bounce). After
  // a successful open, any subsequent close is treated as session lost
  // and we send the user back to the public site.
  let wsWasOpen = false;

  // "Open VS Code" link. Fetched once on mount; null while loading, ''
  // when the feature is disabled (or user's ports not yet allocated).
  let vsCodeURL = $state<string | null>(null);

  // Context-window usage shown in the header. Two independent sources:
  //   - promptTokens (live)  ← session_state.prompt_tokens = full
  //     TotalInputTokens for the session (system + tools + history).
  //     nous emits session_state on connect, on each step during a run,
  //     and again at run_done — so this stays fresh end-to-end.
  //   - contextWindow         ← context_stats.context_window only. We
  //     used to also pull `prompt` from context_stats, but that's only
  //     the SYSTEM PROMPT chunk (a few hundred-to-low-thousands of
  //     tokens), not the conversation total — overriding promptTokens
  //     with it made the indicator collapse after every run_done.
  let promptTokens = $state<number | null>(null);
  let contextWindow = $state<number | null>(null);

  // Idle-compaction warning. Orchestrator sends homa.idle_warning frames
  // during the last minute before the sandbox is compacted-and-stopped.
  // We surface the count in the header; clears on next user message
  // (server resets the idle clock) or on WS close (forced disconnect at
  // the actual compaction).
  let idleWarningSeconds = $state<number | null>(null);

  // Browser errors observed in the iframe via the beacon
  // (site-template/vite.config.ts injects it into every page <head>).
  // Buffered + deduped; prepended to the user's next prompt so the LLM
  // can self-correct. Cleared on send.
  let errorBuffer = $state<BufferedError[]>([]);
  let errorsExpanded = $state(false);

  // Formatters for the header pill. "12.3k" rather than 12345 so it
  // stays compact across orders of magnitude.
  function formatTokens(n: number): string {
    if (n < 1000) return String(n);
    if (n < 1_000_000) return (n / 1000).toFixed(n < 10_000 ? 1 : 0) + 'k';
    return (n / 1_000_000).toFixed(1) + 'M';
  }
  const contextDisplay = $derived.by(() => {
    if (promptTokens === null) return null;
    if (contextWindow === null || contextWindow <= 0) {
      return formatTokens(promptTokens);
    }
    const pct = Math.round((promptTokens / contextWindow) * 100);
    return { used: formatTokens(promptTokens), max: formatTokens(contextWindow), pct };
  });

  // --- draggable chat / preview splitter -------------------------------
  //
  // Two-pane grid; user drags the splitter to resize. Width persisted to
  // localStorage so reloads keep the user's layout. Clamped so neither
  // pane disappears.

  const CHAT_WIDTH_KEY = 'homa.chatWidth';
  const CHAT_WIDTH_DEFAULT_PX = 420;
  const CHAT_MIN_PX = 280;
  const PREVIEW_MIN_PX = 320;
  const CHAT_COLLAPSED_KEY = 'homa.chatCollapsed';

  let chatWidth = $state(loadChatWidth());
  // chatCollapsed hides the chat pane entirely + replaces the splitter
  // drag-handle with a `>` tab. Persisted so the user's preference
  // sticks across reloads. The grid column width literally goes to 0
  // when collapsed; no display:none, no DOM tear-down (Chat keeps its
  // streaming state etc.).
  let chatCollapsed = $state(loadChatCollapsed());

  function loadChatCollapsed(): boolean {
    if (typeof window === 'undefined') return false;
    return window.localStorage.getItem(CHAT_COLLAPSED_KEY) === '1';
  }
  function toggleChatCollapsed() {
    chatCollapsed = !chatCollapsed;
    try {
      window.localStorage.setItem(CHAT_COLLAPSED_KEY, chatCollapsed ? '1' : '0');
    } catch { /* private mode */ }
  }

  function loadChatWidth(): number {
    if (typeof window === 'undefined') return CHAT_WIDTH_DEFAULT_PX;
    const raw = window.localStorage.getItem(CHAT_WIDTH_KEY);
    const n = raw ? parseInt(raw, 10) : NaN;
    return Number.isFinite(n) && n > 0 ? clampChatWidth(n) : CHAT_WIDTH_DEFAULT_PX;
  }

  function clampChatWidth(px: number): number {
    if (typeof window === 'undefined') return px;
    const max = Math.max(CHAT_MIN_PX, window.innerWidth - PREVIEW_MIN_PX);
    return Math.min(max, Math.max(CHAT_MIN_PX, px));
  }

  function startSplitterDrag(e: PointerEvent) {
    // Drag is meaningless when chat is collapsed (no width to change).
    // Cleaner than gating in the move handler — never starts the listener.
    if (chatCollapsed) return;
    e.preventDefault();
    document.body.classList.add('splitting');
    // Capture pointer events so the drag survives even when the cursor
    // crosses the iframe (which would otherwise eat pointermove).
    const onMove = (ev: PointerEvent) => {
      chatWidth = clampChatWidth(ev.clientX);
    };
    const onUp = () => {
      document.body.classList.remove('splitting');
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      try {
        window.localStorage.setItem(CHAT_WIDTH_KEY, String(chatWidth));
      } catch { /* private mode etc. — non-fatal */ }
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
  }

  // Re-clamp on viewport resize so the chat pane doesn't end up wider
  // than the window after a window shrink.
  function onWindowResize() {
    chatWidth = clampChatWidth(chatWidth);
  }

  onMount(async () => {
    try {
      const m = await me();
      userEmail = m.email;
      session.previewUrl = m.preview_url || '';
      sessionId = m.nous_session_id || '';
    } catch (err) {
      // Not authenticated — bounce to login.
      window.location.hash = '#/login';
      return;
    }
    ws = openSession({
      workDir,
      sessionId,
      onStatus: (s) => {
        wsStatus = s;
        if (s === 'open') wsWasOpen = true;
        // Lost-session path. We were live, the WS dropped (forced idle
        // compaction, container stopped, orchestrator restart, network
        // blip). Same flow as the Log out button: clear the cookie, send
        // back to main. Re-login via the public site's pill restarts
        // cleanly with a fresh WS.
        else if (s === 'closed' && wsWasOpen) onLogout();
      },
      onEvent: handleEvent
    });

    // Resolve the per-user code-server URL once on mount. Stays null
    // on failure so the button just stays hidden.
    try {
      const r = await codeURL();
      vsCodeURL = r.enabled && r.url ? r.url : '';
    } catch {
      vsCodeURL = '';
    }

    // Iframe error beacon → editor buffer. parseBeaconMessage handles
    // the origin allowlist (only messages from session.previewUrl's
    // origin land in the buffer) + payload shape validation.
    window.addEventListener('message', onIframeMessage);
  });

  onDestroy(() => {
    window.removeEventListener('message', onIframeMessage);
    ws?.close();
  });

  function onIframeMessage(e: MessageEvent) {
    const allowed = originOf(session.previewUrl);
    const err = parseBeaconMessage(e, allowed);
    if (!err) return;
    errorBuffer = addToBuffer(errorBuffer, err);
  }

  function handleEvent(ev: NousEvent) {
    switch (ev.type) {
      case 'session_state':
        if (ev.session_state?.directory) workDir = ev.session_state.directory;
        // PromptTokens here = sess.TokenUsage.TotalInputTokens() — the
        // full input-token count for the session. Single source of truth
        // for the header's "used" number; nous emits session_state on
        // connect, every run step, and run_done.
        if (typeof ev.session_state?.prompt_tokens === 'number') {
          promptTokens = ev.session_state.prompt_tokens;
        }
        break;

      case 'context_stats':
        // We only consume context_window from this — `prompt` here is
        // just the system-prompt overhead estimate, not the total. See
        // promptTokens above for the live total.
        if (ev.stats?.context_window) contextWindow = ev.stats.context_window;
        break;

      case 'homa.idle_warning':
        // Orchestrator-emitted: idle compaction is about to fire.
        // Re-rendered on every gc tick during the warning window, so
        // we just clobber the previous value.
        idleWarningSeconds = ev.seconds_until_compact ?? 0;
        break;

      case 'messages_loaded':
        // Hydrate chat from persisted history. Replaces whatever's in
        // session.messages — nous's view of the session is authoritative.
        // Drops any in-flight streaming buffer for the same reason.
        session.messages = hydrateMessages(ev.messages);
        session.streaming = null;
        break;

      case 'text_delta':
        if (!session.streaming) session.streaming = { text: '', tools: [] };
        session.streaming.text += ev.delta ?? '';
        break;

      case 'tool_start': {
        const t: ToolCall = {
          id: ev.tool_call_id ?? `${Date.now()}-${Math.random()}`,
          name: ev.tool_name ?? '?',
          input: ev.tool_input ?? ''
        };
        if (!session.streaming) session.streaming = { text: '', tools: [] };
        session.streaming.tools.push(t);
        break;
      }

      case 'tool_done': {
        const tools = session.streaming?.tools;
        if (!tools) break;
        // Latest tool with matching call_id (fallback: last).
        const idx = ev.tool_call_id
          ? tools.findLastIndex((x) => x.id === ev.tool_call_id)
          : tools.length - 1;
        if (idx >= 0) {
          tools[idx] = { ...tools[idx], output: ev.output ?? '', isError: !!ev.is_error };
        }
        break;
      }

      case 'run_done':
        // End of a full multi-step run. Flush the in-flight streaming
        // bubble into messages and return to idle. No context_stats
        // re-request needed — nous emits session_state at run_done with
        // updated PromptTokens, which our session_state handler captures.
        flushStreaming();
        session.status = 'idle';
        break;
    }
  }

  function flushStreaming() {
    if (!session.streaming) return;
    session.messages.push({
      role: 'assistant',
      text: session.streaming.text,
      tools: session.streaming.tools
    });
    session.streaming = null;
  }

  function onSend(text: string) {
    // Prepend any buffered iframe errors so the LLM sees the same
    // failure context the user does. The augmented string is also what
    // we push to chat history — transparency over hidden context.
    const prompt = augmentPrompt(text, errorBuffer);
    errorBuffer = [];
    errorsExpanded = false;

    session.messages.push({ role: 'user', text: prompt });
    session.status = 'running';
    // Any user message resets the server-side idle clock, so the
    // warning banner (if showing) becomes stale immediately. Clear it
    // optimistically; if the orchestrator's next tick still considers
    // the user idle for some reason, it'll re-set.
    idleWarningSeconds = null;
    ws?.send({ type: 'run', prompt });
  }

  // Stop the in-flight run. nous handles cancellation and emits the
  // usual run_done back, at which point handleEvent flips status → idle.
  // We don't optimistically flip status here — let the server confirm
  // so the UI stays consistent with backend state.
  function onStop() {
    ws?.send({ type: 'stop' });
  }

  async function onLogout() {
    try { await logout(); } catch { /* idempotent */ }
    // After logout, return the user to the public site (the main vite
    // container serving '/'), not the editor's SPA login. The login
    // pill on the public site remains for re-entry. Using assign+'/'
    // rather than hash navigation so the orchestrator's proxy serves
    // the mainsite handler, not the SPA's editor route.
    window.location.assign('/');
  }
</script>

<svelte:window onresize={onWindowResize} />

<div class="layout">
  <header>
    <div class="brand">homa</div>
    <div class="meta">
      <span class="email">{userEmail}</span>
      {#if contextDisplay}
        {#if typeof contextDisplay === 'string'}
          <span class="ctx" title="Tokens in current prompt">{contextDisplay}</span>
        {:else}
          <span
            class="ctx"
            class:ctx-warm={contextDisplay.pct >= 60 && contextDisplay.pct < 85}
            class:ctx-hot={contextDisplay.pct >= 85}
            title="Context window usage"
          >
            {contextDisplay.used} / {contextDisplay.max}
            <span class="ctx-pct">{contextDisplay.pct}%</span>
          </span>
        {/if}
      {/if}
      {#if idleWarningSeconds !== null}
        <span class="idle-warn" title="Send a message to defer" aria-live="polite">
          ⚠ Idle compaction in {idleWarningSeconds}s
        </span>
      {/if}
      {#if session.status === 'running'}
        <span class="working" title="LLM is working" aria-live="polite">
          <span class="working-dot"></span> working
        </span>
      {/if}
      <span class="status status-{wsStatus}">{wsStatus}</span>
      {#if vsCodeURL}
        <a
          class="vscode-link"
          href={vsCodeURL}
          target="_blank"
          rel="noopener noreferrer"
          title="Open this sandbox in a full VS Code (browser)"
        >Open VS Code</a>
      {/if}
      <button onclick={onLogout}>Log out</button>
    </div>
  </header>
  <main
    style:--chat-width="{chatCollapsed ? 0 : chatWidth}px"
    class:chat-collapsed={chatCollapsed}
  >
    <section class="chat-pane">
      {#if errorBuffer.length > 0}
        <!--
          Browser-error badge. Click toggles the expanded list; ✕ clears
          the buffer without sending. Errors flush automatically on the
          next prompt — see onSend.
        -->
        <div class="err-badge" class:expanded={errorsExpanded}>
          <button
            class="err-summary"
            onclick={() => (errorsExpanded = !errorsExpanded)}
            title="Click to expand. Will be sent with next message."
          >
            ⚠ {errorBuffer.length} browser
            {errorBuffer.length === 1 ? 'error' : 'errors'}
          </button>
          <button
            class="err-clear"
            onclick={() => {
              errorBuffer = [];
              errorsExpanded = false;
            }}
            title="Discard without sending"
          >
            ✕
          </button>
          {#if errorsExpanded}
            <ul class="err-list">
              {#each errorBuffer as e}
                <li>
                  <span class="err-kind">{e.kind}</span>
                  {#if e.count > 1}<span class="err-count">×{e.count}</span>{/if}
                  <code class="err-msg">{e.message}</code>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}
      <Chat
        messages={session.messages}
        streaming={session.streaming}
        status={session.status}
        {onSend}
        {onStop}
      />
    </section>
    <div
      class="splitter"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize chat panel"
      onpointerdown={startSplitterDrag}
    >
      <!--
        Collapse-toggle tab. Sits in the center of the splitter strip.
        Click stops propagation so it doesn't initiate a drag.
      -->
      <button
        class="collapse-toggle"
        onclick={(e) => { e.stopPropagation(); toggleChatCollapsed(); }}
        onpointerdown={(e) => e.stopPropagation()}
        title={chatCollapsed ? 'Show chat' : 'Hide chat'}
        aria-label={chatCollapsed ? 'Show chat' : 'Hide chat'}
      >{chatCollapsed ? '›' : '‹'}</button>
    </div>
    <section class="preview-pane">
      {#if session.previewUrl}
        <iframe title="preview" src={session.previewUrl}></iframe>
      {:else}
        <div class="placeholder">Preview URL not configured yet.</div>
      {/if}
    </section>
  </main>
</div>

<style>
  .layout { display: flex; flex-direction: column; height: 100vh; }
  header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.4rem 0.8rem; border-bottom: 1px solid #ddd; background: #fff;
  }
  .brand { font-weight: 700; }
  .meta { display: flex; align-items: center; gap: 0.75rem; font-size: 0.85rem; }
  .email { color: #555; }
  .status { padding: 0.1rem 0.4rem; border-radius: 3px; font-size: 0.7rem; text-transform: uppercase; }
  .status-connecting { background: #fee; color: #b80; }
  .status-open { background: #efe; color: #060; }
  .status-closed { background: #fdd; color: #c00; }

  /* Context-usage pill — small, monospace-y. Three tiers by saturation:
     normal (<60%), warm (60-84%), hot (>=85%). Tiers map to actionable
     points: warm = "consider compacting soon", hot = "compact now or
     the next turn might blow past the window". */
  .ctx {
    display: inline-flex; align-items: baseline; gap: 0.35rem;
    padding: 0.1rem 0.45rem;
    border-radius: 3px;
    background: #f1f3f8; color: #345;
    font-family: ui-monospace, Menlo, Consolas, monospace;
    font-size: 0.72rem;
  }
  .ctx-pct { opacity: 0.7; font-size: 0.68rem; }
  .ctx-warm { background: #fff4e0; color: #8a5a00; }
  .ctx-hot  { background: #ffe0e0; color: #a00; }

  /* Idle-compaction warning pill — shown during the last minute before
     the orchestrator stops the user's sandbox. Amber + warning glyph;
     pulses gently to draw the eye without being obnoxious. */
  .idle-warn {
    display: inline-flex; align-items: center; gap: 0.3rem;
    padding: 0.1rem 0.5rem; border-radius: 3px;
    background: #fff0d0; color: #6a4a00;
    font-size: 0.72rem; font-weight: 600;
    animation: idle-pulse 1.4s ease-in-out infinite;
  }
  @keyframes idle-pulse {
    0%, 100% { background: #fff0d0; }
    50%      { background: #ffe2a8; }
  }

  /* "working" pulse — peripheral indicator visible even when the chat is
     scrolled, so the user doesn't have to look at the message list to know
     the LLM is doing something. */
  .working {
    display: inline-flex; align-items: center; gap: 0.35rem;
    padding: 0.1rem 0.45rem; border-radius: 3px;
    background: #fff7e0; color: #8a6a00;
    font-size: 0.7rem; text-transform: uppercase; letter-spacing: 0.04em;
  }
  .working-dot {
    width: 0.45rem; height: 0.45rem; border-radius: 50%;
    background: #d99800;
    animation: pulse 1.1s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { opacity: 0.3; transform: scale(0.85); }
    50%      { opacity: 1;   transform: scale(1.05); }
  }

  button { padding: 0.25rem 0.6rem; border: 1px solid #aaa; background: #fff; border-radius: 4px; cursor: pointer; }

  /* "Open VS Code" link — looks like a button, opens a new tab.
     Subtle blue accent so it stands out vs Log out without screaming. */
  .vscode-link {
    padding: 0.25rem 0.6rem;
    border: 1px solid #1f6feb;
    background: #f1f7ff;
    color: #1f6feb;
    border-radius: 4px;
    text-decoration: none;
    font-size: 0.85rem;
    white-space: nowrap;
  }
  .vscode-link:hover { background: #e6f0ff; }

  /* Three-column grid: chat (user-resized) | splitter (6px) | preview (rest). */
  main {
    display: grid;
    grid-template-columns: var(--chat-width, 420px) 6px 1fr;
    flex: 1;
    min-height: 0;
  }
  /* min-width must be 0 so the grid track can collapse to 0 when chat
     hidden. `overflow: hidden` keeps the (still-rendered) chat content
     from spilling out of the 0-width track. */
  .chat-pane {
    display: flex; flex-direction: column;
    min-height: 0; min-width: 0;
    overflow: hidden;
  }
  /* When collapsed, the splitter grid track stays a thin strip — wider
     than the resize-handle case so the > toggle button is comfortably
     clickable. */
  main.chat-collapsed { grid-template-columns: 0 18px 1fr; }

  /* Browser-error badge — sits above Chat in the chat pane. Amber
     background + warn glyph mirror the idle-warning header pill, so
     the visual language for "needs your attention" is consistent. */
  .err-badge {
    display: flex; flex-wrap: wrap; align-items: center; gap: 0.25rem;
    padding: 0.4rem 0.6rem;
    background: #fff4e0; color: #8a5a00;
    border-bottom: 1px solid #f0d28a;
    font-size: 0.82rem;
  }
  .err-summary, .err-clear {
    background: transparent; border: 0; cursor: pointer;
    color: inherit; font: inherit;
    padding: 0.1rem 0.35rem; border-radius: 3px;
    min-width: 0;
  }
  .err-summary:hover, .err-clear:hover { background: rgba(0,0,0,0.05); }
  .err-summary { font-weight: 600; }
  .err-clear { margin-left: auto; font-size: 0.95rem; line-height: 1; }
  .err-list {
    width: 100%; margin: 0.35rem 0 0 0; padding: 0;
    list-style: none;
    max-height: 12rem; overflow-y: auto;
    font-size: 0.78rem; line-height: 1.4;
  }
  .err-list li { padding: 0.15rem 0; }
  .err-kind {
    display: inline-block; padding: 0 0.3rem;
    border-radius: 2px;
    background: rgba(138, 90, 0, 0.15);
    margin-right: 0.35rem;
    font-family: ui-monospace, Menlo, Consolas, monospace;
    font-size: 0.72rem;
  }
  .err-count {
    margin-right: 0.35rem;
    font-weight: 600;
  }
  .err-msg {
    font-family: ui-monospace, Menlo, Consolas, monospace;
    word-break: break-all;
  }
  .preview-pane { display: flex; min-height: 0; min-width: 0; }
  iframe { flex: 1; border: 0; }
  .placeholder { display: flex; align-items: center; justify-content: center; flex: 1; color: #999; }

  /* Drag handle between the two panes. Stays subtle until hover.
     Positioned-relative so the collapse-toggle button can absolute-
     anchor inside. */
  .splitter {
    position: relative;
    background: #e6e6e6;
    cursor: col-resize;
    transition: background 0.12s;
    touch-action: none;     /* prevent browser scroll-gesture from eating pointerdown */
    outline: none;
  }
  .splitter:hover { background: #888; }
  /* When chat is collapsed, the splitter has no drag function — show it
     as a plain bar with the > toggle in the middle. */
  main.chat-collapsed .splitter { cursor: default; }
  main.chat-collapsed .splitter:hover { background: #d8d8d8; }

  /* Collapse-toggle: small chevron pinned to the splitter's vertical
     midpoint. Subtle until hovered; not a visual focal point but always
     findable. */
  .collapse-toggle {
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: 18px; height: 38px;
    padding: 0;
    border: 1px solid #bbb;
    border-radius: 3px;
    background: #fafafa;
    color: #555;
    font-size: 0.85rem;
    line-height: 1;
    cursor: pointer;
    z-index: 1; /* over the splitter background */
  }
  .collapse-toggle:hover {
    background: #fff;
    color: #1f6feb;
    border-color: #1f6feb;
  }

  /* While dragging: kill iframe interception of pointermove + show the
     col-resize cursor everywhere, even over the iframe. */
  :global(body.splitting) { cursor: col-resize !important; user-select: none; }
  :global(body.splitting iframe) { pointer-events: none; }
</style>
