<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { me, logout } from '../lib/api';
  import { openSession, type Session } from '../lib/ws';
  import type { ChatMessage, Event as NousEvent, Streaming, ToolCall } from '../lib/types';
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
        // bubble into messages and return to idle.
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
    session.messages.push({ role: 'user', text });
    session.status = 'running';
    ws?.send({ type: 'run', prompt: text });
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
        onSend={onSend}
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
