import type { Group } from '../../api';
import { finishedCount } from '../../lib/groups';

/**
 * A bar of a group's tasks: succeeded, failed and cancelled segments, the
 * rest still to finish, with finished/total beside it.
 */
export function GroupProgress({ group: g, className }: { group: Group; className?: string }) {
  const pct = (n: number) => (g.total === 0 ? 0 : (n / g.total) * 100);
  const title = `${g.succeeded} succeeded · ${g.failed} failed · ${g.cancelled} cancelled · ${g.remaining} remaining`;
  return (
    <div className={className} title={title}>
      <div className="flex items-center gap-2">
        <div className="flex h-1.5 w-28 overflow-hidden rounded-full bg-muted">
          <div className="bg-success" style={{ width: `${pct(g.succeeded)}%` }} />
          <div className="bg-destructive" style={{ width: `${pct(g.failed)}%` }} />
          <div className="bg-muted-foreground/40" style={{ width: `${pct(g.cancelled)}%` }} />
        </div>
        <span className="font-mono text-xs text-muted-foreground tabular-nums">
          {finishedCount(g)}/{g.total}
        </span>
      </div>
    </div>
  );
}
