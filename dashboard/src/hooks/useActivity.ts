import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getActivityOptions } from '../api/@tanstack/react-query.gen';
import { toSeries } from '../lib/activity';
import { useRange } from './useRange';

/**
 * The workspace's tasks created and finished over the page's range, shared by
 * the charts and stats that count over it. `outcomes` is false against a
 * server from before buckets carried succeeded and failed (a wrapping app may
 * run an older one): those counts are then 0 here, and callers say they're
 * unknown rather than show them.
 */
export function useActivity() {
  const { value: range } = useRange();
  const query = useQuery({ ...getActivityOptions({ query: { range } }), refetchInterval: 15_000 });
  const raw = query.data;
  const outcomes = raw ? raw.buckets.every((b) => typeof b.succeeded === 'number') : undefined;
  const data = useMemo(
    () =>
      raw && {
        ...raw,
        buckets: raw.buckets.map((b) => ({ ...b, succeeded: b.succeeded ?? 0, failed: b.failed ?? 0 })),
      },
    [raw],
  );
  const series = useMemo(() => (raw ? toSeries(raw.buckets) : undefined), [raw]);
  return { ...query, data, series, outcomes };
}
