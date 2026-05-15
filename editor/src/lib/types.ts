// Minimal shape of nous wire types used by the editor.
// Source of truth: ~/nous/internal/director/event.go + gateway.go.

export interface SessionSnapshot {
  id: string;
  title?: string;
  directory: string;
  prompt_tokens?: number;
  yolo_on?: boolean;
  running?: boolean;
}

export type EventType =
  | 'session_state'
  | 'text_delta'
  | 'reasoning_delta'
  | 'tool_start'
  | 'tool_done'
  | 'turn_done'
  | 'run_done'
  | 'prompt_queued'
  | 'permission_request';

export interface Event {
  type: EventType;
  session_state?: SessionSnapshot;
  delta?: string;
  tool_name?: string;
  tool_input?: string;
  tool_call_id?: string;
  output?: string;
  is_error?: boolean;
  err_str?: string;
}

export type RequestType =
  | 'run'
  | 'stop'
  | 'new_session'
  | 'switch_session';

export interface Request {
  type: RequestType;
  prompt?: string;
  session_id?: string;
}

export interface ChatMessage {
  role: 'user' | 'assistant';
  text: string;
  // Tool calls captured while assistant message was streaming.
  tools?: ToolCall[];
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
}
