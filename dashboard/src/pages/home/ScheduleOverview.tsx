import { useState } from 'react';
import { CalendarClock, Plus } from 'lucide-react';
import { Link } from 'react-router';
import { cn } from 'cn';
import type { Schedule } from '../../api';
import { EmptyState } from '../../components/EmptyState';
import { Section } from '../../components/Section';
import { useRelativeTime } from '../../hooks/useRelativeTime';
import { describeCron } from '../../lib/cron';
import { formatDate } from '../../lib/format';
import { Button } from '../../primitives/button';
import { EditSchedule } from '../schedules/EditSchedule';
import { RecentRuns } from '../schedules/RecentRuns';
import { useRecentRuns } from '../schedules/useRecentRuns';

const SHOWN = 5;

/** The last run's state, as a dot on the timeline. */
const LAST = {
  succeeded: 'bg-success',
  failed: 'bg-destructive',
  skipped: 'bg-warning-fill',
  running: 'bg-primary',
  pending: 'bg-muted-foreground/50',
  cancelled: 'bg-muted-foreground/50',
} as const;

const next = (s: Schedule) => (s.next_run_at ? Date.parse(s.next_run_at) : Infinity);

/**
 * Schedules as a timeline: soonest next run first, each with a dot for how
 * its last run went and a strip of the ones before. Paused ones go last.
 */
export function ScheduleOverview({ schedules }: { schedules: Schedule[] }) {
  const [creating, setCreating] = useState(false);
  const top = [...schedules].sort((a, b) => next(a) - next(b) || a.name.localeCompare(b.name)).slice(0, SHOWN);
  const recent = useRecentRuns(top);

  return (
    <Section
      title="Schedules"
      link={{ to: '/schedules', label: schedules.length > SHOWN ? `All ${schedules.length} schedules` : 'Schedules' }}
    >
      <EditSchedule open={creating} onClose={() => setCreating(false)} />
      {schedules.length === 0 ? (
        <EmptyState
          icon={CalendarClock}
          title="No schedules yet"
          hint="A schedule calls a URL on a cron timetable, with retries and a history of every run."
          action={
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus />
              New schedule
            </Button>
          }
        />
      ) : (
        <ol className="flex flex-col">
          {top.map((s, i) => (
            <ScheduleRow
              key={s.name}
              schedule={s}
              runs={recent.get(s.name)}
              first={i === 0}
              last={i === top.length - 1}
            />
          ))}
        </ol>
      )}
    </Section>
  );
}

function ScheduleRow({
  schedule: s,
  runs,
  first,
  last,
}: {
  schedule: Schedule;
  runs: Parameters<typeof RecentRuns>[0]['runs'];
  first: boolean;
  last: boolean;
}) {
  return (
    <li>
      <Link
        to={`/schedules/${encodeURIComponent(s.name)}`}
        className="group/row flex h-10 items-center gap-3 rounded-md px-1 transition-colors hover:bg-muted/60"
      >
        <span className="w-14 shrink-0 text-right font-mono text-xs tabular-nums">
          {s.next_run_at ? (
            <NextRun iso={s.next_run_at} accent={first} />
          ) : (
            <span className="font-sans text-warning">paused</span>
          )}
        </span>
        {/* The timeline: a rail through every row, each dot how that schedule's last run went. */}
        <span className="relative flex h-full w-3 shrink-0 items-center justify-center">
          <span
            className={cn('absolute w-px bg-border', first ? 'top-1/2' : 'top-0', last ? 'bottom-1/2' : 'bottom-0')}
          />
          <span
            title={s.last_run ? `Last run ${s.last_run.state}` : 'No runs yet'}
            className={cn(
              'relative size-2 rounded-full',
              s.last_run ? LAST[s.last_run.state] : 'border border-input bg-surface',
            )}
          />
        </span>
        <span className="min-w-0 grow truncate">
          <span className="font-mono text-xs">{s.name}</span>
          <span className="text-xs text-faint" title={`${s.cron} · ${s.timezone}`}>
            {' '}
            · {describeCron(s.cron) ?? s.cron}
          </span>
        </span>
        <span className="hidden shrink-0 sm:block">
          <RecentRuns runs={runs} slots={8} />
        </span>
      </Link>
    </li>
  );
}

function NextRun({ iso, accent }: { iso: string; accent: boolean }) {
  return (
    <time
      dateTime={iso}
      title={formatDate(iso, { seconds: false })}
      className={accent ? 'text-primary' : 'text-muted-foreground'}
    >
      {useRelativeTime(iso)}
    </time>
  );
}
