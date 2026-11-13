import { CircleCheck } from 'lucide-react';
import { Link } from 'react-router';
import { EmptyState, RowsSkeleton } from '../../components/EmptyState';
import { ErrorText } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { Section } from '../../components/Section';
import { lastFailed, useQueueFailureGroups, useRunFailureGroups } from '../../hooks/useFailureGroups';
import { formatCount } from '../../lib/format';

/** How many failure groups the card lists; the rest are a link away. */
const SHOWN = 5;

type Row = {
  key: string;
  n: number;
  count: string;
  name: string;
  kind: 'queue' | 'schedule';
  error: string;
  at: string;
  to: string;
};

/**
 * The biggest failures across queues and schedules, grouped by error as on
 * the Failures page: what's broken, and how badly, without leaving home.
 */
export function NeedsAttention() {
  const queues = useQueueFailureGroups();
  const runs = useRunFailureGroups();
  const rows: Row[] = [
    ...queues.groups.map((g) => ({
      key: `q\n${g.queue}\n${g.error}`,
      n: g.count,
      count: formatCount(g.count),
      name: g.queue,
      kind: 'queue' as const,
      error: g.error,
      at: g.last_failed_at,
      to: `/queues/${encodeURIComponent(g.queue)}?${new URLSearchParams({ state: 'failed', last_error: g.error })}`,
    })),
    ...runs.groups.map((g) => ({
      key: `s\n${g.schedule}\n${g.error}`,
      n: g.count,
      count: formatCount(g.count) + (g.more ? '+' : ''),
      name: g.schedule,
      kind: 'schedule' as const,
      error: g.error,
      at: new Date(lastFailed(g)).toISOString(),
      to: `/schedules/${encodeURIComponent(g.schedule)}?state=failed`,
    })),
  ].sort((a, b) => b.n - a.n || Date.parse(b.at) - Date.parse(a.at));
  const error = queues.error ?? runs.runs.error;
  const pending = queues.isPending || runs.runs.isPending;
  const more = rows.length - SHOWN;

  return (
    <Section title="Needs attention" link={{ to: '/failures', label: 'Failures' }}>
      {error ? (
        <ErrorText error={error} />
      ) : pending ? (
        <RowsSkeleton />
      ) : rows.length === 0 ? (
        <EmptyState
          icon={CircleCheck}
          tone="success"
          title="Nothing failing"
          hint="Failed tasks and schedule runs show up here, grouped by the error they ended with."
        />
      ) : (
        <div className="flex flex-col">
          {rows.slice(0, SHOWN).map((r) => (
            <Link
              key={r.key}
              to={r.to}
              title={r.error}
              className="group/row flex h-10 items-center gap-3 border-b border-border px-1 text-sm transition-colors hover:bg-muted/60"
            >
              <span className="w-10 shrink-0 text-right font-mono text-xs text-destructive tabular-nums">
                {r.count}
              </span>
              <span className="max-w-36 shrink-0 truncate font-mono text-xs" title={`${r.kind} ${r.name}`}>
                {r.name}
              </span>
              <span className="min-w-0 grow truncate text-muted-foreground">
                {r.error || <span className="text-faint">No error recorded</span>}
              </span>
              <span className="shrink-0 text-xs text-faint">
                <RelativeTime iso={r.at} />
              </span>
            </Link>
          ))}
          {more > 0 && (
            <Link to="/failures" className="px-1 pt-2 text-xs text-faint hover:text-foreground">
              and {more} more {more === 1 ? 'error' : 'errors'}
            </Link>
          )}
        </div>
      )}
    </Section>
  );
}
