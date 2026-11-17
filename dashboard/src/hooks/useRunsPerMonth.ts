import { useQuery } from '@tanstack/react-query';
import { previewCronOptions } from '../api/@tanstack/react-query.gen';
import { useNow } from './useNow';

const HOUR = 3_600_000;
const MONTH = 30 * 24 * HOUR;

/**
 * How many times a timetable runs in the next 30 days, from the server's
 * cron parser, so a schedule's cost is plain before it adds up. Undefined
 * until it's known or for an expression the server refuses.
 */
export function useRunsPerMonth(cron: string, timezone: string): number | undefined {
  // From the hour, so the window, and with it the query, is stable for an hour.
  const from = Math.floor(useNow(HOUR) / HOUR) * HOUR;
  const { data } = useQuery({
    ...previewCronOptions({
      query: {
        cron,
        timezone: timezone || 'UTC',
        from: new Date(from).toISOString(),
        until: new Date(from + MONTH).toISOString(),
      },
    }),
    enabled: cron !== '',
    staleTime: Infinity,
    retry: false,
  });
  return data?.spans?.reduce((n, s) => n + s.count, 0);
}

/** "43,200 runs a month", "1 run a month". */
export function formatRunsPerMonth(n: number): string {
  return `${n.toLocaleString()} ${n === 1 ? 'run' : 'runs'} a month`;
}
