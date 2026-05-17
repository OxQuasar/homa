// Pure timestamp helpers for chat-message rendering. Side-effect-free
// so it's trivially testable under Vitest.

// formatMessageTime formats a chat-message timestamp for inline display
// next to the role label. Tier system reduces noise: within a day shows
// HH:MM; within a week shows "Mon HH:MM"; older shows "Mon DD HH:MM";
// across years shows "Mon DD YYYY".
//
// `now` is a parameter (default Date.now()) so tests can pin the
// reference moment without monkey-patching the clock.
//
// Returns '' for non-positive ts (= missing timestamp, e.g. historical
// messages that never carried one).
export function formatMessageTime(ts: number, now: number = Date.now()): string {
  if (!ts || ts <= 0) return '';
  const d = new Date(ts);
  const n = new Date(now);
  const sameDay =
    d.getFullYear() === n.getFullYear() &&
    d.getMonth() === n.getMonth() &&
    d.getDate() === n.getDate();
  if (sameDay) return hhmm(d);

  const sameYear = d.getFullYear() === n.getFullYear();
  const ageMs = now - ts;
  const SEVEN_DAYS = 7 * 24 * 60 * 60 * 1000;
  if (sameYear && ageMs < SEVEN_DAYS) {
    // Within the past week: "Mon 14:32"
    return `${weekday(d)} ${hhmm(d)}`;
  }
  if (sameYear) {
    // Older than a week, same year: "May 15 14:32"
    return `${monthDay(d)} ${hhmm(d)}`;
  }
  // Cross-year: "May 15 2024"
  return `${monthDay(d)} ${d.getFullYear()}`;
}

// formatMessageTimeISO returns the full ISO string for the `title=` attr.
// Returns '' on missing.
export function formatMessageTimeISO(ts: number): string {
  if (!ts || ts <= 0) return '';
  return new Date(ts).toISOString();
}

function hhmm(d: Date): string {
  return pad2(d.getHours()) + ':' + pad2(d.getMinutes());
}
function pad2(n: number): string {
  return n < 10 ? '0' + n : String(n);
}
function weekday(d: Date): string {
  return ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'][d.getDay()];
}
function monthDay(d: Date): string {
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  return months[d.getMonth()] + ' ' + d.getDate();
}
