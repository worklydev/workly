import { useLocation } from 'react-router';

/**
 * Where a task page was opened from: the path and query of a queue page or a
 * filtered task list, carried in router state so breadcrumbs and the sidebar
 * keep that place, and survive a reload.
 */
export type FromState = { from?: string };

export function useFrom(): string | undefined {
  const state = useLocation().state as FromState | null;
  return typeof state?.from === 'string' ? state.from : undefined;
}

/** Router state for links to a task from the current page. */
export function useFromHere(): FromState {
  const { pathname, search } = useLocation();
  return { from: pathname + search };
}
