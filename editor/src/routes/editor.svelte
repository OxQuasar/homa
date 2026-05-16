<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { me, logout } from '../lib/api';
  import { openSession, type Session } from '../lib/ws';
  import type { ChatMessage, ContextStats, Event as NousEvent, Streaming, ToolCall } from '../lib/types';
  import { hydrateMessages } from '../lib/history';
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

  // Context-window usage shown in the header. Initial baseline lands when
  // ws.ts sends context_stats on connect; session_state events update the
  // `prompt` field live during a run; we re-request after run_done so
  // the post-turn total is accurate.
  let contextStats = $state<ContextStats | null>(null);

  // Idle-compaction warning. Orchestrator sends homa.idle_warning frames
  // during the last minute before the sandbox is compacted-and-stopped.
  // We surface the count in the header; clears on next user message
  // (server resets the idle clock) or on WS close (forced disconnect at
  // the actual compaction).
  let idleWarningSeconds = $state<number | null>(null);

  // Formatters for the header pill. "12.3k" rather than 12345 so it
  // stays compact across orders of magnitude.
  function formatTokens(n: number): string {
    if (n < 1000) return String(n);
    if (n < 1_000_000) return (n / 1000).toFixed(n < 10_000 ? 1 : 0) + 'k';
    return (n / 1_000_000).toFixed(1) + 'M';
  }
  const contextDisplay = $derived.by(() => {
    if (!contextStats) return null;
    const used = contextStats.prompt ?? 0;
    const max = contextStats.context_window ?? 0;
    if (max <= 0) return formatTokens(used);
    const pct = Math.round((used / max) * 100);
    return { used: formatTokens(used), max: formatTokens(max), pct };
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

  let chatWidth = $state(loadChatWidth());

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
      onStatus: (s) => (wsStatus = s),
      onEvent: handleEvent
    });
  });

  onDestroy(() => ws?.close());

  function handleEvent(ev: NousEvent) {
    switch (ev.type) {
      case 'session_state':
        if (ev.session_state?.directory) workDir = ev.session_state.directory;
        // session_state carries prompt_tokens — refresh just that field
        // without losing context_window (which only context_stats supplies).
        if (typeof ev.session_state?.prompt_tokens === 'number' && contextStats) {
          contextStats = { ...contextStats, prompt: ev.session_state.prompt_tokens };
        }
        break;

      case 'context_stats':
        if (ev.stats) contextStats = ev.stats;
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
        // End of a full multi-step run. Flush any in-flight streaming
        // bubble into messages and return to idle. Refresh context stats —
        // the run's tool outputs / new messages all add tokens that
        // session_state events may not have surfaced cleanly.
        flushStreaming();
        session.status = 'idle';
        ws?.send({ type: 'context_stats' });
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
    session.messages.push({ role: 'user', text });
    session.status = 'running';
    // Any user message resets the server-side idle clock, so the
    // warning banner (if showing) becomes stale immediately. Clear it
    // optimistically; if the orchestrator's next tick still considers
    // the user idle for some reason, it'll re-set.
    idleWarningSeconds = null;
    ws?.send({ type: 'run', prompt: text });
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
    window.location.hash = '#/login';
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
      <button onclick={onLogout}>Log out</button>
    </div>
  </header>
  <main style:--chat-width="{chatWidth}px">
    <section class="chat-pane">
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
    ></div>
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

  /* Three-column grid: chat (user-resized) | splitter (6px) | preview (rest). */
  main {
    display: grid;
    grid-template-columns: var(--chat-width, 420px) 6px 1fr;
    flex: 1;
    min-height: 0;
  }
  .chat-pane { display: flex; flex-direction: column; min-height: 0; min-width: 0; }
  .preview-pane { display: flex; min-height: 0; min-width: 0; }
  iframe { flex: 1; border: 0; }
  .placeholder { display: flex; align-items: center; justify-content: center; flex: 1; color: #999; }

  /* Drag handle between the two panes. Stays subtle until hover. */
  .splitter {
    background: #e6e6e6;
    cursor: col-resize;
    transition: background 0.12s;
    touch-action: none;     /* prevent browser scroll-gesture from eating pointerdown */
    outline: none;
  }
  .splitter:hover { background: #888; }

  /* While dragging: kill iframe interception of pointermove + show the
     col-resize cursor everywhere, even over the iframe. */
  :global(body.splitting) { cursor: col-resize !important; user-select: none; }
  :global(body.splitting iframe) { pointer-events: none; }
</style>
