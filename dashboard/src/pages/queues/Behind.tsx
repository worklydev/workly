import { useNow } from '../../hooks/useNow';
import { formatDuration } from '../../lib/format';

/** How long the oldest due task has waited; yellow past a minute. */
export function Behind({ since }: { since?: string }) {
  const now = useNow();
  if (!since) return <span className="text-faint">—</span>;
  const ms = now - new Date(since).getTime();
  return <span className={ms > 60_000 ? 'text-warning' : undefined}>{formatDuration(Math.max(0, ms))}</span>;
}
