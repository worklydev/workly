import type { Queue } from '../api';

/** "5/s · 2 concurrent", or "2 concurrent" without a rate. */
export function limits(q: Queue): string {
  const concurrent = `${q.max_concurrency} concurrent`;
  return q.rate_per_second === undefined ? concurrent : `${q.rate_per_second}/s · ${concurrent}`;
}

/** A success rate below this is red, in the queue list and on the queue's page alike. */
export const LOW_SUCCESS = 0.9;
