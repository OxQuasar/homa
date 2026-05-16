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

<div class="layout">
  <header>
    <div class="brand">homa</div>
    <div class="meta">
      <span class="email">{userEmail}</span>
      <span class="status status-{wsStatus}">{wsStatus}</span>
      <button onclick={onLogout}>Log out</button>
    </div>
  </header>
  <main>
    <section class="chat-pane">
      <Chat
        messages={session.messages}
        streaming={session.streaming}
        status={session.status}
        onSend={onSend}
      />
    </section>
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
  button { padding: 0.25rem 0.6rem; border: 1px solid #aaa; background: #fff; border-radius: 4px; cursor: pointer; }
  main { display: grid; grid-template-columns: minmax(340px, 1fr) 2fr; flex: 1; min-height: 0; }
  .chat-pane { border-right: 1px solid #ddd; display: flex; flex-direction: column; min-height: 0; }
  .preview-pane { display: flex; min-height: 0; }
  iframe { flex: 1; border: 0; }
  .placeholder { display: flex; align-items: center; justify-content: center; flex: 1; color: #999; }
</style>
