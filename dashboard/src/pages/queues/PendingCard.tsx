import { StatCard } from '../../components/StatCard';
import { COUNT_LIMIT, formatCount } from '../../lib/format';

/**
 * Pending tasks, as in the Pending tab: due now plus scheduled for later
 * (including retries), split in the line below, as the task badges name them.
 */
export function PendingCard({ due, scheduled, to }: { due: number; scheduled: number; to?: string }) {
  const pending = Math.min(COUNT_LIMIT, due + scheduled);
  return (
    <StatCard
      label="Pending"
      value={formatCount(pending)}
      detail={`${formatCount(due)} now · ${formatCount(scheduled)} scheduled`}
      to={pending > 0 ? to : undefined}
      title="Tasks not run yet: due now, or scheduled for later (including ones waiting to retry)"
    />
  );
}
