import { Link } from 'react-router';
import { cn } from 'cn';
import type { Run } from '../../api';
import { formatDate } from '../../lib/format';

const TONE: Record<Run['state'], string> = {
  succeeded: 'bg-success',
  failed: 'bg-destructive',
  skipped: 'bg-warning',
  running: 'bg-primary',
  pending: 'bg-muted-foreground/40',
  cancelled: 'bg-muted-foreground/40',
};

/** The latest runs as a strip of small bars, oldest first; each links to its run. */
export function RecentRuns({ runs }: { runs?: Run[] }) {
  if (runs === undefined) return <span className="text-xs text-faint">…</span>;
  if (runs.length === 0) return <span className="text-xs text-faint">no runs yet</span>;
  return (
    <div className="flex items-center gap-0.5" onClick={(e) => e.stopPropagation()}>
      {[...runs].reverse().map((r) => (
        <Link
          key={r.id}
          to={`/runs/${r.id}`}
          state={{ from: `/schedules/${r.schedule}` }}
          title={`${r.state}${r.skip_reason ? ` (${r.skip_reason})` : ''} · ${formatDate(r.scheduled_for)}`}
          className={cn('h-4 w-1.5 rounded-sm transition-opacity hover:opacity-70', TONE[r.state])}
        />
      ))}
    </div>
  );
}
