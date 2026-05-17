// Minimal shape of nous wire types used by the editor.
// Source of truth: ~/nous/internal/director/event.go + gateway.go and
// ~/nous/internal/message/message.go.

export interface SessionSnapshot {
  id: string;
  title?: string;
  directory: string;
  prompt_tokens?: number;
  yolo_on?: boolean;
  running?: boolean;
}

// --- Persisted message format (per ~/nous/internal/message/message.go) ---
// Returned by nous in EventMessagesLoaded.messages so the editor can render
// chat history on reconnect.

export type Role = 'user' | 'assistant' | 'info';

export type PartType =
  | 'text'
  | 'tool_call'
  | 'tool_result'
  | 'reasoning'
  | 'server_tool_use'
  | 'web_search_result';

export interface Part {
  type: PartType;
  // The data shape depends on type; we narrow at the call site.
  data: unknown;
}

export interface NousMessage {
  id: string;
  session_id: string;
  role: Role;
  parts: Part[];
  model?: string;
  created_at?: string;
  is_summary?: boolean;
}

export interface TextData {
  text: string;
}
export interface ToolCallData {
  id: string;
  name: string;
  input: unknown; // raw JSON
}
export interface ToolResultData {
  tool_call_id: string;
  content: string;
  is_error?: boolean;
}

// --- Wire events ---

export type EventType =
  | 'session_state'
  | 'messages_loaded'
  | 'text_delta'
  | 'tool_start'
  | 'tool_done'
  | 'run_done'
  | 'prompt_queued'
  | 'permission_request'
  | 'context_stats'
  | 'homa.idle_warning'; // synthetic — emitted by orchestrator, not nous

export interface Event {
  type: EventType;
  session_state?: SessionSnapshot;
  messages?: NousMessage[];
  reconnected?: boolean;
  delta?: string;
  tool_name?: string;
  tool_input?: string;
  tool_call_id?: string;
  output?: string;
  is_error?: boolean;
  err_str?: string;
  stats?: ContextStats; // populated on EventContextStats
  // homa.idle_warning: how many seconds until the lifecycle compacts +
  // stops the user's sandbox. Sent every gc tick the user is inside
  // the warning window (last minute by default).
  seconds_until_compact?: number;
}

// ContextStats — payload of EventContextStats; tokens broken down by
// section of the prompt. Sent in response to a context_stats request.
export interface ContextStats {
  context_window: number;
  prompt: number;
  tools: number;
  context_files: number;
  skills: number;
  messages: number;
}

// --- Wire requests ---

export type RequestType =
  | 'run'
  | 'stop'
  | 'get_messages'
  | 'context_stats';

export interface Request {
  type: RequestType;
  prompt?: string;
}

// --- Browser-error feedback (from the iframe's beacon) ---

// BrowserError is the wire payload posted by the vite-injected beacon
// in the user's site iframe. Mirrored in site-template/vite.config.ts.
// Field names match what the beacon emits — keep in sync.
export interface BrowserError {
  kind: 'error' | 'unhandledrejection';
  message: string;
  stack?: string | null;
  source?: string | null;
  line?: number | null;
  col?: number | null;
  url: string;
  timestamp: number;
}

// BufferedError aggregates duplicates: the beacon throttles per-signature
// but a single page reload can still emit many distinct-but-same-shape
// errors. Editor coalesces by (kind, message) and bumps count.
export interface BufferedError {
  kind: BrowserError['kind'];
  message: string;
  stack?: string | null;
  url: string;       // url of the FIRST occurrence (subsequent ones are usually same)
  firstSeen: number; // ms
  lastSeen: number;  // ms
  count: number;
}

// --- Editor view model ---

export interface ChatMessage {
  role: 'user' | 'assistant';
  text: string;
  // unix ms when the message originated. For user messages: when Send was
  // clicked. For assistant messages: when streaming began (the start of
  // the LLM response). For rehydrated messages: parsed from
  // NousMessage.created_at; 0 if missing.
  createdAt: number;
  // Tool calls captured while assistant message was streaming, or rehydrated
  // from history. `output` is filled in when the matching tool_result is
  // seen (later in the same or a following message).
  tools?: ToolCall[];
  // Optional override for the role label rendered in Message.svelte's
  // header. Used by DM rendering to show the sender's username instead
  // of "user"/"assistant".
  displayLabel?: string;
}

// --- Direct messages (cross-user) -------------------------------------

// DmMessage is the wire shape returned by /api/messages/with/<peer>.
// Mirror of internal/messages.Message on the Go side.
export interface DmMessage {
  id: number;
  sender_id: string;
  sender_username: string;
  content: string;
  created_at: number; // unix seconds (multiply by 1000 for Date)
}

// DmConversation is the wire shape returned by /api/messages/conversations.
export interface DmConversation {
  peer_id: string;
  peer_username: string;
  last_at: number;        // unix seconds
  last_preview: string;
  unread_count: number;
}

// ActiveTab — what the chat pane is currently showing. Discriminated
// union so the renderer can switch behavior cleanly.
export type ActiveTab = { kind: 'ai' } | { kind: 'dm'; peerId: string };

// DmTab — the per-peer tab record stored in the editor's open-tabs list.
export interface DmTab {
  peerId: string;
  username: string;
}

export interface ToolCall {
  id: string;
  name: string;
  input: string;
  output?: string;
  isError?: boolean;
}

export interface Streaming {
  text: string;
  tools: ToolCall[];
  // unix ms when this streaming bubble first appeared (first text/tool
  // delta of the assistant turn). Used as the createdAt of the resulting
  // ChatMessage when flushed; also surfaced in the live bubble so the
  // timestamp doesn't pop in only after the run ends.
  startedAt: number;
}
