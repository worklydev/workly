import { cn } from 'cn';
import { COUNT_LIMIT, formatCount } from '../lib/format';

/**
 * A count in a table cell. Zeros recede to faint, so the rows with something
 * in them stand out; `danger` turns a non-zero count red (failed tasks).
 */
export function Count({ n, tone, className }: { n: number; tone?: 'danger'; className?: string }) {
  return (
    <span className={cn(n === 0 ? 'text-faint' : tone === 'danger' && 'text-destructive', className)}>
      {formatCount(Math.min(COUNT_LIMIT, n))}
    </span>
  );
}
