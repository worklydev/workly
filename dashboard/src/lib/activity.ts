import type { ActivityBucket } from '../api';

/** The busiest queues get a color each; the rest share one. */
const COLORS = ['bg-chart-1', 'bg-chart-2', 'bg-chart-3', 'bg-chart-4', 'bg-chart-5'];
export const OTHER = 'other';

export type Series = {
  /** The top queues by total, then OTHER if there are more. */
  keys: string[];
  color: Record<string, string>;
  totals: Record<string, number>;
  total: number;
  /** The top of the chart: the tallest bar rounded up to 1, 2 or 5 × 10ⁿ, and at least 5, so a few tasks don't read as a spike. */
  scale: number;
};

export function toSeries(buckets: ActivityBucket[]): Series {
  const totals: Record<string, number> = {};
  for (const b of buckets) for (const [q, n] of Object.entries(b.counts)) totals[q] = (totals[q] ?? 0) + n;
  const ranked = Object.keys(totals).sort((a, b) => totals[b] - totals[a] || a.localeCompare(b));
  const top = ranked.length > COLORS.length ? ranked.slice(0, COLORS.length - 1) : ranked;
  const keys = ranked.length > top.length ? [...top, OTHER] : top;
  const color: Record<string, string> = {};
  top.forEach((q, i) => (color[q] = COLORS[i]));
  if (keys.includes(OTHER)) {
    color[OTHER] = 'bg-chart-other';
    totals[OTHER] = ranked.slice(top.length).reduce((n, q) => n + totals[q], 0);
  }
  const total = ranked.reduce((n, q) => n + totals[q], 0);
  return { keys, color, totals, total, scale: niceCeil(Math.max(5, ...buckets.map((b) => sum(b.counts)))) };
}

export function countFor(b: ActivityBucket, key: string, keys: string[]): number {
  if (key !== OTHER) return b.counts[key] ?? 0;
  return Object.entries(b.counts).reduce((n, [q, c]) => (keys.includes(q) ? n : n + c), 0);
}

export const sum = (counts: Record<string, number>) => Object.values(counts).reduce((a, b) => a + b, 0);

/** 1, 2 or 5 × 10ⁿ, the first at or above n: the top of a chart's scale. */
export function niceCeil(n: number): number {
  if (n <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(n));
  return ([1, 2, 5, 10].find((m) => m * p >= n) ?? 10) * p;
}
