import { useState } from 'react';

/** The subset of an infinite query (pages fetched by cursor) that paging needs. */
export type InfinitePages<P> = {
  data?: { pages: P[] };
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => Promise<unknown>;
};

export type CursorPage<T> = {
  items: T[];
  /** Zero-based index of the first item shown. */
  offset: number;
  /** All items, once the last page is loaded. */
  total?: number;
  hasPrev: boolean;
  hasNext: boolean;
  loading: boolean;
  prev: () => void;
  next: () => void;
};

/**
 * Pages through a list the API returns a cursor page at a time: one API page
 * per dashboard page, fetched when first shown. Without a total from the API,
 * it's known only once the last page is loaded.
 */
export function useCursorPages<P, T>(pages: InfinitePages<P>, items: (page: P) => T[]): CursorPage<T> {
  const [index, setIndex] = useState(0);
  const loaded = pages.data?.pages ?? [];
  // A new filter starts a new query with fewer pages loaded.
  const i = Math.min(index, Math.max(0, loaded.length - 1));
  const offset = loaded.slice(0, i).reduce((n, p) => n + items(p).length, 0);
  const atEnd = i === loaded.length - 1 && !pages.hasNextPage;
  return {
    items: loaded[i] ? items(loaded[i]) : [],
    offset,
    total: atEnd ? offset + (loaded[i] ? items(loaded[i]).length : 0) : undefined,
    hasPrev: i > 0,
    hasNext: i < loaded.length - 1 || pages.hasNextPage,
    loading: pages.isFetchingNextPage,
    prev: () => setIndex(Math.max(0, i - 1)),
    next: () => {
      if (i < loaded.length - 1) return setIndex(i + 1);
      void pages.fetchNextPage().then(() => setIndex(i + 1));
    },
  };
}
