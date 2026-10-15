import type { Queue } from '../api';

/** "5/s · 2 concurrent", or "2 concurrent" without a rate. */
export function limits(q: Queue): string {
  const concurrent = `${q.max_concurrency} concurrent`;
  return q.rate_per_second === undefined ? concurrent : `${q.rate_per_second}/s · ${concurrent}`;
}
