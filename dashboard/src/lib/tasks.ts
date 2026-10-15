import type { TaskList } from '../api';

/** The subset of an infinite query over task pages that TaskTable needs. */
export type TaskPages = {
  data?: { pages: TaskList[] };
  error: unknown;
  isPending: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => Promise<unknown>;
};

/** Paging options for the generated infinite task queries; the page param is the cursor. */
export const taskPaging = {
  initialPageParam: '',
  getNextPageParam: (last: TaskList) => last.next_cursor,
};
