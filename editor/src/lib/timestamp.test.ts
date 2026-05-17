import { describe, expect, it } from 'vitest';
import { formatMessageTime, formatMessageTimeISO } from './timestamp';

// Pin reference time so tests are deterministic: 2026-05-17 14:00:00 UTC.
// The format helpers use Date getters which return values in the local
// timezone. Tests assert relative-shape (same-day vs same-week vs older)
// rather than exact strings to stay timezone-independent.
const NOW = Date.parse('2026-05-17T14:00:00Z');

describe('formatMessageTime', () => {
  it('returns empty for non-positive', () => {
    expect(formatMessageTime(0, NOW)).toBe('');
    expect(formatMessageTime(-1, NOW)).toBe('');
  });

  it('same day → HH:MM (no weekday/date)', () => {
    const ts = Date.parse('2026-05-17T08:30:00Z');
    const got = formatMessageTime(ts, NOW);
    // Should NOT contain weekday or month
    expect(got).not.toMatch(/Mon|Tue|Wed|Thu|Fri|Sat|Sun/);
    expect(got).not.toMatch(/Jan|Feb|Mar|Apr|May|Jun/);
    // Should be HH:MM shape (5 chars, contains :)
    expect(got).toMatch(/^\d\d:\d\d$/);
  });

  it('past week, same year → "Mon HH:MM"', () => {
    const ts = Date.parse('2026-05-13T14:00:00Z'); // 4 days ago
    const got = formatMessageTime(ts, NOW);
    expect(got).toMatch(/^(Sun|Mon|Tue|Wed|Thu|Fri|Sat) \d\d:\d\d$/);
  });

  it('older than a week, same year → "Mon DD HH:MM"', () => {
    const ts = Date.parse('2026-04-10T14:00:00Z'); // > 7 days ago
    const got = formatMessageTime(ts, NOW);
    // e.g. "Apr 10 07:00" or "Apr 9 21:00" depending on TZ
    expect(got).toMatch(/^[A-Z][a-z]{2} \d{1,2} \d\d:\d\d$/);
  });

  it('cross-year → "Mon DD YYYY"', () => {
    const ts = Date.parse('2024-12-15T14:00:00Z');
    const got = formatMessageTime(ts, NOW);
    expect(got).toMatch(/^[A-Z][a-z]{2} \d{1,2} \d{4}$/);
    expect(got).toContain('2024');
  });
});

describe('formatMessageTimeISO', () => {
  it('returns empty for missing', () => {
    expect(formatMessageTimeISO(0)).toBe('');
  });
  it('returns full ISO for valid', () => {
    const ts = Date.parse('2026-05-17T14:32:00.000Z');
    expect(formatMessageTimeISO(ts)).toBe('2026-05-17T14:32:00.000Z');
  });
});
