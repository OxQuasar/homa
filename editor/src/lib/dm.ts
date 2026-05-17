// dm.ts — pure helpers for the editor's DM feature. API client wrappers
// + state-transition helpers; no Svelte state, no DOM access. Lives
// here so the heavy lifting can be unit-tested under Vitest and
// editor.svelte stays focused on wiring.

import type { ApiError } from './api';
import type {
  ActiveTab,
  ChatMessage,
  DmConversation,
  DmMessage,
  DmTab
} from './types';

// --- API client wrappers ----------------------------------------------
//
// All endpoints are cookie-gated; credentials must travel with the
// request. The editor is same-origin to the orchestrator, so relative
// URLs work — no need for an absolute API origin like the cross-origin
// site-template pages use.
//
// All non-2xx responses throw an ApiError carrying .status so callers
// can branch on 401 (→ logout) and 404 (→ drop tab for dead peer).

async function dmFetch(method: string, path: string, body?: unknown): Promise<Response> {
  const init: RequestInit = { method, credentials: 'include' };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  const r = await fetch(path, init);
  if (!r.ok) {
    const err = new Error(`${method} ${path}: ${r.status}`) as ApiError;
    err.status = r.status;
    throw err;
  }
  return r;
}

export async function fetchConversations(): Promise<DmConversation[]> {
  return (await dmFetch('GET', '/api/messages/conversations')).json();
}

export async function fetchThread(peerId: string): Promise<DmMessage[]> {
  return (await dmFetch('GET', `/api/messages/with/${encodeURIComponent(peerId)}`)).json();
}

export async function sendDm(peerId: string, content: string): Promise<DmMessage> {
  return (await dmFetch('POST', `/api/messages/with/${encodeURIComponent(peerId)}`, { content })).json();
}

export async function fetchUnreadCount(): Promise<number> {
  const j = (await (await dmFetch('GET', '/api/messages/unread-count')).json()) as { count: number };
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

// --- Persistence (localStorage) ----------------------------------------
//
// Parse helpers kept pure so they're testable + don't reach into the
// browser global. Editor.svelte calls them with localStorage.getItem
// results.

export const DM_TABS_STORAGE_KEY = 'homa.dmTabs';
export const ACTIVE_TAB_STORAGE_KEY = 'homa.activeTab';

// parseStoredTabs returns a clean DmTab[] from the raw localStorage
// string. Drops malformed entries silently rather than throwing — a
// stale/corrupted blob shouldn't break the editor's load.
export function parseStoredTabs(raw: string | null): DmTab[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter(
        (t): t is DmTab =>
          t !== null &&
          typeof t === 'object' &&
          typeof (t as DmTab).peerId === 'string' &&
          typeof (t as DmTab).username === 'string'
      )
      .map((t) => ({ peerId: t.peerId, username: t.username })); // strip extra fields
  } catch {
    return [];
  }
}

// parseStoredActiveTab returns the restored active tab, falling back
// to {kind:'ai'} on malformed / unknown / DM-without-peerId input.
// Caller is responsible for verifying the restored DM peerId is still
// in the restored tabs list (an active tab whose peer was closed in a
// previous session shouldn't resurrect).
export function parseStoredActiveTab(raw: string | null): ActiveTab {
  if (!raw) return { kind: 'ai' };
  try {
    const parsed = JSON.parse(raw);
    if (parsed?.kind === 'ai') return { kind: 'ai' };
    if (parsed?.kind === 'dm' && typeof parsed.peerId === 'string' && parsed.peerId !== '') {
      return { kind: 'dm', peerId: parsed.peerId };
    }
  } catch {
    /* fall through */
  }
  return { kind: 'ai' };
}


