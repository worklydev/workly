import type { Queue } from '../../api';
import { StatCard, StatStrip } from '../../components/StatCard';
import { useSetting } from '../../context';
import { useNow } from '../../hooks/useNow';
import { COUNT_LIMIT, formatCount, formatDuration } from '../../lib/format';
import { LONGEST_WAIT_TITLE } from './LongestWait';
import { PendingCard } from './PendingCard';

/** Every queue's current work, summed: what's waiting, running and failed right now. */
export function Totals({ queues }: { queues: Queue[] }) {
  const sum = (key: 'due' | 'scheduled' | 'running' | 'failed') =>
    Math.min(
      COUNT_LIMIT,
      queues.reduce((n, q) => n + (q.stats?.[key] ?? 0), 0),
    );
  const oldest = queues
    .map((q) => q.stats?.oldest_due_at)
    .filter((t): t is string => t !== undefined)
    .sort()[0];
  const now = useNow();
  const setting = useSetting();
  const behind = oldest ? Math.max(0, now - new Date(oldest).getTime()) : 0;

  const failed = sum('failed');

  return (
    <StatStrip className="grid-cols-2 md:grid-cols-4">
      <PendingCard due={sum('due')} scheduled={sum('scheduled')} to="/tasks?state=pending" />
      <StatCard label="Running" value={formatCount(sum('running'))} />
      <StatCard
        label="Longest wait"
        value={oldest ? formatDuration(behind) : '—'}
        detail={oldest ? 'oldest task past its run time' : 'nothing waiting'}
        tone={behind > 60_000 ? 'warning' : undefined}
        title={LONGEST_WAIT_TITLE}
      />
      <StatCard
        label="Failed"
        value={formatCount(failed)}
        tone={failed > 0 ? 'danger' : undefined}
        to={failed > 0 ? '/tasks?state=failed' : undefined}
        title={`Failed tasks still kept${setting('WORKLY_RETENTION')}`}
      />
    </StatStrip>
  );
}
