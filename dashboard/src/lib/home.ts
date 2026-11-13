import type { Queue, Schedule, Workflow } from '../api';
import type { QueueFailureGroup, RunFailureGroup } from '../hooks/useFailureGroups';
import { lastFailed } from '../hooks/useFailureGroups';
import { COUNT_LIMIT, formatCount, formatDuration } from './format';

/** Something about one queue, schedule or workflow that someone should look at. */
export type Issue = {
  key: string;
  tone: 'danger' | 'warning' | 'neutral';
  /** The queue or schedule; for paused ones, what's paused. */
  name: string;
  /** What's wrong, in a few words: "6 failed", "4m behind". */
  what: string;
  /** Why, when known: the error it failed with. */
  detail?: string;
  /** More of the same: "+2 other errors". */
  more?: string;
  /** When it last happened. */
  at?: string;
  to: string;
};

export type Status = {
  tone: 'danger' | 'warning' | 'ok' | 'idle';
  title: string;
  issues: Issue[];
  /** Tasks with a delivery in flight. */
  running: number;
};

/** A due task waiting longer than this means its queue is behind, as on the Queues page. */
export const BEHIND_MS = 60_000;

const plural = (n: number, one: string, many = one + 's') => `${formatCount(n)} ${n === 1 ? one : many}`;
const queuePath = (name: string) => `/queues/${encodeURIComponent(name)}`;
const schedulePath = (name: string) => `/schedules/${encodeURIComponent(name)}`;
const workflowPath = (name: string) => `/workflows/${encodeURIComponent(name)}`;

/** How long a queue's oldest due task has waited, if it's behind. */
export function behindBy(q: Queue, now: number): number | undefined {
  const since = q.stats?.oldest_due_at;
  if (q.paused || !since) return undefined;
  const ms = now - Date.parse(since);
  return ms > BEHIND_MS ? ms : undefined;
}

/**
 * The workspace's status: one issue per queue or schedule, worst first
 * (failures, then queues falling behind, then paused work, which is usually
 * on purpose and never raises the tone), and a sentence that sums them up.
 */
export function workspaceStatus({
  queues,
  schedules,
  workflows = [],
  queueFailures,
  runFailures,
  created,
  range,
  now,
}: {
  queues: Queue[];
  schedules: Schedule[];
  workflows?: Workflow[];
  queueFailures: QueueFailureGroup[];
  runFailures: RunFailureGroup[];
  /** Tasks created in the range; undefined while loading. */
  created?: number;
  range: string;
  now: number;
}): Status {
  const stat = (key: 'due' | 'scheduled' | 'running') =>
    Math.min(
      COUNT_LIMIT,
      queues.reduce((n, q) => n + (q.stats?.[key] ?? 0), 0),
    );
  const running = stat('running');
  const issues: Issue[] = [];

  for (const q of [...queues].sort((a, b) => (b.stats?.failed ?? 0) - (a.stats?.failed ?? 0))) {
    const failed = q.stats?.failed ?? 0;
    if (failed === 0) continue;
    const groups = queueFailures.filter((g) => g.queue === q.name);
    issues.push({
      key: `failed\n${q.name}`,
      tone: 'danger',
      name: q.name,
      what: `${formatCount(failed)} failed`,
      detail: groups[0]?.error || undefined,
      more: groups.length > 1 ? `+${plural(groups.length - 1, 'other error')}` : undefined,
      at: groups
        .map((g) => g.last_failed_at)
        .sort()
        .at(-1),
      to: `${queuePath(q.name)}?state=failed`,
    });
  }

  for (const s of schedules) {
    if (s.last_run?.state !== 'failed') continue;
    const latest = runFailures.filter((g) => g.schedule === s.name).sort((a, b) => lastFailed(b) - lastFailed(a))[0];
    issues.push({
      key: `run\n${s.name}`,
      tone: 'danger',
      name: s.name,
      what: 'last run failed',
      detail: latest?.error || undefined,
      at: latest ? new Date(lastFailed(latest)).toISOString() : s.last_run.scheduled_for,
      to: `${schedulePath(s.name)}?state=failed`,
    });
  }

  for (const w of workflows) {
    const last = w.last_run;
    if (last?.status !== 'failed') continue;
    issues.push({
      key: `workflow\n${w.name}`,
      tone: 'danger',
      name: w.name,
      what: 'last workflow run failed',
      detail: last.error,
      at: last.finished_at ?? last.created_at,
      to: `${workflowPath(w.name)}?status=failed`,
    });
  }

  const behind = queues
    .map((q) => ({ q, ms: behindBy(q, now) }))
    .filter((b): b is { q: Queue; ms: number } => b.ms !== undefined)
    .sort((a, b) => b.ms - a.ms);
  for (const { q, ms } of behind) {
    issues.push({
      key: `behind\n${q.name}`,
      tone: 'warning',
      name: q.name,
      what: `${formatDuration(ms)} behind`,
      detail: `${formatCount(q.stats?.due ?? 0)} due now`,
      to: `${queuePath(q.name)}?state=pending`,
    });
  }

  const pausedQueues = queues.filter((q) => q.paused);
  const pausedSchedules = schedules.filter((s) => s.paused);
  const paused = [...pausedQueues.map((q) => q.name), ...pausedSchedules.map((s) => s.name)];
  if (paused.length > 0) {
    issues.push({
      key: 'paused',
      tone: 'neutral',
      name: paused.length === 1 ? paused[0] : `${paused.length} paused`,
      // One is named in the row already; several are counted, and named in the detail.
      what:
        paused.length === 1
          ? `${pausedQueues.length === 1 ? 'queue' : 'schedule'} paused`
          : [
              pausedQueues.length > 0 && plural(pausedQueues.length, 'queue'),
              pausedSchedules.length > 0 && plural(pausedSchedules.length, 'schedule'),
            ]
              .filter(Boolean)
              .join(' and ')
              .concat(' paused'),
      detail: paused.length > 1 ? paused.join(', ') : undefined,
      to:
        paused.length === 1
          ? pausedQueues.length === 1
            ? queuePath(pausedQueues[0].name)
            : schedulePath(pausedSchedules[0].name)
          : pausedQueues.length > 0
            ? '/queues'
            : '/schedules',
    });
  }

  const urgent = issues.filter((i) => i.tone !== 'neutral');
  if (urgent.length > 0) {
    return {
      tone: urgent.some((i) => i.tone === 'danger') ? 'danger' : 'warning',
      title: `${urgent.length} ${urgent.length === 1 ? 'thing needs' : 'things need'} attention`,
      issues,
      running,
    };
  }
  if (created === 0 && stat('due') + stat('scheduled') + running === 0) {
    return { tone: 'idle', title: `Quiet: nothing enqueued in the last ${range}`, issues, running };
  }
  const things = [
    queues.length > 0 && plural(queues.length, 'queue'),
    schedules.length > 0 && plural(schedules.length, 'schedule'),
  ].filter(Boolean);
  return { tone: 'ok', title: `All quiet: ${things.join(' and ')}, nothing failing`, issues, running };
}
