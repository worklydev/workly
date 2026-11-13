import { useQueries } from '@tanstack/react-query';
import { listRunsOptions } from '../../api/@tanstack/react-query.gen';
import type { Run, Schedule } from '../../api';
import { LIVE } from '../../lib/api';

/** How many recent runs the overview shows per schedule. */
export const RECENT = 12;

export function useRecentRuns(schedules: Schedule[]): Map<string, Run[]> {
  const results = useQueries({
    queries: schedules.map((s) => ({
      ...listRunsOptions({ query: { schedule: s.name, limit: RECENT } }),
      ...LIVE,
    })),
  });
  return new Map(schedules.flatMap((s, i) => (results[i].data ? [[s.name, results[i].data.runs]] : [])));
}
