import { Link } from 'react-router';
import { cn } from 'cn';
import type { WorkflowRunStatus, WorkflowRunSummary } from '../../api';
import { formatDate } from '../../lib/format';
import { RECENT } from './useRecentWorkflowRuns';
const TONE: Record<WorkflowRunStatus, string> = {
  succeeded: 'bg-success',
  failed: 'bg-destructive',
  running: 'bg-primary',
  cancelled: 'bg-muted-foreground/40',
};

/** A workflow's latest runs as a strip, like a schedule's: newest on the right, always the same width. */
export function RecentWorkflowRuns({
  runs,
  phoneSlots = RECENT,
}: {
  runs?: WorkflowRunSummary[];
  phoneSlots?: number;
}) {
  if (runs === undefined) return <span className="text-xs text-faint">…</span>;
  const shown = runs.slice(0, RECENT);
  const empty = RECENT - shown.length;
  const wideOnly = (i: number) => i < RECENT - phoneSlots && 'max-sm:hidden';
  return (
    <div
      className="flex items-center gap-0.5"
      title={runs.length === 0 ? 'No runs yet' : undefined}
      onClick={(e) => e.stopPropagation()}
    >
      {Array.from({ length: empty }, (_, i) => (
        <span key={i} aria-hidden className={cn('h-4 w-1.5 rounded-sm bg-muted', wideOnly(i))} />
      ))}
      {[...shown].reverse().map((r, i) => (
        <Link
          key={r.id}
          to={`/workflow-runs/${r.id}`}
          title={`${r.status} · ${formatDate(r.created_at)}`}
          className={cn(
            'h-4 w-1.5 rounded-sm transition-opacity hover:opacity-70',
            TONE[r.status],
            wideOnly(empty + i),
          )}
        />
      ))}
    </div>
  );
}
