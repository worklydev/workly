import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { Link, useNavigate } from 'react-router';
import { listQueuesOptions } from '../../api/@tanstack/react-query.gen';
import type { Queue, QueueActivity } from '../../api';
import { useSetting } from '../../context';
import { ListPager, ListSearch } from '../../components/ListControls';
import { PageHeader } from '../../components/PageHeader';
import { Section } from '../../components/Section';
import { ErrorText, Muted } from '../../components/QueryState';
import { LIVE } from '../../lib/api';
import { Badge } from '../../primitives/badge';
import { Button } from '../../primitives/button';
import { ActivityChart } from './ActivityChart';
import { QueuesGettingStarted } from './GettingStarted';
import { Totals } from './Totals';
import { useQueueActivity } from '../../hooks/useQueueActivity';
import { useRange } from '../../hooks/useRange';
import { EditPolicy } from './EditPolicy';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { LONGEST_WAIT_TITLE, LongestWait } from './LongestWait';
import { isSystemQueue, limits, LOW_SUCCESS, systemQueuePurpose } from '../../lib/queues';
import { Count } from '../../components/Count';
import { RowHint } from '../../components/RowHint';
import { useListPage } from '../../hooks/useListPage';
import { formatRate } from '../../lib/format';
import { cn } from 'cn';

export function QueuesPage() {
  const { data, error, isPending } = useQuery({ ...listQueuesOptions(), ...LIVE });
  const all = data?.queues ?? [];
  const queues = all.filter((q) => !isSystemQueue(q.name));
  const system = all.filter((q) => isSystemQueue(q.name));
  const [creating, setCreating] = useState(false);

  return (
    <>
      <PageHeader title="Queues" description="Tasks waiting to be delivered to your endpoints.">
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
      ) : all.length === 0 ? (
        <QueuesGettingStarted />
      ) : (
        <>
          <Totals queues={all} />
          <ActivityChart />
          {queues.length === 0 ? <QueuesGettingStarted /> : <QueueList queues={queues} />}
          {system.length > 0 && <SystemQueues queues={system} />}
        </>
      )}
    </>
  );
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

/**
 * Workly's own queues, apart from the application's: they fill on their own
 * (callbacks, workflow steps), and only their settings are the user's.
 */
function SystemQueues({ queues }: { queues: Queue[] }) {
  return (
    <Section title="System queues">
      <p className="-mt-1 text-sm text-muted-foreground">
        Workly enqueues into these itself. You can change their settings, but not enqueue into them.
      </p>
      <QueueTable queues={queues} system />
    </Section>
  );
}

/** Only the queues shown get their activity fetched. */
function QueueTable({ queues, system = false }: { queues: Queue[]; system?: boolean }) {
  const navigate = useNavigate();
  const day = useQueueActivity(queues);
  const { value: range, query } = useRange();
  const setting = useSetting();
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Queue</TableHead>
          <TableHead className="text-right" title="Tasks not run yet: due now, or scheduled for later">
            Pending
          </TableHead>
          <TableHead className="text-right">Running</TableHead>
          <TableHead className="text-right" title={`Failed tasks still kept${setting('WORKLY_RETENTION')}`}>
            Failed
          </TableHead>
          <TableHead className="text-right" title={LONGEST_WAIT_TITLE}>
            Longest wait
          </TableHead>
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
            <TableCell className="w-full max-w-0 min-w-40">
              <div className="flex items-center gap-2">
                <Link
                  to={`/queues/${q.name}${query}`}
                  title={q.name}
                  className="truncate font-medium"
                  onClick={(e) => e.stopPropagation()}
                >
                  {q.name}
                </Link>
                {q.paused && <Badge variant="warning">paused</Badge>}
                <RowHint />
              </div>
              {system && (
                <div className="truncate text-xs text-muted-foreground" title={systemQueuePurpose(q.name)}>
                  {systemQueuePurpose(q.name)}
                </div>
              )}
            </TableCell>
            <TableCell className="text-right font-mono text-xs">
              <Count n={(q.stats?.due ?? 0) + (q.stats?.scheduled ?? 0)} />
            </TableCell>
            <TableCell className="text-right font-mono text-xs">
              <Count n={q.stats?.running ?? 0} />
            </TableCell>
            <TableCell className="text-right font-mono text-xs">
              <Count n={q.stats?.failed ?? 0} tone="danger" />
            </TableCell>
            <TableCell className="text-right font-mono text-xs">
              <LongestWait since={q.stats?.oldest_due_at} />
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
      <TableCell className="text-right font-mono text-xs">
        {a ? <Count n={done} /> : <span className="text-faint">…</span>}
      </TableCell>
      <TableCell
        className={cn('text-right font-mono text-xs', rate !== undefined && rate < LOW_SUCCESS && 'text-destructive')}
        title={a && `${a.succeeded.toLocaleString()} succeeded, ${a.failed.toLocaleString()} failed`}
      >
        {rate === undefined ? <span className="text-faint">—</span> : formatRate(rate)}
      </TableCell>
    </>
  );
}
