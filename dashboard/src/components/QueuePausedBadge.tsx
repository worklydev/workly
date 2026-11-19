import type { Queue } from '../api';
import { Badge } from '../primitives/badge';

/** Paused by hand, or paused by itself after failures in a row (auto_pause). */
export function QueuePausedBadge({ queue }: { queue: Pick<Queue, 'paused' | 'auto_paused'> }) {
  if (queue.paused) return <Badge variant="warning">paused</Badge>;
  if (queue.auto_paused) {
    return (
      <Badge variant="warning" title={`Paused itself after failures in a row: ${queue.auto_paused.error}`}>
        auto-paused
      </Badge>
    );
  }
  return null;
}
