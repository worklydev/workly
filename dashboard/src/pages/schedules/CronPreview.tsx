import { useEffect, useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { previewCronOptions } from '../../api/@tanstack/react-query.gen';
import { errorMessage } from '../../lib/api';
import { formatSeconds } from '../../lib/format';

const when = new Intl.DateTimeFormat(undefined, {
  weekday: 'short',
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
});

/**
 * The next runs of a timetable as it's typed, from the server's own cron
 * parser, so timezones and DST match what the schedule will do.
 */
export function CronPreview({ cron, timezone }: { cron: string; timezone: string }) {
  const [settled, setSettled] = useState({ cron, timezone });
  useEffect(() => {
    const t = setTimeout(() => setSettled({ cron: cron.trim(), timezone: timezone.trim() || 'UTC' }), 300);
    return () => clearTimeout(t);
  }, [cron, timezone]);

  const { data, error } = useQuery({
    ...previewCronOptions({ query: settled }),
    enabled: settled.cron !== '',
    placeholderData: keepPreviousData,
    retry: false,
  });

  if (settled.cron === '')
    return <span className="text-xs text-faint">Type a cron expression to see its next runs.</span>;
  if (error) return <span className="text-xs text-destructive">{errorMessage(error)}</span>;
  if (!data) return null;
  return (
    <span className="flex flex-col gap-0.5 text-xs text-muted-foreground">
      {data.next_runs.map((t) => (
        <span key={t} className="font-mono">
          {when.format(new Date(t))}
        </span>
      ))}
      <span className="text-faint">In your browser's time. At most every {formatSeconds(data.interval_seconds)}.</span>
    </span>
  );
}
