import { useQueries, useQuery } from '@tanstack/react-query';
import {
  getQueueFailuresOptions,
  listQueuesOptions,
  listRunsOptions,
  listWorkflowRunsOptions,
} from '../api/@tanstack/react-query.gen';
import type { FailureGroup, Run, WorkflowRunSummary } from '../api';
import { LIVE } from '../lib/api';
import { WORKFLOW_QUEUE } from '../lib/workflows';

export type QueueFailureGroup = FailureGroup & { queue: string };

/**
 * Failed tasks in every queue, grouped by the error they ended with; biggest
 * groups first. Workflow steps in Workly's own queue are left out: they fail
 * their run, which is where they're shown and retried.
 */
export function useQueueFailureGroups() {
  const queues = useQuery({ ...listQueuesOptions(), ...LIVE });
  const failing = (queues.data?.queues ?? []).filter((q) => q.name !== WORKFLOW_QUEUE && (q.stats?.failed ?? 0) > 0);
  const failures = useQueries({
    queries: failing.map((q) => ({
      ...getQueueFailuresOptions({ path: { queue: q.name }, query: { limit: 50 } }),
      refetchInterval: 15_000,
    })),
  });
  const groups: QueueFailureGroup[] = failures
    .flatMap((f, i) => (f.data?.groups ?? []).map((g) => ({ ...g, queue: failing[i].name })))
    .sort((a, b) => b.count - a.count || Date.parse(b.last_failed_at) - Date.parse(a.last_failed_at));
  return {
    groups,
    error: queues.error ?? failures.find((f) => f.error)?.error,
    isPending: queues.isPending || failures.some((f) => f.isPending),
  };
}

/** How many of the latest failed runs are grouped; if there are more, counts show as "12+". */
const RUN_LIMIT = 200;

export type RunFailureGroup = { schedule: string; error: string; count: number; more: boolean; latest: Run };

/**
 * Failed runs of every schedule, grouped like the queues' failures. The API
 * has no failure summary for schedules, so this groups the latest failed
 * runs here.
 */
export function useRunFailureGroups() {
  const runs = useQuery({
    ...listRunsOptions({ query: { state: 'failed', limit: RUN_LIMIT } }),
    refetchInterval: 15_000,
  });
  const byKey = new Map<string, RunFailureGroup>();
  // Newest first, so the first run of each group is its latest.
  for (const run of runs.data?.runs ?? []) {
    const error = run.last_error ?? '';
    const key = `${run.schedule}\n${error}`;
    const g = byKey.get(key);
    if (g) g.count++;
    else byKey.set(key, { schedule: run.schedule, error, count: 1, more: false, latest: run });
  }
  if (runs.data?.next_cursor) byKey.forEach((g) => (g.more = true));
  const groups = [...byKey.values()].sort((a, b) => b.count - a.count || lastFailed(b) - lastFailed(a));
  return { groups, runs };
}

export const lastFailed = (g: RunFailureGroup) => Date.parse(g.latest.finished_at ?? g.latest.updated_at);

export type WorkflowFailureGroup = {
  workflow: string;
  error: string;
  count: number;
  more: boolean;
  latest: WorkflowRunSummary;
};

/** Failed runs of every workflow, grouped by workflow and error (which names the failed step), like the schedules'. */
export function useWorkflowFailureGroups() {
  const runs = useQuery({
    ...listWorkflowRunsOptions({ query: { status: 'failed', limit: RUN_LIMIT } }),
    refetchInterval: 15_000,
  });
  const byKey = new Map<string, WorkflowFailureGroup>();
  // Newest first, so the first run of each group is its latest.
  for (const run of runs.data?.workflow_runs ?? []) {
    const error = run.error ?? '';
    const key = `${run.workflow}\n${error}`;
    const g = byKey.get(key);
    if (g) g.count++;
    else byKey.set(key, { workflow: run.workflow, error, count: 1, more: false, latest: run });
  }
  if (runs.data?.next_cursor) byKey.forEach((g) => (g.more = true));
  const at = (g: WorkflowFailureGroup) => Date.parse(g.latest.finished_at ?? g.latest.created_at);
  const groups = [...byKey.values()].sort((a, b) => b.count - a.count || at(b) - at(a));
  return { groups, runs };
}
