/** A duration in milliseconds, compact: "340ms", "4.2s", "3m 12s", "2h 5m", "3d 4h". */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s < 10 ? s.toFixed(1) : Math.round(s)}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${Math.round(s % 60)}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

/** Seconds from a queue policy: "500ms", "30s", "1h", "1m 30s". */
export function formatSeconds(s: number): string {
  if (s < 1) return `${Math.round(s * 1000)}ms`;
  const parts: string[] = [];
  const units: [number, string][] = [
    [86400, 'd'],
    [3600, 'h'],
    [60, 'm'],
    [1, 's'],
  ];
  let rest = Math.round(s);
  for (const [size, unit] of units) {
    if (rest >= size) {
      parts.push(`${Math.floor(rest / size)}${unit}`);
      rest %= size;
    }
  }
  return parts.slice(0, 2).join(' ');
}

/** "12s ago", "3m ago", "in 5m"; the date from 30 days on. */
export function formatRelative(iso: string, now: number = Date.now()): string {
  const diff = new Date(iso).getTime() - now;
  const abs = Math.abs(diff);
  if (abs >= 30 * DAY) return formatDate(iso);
  if (abs < 1000) return 'now';
  const label = compact(abs);
  return diff > 0 ? `in ${label}` : `${label} ago`;
}

/** How long until a formatRelative label can change. */
export function relativeRefreshMs(iso: string, now: number = Date.now()): number | null {
  const abs = Math.abs(new Date(iso).getTime() - now);
  if (abs < MINUTE) return 1000;
  if (abs < HOUR) return 15_000;
  if (abs < 30 * DAY) return MINUTE;
  return null;
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

function compact(ms: number): string {
  if (ms < MINUTE) return `${Math.floor(ms / 1000)}s`;
  if (ms < HOUR) return `${Math.floor(ms / MINUTE)}m`;
  if (ms < DAY) return `${Math.floor(ms / HOUR)}h`;
  return `${Math.floor(ms / DAY)}d`;
}

export function formatDate(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

/** Queue stats counts stop at 10,000 on the server. */
export const COUNT_LIMIT = 10_000;

export function formatCount(n: number): string {
  return n >= COUNT_LIMIT ? '10k+' : n.toLocaleString();
}

/** "100%", "98.5%": one decimal below 100, so a few failures still show. */
export function formatRate(rate: number): string {
  if (rate === 1) return '100%';
  return `${Math.min(99.9, Math.floor(rate * 1000) / 10)}%`;
}

/** Pretty-prints JSON; other text is returned as is. */
export function prettyBody(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
