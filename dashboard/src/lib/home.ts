import type { Queue, Schedule } from '../api';
import { COUNT_LIMIT, formatCount, formatDuration } from './format';

/** A piece of the health sentence: words, or words that link to what they name. */
export type Part = string | { text: string; to: string; mono?: boolean };

export type Health = {
  /** `new`: nothing in the workspace yet. */
  tone: 'ok' | 'warning' | 'danger' | 'new';
  /** At most two clauses: it's a headline, not a report. */
  clauses: Part[][];
  /** Tasks with a delivery in flight, for the live dot. */
  running: number;
};

/** A due task waiting longer than this means the queue is behind (as on the Queues page). */
const BEHIND_MS = 60_000;

const plural = (n: number, one: string, many = one + 's') => `${formatCount(n)} ${n === 1 ? one : many}`;
const queueLink = (name: string): Part => ({ text: name, to: `/queues/${encodeURIComponent(name)}`, mono: true });
const scheduleLink = (name: string): Part => ({ text: name, to: `/schedules/${encodeURIComponent(name)}`, mono: true });

/**
 * One sentence on how the workspace is doing, worst news first: failures,
 * then queues falling behind or paused work, else that all is quiet.
 */
export function workspaceHealth(queues: Queue[], schedules: Schedule[], now: number): Health {
  const running = Math.min(
    COUNT_LIMIT,
    queues.reduce((n, q) => n + (q.stats?.running ?? 0), 0),
  );
  if (queues.length === 0 && schedules.length === 0) return { tone: 'new', clauses: [], running };

  const danger: Part[][] = [];
  const failingQueues = queues.filter((q) => (q.stats?.failed ?? 0) > 0);
  const failed = Math.min(
    COUNT_LIMIT,
    failingQueues.reduce((n, q) => n + (q.stats?.failed ?? 0), 0),
  );
  if (failed > 0) {
    danger.push([
      { text: plural(failed, 'failed task'), to: '/failures' },
      ...(failingQueues.length === 1
        ? [' in ', queueLink(failingQueues[0].name)]
        : [` in ${failingQueues.length} queues`]),
    ]);
  }
  const failingSchedules = schedules.filter((s) => s.last_run?.state === 'failed');
  if (failingSchedules.length === 1) danger.push([scheduleLink(failingSchedules[0].name), ' failed its last run']);
  else if (failingSchedules.length > 1)
    danger.push([{ text: plural(failingSchedules.length, 'schedule'), to: '/failures' }, ' failed their last run']);

  const warning: Part[][] = [];
  const behind = queues
    .filter((q) => !q.paused && q.stats?.oldest_due_at)
    .map((q) => ({ q, ms: now - Date.parse(q.stats!.oldest_due_at!) }))
    .filter((b) => b.ms > BEHIND_MS)
    .sort((a, b) => b.ms - a.ms);
  if (behind.length === 1) warning.push([queueLink(behind[0].q.name), ` is ${formatDuration(behind[0].ms)} behind`]);
  else if (behind.length > 1)
    warning.push([
      `${behind.length} queues are behind, `,
      queueLink(behind[0].q.name),
      ` by ${formatDuration(behind[0].ms)}`,
    ]);
  const pausedQueues = queues.filter((q) => q.paused).length;
  const pausedSchedules = schedules.filter((s) => s.paused).length;
  const paused = [
    pausedQueues && plural(pausedQueues, 'queue'),
    pausedSchedules && plural(pausedSchedules, 'schedule'),
  ].filter(Boolean);
  if (paused.length > 0) warning.push([`${paused.join(' and ')} paused`]);

  const clauses = [...danger, ...warning].slice(0, 2);
  if (clauses.length > 0) return { tone: danger.length > 0 ? 'danger' : 'warning', clauses, running };

  const things = [
    queues.length > 0 && plural(queues.length, 'queue'),
    schedules.length > 0 && plural(schedules.length, 'schedule'),
  ].filter(Boolean);
  return { tone: 'ok', clauses: [[`All quiet: ${things.join(' and ')}, nothing failing`]], running };
}
