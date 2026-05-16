import { describe, expect, it } from 'vitest';
import type { BrowserError, BufferedError } from './types';
import {
	addToBuffer,
	augmentPrompt,
	formatBufferForPrompt,
	MAX_BUFFERED,
	MESSAGE_TYPE,
	originOf,
	parseBeaconMessage
} from './iframe_errors';

// Synthetic MessageEvent shape — happy enough for the parser. The real
// thing has more fields; we use what parseBeaconMessage actually reads.
function ev(origin: string, data: unknown): MessageEvent {
	return new MessageEvent('message', { origin, data });
}

const PREVIEW_ORIGIN = 'https://gandiva.kingfisher-celsius.ts.net:10001';

function baseError(over: Partial<BrowserError> = {}): BrowserError {
	return {
		kind: 'error',
		message: 'TypeError: foo of undefined',
		stack: 'TypeError: foo\n  at x:1:1',
		source: 'app.js',
		line: 10,
		col: 3,
		url: PREVIEW_ORIGIN + '/dashboard',
		timestamp: 1_000,
		...over
	};
}

describe('originOf', () => {
	it('extracts scheme + host + port', () => {
		expect(originOf('https://example.com:8080/foo?x=1')).toBe('https://example.com:8080');
		expect(originOf('http://localhost/')).toBe('http://localhost');
	});
	it('returns empty string on invalid', () => {
		expect(originOf('not-a-url')).toBe('');
		expect(originOf('')).toBe('');
	});
});

describe('parseBeaconMessage', () => {
	const payload = {
		kind: 'error',
		message: 'TypeError: foo',
		stack: 'stack-line',
		source: 'app.js',
		line: 1,
		col: 2,
		url: PREVIEW_ORIGIN + '/page',
		timestamp: 5_000
	};

	it('accepts a well-formed message from the allowed origin', () => {
		const r = parseBeaconMessage(
			ev(PREVIEW_ORIGIN, { type: MESSAGE_TYPE, payload }),
			PREVIEW_ORIGIN
		);
		expect(r).not.toBeNull();
		expect(r!.kind).toBe('error');
		expect(r!.message).toBe('TypeError: foo');
		expect(r!.stack).toBe('stack-line');
		expect(r!.line).toBe(1);
	});

	it('rejects messages from a different origin', () => {
		const r = parseBeaconMessage(
			ev('https://evil.example/', { type: MESSAGE_TYPE, payload }),
			PREVIEW_ORIGIN
		);
		expect(r).toBeNull();
	});

	it('skips origin check when allowedOrigin is empty', () => {
		const r = parseBeaconMessage(
			ev('https://anywhere/', { type: MESSAGE_TYPE, payload }),
			''
		);
		expect(r).not.toBeNull();
	});

	it('rejects unrelated message types', () => {
		const r = parseBeaconMessage(
			ev(PREVIEW_ORIGIN, { type: 'other', payload }),
			PREVIEW_ORIGIN
		);
		expect(r).toBeNull();
	});

	it('rejects malformed payloads', () => {
		expect(parseBeaconMessage(ev(PREVIEW_ORIGIN, null), PREVIEW_ORIGIN)).toBeNull();
		expect(parseBeaconMessage(ev(PREVIEW_ORIGIN, {}), PREVIEW_ORIGIN)).toBeNull();
		expect(
			parseBeaconMessage(ev(PREVIEW_ORIGIN, { type: MESSAGE_TYPE }), PREVIEW_ORIGIN)
		).toBeNull();
		expect(
			parseBeaconMessage(
				ev(PREVIEW_ORIGIN, { type: MESSAGE_TYPE, payload: 'string-not-object' }),
				PREVIEW_ORIGIN
			)
		).toBeNull();
	});

	it('rejects unknown kinds', () => {
		const r = parseBeaconMessage(
			ev(PREVIEW_ORIGIN, {
				type: MESSAGE_TYPE,
				payload: { ...payload, kind: 'console.error' }
			}),
			PREVIEW_ORIGIN
		);
		expect(r).toBeNull();
	});

	it('requires message field', () => {
		const r = parseBeaconMessage(
			ev(PREVIEW_ORIGIN, {
				type: MESSAGE_TYPE,
				payload: { ...payload, message: 0 }
			}),
			PREVIEW_ORIGIN
		);
		expect(r).toBeNull();
	});

	it('falls back to now() when timestamp missing/invalid', () => {
		const before = Date.now();
		const r = parseBeaconMessage(
			ev(PREVIEW_ORIGIN, {
				type: MESSAGE_TYPE,
				payload: { ...payload, timestamp: 'banana' }
			}),
			PREVIEW_ORIGIN
		);
		expect(r).not.toBeNull();
		expect(r!.timestamp).toBeGreaterThanOrEqual(before);
	});
});

describe('addToBuffer', () => {
	it('appends a new error', () => {
		const next = addToBuffer([], baseError());
		expect(next).toHaveLength(1);
		expect(next[0].count).toBe(1);
		expect(next[0].firstSeen).toBe(1_000);
		expect(next[0].lastSeen).toBe(1_000);
	});

	it('coalesces same (kind, message); bumps count + lastSeen', () => {
		let buf: BufferedError[] = [];
		buf = addToBuffer(buf, baseError({ timestamp: 1_000 }));
		buf = addToBuffer(buf, baseError({ timestamp: 2_000 }));
		buf = addToBuffer(buf, baseError({ timestamp: 3_000 }));
		expect(buf).toHaveLength(1);
		expect(buf[0].count).toBe(3);
		expect(buf[0].firstSeen).toBe(1_000);
		expect(buf[0].lastSeen).toBe(3_000);
	});

	it('treats different kind as different signature', () => {
		let buf: BufferedError[] = [];
		buf = addToBuffer(buf, baseError({ kind: 'error', message: 'X' }));
		buf = addToBuffer(buf, baseError({ kind: 'unhandledrejection', message: 'X' }));
		expect(buf).toHaveLength(2);
	});

	it('treats different message as different signature', () => {
		let buf: BufferedError[] = [];
		buf = addToBuffer(buf, baseError({ message: 'A' }));
		buf = addToBuffer(buf, baseError({ message: 'B' }));
		expect(buf).toHaveLength(2);
	});

	it('returns new array reference for reactivity', () => {
		const original: BufferedError[] = [];
		const next = addToBuffer(original, baseError());
		expect(next).not.toBe(original);
	});

	it('evicts oldest when MAX_BUFFERED is reached', () => {
		let buf: BufferedError[] = [];
		for (let i = 0; i < MAX_BUFFERED + 3; i++) {
			buf = addToBuffer(buf, baseError({ message: 'unique-' + i }));
		}
		expect(buf).toHaveLength(MAX_BUFFERED);
		// First few got evicted — the oldest remaining should NOT be 'unique-0'.
		const messages = buf.map((b) => b.message);
		expect(messages.includes('unique-0')).toBe(false);
		expect(messages.includes('unique-' + (MAX_BUFFERED + 2))).toBe(true);
	});
});

describe('formatBufferForPrompt', () => {
	it('returns empty string for empty buffer', () => {
		expect(formatBufferForPrompt([])).toBe('');
	});

	it('includes header + each error + dedupe count', () => {
		const buf: BufferedError[] = [
			{
				kind: 'error',
				message: 'TypeError: X',
				stack: 'stack-line-1\nstack-line-2',
				url: 'https://x/y',
				firstSeen: 1,
				lastSeen: 9,
				count: 3
			},
			{
				kind: 'unhandledrejection',
				message: 'Promise reject',
				stack: null,
				url: 'https://x/z',
				firstSeen: 2,
				lastSeen: 4,
				count: 1
			}
		];
		const out = formatBufferForPrompt(buf);
		expect(out).toContain('[browser errors observed since last prompt]');
		expect(out).toContain('(3×) error: TypeError: X');
		expect(out).toContain('stack-line-1');
		expect(out).toContain('url: https://x/y');
		// count==1 → no "(1×)" prefix
		expect(out).toContain('unhandledrejection: Promise reject');
		expect(out).not.toContain('(1×)');
	});

	it('caps long stacks to first 3 lines', () => {
		// Use unambiguous tokens — bare letters like 'd' false-positive on
		// other text in the header ("observed").
		const buf: BufferedError[] = [
			{
				kind: 'error',
				message: 'M',
				stack:
					'STACKLINE_AA\nSTACKLINE_BB\nSTACKLINE_CC\nSTACKLINE_DD\nSTACKLINE_EE',
				url: 'u',
				firstSeen: 0,
				lastSeen: 0,
				count: 1
			}
		];
		const out = formatBufferForPrompt(buf);
		expect(out).toContain('STACKLINE_AA');
		expect(out).toContain('STACKLINE_BB');
		expect(out).toContain('STACKLINE_CC');
		expect(out).not.toContain('STACKLINE_DD');
		expect(out).not.toContain('STACKLINE_EE');
	});
});

describe('augmentPrompt', () => {
	it('passes through when buffer empty', () => {
		expect(augmentPrompt('hi', [])).toBe('hi');
	});

	it('prepends formatted buffer when non-empty', () => {
		const buf: BufferedError[] = [
			{
				kind: 'error',
				message: 'M',
				stack: null,
				url: 'u',
				firstSeen: 0,
				lastSeen: 0,
				count: 1
			}
		];
		const out = augmentPrompt('fix it', buf);
		expect(out.startsWith('[browser errors')).toBe(true);
		expect(out.endsWith('fix it')).toBe(true);
		expect(out).toContain('error: M');
	});
});
