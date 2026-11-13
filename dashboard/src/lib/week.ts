import type { CronSpan } from '../api';

/**
 * A week's timetable in a schedule's own timezone, from the runs the server
 * lists (spans): which days it runs, and when.
 */

/** The wall-clock parts of instant `t` in `zone`. */
function wallParts(t: number, zone: string) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hourCycle: 'h23',
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
    minute: 'numeric',
    second: 'numeric',
  }).formatToParts(t);
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  return {
    year: get('year'),
    month: get('month') - 1,
    day: get('day'),
    hour: get('hour'),
    minute: get('minute'),
    second: get('second'),
  };
}

/** How far `zone` is ahead of UTC at instant `t`, in ms. */
function offset(t: number, zone: string): number {
  const p = wallParts(t, zone);
  return Date.UTC(p.year, p.month, p.day, p.hour, p.minute, p.second) - Math.floor(t / 1000) * 1000;
}

/** The instant of a wall-clock midnight in `zone` (the date may overflow, e.g. day 32). */
function zonedMidnight(year: number, month: number, day: number, zone: string): number {
  const wall = Date.UTC(year, month, day);
  // Twice, so a day whose midnight is on the other side of a DST change lands right.
  const first = wall - offset(wall, zone);
  return wall - offset(first, zone);
}

/** This week's days, Monday to Sunday, each from midnight to midnight in `zone`. */
export function zonedWeek(zone: string, now = Date.now()): { start: number; end: number }[] {
  const p = wallParts(now, zone);
  const weekday = new Date(Date.UTC(p.year, p.month, p.day)).getUTCDay();
  const monday = p.day - ((weekday + 6) % 7);
  return Array.from({ length: 7 }, (_, i) => ({
    start: zonedMidnight(p.year, p.month, monday + i, zone),
    end: zonedMidnight(p.year, p.month, monday + i + 1, zone),
  }));
}

/** What a schedule does in one day: no runs, a few at their times, or runs at an interval through a window. */
export type DayPlan =
  | { kind: 'none' }
  | { kind: 'times'; times: number[] }
  | { kind: 'every'; every: number; from: number; to: number; allDay: boolean }
  | { kind: 'many'; count: number };

/** Up to this many runs a day are listed by time. */
const LISTED = 3;

/** The runs of `spans` from `start` until `end` (ms), summed up for a calendar cell. */
export function dayPlan(spans: CronSpan[], start: number, end: number): DayPlan {
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
  if (count <= LISTED) {
    const times = parts.flatMap((p) => Array.from({ length: p.count }, (_, k) => p.first + k * p.every));
    return { kind: 'times', times: times.sort((a, b) => a - b) };
  }
  // One steady interval through the day (or a window of it) reads as "every 15m".
  const times =
    parts.length === 1
      ? undefined
      : parts
          .flatMap((p) => Array.from({ length: Math.min(p.count, 2000) }, (_, k) => p.first + k * p.every))
          .sort((a, b) => a - b);
  const gaps = times && new Set(times.slice(1).map((t, i) => t - times[i]));
  const every = parts.length === 1 ? parts[0].every : gaps && gaps.size === 1 ? [...gaps][0] : undefined;
  if (every === undefined) return { kind: 'many', count };
  const from = Math.min(...parts.map((p) => p.first));
  const to = Math.max(...parts.map((p) => p.last));
  return { kind: 'every', every, from, to, allDay: from - start < every && end - to <= every };
}
