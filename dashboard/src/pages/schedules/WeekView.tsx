import { useQueries } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { Link, useSearchParams } from 'react-router';
import { cn } from 'cn';
import { listRunsOptions, previewCronOptions } from '../../api/@tanstack/react-query.gen';
import type { CronSpan, Run, Schedule } from '../../api';
import { RelativeTime } from '../../components/RelativeTime';
import { useNow } from '../../hooks/useNow';
import { LIVE } from '../../lib/api';
import { describeCron } from '../../lib/cron';
import { formatSeconds } from '../../lib/format';
import { dayFraction, dayRuns, isToday, weekDays, weekStart, type DayRuns } from '../../lib/week';
import { Badge } from '../../primitives/badge';
import { Button } from '../../primitives/button';

/** How a past run went, as its tick's color. */
const RUN = {
  succeeded: 'bg-success',
  failed: 'bg-destructive',
  skipped: 'bg-warning-fill',
  running: 'bg-primary',
  pending: 'bg-muted-foreground/50',
  cancelled: 'bg-muted-foreground/50',
} as const;

// Ticks and bands still to come.
const PLANNED = 'bg-chart-1';
const PLANNED_BAND = 'bg-chart-1/45';
// A slot that passed with no run on record (before the schedule existed, or deleted by retention).
const PASSED = 'bg-muted-foreground/30';

const time = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' });
const weekday = new Intl.DateTimeFormat(undefined, { weekday: 'short' });
const dayMonth = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' });
const full = new Intl.DateTimeFormat(undefined, {
  weekday: 'short',
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
});

/**
 * Schedules on a week, Monday to Sunday, in the viewer's timezone: each row
 * a schedule, each cell when it runs that day. Slots still to come are blue;
 * ones that passed take the color of how their run went, so a week reads as
 * both a timetable and a history.
 */
export function WeekView({ schedules }: { schedules: Schedule[] }) {
  const [params, setParams] = useSearchParams();
  const offset = Number(params.get('week') ?? 0) || 0;
  const now = useNow(60_000);
  const start = weekStart(offset, new Date(now));
  const days = weekDays(start);
  const from = new Date(days[0].start).toISOString();
  const until = new Date(days[6].end).toISOString();
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;

  const setWeek = (week: number) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (week === 0) next.delete('week');
        else next.set('week', String(week));
        return next;
      },
      { replace: true },
    );

  // The timetable for the week, from the server, which is the authority on what a cron expression means.
  const previews = useQueries({
    queries: schedules.map((s) => ({
      ...previewCronOptions({ query: { cron: s.cron, timezone: s.timezone, from, until } }),
      staleTime: Infinity,
    })),
  });
  // What actually happened, to color the slots that passed. Only weeks that have started have any.
  const runs = useQueries({
    queries: schedules.map((s) => ({
      ...listRunsOptions({ query: { schedule: s.name, limit: 200 } }),
      ...LIVE,
      enabled: days[0].start <= now,
    })),
  });

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon" aria-label="Previous week" onClick={() => setWeek(offset - 1)}>
            <ChevronLeft />
          </Button>
          <Button variant="ghost" size="icon" aria-label="Next week" onClick={() => setWeek(offset + 1)}>
            <ChevronRight />
          </Button>
          <span className="ml-1 text-sm">
            {dayMonth.format(days[0].date)} – {dayMonth.format(days[6].date)}
          </span>
          {offset !== 0 && (
            <Button variant="ghost" size="sm" className="ml-1" onClick={() => setWeek(0)}>
              This week
            </Button>
          )}
        </div>
        <Legend zone={zone} />
      </div>
      {/* Wide on purpose: on a narrow screen the grid scrolls sideways inside its container, with the names pinned. */}
      <div className="overflow-x-auto rounded-lg border border-border">
        <div className="grid min-w-[60rem] grid-cols-[minmax(12rem,14rem)_repeat(7,minmax(6.5rem,1fr))]">
          <div className="sticky left-0 z-10 flex h-10 items-center border-b border-border bg-surface px-4 text-xs text-muted-foreground">
            Schedule
          </div>
          {days.map((d) => {
            const today = isToday(d.start, d.end, now);
            return (
              <div
                key={d.start}
                className={cn(
                  'flex h-10 items-center gap-1.5 border-b border-l border-border px-3 text-xs',
                  today ? 'bg-primary/5 text-foreground' : 'text-muted-foreground',
                )}
              >
                {weekday.format(d.date)}
                <span className={cn('font-mono tabular-nums', today ? 'text-primary' : 'text-faint')}>
                  {d.date.getDate()}
                </span>
                {today && <span className="size-1.5 rounded-full bg-primary" />}
              </div>
            );
          })}
          {schedules.map((s, i) => (
            <Row
              key={s.name}
              schedule={s}
              spans={previews[i].data?.spans}
              runs={runs[i].data?.runs}
              days={days}
              now={now}
              last={i === schedules.length - 1}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

function Legend({ zone }: { zone: string }) {
  const item = (color: string, label: string) => (
    <span className="flex items-center gap-1.5">
      <span className={cn('h-2.5 w-0.5 rounded-full', color)} />
      {label}
    </span>
  );
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
      {item(PLANNED, 'planned')}
      {item(RUN.succeeded, 'succeeded')}
      {item(RUN.failed, 'failed')}
      {item(RUN.skipped, 'skipped')}
      <span className="text-faint" title="Each schedule runs in its own timezone; the week is laid out in yours">
        Times in {zone}
      </span>
    </div>
  );
}

function Row({
  schedule: s,
  spans,
  runs,
  days,
  now,
  last,
}: {
  schedule: Schedule;
  spans?: CronSpan[];
  runs?: Run[];
  days: ReturnType<typeof weekDays>;
  now: number;
  last: boolean;
}) {
  const byTime = new Map(
    (runs ?? []).filter((r) => r.trigger === 'schedule').map((r) => [Date.parse(r.scheduled_for), r]),
  );
  const border = !last && 'border-b';
  return (
    <>
      <div
        className={cn(
          'sticky left-0 z-10 flex min-w-0 flex-col justify-center gap-0.5 border-border bg-surface px-4 py-2.5',
          border,
        )}
      >
        <div className="flex min-w-0 items-center gap-2">
          <Link
            to={`/schedules/${encodeURIComponent(s.name)}`}
            title={s.name}
            className="truncate font-medium hover:underline"
          >
            {s.name}
          </Link>
          {s.paused && <Badge variant="warning">paused</Badge>}
        </div>
        <span className="truncate text-xs text-muted-foreground" title={s.cron}>
          {describeCron(s.cron) ?? <span className="font-mono">{s.cron}</span>}
        </span>
        <span className="truncate text-xs text-faint">
          {s.next_run_at ? (
            <>
              next <RelativeTime iso={s.next_run_at} />
            </>
          ) : (
            'not running'
          )}
          <span className="font-mono"> · {s.timezone}</span>
        </span>
      </div>
      {days.map((d) => (
        <Cell
          key={d.start}
          day={d}
          runs={spans ? dayRuns(spans, d.start, d.end) : undefined}
          byTime={byTime}
          history={runs ?? []}
          now={now}
          paused={s.paused}
          className={cn('border-l border-border', border)}
        />
      ))}
    </>
  );
}

function Cell({
  day,
  runs,
  byTime,
  history,
  now,
  paused,
  className,
}: {
  day: { start: number; end: number };
  runs?: DayRuns;
  byTime: Map<number, Run>;
  history: Run[];
  now: number;
  paused: boolean;
  className?: string;
}) {
  const today = isToday(day.start, day.end, now);
  const at = (t: number) => `${dayFraction(t, day.start, day.end) * 100}%`;
  const past = (t: number) => t <= now;

  return (
    <div className={cn('flex flex-col justify-center gap-1.5 px-3 py-2.5', today && 'bg-primary/5', className)}>
      <div className="relative h-3.5">
        {/* The day as a line from midnight to midnight, marked at 6, 12 and 18. */}
        <div className="absolute inset-x-0 top-1/2 h-px bg-border" />
        {[0.25, 0.5, 0.75].map((f) => (
          <div
            key={f}
            className="absolute top-1/2 h-1.5 w-px -translate-y-1/2 bg-border"
            style={{ left: `${f * 100}%` }}
          />
        ))}
        {runs?.kind === 'bands' &&
          runs.bands.map((b) => {
            // The part that passed is grey; failures in it are drawn on top as ticks.
            const split = Math.min(Math.max(now, b.from), b.to);
            return (
              <div key={b.from}>
                {split > b.from && (
                  <div
                    className={cn(
                      'absolute top-1/2 h-1.5 -translate-y-1/2 rounded-l-full',
                      PASSED,
                      split >= b.to && 'rounded-r-full',
                    )}
                    style={{ left: at(b.from), width: `calc(${at(split)} - ${at(b.from)})` }}
                  />
                )}
                {split < b.to && (
                  <div
                    className={cn(
                      'absolute top-1/2 h-1.5 -translate-y-1/2 rounded-r-full',
                      paused ? 'bg-faint/40' : PLANNED_BAND,
                      split <= b.from && 'rounded-l-full',
                    )}
                    style={{ left: at(split), width: `calc(${at(b.to)} - ${at(split)})` }}
                  />
                )}
              </div>
            );
          })}
        {runs?.kind === 'bands' &&
          history
            .filter((r) => (r.state === 'failed' || r.state === 'skipped') && inDay(r, day))
            .map((r) => (
              <Tick
                key={r.id}
                left={at(Date.parse(r.scheduled_for))}
                color={RUN[r.state]}
                run={r}
                t={Date.parse(r.scheduled_for)}
              />
            ))}
        {runs?.kind === 'times' &&
          runs.times.map((t) => {
            const run = byTime.get(t);
            const color = past(t) ? (run ? RUN[run.state] : PASSED) : paused ? 'bg-faint/50' : PLANNED;
            return (
              <Tick
                key={t}
                left={at(t)}
                color={color}
                run={past(t) ? run : undefined}
                t={t}
                thin={runs.times.length > 8}
              />
            );
          })}
        {today && (
          <div
            aria-hidden
            className="absolute -top-1 -bottom-1 w-px bg-primary"
            style={{ left: at(now) }}
            title="Now"
          />
        )}
      </div>
      <Label runs={runs} />
    </div>
  );
}

const inDay = (r: Run, day: { start: number; end: number }) => {
  const t = Date.parse(r.scheduled_for);
  return t >= day.start && t < day.end;
};

function Tick({ left, color, run, t, thin }: { left: string; color: string; run?: Run; t: number; thin?: boolean }) {
  // Many in a day (hourly) are thin, a comb rather than a barcode.
  const className = cn('absolute top-0 h-3.5 -translate-x-1/2 rounded-full', thin ? 'w-px' : 'w-[3px]', color);
  const title = `${full.format(t)}${run ? ` · ${run.state}${run.skip_reason ? ` (${run.skip_reason})` : ''}` : ''}`;
  return run ? (
    <Link to={`/runs/${run.id}`} title={title} className={cn(className, 'hover:scale-y-125')} style={{ left }} />
  ) : (
    <span title={title} className={className} style={{ left }} />
  );
}

/**
 * Under the line, on at most two lines: the times when there are one or two,
 * otherwise how often (or how many) and, unless it's all day, from when to
 * when. Always two lines tall, so the day lines of a row stay level.
 */
function Label({ runs }: { runs?: DayRuns }) {
  let lines: [string, string?] = [''];
  // One or two runs: both lines are times, so both mono and equally bright.
  const times = runs?.kind === 'times' && runs.times.length <= 2;
  if (runs?.kind === 'none') lines = ['—'];
  else if (runs?.kind === 'times') {
    const ts = runs.times;
    if (ts.length <= 2) lines = [time.format(ts[0]), ts[1] === undefined ? undefined : time.format(ts[1])];
    else {
      const gaps = new Set(ts.slice(1).map((t, i) => t - ts[i]));
      const every = gaps.size === 1 ? [...gaps][0] : undefined;
      const band = { from: ts[0], to: ts.at(-1)!, every: every ?? 0 };
      lines = [
        every ? `every ${formatSeconds(every / 1000)}` : `${ts.length} runs`,
        every && coversDay(band) ? undefined : `${time.format(ts[0])}–${time.format(ts.at(-1)!)}`,
      ];
    }
  } else if (runs?.kind === 'bands') {
    const every = new Set(runs.bands.map((b) => b.every));
    const b = runs.bands[0];
    lines = [
      every.size === 1 ? `every ${formatSeconds([...every][0] / 1000)}` : `${runs.count.toLocaleString()} runs`,
      runs.bands.length === 1 && !coversDay(b) ? `${time.format(b.from)}–${time.format(b.to)}` : undefined,
    ];
  }
  return (
    <span className="flex h-8 min-w-0 flex-col text-xs leading-4" title={lines.filter(Boolean).join(', ')}>
      <span
        className={cn(
          'truncate',
          runs?.kind === 'none' ? 'text-faint' : 'text-muted-foreground',
          times && 'font-mono tabular-nums',
        )}
      >
        {lines[0]}
      </span>
      {lines[1] && (
        <span className={cn('truncate font-mono tabular-nums', times ? 'text-muted-foreground' : 'text-faint')}>
          {lines[1]}
        </span>
      )}
    </span>
  );
}

/** Whether a band runs from the first slot after midnight to the last before the next. */
function coversDay(b: { from: number; to: number; every: number }) {
  const from = new Date(b.from);
  const to = new Date(b.to + b.every);
  return (
    from.getHours() === 0 &&
    from.getMinutes() * 60_000 < b.every &&
    to.getHours() === 0 &&
    to.getMinutes() * 60_000 < b.every
  );
}
