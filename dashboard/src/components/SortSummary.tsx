import { ArrowDownUp, RotateCcw } from 'lucide-react';
import { describeSort, type TaskSorting } from '../lib/taskSort';
import { Button } from '../primitives/button';

/**
 * How a task list is sorted, and whether that's its default; once changed
 * (by clicking a column), a way back.
 */
export function SortSummary({ sorting }: { sorting: TaskSorting }) {
  const { sort, isDefault, fallback, reset } = sorting;
  const why =
    sort.field === 'run_at' && sort.order === 'asc'
      ? 'Pending tasks are delivered in this order, so the top one runs next.'
      : 'Click a column header to sort by it, and again to reverse.';
  return (
    <div className="flex h-7 items-center gap-1.5 text-xs text-muted-foreground" title={why}>
      <ArrowDownUp className="size-3.5 text-faint" />
      <span>
        Sorted by <span className="text-foreground">{describeSort(sort)}</span>
      </span>
      {isDefault ? (
        <span className="text-faint">· default</span>
      ) : (
        <Button variant="ghost" size="sm" onClick={reset} title={`Back to the default: ${describeSort(fallback)}`}>
          <RotateCcw />
          Reset
        </Button>
      )}
    </div>
  );
}
