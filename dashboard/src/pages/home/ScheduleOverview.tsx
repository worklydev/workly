import { Link, useNavigate } from 'react-router';
import type { Schedule } from '../../api';
import { RelativeTime } from '../../components/RelativeTime';
import { RowHint } from '../../components/RowHint';
import { Section } from '../../components/Section';
import { TaskStateBadge } from '../../components/states';
import { describeCron } from '../../lib/cron';
import { Badge } from '../../primitives/badge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { RecentRuns } from '../schedules/RecentRuns';
import { useRecentRuns } from '../schedules/useRecentRuns';

const SHOWN = 5;

/** Failing schedules first, then the ones about to run; paused ones last. */
function rank(a: Schedule, b: Schedule): number {
  const failing = (s: Schedule) => (s.last_run?.state === 'failed' ? 0 : 1);
  const next = (s: Schedule) => (s.next_run_at ? Date.parse(s.next_run_at) : Infinity);
  return failing(a) - failing(b) || next(a) - next(b) || a.name.localeCompare(b.name);
}

export function ScheduleOverview({ schedules }: { schedules: Schedule[] }) {
  const navigate = useNavigate();
  const top = [...schedules].sort(rank).slice(0, SHOWN);
  const recent = useRecentRuns(top);

  return (
    <Section
      title="Schedules"
      link={{ to: '/schedules', label: schedules.length > SHOWN ? `All ${schedules.length} schedules` : 'Schedules' }}
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Schedule</TableHead>
            <TableHead>Last run</TableHead>
            <TableHead title="The latest runs, oldest first">Recent</TableHead>
            <TableHead className="hidden sm:table-cell">Next run</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {top.map((s) => (
            <TableRow key={s.name} className="cursor-pointer" onClick={() => navigate(`/schedules/${s.name}`)}>
              <TableCell className="w-full max-w-0 min-w-36 py-2">
                <div className="flex items-center gap-2">
                  <Link
                    to={`/schedules/${s.name}`}
                    className="truncate font-medium"
                    title={s.name}
                    onClick={(e) => e.stopPropagation()}
                  >
                    {s.name}
                  </Link>
                  {s.paused && <Badge variant="warning">paused</Badge>}
                  <RowHint />
                </div>
                <div className="truncate text-xs text-faint" title={`${s.cron} · ${s.timezone}`}>
                  {describeCron(s.cron) ?? s.cron}
                </div>
              </TableCell>
              <TableCell>
                {s.last_run ? (
                  <span className="flex items-center gap-2 text-xs whitespace-nowrap text-muted-foreground">
                    <TaskStateBadge state={s.last_run.state} />
                    <RelativeTime iso={s.last_run.scheduled_for} />
                  </span>
                ) : (
                  <span className="text-xs text-faint">never</span>
                )}
              </TableCell>
              <TableCell>
                <RecentRuns runs={recent.get(s.name)} phoneSlots={6} />
              </TableCell>
              <TableCell className="hidden text-xs whitespace-nowrap text-muted-foreground sm:table-cell">
                {s.next_run_at ? <RelativeTime iso={s.next_run_at} /> : <span className="text-warning">paused</span>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Section>
  );
}
