import { useState } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { RowHint } from '../../components/RowHint';
import { Link, useNavigate } from 'react-router';
import { listRunsOptions, listSchedulesOptions } from '../../api/@tanstack/react-query.gen';
import type { Run, Schedule } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { useSlots } from '../../context';
import { ListPager, ListSearch } from '../../components/ListControls';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { TaskStateBadge } from '../../components/states';
import { useListPage } from '../../hooks/useListPage';
import { LIVE } from '../../lib/api';
import { describeCron } from '../../lib/cron';
import { formatTarget, sharedHost } from '../../lib/format';
import { Badge } from '../../primitives/badge';
import { Button } from '../../primitives/button';
import { EditSchedule } from './EditSchedule';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { RecentRuns } from './RecentRuns';

export function SchedulesPage() {
  const { data, error, isPending } = useQuery({
    ...listSchedulesOptions(),
    ...LIVE,
  });
  const schedules = data?.schedules ?? [];
  const [creating, setCreating] = useState(false);

  return (
    <>
      <PageHeader
        title="Schedules"
        description="Cron for HTTP endpoints. Each call is a run, retried and recorded like a task, separately from queues."
      >
        <Button onClick={() => setCreating(true)}>
          <Plus />
          New schedule
        </Button>
      </PageHeader>
      <EditSchedule open={creating} onClose={() => setCreating(false)} />
      {isPending ? (
        <Muted>Loading…</Muted>
      ) : error ? (
        <ErrorText error={error} />
      ) : schedules.length === 0 ? (
        <SchedulesGettingStarted />
      ) : (
        <ScheduleList schedules={schedules} />
      )}
    </>
  );
}

/** How many recent runs the overview shows per schedule. */
const RECENT = 12;

function useRecentRuns(schedules: Schedule[]): Map<string, Run[]> {
  const results = useQueries({
    queries: schedules.map((s) => ({
      ...listRunsOptions({
        path: { schedule: s.name },
        query: { limit: RECENT },
      }),
      ...LIVE,
    })),
  });
  return new Map(schedules.flatMap((s, i) => (results[i].data ? [[s.name, results[i].data.runs]] : [])));
}

function ScheduleList({ schedules }: { schedules: Schedule[] }) {
  const list = useListPage(schedules, (s) => [s.name, s.cron, describeCron(s.cron) ?? '', s.timezone, s.method, s.url]);
  return (
    <div className="flex flex-col gap-3">
      <ListSearch list={list} placeholder="Search name, timetable or URL" />
      {list.items.length === 0 ? (
        <Muted>No schedules match “{list.search}”.</Muted>
      ) : (
        <ScheduleTable schedules={list.items} />
      )}
      <ListPager list={list} noun="schedules" />
    </div>
  );
}

/** Only the schedules shown get their recent runs fetched. */
// On a phone the row keeps what tells you whether a schedule is healthy (its
// last run and recent runs); when it runs moves under the name, in words.
const WIDE = 'hidden sm:table-cell';

function ScheduleTable({ schedules }: { schedules: Schedule[] }) {
  const navigate = useNavigate();
  const recent = useRecentRuns(schedules);
  // When every schedule calls the same host, the path is what differs.
  const host = sharedHost(schedules.map((s) => s.url));
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Schedule</TableHead>
          <TableHead className={WIDE}>When</TableHead>
          <TableHead className={WIDE}>Next run</TableHead>
          <TableHead>Last run</TableHead>
          <TableHead title="The latest runs, oldest first">Recent</TableHead>
          <TableHead className="hidden lg:table-cell">Target</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {schedules.map((s) => (
          <TableRow key={s.name} className="cursor-pointer" onClick={() => navigate(`/schedules/${s.name}`)}>
            {/* Two-line cells get room above and below, so the row doesn't look squeezed. */}
            <TableCell className="py-2">
              <div className="flex items-center gap-2">
                <Link to={`/schedules/${s.name}`} className="font-medium" onClick={(e) => e.stopPropagation()}>
                  {s.name}
                </Link>
                {s.paused && <Badge variant="warning">paused</Badge>}
                <RowHint />
              </div>
              <div className="text-xs text-faint sm:hidden">{describeCron(s.cron) ?? s.cron}</div>
            </TableCell>
            <TableCell className={`py-2 ${WIDE}`}>
              <When schedule={s} />
            </TableCell>
            <TableCell className={`text-xs text-muted-foreground ${WIDE}`}>
              {s.next_run_at ? <RelativeTime iso={s.next_run_at} /> : <span className="text-warning">paused</span>}
            </TableCell>
            <TableCell>
              {s.last_run ? (
                <span className="flex items-center gap-2 text-xs text-muted-foreground">
                  <TaskStateBadge state={s.last_run.state} />
                  <RelativeTime iso={s.last_run.scheduled_for} />
                </span>
              ) : (
                <span className="text-xs text-faint">never</span>
              )}
            </TableCell>
            <TableCell>
              <RecentRuns runs={recent.get(s.name)} phoneSlots={8} />
            </TableCell>
            <TableCell className="hidden max-w-56 truncate font-mono text-xs text-faint lg:table-cell" title={s.url}>
              {formatTarget(s.method, s.url, host)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

/** The cron expression in words, with the expression and timezone underneath. */
export function When({ schedule: s }: { schedule: Schedule }) {
  const words = describeCron(s.cron);
  return (
    <div className="flex flex-col gap-0.5">
      <span className={words ? undefined : 'font-mono text-xs'}>{words ?? s.cron}</span>
      <span className="font-mono text-xs text-faint">
        {words && `${s.cron} · `}
        {s.timezone}
      </span>
    </div>
  );
}

/** A wrapping app's own first steps, or the `workly dev` example. */
function SchedulesGettingStarted() {
  const custom = useSlots().gettingStarted?.schedules;
  return custom ?? <GettingStarted />;
}

function GettingStarted() {
  const origin = window.location.origin;
  return (
    <div className="flex max-w-2xl flex-col gap-3">
      <p className="text-muted-foreground">
        No schedules yet. A schedule calls a URL on a cron timetable, with retries and a history of every run. With{' '}
        <span className="font-mono text-xs">workly dev</span>, the built-in echo endpoint makes a handy target:
      </p>
      <CodePanel title="Terminal">
        {`curl -X PUT ${origin}/v1/schedules/hello \\
  -H 'Content-Type: application/json' \\
  -d '{"cron": "* * * * *", "url": "${origin}/_dev/echo", "payload": {"hello": "world"}}'`}
      </CodePanel>
    </div>
  );
}
