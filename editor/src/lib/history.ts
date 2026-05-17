// Convert nous's persisted Message[] into the editor's ChatMessage[] view
// model. The persisted format is structured (parts with typed data); the
// editor renders flat text + tool cards.
//
// Tool-result parts can live in a later message than their tool_call; we
// build a global tool_call_id → ToolCall map first, then attach results
// across message boundaries.

import type {
  ChatMessage,
  NousMessage,
  ToolCall,
  TextData,
  ToolCallData,
  ToolResultData,
  Part
} from './types';

export function hydrateMessages(msgs: NousMessage[] | undefined): ChatMessage[] {
  if (!msgs || msgs.length === 0) return [];

  // First pass: index tool_call → ToolCall (mutable, output filled later).
  const callsById = new Map<string, ToolCall>();
  for (const m of msgs) {
    for (const p of m.parts ?? []) {
      if (p.type === 'tool_call') {
        const d = p.data as ToolCallData;
        callsById.set(d.id, {
          id: d.id,
          name: d.name,
          input: stringifyInput(d.input)
        });
      }
    }
  }

  // Second pass: attach tool_result outputs.
  for (const m of msgs) {
    for (const p of m.parts ?? []) {
      if (p.type === 'tool_result') {
        const d = p.data as ToolResultData;
        const call = callsById.get(d.tool_call_id);
        if (call) {
          call.output = d.content;
          call.isError = !!d.is_error;
        }
      }
    }
  }

  // Third pass: build ChatMessages, skipping info-role and pure-tool_result
  // assistant messages (their content is folded into the prior assistant
  // message's tool cards).
  const out: ChatMessage[] = [];
  for (const m of msgs) {
    if (m.role === 'info') continue;
    if (m.role !== 'user' && m.role !== 'assistant') continue;

    const text = collectText(m.parts ?? []);
    const tools = collectToolCalls(m.parts ?? [], callsById);

    // Drop messages that carry only tool_results (no text, no calls of
    // their own) — their data is already attached to the preceding turn.
    if (!text && tools.length === 0) continue;

    out.push({
      role: m.role,
      text,
      createdAt: parseCreatedAt(m.created_at),
      ...(tools.length ? { tools } : {})
    });
  }
  return out;
}

// parseCreatedAt converts nous's ISO timestamp string to unix ms.
// Returns 0 when the field is missing or unparseable — the renderer
// hides the timestamp UI on zero. Pure helper for testability.
function parseCreatedAt(s: string | undefined): number {
  if (!s) return 0;
  const t = Date.parse(s);
  return Number.isFinite(t) ? t : 0;
}

function collectText(parts: Part[]): string {
  const chunks: string[] = [];
  for (const p of parts) {
    if (p.type === 'text') {
      const d = p.data as TextData;
      if (d?.text) chunks.push(d.text);
    }
  }
  return chunks.join('');
}

function collectToolCalls(parts: Part[], byId: Map<string, ToolCall>): ToolCall[] {
  const out: ToolCall[] = [];
  for (const p of parts) {
    if (p.type === 'tool_call') {
      const d = p.data as ToolCallData;
      const c = byId.get(d.id);
      if (c) out.push(c);
    }
  }
  return out;
}

function stringifyInput(input: unknown): string {
  if (typeof input === 'string') return input;
  try {
    return JSON.stringify(input);
  } catch {
    return String(input);
  }
}
