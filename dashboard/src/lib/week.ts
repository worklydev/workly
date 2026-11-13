import type { CronSpan } from '../api';

const DAY = 86_400_000;

/**
 * Monday 00:00 of the week `offset` weeks from this one, in the viewer's
 * timezone. Days are stepped with the calendar, not by 24 hours, so a week
 * with a daylight saving change still starts and ends at midnight.
 */
export function weekStart(offset: number, now = new Date()): Date {
  const d = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  d.setDate(d.getDate() - ((d.getDay() + 6) % 7) + offset * 7);
  return d;
}

/** The week's seven days, Monday first, each from its midnight to the next. */
export function weekDays(start: Date): { start: number; end: number; date: Date }[] {
  return Array.from({ length: 7 }, (_, i) => {
    const date = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
    const next = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i + 1);
    return { start: date.getTime(), end: next.getTime(), date };
  });
}

/**
 * What a schedule does in one day: its runs at their times, or, past
 * MAX_TIMES, stretches of runs at an interval, which read better than a
 * wall of ticks.
 */
export type DayRuns =
  | { kind: 'none' }
  | { kind: 'times'; times: number[] }
  | { kind: 'bands'; count: number; bands: { from: number; to: number; every: number }[] };

/** Up to this many runs in a day (every half hour) are ticks; more become bands. */
export const MAX_TIMES = 48;

/** The runs of `spans` from `start` until `end` (ms). */
export function dayRuns(spans: CronSpan[], start: number, end: number): DayRuns {
  const parts: { first: number; last: number; every: number; count: number }[] = [];
  for (const s of spans) {
    const t0 = Date.parse(s.start);
    const every = s.every_seconds * 1000;
    if (every === 0) {
      if (t0 >= start && t0 < end) parts.push({ first: t0, last: t0, every: 0, count: 1 });
      continue;
    }
    // The span's runs are t0 + k·every for k in [0, count); keep the ones in the day.
    const lo = Math.max(0, Math.ceil((start - t0) / every));
    const hi = Math.min(s.count - 1, Math.floor((end - 1 - t0) / every));
    if (hi >= lo) parts.push({ first: t0 + lo * every, last: t0 + hi * every, every, count: hi - lo + 1 });
  }
  const count = parts.reduce((n, p) => n + p.count, 0);
  if (count === 0) return { kind: 'none' };
  if (count <= MAX_TIMES) {
    const times = parts.flatMap((p) => Array.from({ length: p.count }, (_, k) => p.first + k * p.every));
    return { kind: 'times', times: times.sort((a, b) => a - b) };
  }
  return {
    kind: 'bands',
    count,
    bands: parts.filter((p) => p.count > 1).map((p) => ({ from: p.first, to: p.last, every: p.every })),
  };
}

/** Where in its day a time falls, from 0 (midnight) to 1 (the next). */
export const dayFraction = (t: number, start: number, end: number) => (t - start) / (end - start);

export const isToday = (start: number, end: number, now: number) => now >= start && now < end;

export const WEEK_MS = 7 * DAY;
