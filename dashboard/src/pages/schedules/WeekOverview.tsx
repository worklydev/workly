import { useQueries } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router';
import { cn } from 'cn';
import { previewCronOptions } from '../../api/@tanstack/react-query.gen';
import type { CronSpan, Schedule } from '../../api';
import { RelativeTime } from '../../components/RelativeTime';
import { RowHint } from '../../components/RowHint';
import { useNow } from '../../hooks/useNow';
import { describeCron } from '../../lib/cron';
import { formatSeconds } from '../../lib/format';
import { dayPlan, zonedWeek, type DayPlan } from '../../lib/week';
import { ScheduleDot } from './ScheduleDot';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';

const DAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

// Hairlines between columns, so it reads as a timetable; today's column a whisper of the accent.
const GRID = 'border-l border-border';
const TODAY = 'bg-primary/[0.035]';

/**
 * Every schedule's week at a glance, Monday to Sunday: which days it runs and
 * when, in its own timezone, as its cron expression says. Schedules that go
 * by the date (monthly, yearly) don't have a weekly shape, so their row says
 * when they next run instead.
 */
export function WeekOverview({ schedules }: { schedules: Schedule[] }) {
  const navigate = useNavigate();
  // The hour is enough: it only decides which week the timetable is read from.
  const now = useNow(3_600_000);
  const today = (new Date(now).getDay() + 6) % 7;
  const weeks = schedules.map((s) => zonedWeek(s.timezone, now));
  // The server is the authority on when an expression fires, daylight saving included.
  const previews = useQueries({
    queries: schedules.map((s, i) => ({
      ...previewCronOptions({
        query: {
          cron: s.cron,
          timezone: s.timezone,
          from: new Date(weeks[i][0].start).toISOString(),
          until: new Date(weeks[i][6].end).toISOString(),
        },
      }),
      staleTime: Infinity,
    })),
  });

  return (
    // Fixed columns: the days share what's left equally and their text wraps, so a week fits the page.
    <Table className="min-w-[56rem] table-fixed">
      <TableHeader>
        <TableRow>
          <TableHead className="w-[17%]">Schedule</TableHead>
          <TableHead className={cn('w-[20%]', GRID)} title="Times are in each schedule's own timezone">
            When
          </TableHead>
          {DAYS.map((d, i) => (
            <TableHead key={d} className={cn(GRID, i === today && TODAY, i === today && 'text-foreground')}>
              <span className="flex items-center gap-1.5">
                {d}
                {i === today && <span className="size-1.5 rounded-full bg-primary" title="Today" />}
              </span>
            </TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {schedules.map((s, i) => (
          <TableRow
            key={s.name}
            className={cn('cursor-pointer', s.paused && '[&_td]:text-faint')}
            onClick={() => navigate(`/schedules/${s.name}`)}
          >
            <TableCell className="py-2">
              <div className="flex items-center gap-2.5">
                <ScheduleDot schedule={s} />
                <Link
                  to={`/schedules/${s.name}`}
                  title={s.name}
                  className="min-w-0 truncate font-medium"
                  onClick={(e) => e.stopPropagation()}
                >
                  {s.name}
                </Link>
                <RowHint />
              </div>
              {/* Under the name, not the dot, so the names line up. */}
              <div className="truncate pl-[1.125rem] text-xs text-faint">
                {s.next_run_at ? (
                  <>
                    next <RelativeTime iso={s.next_run_at} />
                  </>
                ) : (
                  'not running'
                )}
              </div>
            </TableCell>
            <TableCell className={cn('py-2', GRID)}>
              <div className="truncate font-mono text-xs" title={s.cron}>
                {s.cron}
              </div>
              <div className="truncate text-xs text-faint" title={`${describeCron(s.cron) ?? s.cron} · ${s.timezone}`}>
                {describeCron(s.cron) ?? 'Custom timetable'} · <span className="font-mono">{s.timezone}</span>
              </div>
            </TableCell>
            {byDate(s.cron) ? (
              <TableCell colSpan={7} className={cn('text-xs text-muted-foreground', GRID)}>
                Runs by date, not weekday
                {s.next_run_at && <span className="text-faint"> · next {zonedDate(s.next_run_at, s.timezone)}</span>}
              </TableCell>
            ) : (
              weeks[i].map((day, d) => (
                <TableCell key={d} className={cn('align-middle', GRID, d === today && TODAY)}>
                  <Day plan={dayFrom(previews[i].data?.spans, day)} zone={s.timezone} />
                </TableCell>
              ))
            )}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

const dayFrom = (spans: CronSpan[] | undefined, day: { start: number; end: number }) =>
  spans && dayPlan(spans, day.start, day.end);

/**
 * Whether the expression goes by the day of the month or the month, so a
 * week isn't its shape. Only reads the fields; when it runs is the server's.
 */
function byDate(cron: string): boolean {
  const expr = cron.trim().toLowerCase();
  if (expr.startsWith('@')) return ['@monthly', '@yearly', '@annually'].includes(expr);
  const fields = expr.split(/\s+/);
  return fields.length === 5 && (fields[2] !== '*' || fields[3] !== '*');
}

const formats = new Map<string, Intl.DateTimeFormat>();

/** A time of day in the schedule's timezone: "03:30" or "3:30 AM", as the viewer's locale writes it. */
function clock(t: number, zone: string): string {
  let f = formats.get(zone);
  if (!f)
    formats.set(zone, (f = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit', timeZone: zone })));
  return f.format(t);
}

function zonedDate(iso: string, zone: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: zone,
  }).format(new Date(iso));
}

/** One day: the times when there are a few, otherwise how often and, unless it's all day, between when. */
function Day({ plan, zone }: { plan?: DayPlan; zone: string }) {
  if (!plan) return <span className="text-xs text-faint">…</span>;
  switch (plan.kind) {
    case 'none':
      return <span className="text-xs text-faint">—</span>;
    case 'times':
      return (
        <span className="flex flex-col font-mono text-xs leading-4 tabular-nums">
          {plan.times.map((t) => (
            <span key={t}>{clock(t, zone)}</span>
          ))}
        </span>
      );
    case 'every':
      return (
        <span className="flex flex-col text-xs leading-4">
          <span className="whitespace-nowrap">every {formatSeconds(plan.every / 1000)}</span>
          {!plan.allDay && (
            <span className="font-mono text-faint tabular-nums">
              <span className="whitespace-nowrap">{clock(plan.from, zone)}–</span>
              <wbr />
              <span className="whitespace-nowrap">{clock(plan.to, zone)}</span>
            </span>
          )}
        </span>
      );
    case 'many':
      return <span className="text-xs whitespace-nowrap">{plan.count} runs</span>;
  }
}
