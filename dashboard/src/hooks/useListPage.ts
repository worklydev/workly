import { useState } from 'react';
import { useSearchParams } from 'react-router';

export const PAGE_SIZE = 25;

export type ListPage<T> = {
  /** The items on the current page. */
  items: T[];
  /** How many items match the search, across all pages. */
  matching: number;
  /** Zero-based, clamped to the pages there are. */
  page: number;
  pages: number;
  setPage: (page: number) => void;
  search: string;
  setSearch: (search: string) => void;
};

/**
 * Searches and pages a list the API returns whole. The search and page live
 * in the URL (`q`, `page`), so they survive going back from a detail page.
 */
export function useListPage<T>(all: T[], text: (item: T) => string[]): ListPage<T> {
  const [params, setParams] = useSearchParams();
  // Typing goes to state first: the URL updates a render later, and reading
  // the search back from it would drop keystrokes.
  const [search, setSearchState] = useState(() => params.get('q') ?? '');
  const terms = search.toLowerCase().split(/\s+/).filter(Boolean);
  const matches = terms.length
    ? all.filter((item) => {
        const haystack = text(item).join(' ').toLowerCase();
        return terms.every((t) => haystack.includes(t));
      })
    : all;

  const pages = Math.max(1, Math.ceil(matches.length / PAGE_SIZE));
  const page = Math.min(Math.max(0, Number(params.get('page') ?? 1) - 1 || 0), pages - 1);

  const update = (set: (next: URLSearchParams) => void) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        set(next);
        return next;
      },
      { replace: true },
    );

  return {
    items: matches.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE),
    matching: matches.length,
    page,
    pages,
    setPage: (p) => update((next) => (p > 0 ? next.set('page', String(p + 1)) : next.delete('page'))),
    search,
    setSearch: (s) => {
      setSearchState(s);
      update((next) => {
        if (s) next.set('q', s);
        else next.delete('q');
        // A new search starts from its first page.
        next.delete('page');
      });
    },
  };
}
