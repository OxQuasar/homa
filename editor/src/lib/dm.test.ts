import { describe, expect, it } from 'vitest';
import {
  closeTab,
  dmToChatMessage,
  dmsToChatMessages,
  openTab,
  otherUnread,
  parseStoredActiveTab,
  parseStoredTabs
} from './dm';
import type { DmConversation, DmMessage, DmTab } from './types';

describe('dmToChatMessage', () => {
  const base: DmMessage = {
    id: 1,
    sender_id: 'alice001',
    sender_username: 'alice',
    content: 'hi bob',
    created_at: 1_700_000_000
  };

  it("role='user' when sender is self", () => {
    const m = dmToChatMessage(base, 'alice001');
    expect(m.role).toBe('user');
    expect(m.displayLabel).toBe('alice');
  });

  it("role='assistant' when sender is peer", () => {
    const m = dmToChatMessage(base, 'bob00001');
    expect(m.role).toBe('assistant');
  });

  it('createdAt is ms (s × 1000)', () => {
    expect(dmToChatMessage(base, 'alice001').createdAt).toBe(1_700_000_000_000);
  });

  it('falls back to sender_id when username empty', () => {
    const m = dmToChatMessage({ ...base, sender_username: '' }, 'bob00001');
    expect(m.displayLabel).toBe('alice001');
  });
});

describe('dmsToChatMessages', () => {
  it('maps array preserving order', () => {
    const msgs: DmMessage[] = [
      { id: 1, sender_id: 'a', sender_username: 'a', content: 'x', created_at: 1 },
      { id: 2, sender_id: 'b', sender_username: 'b', content: 'y', created_at: 2 }
    ];
    const out = dmsToChatMessages(msgs, 'a');
    expect(out).toHaveLength(2);
    expect(out[0].role).toBe('user'); // a is self
    expect(out[1].role).toBe('assistant');
  });
});

describe('openTab', () => {
  const a: DmTab = { peerId: 'a', username: 'alice' };
  const b: DmTab = { peerId: 'b', username: 'bob' };
  const c: DmTab = { peerId: 'c', username: 'carol' };

  it('adds when absent', () => {
    const out = openTab([], a);
    expect(out).toHaveLength(1);
    expect(out[0]).toEqual(a);
  });

  it('moves to front when already present', () => {
    const out = openTab([a, b, c], b);
    expect(out.map((t) => t.peerId)).toEqual(['b', 'a', 'c']);
  });

  it('returns new array reference', () => {
    const input = [a];
    const out = openTab(input, b);
    expect(out).not.toBe(input);
  });
});

describe('closeTab', () => {
  const a: DmTab = { peerId: 'a', username: 'alice' };
  const b: DmTab = { peerId: 'b', username: 'bob' };

  it('removes by peerId', () => {
    const out = closeTab([a, b], 'a');
    expect(out).toEqual([b]);
  });

  it('no-op when peer absent', () => {
    const out = closeTab([a, b], 'ghost');
    expect(out).toEqual([a, b]);
  });
});

describe('otherUnread', () => {
  const convos: DmConversation[] = [
    { peer_id: 'a', peer_username: 'alice', last_at: 1, last_preview: 'x', unread_count: 3 },
    { peer_id: 'b', peer_username: 'bob', last_at: 1, last_preview: 'y', unread_count: 2 },
    { peer_id: 'c', peer_username: 'carol', last_at: 1, last_preview: 'z', unread_count: 1 }
  ];

  it('sums all when no tabs open', () => {
    expect(otherUnread(convos, [])).toBe(6);
  });

  it('excludes peers represented as open tabs', () => {
    expect(otherUnread(convos, [{ peerId: 'b', username: 'bob' }])).toBe(4); // 3+1
  });

  it('returns 0 when all peers open', () => {
    const opens = convos.map((c) => ({ peerId: c.peer_id, username: c.peer_username }));
    expect(otherUnread(convos, opens)).toBe(0);
  });
});

describe('parseStoredTabs', () => {
  it('returns empty for null / empty / non-array / invalid JSON', () => {
    expect(parseStoredTabs(null)).toEqual([]);
    expect(parseStoredTabs('')).toEqual([]);
    expect(parseStoredTabs('not-json')).toEqual([]);
    expect(parseStoredTabs('{"not":"array"}')).toEqual([]);
    expect(parseStoredTabs('null')).toEqual([]);
  });

  it('parses valid tab array', () => {
    const raw = JSON.stringify([
      { peerId: 'alice001', username: 'alice' },
      { peerId: 'bob00001', username: 'bob' }
    ]);
    const out = parseStoredTabs(raw);
    expect(out).toHaveLength(2);
    expect(out[0].peerId).toBe('alice001');
    expect(out[1].username).toBe('bob');
  });

  it('drops malformed entries (missing fields, wrong types)', () => {
    const raw = JSON.stringify([
      { peerId: 'good01', username: 'good' },
      { peerId: 'nouser' },                  // missing username
      { username: 'nopeer' },                // missing peerId
      { peerId: 42, username: 'numeric' },   // wrong type
      null,
      'string-not-object'
    ]);
    const out = parseStoredTabs(raw);
    expect(out).toHaveLength(1);
    expect(out[0].peerId).toBe('good01');
  });

  it('strips extra fields from valid entries', () => {
    const raw = JSON.stringify([{ peerId: 'x', username: 'y', injected: 'evil' }]);
    const out = parseStoredTabs(raw);
    expect(out[0]).toEqual({ peerId: 'x', username: 'y' });
  });
});

describe('parseStoredActiveTab', () => {
  it('null / empty / invalid → AI', () => {
    expect(parseStoredActiveTab(null)).toEqual({ kind: 'ai' });
    expect(parseStoredActiveTab('')).toEqual({ kind: 'ai' });
    expect(parseStoredActiveTab('not-json')).toEqual({ kind: 'ai' });
  });

  it('explicit AI', () => {
    expect(parseStoredActiveTab('{"kind":"ai"}')).toEqual({ kind: 'ai' });
  });

  it('valid DM', () => {
    expect(parseStoredActiveTab('{"kind":"dm","peerId":"alice001"}')).toEqual({
      kind: 'dm',
      peerId: 'alice001'
    });
  });

  it('malformed DM (missing peerId, empty peerId) → AI fallback', () => {
    expect(parseStoredActiveTab('{"kind":"dm"}')).toEqual({ kind: 'ai' });
    expect(parseStoredActiveTab('{"kind":"dm","peerId":""}')).toEqual({ kind: 'ai' });
  });

  it('unknown kind → AI fallback', () => {
    expect(parseStoredActiveTab('{"kind":"group"}')).toEqual({ kind: 'ai' });
  });
});
