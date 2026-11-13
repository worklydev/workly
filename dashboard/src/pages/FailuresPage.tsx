import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bell, RotateCcw } from 'lucide-react';
import { Link, useNavigate } from 'react-router';
import { listAlertChannelsOptions, retryFailedTasksMutation } from '../api/@tanstack/react-query.gen';
import { ListPager } from '../components/ListControls';
import { PageHeader } from '../components/PageHeader';
import { ErrorText, Muted } from '../components/QueryState';
import { RelativeTime } from '../components/RelativeTime';
import { Section } from '../components/Section';
import { usePage } from '../hooks/usePage';
import { errorMessage } from '../lib/api';
import { useQueueFailureGroups, useRunFailureGroups, type QueueFailureGroup } from '../hooks/useFailureGroups';
import { formatCount, shortId } from '../lib/format';
import { useFromHere } from '../lib/from';
import { Button } from '../primitives/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../primitives/table';

/**
 * What's broken, and how badly: failed tasks in every queue and failed runs
 * of every schedule, grouped by the error they ended with.
 */
export function FailuresPage() {
  return (
    <>
      <PageHeader
        title="Failures"
        description="Failed tasks and schedule runs, grouped by the error they ended with. Fix the cause, then retry them."
      >
        <AlertsLink />
      </PageHeader>
      <QueueFailures />
      <ScheduleFailures />
    </>
  );
}

function QueueFailures() {
  const { groups, error, isPending } = useQueueFailureGroups();
  const list = usePage(groups);

  return (
    <Section title="Queues">
      {error ? (
        <ErrorText error={error} />
      ) : isPending ? (
        <Muted>Loading…</Muted>
      ) : groups.length === 0 ? (
        <Muted>No failed tasks in any queue.</Muted>
      ) : (
        <div className="flex flex-col gap-3">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Queue</TableHead>
                <TableHead className="text-right">Tasks</TableHead>
                <TableHead>Error</TableHead>
                <TableHead className="w-full">Latest response</TableHead>
                <TableHead>Last failed</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.items.map((g) => (
                <GroupRow key={`${g.queue}\n${g.error}`} group={g} />
              ))}
            </TableBody>
          </Table>
          <ListPager list={list} noun="errors" />
        </div>
      )}
    </Section>
  );
}

function ScheduleFailures() {
  const from = useFromHere();
  const { groups, runs } = useRunFailureGroups();
  const list = usePage(groups);

  return (
    <Section title="Schedules">
      {runs.error ? (
        <ErrorText error={runs.error} />
      ) : runs.isPending ? (
        <Muted>Loading…</Muted>
      ) : groups.length === 0 ? (
        <Muted>No failed schedule runs.</Muted>
      ) : (
        <div className="flex flex-col gap-3">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Schedule</TableHead>
                <TableHead className="text-right">Runs</TableHead>
                <TableHead className="w-full">Error</TableHead>
                <TableHead>Latest run</TableHead>
                <TableHead>Last failed</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.items.map((g) => (
                <TableRow key={`${g.schedule}\n${g.error}`}>
                  <TableCell className="font-medium">
                    <Link to={`/schedules/${encodeURIComponent(g.schedule)}`}>{g.schedule}</Link>
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs">
                    <Link
                      to={`/schedules/${encodeURIComponent(g.schedule)}?state=failed`}
                      className="text-destructive hover:underline"
                      title="Show the failed runs"
                    >
                      {formatCount(g.count)}
                      {g.more && '+'}
                    </Link>
                  </TableCell>
                  <TableCell className="max-w-0 min-w-48 truncate" title={g.error}>
                    {g.error || <span className="text-faint">No error recorded</span>}
                  </TableCell>
                  <TableCell className="font-mono text-xs whitespace-nowrap text-muted-foreground">
                    <Link
                      to={`/runs/${g.latest.id}`}
                      state={from}
                      title={g.latest.id}
                      className="hover:text-foreground hover:underline"
                    >
                      {shortId(g.latest.id)}
                    </Link>
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                    <RelativeTime iso={g.latest.finished_at ?? g.latest.updated_at} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <ListPager list={list} noun="errors" />
        </div>
      )}
    </Section>
  );
}

/** Where failures are announced, or a nudge to set that up. */
function AlertsLink() {
  const navigate = useNavigate();
  const { data } = useQuery(listAlertChannelsOptions());
  const n = data?.channels.length;
  return (
    <Button variant="ghost" onClick={() => navigate('/alerts')}>
      <Bell />
      {n === undefined ? 'Alerts' : n === 0 ? 'Get alerted' : `Alerts to ${n} ${n === 1 ? 'channel' : 'channels'}`}
    </Button>
  );
}

function GroupRow({ group: g }: { group: QueueFailureGroup }) {
  const qc = useQueryClient();
  const from = useFromHere();
  const retry = useMutation({ ...retryFailedTasksMutation(), onSuccess: () => qc.invalidateQueries() });
  const body = g.response_body?.replace(/\s+/g, ' ').trim();
  const tasks = `/queues/${encodeURIComponent(g.queue)}?${new URLSearchParams({ state: 'failed', last_error: g.error })}`;

  return (
    <TableRow>
      <TableCell className="max-w-48 truncate font-medium" title={g.queue}>
        <Link to={`/queues/${encodeURIComponent(g.queue)}`}>{g.queue}</Link>
      </TableCell>
      <TableCell className="text-right font-mono text-xs">
        <Link to={tasks} className="text-destructive hover:underline" title="Show these tasks">
          {formatCount(g.count)}
        </Link>
      </TableCell>
      <TableCell className="max-w-60 truncate" title={g.error}>
        {g.error || <span className="text-faint">No error recorded</span>}
      </TableCell>
      <TableCell className="max-w-0 min-w-32 truncate font-mono text-xs text-muted-foreground">
        <Link
          to={`/tasks/${g.latest_task_id}`}
          state={from}
          title={`Open the latest task${body ? `: ${body}` : ''}`}
          className="hover:text-foreground hover:underline"
        >
          {body || <span className="text-faint">no body</span>}
        </Link>
      </TableCell>
      <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
        <RelativeTime iso={g.last_failed_at} />
      </TableCell>
      <TableCell className="py-0">
        <span className="flex items-center justify-end gap-2">
          {retry.error && <span className="text-xs text-destructive">{errorMessage(retry.error)}</span>}
          <Button
            variant="ghost"
            size="sm"
            title="Retry every task in this group with a fresh set of attempts"
            disabled={retry.isPending}
            onClick={() => retry.mutate({ path: { queue: g.queue }, query: { last_error: g.error } })}
          >
            <RotateCcw />
            Retry {formatCount(g.count)}
          </Button>
        </span>
      </TableCell>
    </TableRow>
  );
}
