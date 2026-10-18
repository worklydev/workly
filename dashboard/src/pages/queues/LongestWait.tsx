import { useNow } from '../../hooks/useNow';
import { formatDuration } from '../../lib/format';

/** What the longest wait is and why it matters, for tooltips. */
export const LONGEST_WAIT_TITLE =
  "How long the oldest pending task has waited past its run time. It grows when the queue can't keep up: rate or concurrency limits, a paused queue, or a slow endpoint.";

/** How long the oldest due task has waited to start; yellow past a minute. */
export function LongestWait({ since }: { since?: string }) {
  const now = useNow();
  if (!since) return <span className="text-faint">—</span>;
  const ms = now - new Date(since).getTime();
  return <span className={ms > 60_000 ? 'text-warning' : undefined}>{formatDuration(Math.max(0, ms))}</span>;
}
