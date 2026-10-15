import type { Run, RunList } from '../api';

/** The subset of an infinite query over run pages that RunTable needs. */
export type RunPages = {
  data?: { pages: RunList[] };
  error: unknown;
  isPending: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => Promise<unknown>;
};

/** Paging options for the generated infinite run queries; the page param is the cursor. */
export const runPaging = {
  initialPageParam: '',
  getNextPageParam: (last: RunList) => last.next_cursor,
};

export const SKIP_REASONS = {
  overlap: 'The previous run was still pending or running (overlap: skip).',
  missed: 'The server was down at this time (missed: skip).',
} as const;

export const runFinished = (r: Run) =>
  r.state === 'succeeded' || r.state === 'failed' || r.state === 'cancelled' || r.state === 'skipped';
