import type { Group, GroupList } from '../api';

/** The subset of an infinite query over group pages that the groups table needs. */
export type GroupPages = {
  data?: { pages: GroupList[] };
  error: unknown;
  isPending: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => Promise<unknown>;
};

/** Paging options for the generated infinite group queries; the page param is the cursor. */
export const groupPaging = {
  initialPageParam: '',
  getNextPageParam: (last: GroupList) => last.next_cursor,
};

/** How many of a group's tasks have finished, out of how many. */
export const finishedCount = (g: Group) => g.total - g.remaining;

export const groupFinished = (g: Group) => g.status === 'succeeded' || g.status === 'failed';
