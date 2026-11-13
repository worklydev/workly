import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getActivityOptions } from '../api/@tanstack/react-query.gen';
import { toSeries } from '../lib/activity';
import { useRange } from './useRange';

/** The workspace's tasks created over the page's range, shared by the chart and the queue sparklines. */
export function useActivity() {
  const { value: range } = useRange();
  const query = useQuery({ ...getActivityOptions({ query: { range } }), refetchInterval: 15_000 });
  const series = useMemo(() => (query.data ? toSeries(query.data.buckets) : undefined), [query.data]);
  return { ...query, series };
}
