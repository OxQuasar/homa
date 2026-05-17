// dm.ts — pure helpers for the editor's DM feature. API client wrappers
// + state-transition helpers; no Svelte state, no DOM access. Lives
// here so the heavy lifting can be unit-tested under Vitest and
// editor.svelte stays focused on wiring.

import type {
  ChatMessage,
  DmConversation,
  DmMessage,
  DmTab
} from './types';

// --- API client wrappers ----------------------------------------------

// All endpoints are cookie-gated; credentials must travel with the
// request. The editor is same-origin to the orchestrator, so relative
// URLs work — no need for an absolute API origin like the cross-origin
// site-template pages use.

export async function fetchConversations(): Promise<DmConversation[]> {
  const r = await fetch('/api/messages/conversations', { credentials: 'include' });
  if (!r.ok) throw new Error(`conversations: ${r.status}`);
  return r.json();
}

export async function fetchThread(peerId: string): Promise<DmMessage[]> {
  const r = await fetch(`/api/messages/with/${encodeURIComponent(peerId)}`, {
    credentials: 'include'
  });
  if (!r.ok) throw new Error(`thread ${peerId}: ${r.status}`);
  return r.json();
}

export async function sendDm(peerId: string, content: string): Promise<DmMessage> {
  const r = await fetch(`/api/messages/with/${encodeURIComponent(peerId)}`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content })
  });
  if (!r.ok) throw new Error(`send ${peerId}: ${r.status}`);
  return r.json();
}

export async function fetchUnreadCount(): Promise<number> {
  const r = await fetch('/api/messages/unread-count', { credentials: 'include' });
  if (!r.ok) throw new Error(`unread: ${r.status}`);
  const j = (await r.json()) as { count: number };
  return j.count ?? 0;
}

// --- Wire-shape → view-model conversion --------------------------------

// dmToChatMessage maps a DM into the editor's ChatMessage shape so the
// existing Chat.svelte renders it without modification. selfUserId is
// the caller's own user_id — drives whether each message renders on the
// user-side (right) or peer-side (left).
//
// displayLabel = sender's username, so Message.svelte's header shows
// the actual person's name instead of "user"/"assistant".
export function dmToChatMessage(m: DmMessage, selfUserId: string): ChatMessage {
  return {
    role: m.sender_id === selfUserId ? 'user' : 'assistant',
    text: m.content,
    createdAt: m.created_at * 1000, // ms for the timestamp formatter
    displayLabel: m.sender_username || m.sender_id
  };
}

export function dmsToChatMessages(msgs: DmMessage[], selfUserId: string): ChatMessage[] {
  return msgs.map((m) => dmToChatMessage(m, selfUserId));
}

// --- Tab state helpers (pure) ------------------------------------------

// openTab adds a DM tab if absent, else moves it to the front. Returns
// the new array (new ref so $state reactivity fires). Insertion order =
// recency (most-recently-opened first); UI renders left-to-right with
// the existing AI tab pinned first.
export function openTab(tabs: DmTab[], tab: DmTab): DmTab[] {
  const filtered = tabs.filter((t) => t.peerId !== tab.peerId);
  return [tab, ...filtered];
}

// closeTab removes by peerId. Returns new array (new ref). If the
// closed tab was active, caller should pick a new active tab.
export function closeTab(tabs: DmTab[], peerId: string): DmTab[] {
  return tabs.filter((t) => t.peerId !== peerId);
}

// --- Unread aggregation ------------------------------------------------

// otherUnread sums unread counts across conversations EXCEPT the peers
// already represented as open tabs. Drives the "+ N" badge on the
// picker — surfaces the count of unread DMs the user hasn't opened.
export function otherUnread(convos: DmConversation[], openTabs: DmTab[]): number {
  const open = new Set(openTabs.map((t) => t.peerId));
  let n = 0;
  for (const c of convos) {
    if (open.has(c.peer_id)) continue;
    n += c.unread_count;
  }
  return n;
}
