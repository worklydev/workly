import { formatDate } from '../lib/format';
import { useRelativeTime } from '../hooks/useRelativeTime';

/** "3m ago" or "in 5m" that stays current; the exact date on hover. */
export function RelativeTime({ iso }: { iso: string }) {
  return (
    <time dateTime={iso} title={formatDate(iso)}>
      {useRelativeTime(iso)}
    </time>
  );
}
