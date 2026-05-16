// iframe_errors.ts — pure helpers for ingesting BrowserError postMessages
// from the iframe beacon (see site-template/vite.config.ts) into a buffer
// the editor can render + flush into the next LLM prompt.
//
// Kept side-effect-free so it's straightforward to unit-test under Vitest.
// The .svelte file wires the window listener and owns the $state-bound
// buffer; this module just transforms.

import type { BrowserError, BufferedError } from './types';

// MESSAGE_TYPE is the discriminator the beacon stamps onto its frames.
// Mirrors the literal in vite.config.ts's beaconScript.
export const MESSAGE_TYPE = 'homa:browser-error';

// originOf returns the scheme://host[:port] of a URL string, or '' if it
// fails to parse. Used to allowlist incoming messages by origin (e.g.
// only accept messages from the user's preview URL).
export function originOf(url: string): string {
  try {
    return new URL(url).origin;
  } catch {
    return '';
  }
}

// parseBeaconMessage extracts a validated BrowserError from a raw
// MessageEvent. Returns null when the message is unrelated (wrong type,
// wrong shape, wrong origin). Origin checking is the caller's call —
// pass allowedOrigin to enforce; pass '' to skip the check (tests).
export function parseBeaconMessage(
  ev: MessageEvent,
  allowedOrigin: string
): BrowserError | null {
  if (allowedOrigin && ev.origin !== allowedOrigin) return null;
  const data = ev.data;
  if (!data || typeof data !== 'object') return null;
  if ((data as { type?: unknown }).type !== MESSAGE_TYPE) return null;
  const payload = (data as { payload?: unknown }).payload;
  if (!payload || typeof payload !== 'object') return null;

  const p = payload as Record<string, unknown>;
  const kind = p.kind === 'error' || p.kind === 'unhandledrejection' ? p.kind : null;
  if (!kind) return null;
  const message = typeof p.message === 'string' ? p.message : null;
  if (!message) return null;
  const url = typeof p.url === 'string' ? p.url : '';
  const timestamp =
    typeof p.timestamp === 'number' && isFinite(p.timestamp) ? p.timestamp : Date.now();

  return {
    kind,
    message,
    stack: typeof p.stack === 'string' ? p.stack : null,
    source: typeof p.source === 'string' ? p.source : null,
    line: typeof p.line === 'number' ? p.line : null,
    col: typeof p.col === 'number' ? p.col : null,
    url,
    timestamp
  };
}

// addToBuffer coalesces a new error into the buffer by (kind, message).
// Bumps count + lastSeen on duplicate. Returns the new buffer; does NOT
// mutate the input (so $state reactivity sees a new array reference).
//
// Bounded: if `buf.length` is already at MAX_BUFFERED, the oldest entry
// is dropped to make room for the new one. Prevents pathological pages
// from filling memory.
export function addToBuffer(buf: BufferedError[], e: BrowserError): BufferedError[] {
  const key = (b: BufferedError) => b.kind + '|' + b.message;
  const eKey = e.kind + '|' + e.message;
  const next = [...buf];
  const idx = next.findIndex((b) => key(b) === eKey);
  if (idx >= 0) {
    const old = next[idx];
    next[idx] = {
      ...old,
      count: old.count + 1,
      lastSeen: e.timestamp
    };
    return next;
  }
  const fresh: BufferedError = {
    kind: e.kind,
    message: e.message,
    stack: e.stack ?? null,
    url: e.url,
    firstSeen: e.timestamp,
    lastSeen: e.timestamp,
    count: 1
  };
  if (next.length >= MAX_BUFFERED) next.shift();
  next.push(fresh);
  return next;
}

// MAX_BUFFERED caps in-memory error entries. 50 distinct errors is far
// past "useful diagnostic" territory — anything beyond is either a
// runaway page or a user who's accumulated a session's worth of failures
// without sending. Either way, the oldest gets evicted.
export const MAX_BUFFERED = 50;

// formatBufferForPrompt builds the section that gets prepended to the
// next user message. Empty buffer → empty string (no header, no noise).
// The leading [browser errors ...] header is what tells the LLM "this
// isn't user-typed, this is observational".
export function formatBufferForPrompt(buf: BufferedError[]): string {
  if (buf.length === 0) return '';
  const lines: string[] = ['[browser errors observed since last prompt]'];
  for (const b of buf) {
    const counter = b.count > 1 ? `(${b.count}×) ` : '';
    lines.push(counter + b.kind + ': ' + b.message);
    if (b.stack) {
      // Stack lines often very long; keep first 3 lines to bound prompt size.
      const stackLines = b.stack.split('\n').slice(0, 3).map((s) => '  ' + s.trim());
      lines.push(...stackLines);
    }
    lines.push('  url: ' + b.url);
    lines.push('');
  }
  lines.push('');
  return lines.join('\n');
}

// augmentPrompt produces the final string to send to nous: errors-section
// followed by the user's text. When buffer is empty, returns text as-is.
export function augmentPrompt(text: string, buf: BufferedError[]): string {
  const header = formatBufferForPrompt(buf);
  return header ? header + text : text;
}
