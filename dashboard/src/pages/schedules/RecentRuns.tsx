import { Link } from 'react-router';
import { cn } from 'cn';
import type { Run } from '../../api';
import { formatDate } from '../../lib/format';

const TONE: Record<Run['state'], string> = {
  succeeded: 'bg-success',
  failed: 'bg-destructive',
  skipped: 'bg-warning-fill',
  running: 'bg-primary',
  pending: 'bg-muted-foreground/40',
  cancelled: 'bg-muted-foreground/40',
};

/**
 * The latest runs as a strip of small bars, oldest first, newest on the right;
 * each links to its run. Always `slots` wide, slots without a run as faint
 * stubs, so strips line up down a list and a new schedule reads as new.
 */
export function RecentRuns({
  runs,
  slots = 12,
  phoneSlots = slots,
}: {
  runs?: Run[];
  slots?: number;
  /** Fewer on a phone, where a table row has less room: the oldest drop off. */
  phoneSlots?: number;
}) {
  if (runs === undefined) return <span className="text-xs text-faint">…</span>;
  if (runs.length === 0) return <span className="text-xs text-faint">no runs yet</span>;
  const shown = runs.slice(0, slots);
  const empty = slots - shown.length;
  // Slot i counts from the oldest; the first slots - phoneSlots are wide-screen only.
  const wideOnly = (i: number) => i < slots - phoneSlots && 'max-sm:hidden';
  return (
    <div className="flex items-center gap-0.5" onClick={(e) => e.stopPropagation()}>
      {Array.from({ length: empty }, (_, i) => (
        <span key={i} aria-hidden className={cn('h-4 w-1.5 rounded-sm bg-muted', wideOnly(i))} />
      ))}
      {[...shown].reverse().map((r, i) => (
        <Link
          key={r.id}
          to={`/runs/${r.id}`}
          state={{ from: `/schedules/${r.schedule}` }}
          title={`${r.state}${r.skip_reason ? ` (${r.skip_reason})` : ''} · ${formatDate(r.scheduled_for)}`}
          className={cn('h-4 w-1.5 rounded-sm transition-opacity hover:opacity-70', TONE[r.state], wideOnly(empty + i))}
        />
      ))}
    </div>
  );
}
