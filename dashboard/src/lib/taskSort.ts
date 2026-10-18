import { useSearchParams } from 'react-router';
import type { TaskState } from '../api';

export type SortField = 'created_at' | 'run_at' | 'last_attempt_at' | 'attempt_count';
export type SortOrder = 'asc' | 'desc';
export type TaskSort = { field: SortField; order: SortOrder };

/** Each column's name, the order a first click picks, and how each order reads. */
const FIELDS: Record<SortField, { label: string; first: SortOrder } & Record<SortOrder, string>> = {
  created_at: { label: 'Created', first: 'desc', desc: 'newest first', asc: 'oldest first' },
  run_at: { label: 'Next run', first: 'asc', asc: 'soonest first', desc: 'latest first' },
  last_attempt_at: { label: 'Last run', first: 'desc', desc: 'most recent first', asc: 'never run, then oldest' },
  attempt_count: { label: 'Attempts', first: 'desc', desc: 'most first', asc: 'fewest first' },
};

/**
 * What a list of tasks in this state can be sorted by. Next run only means
 * something for pending tasks: running ones are in flight, finished ones
 * won't run again.
 */
export function sortFields(state: TaskState | 'all'): SortField[] {
  const fields: SortField[] = ['created_at', 'last_attempt_at', 'attempt_count'];
  return state === 'pending' ? ['run_at', ...fields] : fields;
}

/** Pending tasks list in the order they run; everything else newest first. */
export function defaultSort(state: TaskState | 'all'): TaskSort {
  return state === 'pending' ? { field: 'run_at', order: 'asc' } : { field: 'created_at', order: 'desc' };
}

/** E.g. "Next run, soonest first". */
export function describeSort({ field, order }: TaskSort): string {
  return `${FIELDS[field].label}, ${FIELDS[field][order]}`;
}

export type TaskSorting = ReturnType<typeof useTaskSort>;

/**
 * The task list's sort, kept in the URL (`?sort=…&order=…`) so it survives
 * reloads and shared links. Only a non-default sort is written.
 */
export function useTaskSort(state: TaskState | 'all') {
  const [params, setParams] = useSearchParams();
  const fallback = defaultSort(state);
  const fields = sortFields(state);
  const field = params.get('sort') as SortField | null;
  const order = params.get('order');
  // A sort that doesn't apply to this state (Next run on Succeeded) falls back.
  const sort: TaskSort =
    field && fields.includes(field)
      ? { field, order: order === 'asc' || order === 'desc' ? order : FIELDS[field].first }
      : fallback;
  const isDefault = sort.field === fallback.field && sort.order === fallback.order;

  const set = (next: TaskSort) => {
    const p = new URLSearchParams(params);
    if (next.field === fallback.field && next.order === fallback.order) {
      p.delete('sort');
      p.delete('order');
    } else {
      p.set('sort', next.field);
      p.set('order', next.order);
    }
    setParams(p, { replace: true });
  };
  const flip = (o: SortOrder): SortOrder => (o === 'asc' ? 'desc' : 'asc');

  return {
    sort,
    fallback,
    isDefault,
    fields,
    /** Sorts by the column, or reverses the order if it's already the sort. */
    toggle: (f: SortField) =>
      set(sort.field === f ? { field: f, order: flip(sort.order) } : { field: f, order: FIELDS[f].first }),
    reset: () => set(fallback),
    /** The API's `sort` and `order` parameters. */
    query: { sort: sort.field, order: sort.order },
  };
}
