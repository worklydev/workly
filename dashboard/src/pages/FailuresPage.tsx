import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bell, RotateCcw } from 'lucide-react';
import { Link, useNavigate } from 'react-router';
import {
  getQueueFailuresOptions,
  listAlertChannelsOptions,
  listQueuesOptions,
  listRunsOptions,
  listSchedulesOptions,
  retryFailedTasksMutation,
} from '../api/@tanstack/react-query.gen';
import type { FailureGroup, Run } from '../api';
import { ListPager } from '../components/ListControls';
import { PageHeader } from '../components/PageHeader';
import { ErrorText, Muted } from '../components/QueryState';
import { RelativeTime } from '../components/RelativeTime';
import { Section } from '../components/Section';
import { usePage } from '../hooks/usePage';
import { errorMessage, LIVE } from '../lib/api';
import { formatCount, shortId } from '../lib/format';
import { useFromHere } from '../lib/from';
import { Button } from '../primitives/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../primitives/table';

type Group = FailureGroup & { queue: string };

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
  const queues = useQuery({ ...listQueuesOptions(), ...LIVE });
  const failing = (queues.data?.queues ?? []).filter((q) => (q.stats?.failed ?? 0) > 0);
  const failures = useQueries({
    queries: failing.map((q) => ({
      ...getQueueFailuresOptions({ path: { queue: q.name }, query: { limit: 50 } }),
      refetchInterval: 15_000,
    })),
  });
  const groups: Group[] = failures
    .flatMap((f, i) => (f.data?.groups ?? []).map((g) => ({ ...g, queue: failing[i].name })))
    .sort((a, b) => b.count - a.count || Date.parse(b.last_failed_at) - Date.parse(a.last_failed_at));
  const list = usePage(groups);
  const error = queues.error ?? failures.find((f) => f.error)?.error;

  return (
    <Section title="Queues">
      {error ? (
        <ErrorText error={error} />
      ) : queues.isPending || failures.some((f) => f.isPending) ? (
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

/** How many failed runs are looked at per schedule; more shows as "50+". */
const RUN_LIMIT = 50;

type RunGroup = { schedule: string; error: string; count: number; more: boolean; latest: Run };

/**
 * Failed runs of every schedule, grouped like the queues' failures. The API
 * has no failure summary for schedules, so this groups each schedule's
 * latest failed runs here.
 */
function ScheduleFailures() {
  const from = useFromHere();
  const schedules = useQuery({ ...listSchedulesOptions(), ...LIVE });
  const names = (schedules.data?.schedules ?? []).map((s) => s.name);
  const runs = useQueries({
    queries: names.map((name) => ({
      ...listRunsOptions({ path: { schedule: name }, query: { state: 'failed', limit: RUN_LIMIT } }),
      refetchInterval: 15_000,
    })),
  });
  const groups: RunGroup[] = runs
    .flatMap((r, i) => {
      const byError = new Map<string, RunGroup>();
      for (const run of r.data?.runs ?? []) {
        const error = run.last_error ?? '';
        const g = byError.get(error);
        if (g) g.count++;
        // Newest first, so the first run of each error is its latest.
        else byError.set(error, { schedule: names[i], error, count: 1, more: false, latest: run });
      }
      if (r.data?.next_cursor) byError.forEach((g) => (g.more = true));
      return [...byError.values()];
    })
    .sort((a, b) => b.count - a.count || lastFailed(b) - lastFailed(a));
  const list = usePage(groups);
  const error = schedules.error ?? runs.find((r) => r.error)?.error;

  return (
    <Section title="Schedules">
      {error ? (
        <ErrorText error={error} />
      ) : schedules.isPending || runs.some((r) => r.isPending) ? (
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

const lastFailed = (g: RunGroup) => Date.parse(g.latest.finished_at ?? g.latest.updated_at);

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

function GroupRow({ group: g }: { group: Group }) {
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
