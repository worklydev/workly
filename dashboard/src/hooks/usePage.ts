import { useState } from 'react';
import { PAGE_SIZE, type ListPage } from './useListPage';

export type Page<T> = Pick<ListPage<T>, 'items' | 'matching' | 'page' | 'pages' | 'setPage'>;

/**
 * Pages a list held whole, with the page in component state rather than the
 * URL, for a list that shares its page with others (useListPage keeps a
 * page's main list in the URL).
 */
export function usePage<T>(all: T[]): Page<T> {
  const [index, setPage] = useState(0);
  const pages = Math.max(1, Math.ceil(all.length / PAGE_SIZE));
  const page = Math.min(index, pages - 1);
  return { items: all.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE), matching: all.length, page, pages, setPage };
}
