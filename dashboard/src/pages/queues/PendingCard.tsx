import { StatCard } from '../../components/StatCard';
import { COUNT_LIMIT, formatCount } from '../../lib/format';

/**
 * Pending tasks, as in the Pending tab: due now plus scheduled for later
 * (including retries), split in the line below, as the task badges name them.
 */
export function PendingCard({
  due,
  scheduled,
  to,
  className,
}: {
  due: number;
  scheduled: number;
  to?: string;
  className?: string;
}) {
  const pending = Math.min(COUNT_LIMIT, due + scheduled);
  return (
    <StatCard
      className={className}
      label="Pending"
      value={formatCount(pending)}
      // Only the parts there are: "6 scheduled" says the backlog is all for
      // later without a "0 now" beside it, and keeps the line from wrapping.
      detail={
        [due > 0 && `${formatCount(due)} now`, scheduled > 0 && `${formatCount(scheduled)} scheduled`]
          .filter(Boolean)
          .join(' · ') || undefined
      }
      to={pending > 0 ? to : undefined}
      title="Tasks not run yet: due now, or scheduled for later (including ones waiting to retry)"
    />
  );
}
