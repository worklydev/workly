import type { Queue } from '../api';

/** "5/s · 2 concurrent", or "2 concurrent" without a rate. */
export function limits(q: Queue): string {
  const concurrent = `${q.max_concurrency} concurrent`;
  return q.rate_per_second === undefined ? concurrent : `${q.rate_per_second}/s · ${concurrent}`;
}

/** A success rate below this is red, in the queue list and on the queue's page alike. */
export const LOW_SUCCESS = 0.9;

/** Queues named with a leading `_` are Workly's own: their settings can change, but nothing can be enqueued into them. */
export const isSystemQueue = (name: string) => name.startsWith('_');

/** What each system queue carries, for the queue list and the queue's page. */
export function systemQueuePurpose(name: string): string {
  switch (name) {
    case '_callbacks':
      return 'Callbacks of tasks, groups and schedules.';
    case '_workflows':
      return 'Workflow steps that name no queue of their own.';
    default:
      return 'Used by Workly itself.';
  }
}
