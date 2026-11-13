import type { WorkflowRunList, WorkflowStep, WorkflowStepConfig } from '../api';
import { formatDuration, formatRelative, shortId } from './format';

/** Paging options for the generated infinite workflow run queries; the page param is the cursor. */
export const workflowRunPaging = {
  initialPageParam: '',
  getNextPageParam: (last: WorkflowRunList) => last.next_cursor,
};

export const WORKFLOW_QUEUE = '_workflows';

type Node = { name: string; depends_on?: string[] };

/**
 * The steps in columns for drawing: a step's column is the longest path to
 * it from a step with no dependencies, so every edge points right. Within a
 * column, steps keep their definition order. The server only accepts DAGs;
 * a dependency it doesn't know is ignored rather than looping.
 */
export function layers<T extends Node>(steps: T[]): T[][] {
  const byName = new Map(steps.map((s) => [s.name, s]));
  const rank = new Map<string, number>();
  const visiting = new Set<string>();
  const rankOf = (s: T): number => {
    const known = rank.get(s.name);
    if (known !== undefined) return known;
    if (visiting.has(s.name)) return 0;
    visiting.add(s.name);
    let r = 0;
    for (const dep of s.depends_on ?? []) {
      const d = byName.get(dep);
      if (d) r = Math.max(r, rankOf(d) + 1);
    }
    visiting.delete(s.name);
    rank.set(s.name, r);
    return r;
  };
  const out: T[][] = [];
  for (const s of steps) (out[rankOf(s)] ??= []).push(s);
  return out.filter(Boolean);
}

/** The steps in an order where each comes after its dependencies: the DAG as a list, for phones. */
export const topoOrder = <T extends Node>(steps: T[]): T[] => layers(steps).flat();

/** Why a step is where it is, in a few words: what it waits for, how its request went. */
export function stepDetail(s: WorkflowStep, now: number = Date.now()): string | undefined {
  const t = s.task;
  switch (s.status) {
    case 'blocked':
      return s.blocked_by.length > 0 ? `Waiting for ${s.blocked_by.join(', ')}` : 'Waiting for its dependencies';
    case 'pending':
      if (t && t.attempt_count > 0) {
        const after = t.last_status ? `HTTP ${t.last_status}` : (t.last_outcome?.replace('_', ' ') ?? 'an error');
        const when = t.run_at ? `, retrying ${formatRelative(t.run_at, now)}` : '';
        return `Attempt ${t.attempt_count} failed with ${after}${when}`;
      }
      return 'Queued';
    case 'running':
      return t && t.attempt_count > 0 ? `Attempt ${t.attempt_count + 1}` : 'Delivering';
    case 'waiting':
      return s.wait_for ? `Waiting for ${s.wait_for.type} ${shortId(s.wait_for.id)}` : 'Waiting';
    case 'succeeded':
      if (s.wait_for) return `After ${s.wait_for.type} ${shortId(s.wait_for.id)} succeeded`;
      return t?.last_status ? `HTTP ${t.last_status}` : undefined;
    case 'failed':
    case 'skipped':
      return s.error;
  }
}

/** How long a step took, or has taken so far. */
export function stepDuration(s: WorkflowStep): string | undefined {
  return s.duration_ms === undefined ? undefined : formatDuration(s.duration_ms);
}

const STEP_KEYS = [
  'name',
  'depends_on',
  'url',
  'method',
  'headers',
  'payload',
  'body',
  'content_type',
  'queue',
  'timeout_seconds',
  'max_attempts',
] as const;

/**
 * A step as it would be written in workly.yaml: name first, and without
 * what the server fills in when it stores it (POST, the `_workflows` queue, a
 * payload's JSON content type), so it reads like the file and can be copied
 * back into one unchanged in meaning.
 */
export function stepAsWritten(s: WorkflowStepConfig): Partial<WorkflowStepConfig> {
  const out: Record<string, unknown> = {};
  for (const k of STEP_KEYS) if (s[k] !== undefined) out[k] = s[k];
  if (s.method === 'POST') delete out.method;
  if (s.queue === WORKFLOW_QUEUE) delete out.queue;
  if (s.payload !== undefined && s.headers) {
    const headers = Object.fromEntries(
      Object.entries(s.headers).filter(([k, v]) => !(k.toLowerCase() === 'content-type' && v === 'application/json')),
    );
    if (Object.keys(headers).length > 0) out.headers = headers;
    else delete out.headers;
  }
  if (s.depends_on?.length === 0) delete out.depends_on;
  return out as Partial<WorkflowStepConfig>;
}
