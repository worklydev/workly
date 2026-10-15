import { useState } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { Link, useNavigate } from 'react-router';
import { getQueueActivityOptions, listQueuesOptions } from '../../api/@tanstack/react-query.gen';
import type { Queue, QueueActivity } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { useSlots } from '../../context';
import { ListPager, ListSearch } from '../../components/ListControls';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { StatCard } from '../../components/StatCard';
import { LIVE } from '../../lib/api';
import { COUNT_LIMIT, formatCount, formatDuration } from '../../lib/format';
import { Badge } from '../../primitives/badge';
import { Button } from '../../primitives/button';
import { ActivityChart } from './ActivityChart';
import { useRange, useRangeSince } from '../../hooks/useRange';
import { EditPolicy } from './EditPolicy';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { Behind } from './Behind';
import { limits } from '../../lib/queues';
import { useNow } from '../../hooks/useNow';
import { useListPage } from '../../hooks/useListPage';
import { formatRate } from '../../lib/format';
import { cn } from 'cn';

export function QueuesPage() {
  const { data, error, isPending } = useQuery({ ...listQueuesOptions(), ...LIVE });
  const queues = data?.queues ?? [];
  const [creating, setCreating] = useState(false);

  return (
    <>
      <PageHeader title="Queues">
        <Button onClick={() => setCreating(true)}>
          <Plus />
          New queue
        </Button>
      </PageHeader>
      <EditPolicy open={creating} onClose={() => setCreating(false)} />
      {isPending ? (
        <Muted>Loading…</Muted>
      ) : error ? (
        <ErrorText error={error} />
      ) : queues.length === 0 ? (
        <QueuesGettingStarted />
      ) : (
        <>
          <Totals queues={queues} />
          <ActivityChart />
          <QueueList queues={queues} />
        </>
      )}
    </>
  );
}

function Totals({ queues }: { queues: Queue[] }) {
  const sum = (key: 'due' | 'scheduled' | 'running' | 'failed') =>
    Math.min(
      COUNT_LIMIT,
      queues.reduce((n, q) => n + (q.stats?.[key] ?? 0), 0),
    );
  const oldest = queues
    .map((q) => q.stats?.oldest_due_at)
    .filter((t): t is string => t !== undefined)
    .sort()[0];
  const now = useNow();
  const behind = oldest ? Math.max(0, now - new Date(oldest).getTime()) : 0;

  const failed = sum('failed');

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
      <StatCard label="Due" value={formatCount(sum('due'))} title="Pending tasks that are due now" />
      <StatCard label="Running" value={formatCount(sum('running'))} />
      <StatCard
        label="Scheduled"
        value={formatCount(sum('scheduled'))}
        title="Pending tasks due later, including ones waiting to retry"
      />
      <StatCard
        label="Most behind"
        value={oldest ? formatDuration(behind) : '—'}
        tone={behind > 60_000 ? 'warning' : undefined}
      />
      <StatCard
        label="Failed"
        value={formatCount(failed)}
        tone={failed > 0 ? 'danger' : undefined}
        to={failed > 0 ? '/tasks?state=failed' : undefined}
        title="Failed tasks still kept (WORKLY_RETENTION)"
      />
    </div>
  );
}

/** Each queue's finished tasks over the page's range (useRange). */
function useDayActivity(queues: Queue[]): Map<string, QueueActivity> {
  const since = useRangeSince();
  const results = useQueries({
    queries: queues.map((q) => ({
      ...getQueueActivityOptions({ path: { queue: q.name }, query: { since } }),
      refetchInterval: 15_000,
      placeholderData: (previous: QueueActivity | undefined) => previous,
    })),
  });
  return new Map(queues.flatMap((q, i) => (results[i].data ? [[q.name, results[i].data]] : [])));
}

function QueueList({ queues }: { queues: Queue[] }) {
  const list = useListPage(queues, (q) => [q.name]);
  return (
    <div className="flex flex-col gap-3">
      <ListSearch list={list} placeholder="Search queues" />
      {list.items.length === 0 ? <Muted>No queues match “{list.search}”.</Muted> : <QueueTable queues={list.items} />}
      <ListPager list={list} noun="queues" />
    </div>
  );
}

/** Only the queues shown get their activity fetched. */
function QueueTable({ queues }: { queues: Queue[] }) {
  const navigate = useNavigate();
  const day = useDayActivity(queues);
  const { value: range, query } = useRange();
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Queue</TableHead>
          <TableHead className="text-right">Due</TableHead>
          <TableHead className="text-right">Running</TableHead>
          <TableHead className="text-right">Scheduled</TableHead>
          <TableHead className="text-right" title="Failed tasks still kept (WORKLY_RETENTION)">
            Failed
          </TableHead>
          <TableHead className="text-right">Behind</TableHead>
          <TableHead className="text-right" title={`Tasks that succeeded or failed in the last ${range}`}>
            Done {range}
          </TableHead>
          <TableHead className="text-right" title="Share of those that succeeded">
            Success {range}
          </TableHead>
          <TableHead>Limits</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {queues.map((q) => (
          <TableRow key={q.name} className="cursor-pointer" onClick={() => navigate(`/queues/${q.name}${query}`)}>
            <TableCell>
              <div className="flex items-center gap-2">
                <Link to={`/queues/${q.name}${query}`} className="font-medium" onClick={(e) => e.stopPropagation()}>
                  {q.name}
                </Link>
                {q.paused && <Badge variant="warning">paused</Badge>}
              </div>
            </TableCell>
            <TableCell className="text-right font-mono text-xs">{formatCount(q.stats?.due ?? 0)}</TableCell>
            <TableCell className="text-right font-mono text-xs">{formatCount(q.stats?.running ?? 0)}</TableCell>
            <TableCell className="text-right font-mono text-xs text-muted-foreground">
              {formatCount(q.stats?.scheduled ?? 0)}
            </TableCell>
            <TableCell className="text-right font-mono text-xs">
              <Failed n={q.stats?.failed ?? 0} />
            </TableCell>
            <TableCell className="text-right font-mono text-xs">
              <Behind since={q.stats?.oldest_due_at} />
            </TableCell>
            <DayActivity activity={day.get(q.name)} />
            <TableCell className="text-xs text-muted-foreground">{limits(q)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function DayActivity({ activity: a }: { activity?: QueueActivity }) {
  const done = a ? a.succeeded + a.failed : 0;
  const rate = done > 0 ? a!.succeeded / done : undefined;
  return (
    <>
      <TableCell className="text-right font-mono text-xs text-muted-foreground">
        {a ? formatCount(Math.min(COUNT_LIMIT, done)) : <span className="text-faint">…</span>}
      </TableCell>
      <TableCell
        className={cn('text-right font-mono text-xs', rate !== undefined && rate < 0.9 && 'text-destructive')}
        title={a && `${a.succeeded.toLocaleString()} succeeded, ${a.failed.toLocaleString()} failed`}
      >
        {rate === undefined ? <span className="text-faint">—</span> : formatRate(rate)}
      </TableCell>
    </>
  );
}

/** Red when there are any, so a failing queue stands out in the list. */
function Failed({ n }: { n: number }) {
  return <span className={n > 0 ? 'text-destructive' : 'text-muted-foreground'}>{formatCount(n)}</span>;
}

/** A wrapping app's own first steps, or the `workly dev` example. */
function QueuesGettingStarted() {
  const custom = useSlots().gettingStarted?.queues;
  return custom ?? <GettingStarted />;
}

function GettingStarted() {
  const origin = window.location.origin;
  return (
    <div className="flex max-w-2xl flex-col gap-3">
      <p className="text-muted-foreground">
        No queues yet. Enqueue a task and its queue is created. With{' '}
        <span className="font-mono text-xs">workly dev</span>, the built-in echo endpoint makes a handy target:
      </p>
      <CodePanel title="Terminal">
        {`curl -X POST ${origin}/v1/queues/hello/tasks \\
  -H 'Content-Type: application/json' \\
  -d '{"url": "${origin}/_dev/echo", "payload": {"hello": "world"}}'`}
      </CodePanel>
    </div>
  );
}
