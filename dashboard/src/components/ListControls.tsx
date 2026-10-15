import { ChevronLeft, ChevronRight, Search } from 'lucide-react';
import type { CursorPage } from '../hooks/useCursorPages';
import { PAGE_SIZE, type ListPage } from '../hooks/useListPage';
import type { Page } from '../hooks/usePage';
import { Button } from '../primitives/button';
import { DisabledReason } from './DisabledReason';
import { Input } from '../primitives/input';

/** The search box above a paged list. */
export function ListSearch({ list, placeholder }: { list: ListPage<unknown>; placeholder: string }) {
  return (
    <Input
      icon={<Search />}
      type="search"
      placeholder={placeholder}
      value={list.search}
      onChange={(e) => list.setSearch(e.target.value)}
      className="w-72"
    />
  );
}

/** "26–50 of 140" with previous and next, below a paged list. */
export function ListPager({ list, noun }: { list: Page<unknown>; noun: string }) {
  if (list.matching === 0) return null;
  const first = list.page * PAGE_SIZE + 1;
  const last = Math.min(list.matching, first + PAGE_SIZE - 1);
  const isFirst = list.page === 0;
  const isLast = list.page >= list.pages - 1;
  return (
    <div className="flex items-center justify-end gap-2 text-xs text-muted-foreground">
      <span className="font-mono tabular-nums">
        {first}–{last} of {list.matching.toLocaleString()}
      </span>
      <span>{noun}</span>
      <DisabledReason reason={isFirst ? 'This is the first page.' : undefined}>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Previous page"
          title="Previous page"
          disabled={isFirst}
          onClick={() => list.setPage(list.page - 1)}
        >
          <ChevronLeft />
        </Button>
      </DisabledReason>
      <DisabledReason reason={isLast ? 'This is the last page.' : undefined}>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Next page"
          title="Next page"
          disabled={isLast}
          onClick={() => list.setPage(list.page + 1)}
        >
          <ChevronRight />
        </Button>
      </DisabledReason>
    </div>
  );
}

/** "51–100" (of 120, once known) with previous and next, below a cursor-paged list. */
export function CursorPager({ page, noun }: { page: CursorPage<unknown>; noun: string }) {
  if (page.items.length === 0) return null;
  const first = page.offset + 1;
  const last = page.offset + page.items.length;
  return (
    <div className="flex items-center justify-end gap-2 text-xs text-muted-foreground">
      <span className="font-mono tabular-nums">
        {first}–{last}
        {page.total !== undefined && ` of ${page.total.toLocaleString()}`}
      </span>
      <span>{noun}</span>
      <DisabledReason reason={page.hasPrev ? undefined : 'This is the first page.'}>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Previous page"
          title="Previous page"
          disabled={!page.hasPrev}
          onClick={page.prev}
        >
          <ChevronLeft />
        </Button>
      </DisabledReason>
      <DisabledReason reason={page.hasNext ? undefined : 'This is the last page.'}>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Next page"
          title={page.loading ? 'Loading…' : 'Next page'}
          disabled={!page.hasNext || page.loading}
          onClick={page.next}
        >
          <ChevronRight />
        </Button>
      </DisabledReason>
    </div>
  );
}
