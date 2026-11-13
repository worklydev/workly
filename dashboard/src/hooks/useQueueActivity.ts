import { useQueries } from '@tanstack/react-query';
import { getQueueActivityOptions } from '../api/@tanstack/react-query.gen';
import type { Queue, QueueActivity } from '../api';
import { useRangeSince } from './useRange';

/** Each queue's finished tasks over the page's range (useRange). */
export function useQueueActivity(queues: Queue[]): Map<string, QueueActivity> {
  const since = useRangeSince();
  const results = useQueries({
    queries: queues.map((q) => ({
      ...getQueueActivityOptions({ path: { queue: q.name }, query: { since } }),
      refetchInterval: 15_000,
      placeholderData: (previous: QueueActivity | undefined) => previous,
    })),
  });
  return new Map(queues.flatMap((q, i) => (results[i].data ? [[q.name, results[i].data]] : [])));
}
