import { cn } from 'cn';
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Ban, Info, Pause, Pencil, Play, RotateCcw, Send, Trash2, X } from 'lucide-react';
import { useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router';
import {
  cancelPendingTasksMutation,
  getQueueActivityOptions,
  getQueueFailuresOptions,
  getQueueOptions,
  listTasksInfiniteOptions,
  pauseQueueMutation,
  resumeQueueMutation,
  retryFailedTasksMutation,
  deleteQueueMutation,
} from '../../api/@tanstack/react-query.gen';
import type { FailureGroup, ListTasksData, Queue, TaskState } from '../../api';
import { ConfirmItem, MoreActions } from '../../components/MoreActions';
import { isSystemQueue, LOW_SUCCESS, systemQueuePurpose } from '../../lib/queues';
import { WORKFLOW_QUEUE } from '../../lib/workflows';
import { useSetting } from '../../context';
import { ListPager } from '../../components/ListControls';
import { usePage } from '../../hooks/usePage';
import { useRange, useRangeSince } from '../../hooks/useRange';
import { RangeTabs } from '../../components/RangeTabs';
import { DisabledReason } from '../../components/DisabledReason';
import { InlineFields } from '../../components/InlineFields';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { Section } from '../../components/Section';
import { StatCard, StatStrip } from '../../components/StatCard';
import { TaskTable } from '../../components/TaskTable';
import { taskPaging } from '../../lib/tasks';
import { useFromHere } from '../../lib/from';
import { errorMessage, LIVE } from '../../lib/api';
import { COUNT_LIMIT, formatCount, formatRate, formatSeconds } from '../../lib/format';
import { Badge } from '../../primitives/badge';
import { Button } from '../../primitives/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { Tabs, TabsList, TabsTab } from '../../primitives/tabs';
import { LONGEST_WAIT_TITLE, LongestWait } from './LongestWait';
import { PendingCard } from './PendingCard';
import { useTaskSort } from '../../lib/taskSort';
import { EditPolicy } from './EditPolicy';
import { EnqueueTask } from './EnqueueTask';
import { PAGE_SIZE } from '../../hooks/useListPage';

const STATES: (TaskState | 'all')[] = ['all', 'pending', 'running', 'succeeded', 'failed', 'cancelled'];

export function QueuePage() {
  const name = useParams().queue!;
  const { data: queue, error, isPending } = useQuery({ ...getQueueOptions({ path: { queue: name } }), ...LIVE });

  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;

  return (
    <>
      {/* The policy sits with the title: it's what the queue is, the rest is how it's doing. */}
      <div className="flex flex-col gap-3">
        <PageHeader
          title={queue.name}
          description={
            isSystemQueue(queue.name) &&
            `${systemQueuePurpose(queue.name)} A system queue: Workly enqueues into it, you can change its settings.`
          }
          badge={
            <>
              {isSystemQueue(queue.name) && <Badge>system</Badge>}
              {queue.paused && <Badge variant="warning">paused</Badge>}
            </>
          }
        >
          <QueueActions queue={queue} />
        </PageHeader>
        <Policy queue={queue} />
      </div>
      <Overview queue={queue} />
      <Failures queue={queue} />
      <QueueTasks queue={queue} />
    </>
  );
}

/**
 * Two groups: the backlog now, and finished tasks over the page's range
 * (useRange), or as much of it as retention keeps. The range tabs head only
 * the second, since the backlog doesn't depend on them.
 */
function Overview({ queue }: { queue: Queue }) {
  const { value: range, seconds, query } = useRange();
  const since = useRangeSince();
  const { data, error } = useQuery({
    ...getQueueActivityOptions({ path: { queue: queue.name }, query: { since } }),
    refetchInterval: 15_000,
    placeholderData: keepPreviousData,
  });
  const retention = data?.retention_seconds;
  const setting = useSetting();
  const finished = data ? data.succeeded + data.failed : 0;
  const rate = finished > 0 ? data!.succeeded / finished : undefined;
  // Every failed task still kept, whatever the range: the Failed tab's count.
  const failedKept = queue.stats?.failed ?? 0;
  // Keeps the range when filtering the task list below.
  const tasks = (state: TaskState) => `${query ? `${query}&` : '?'}state=${state}`;
  const kept =
    retention === undefined
      ? `Finished tasks are kept forever${setting('WORKLY_RETENTION')}.`
      : `Finished tasks are kept for ${formatDays(retention)}${setting('WORKLY_RETENTION')}.`;

  return (
    <div className="grid gap-x-6 gap-y-8 lg:grid-cols-[minmax(0,3fr)_minmax(0,4fr)]">
      <section className="flex flex-col gap-3">
        {/* As tall as the range tabs beside it, so the two strips line up. */}
        <div className="flex h-8 items-center">
          <h3 className="font-medium">Now</h3>
        </div>
        <StatStrip className="flex-1 grid-cols-3">
          <PendingCard due={queue.stats?.due ?? 0} scheduled={queue.stats?.scheduled ?? 0} to="?state=pending" />
          <StatCard
            label="Running"
            value={formatCount(queue.stats?.running ?? 0)}
            to={queue.stats?.running ? '?state=running' : undefined}
          />
          <StatCard
            label="Longest wait"
            value={<LongestWait since={queue.stats?.oldest_due_at} />}
            detail={queue.stats?.oldest_due_at ? 'past its run time' : 'nothing waiting'}
            title={LONGEST_WAIT_TITLE}
          />
        </StatStrip>
      </section>
      <section className="flex flex-col gap-3">
        <div className="flex h-8 items-center justify-between gap-4">
          <h3 className="font-medium">Last {range}</h3>
          <RangeTabs />
        </div>
        <StatStrip className="flex-1 grid-cols-2 sm:grid-cols-4">
          <StatCard
            label="Succeeded"
            value={data ? formatCount(data.succeeded) : '…'}
            to={data?.succeeded ? tasks('succeeded') : undefined}
            title={kept}
          />
          <StatCard
            label="Failed"
            value={data ? formatCount(data.failed) : '…'}
            tone={data?.failed ? 'danger' : undefined}
            // The range's count and the tab's differ; saying both keeps them from looking like a bug.
            detail={failedKept > 0 ? `${formatCount(failedKept)} kept in all` : undefined}
            to={failedKept > 0 ? tasks('failed') : undefined}
            title={`Failed in the last ${range}. ${formatCount(failedKept)} failed tasks are kept in all, as in the Failed tab.`}
          />
          <StatCard
            label="Cancelled"
            value={data ? formatCount(data.cancelled) : '…'}
            to={data?.cancelled ? tasks('cancelled') : undefined}
            title={kept}
          />
          <StatCard
            label="Success rate"
            value={rate === undefined ? '—' : formatRate(rate)}
            tone={rate !== undefined && rate < LOW_SUCCESS ? 'danger' : undefined}
            title={kept}
          />
        </StatStrip>
        {error && <ErrorText error={error} />}
        {/* Only worth saying when it changes what the numbers mean. */}
        {retention !== undefined && retention < seconds && (
          <p className="flex items-center gap-1.5 text-xs text-faint">
            <Info className="size-3.5 shrink-0" />
            {kept.slice(0, -1)}, so older activity isn't counted.
          </p>
        )}
      </section>
    </div>
  );
}

/** "7 days", "1 day", or "12h" for periods that aren't whole days. */
function formatDays(seconds: number): string {
  const days = seconds / 86_400;
  if (Number.isInteger(days)) return days === 1 ? '1 day' : `${days} days`;
  return formatSeconds(seconds);
}

function QueueActions({ queue }: { queue: Queue }) {
  const qc = useQueryClient();
  const onSuccess = () => qc.invalidateQueries();
  const pause = useMutation({ ...pauseQueueMutation(), onSuccess });
  const resume = useMutation({ ...resumeQueueMutation(), onSuccess });
  const cancelPending = useMutation({ ...cancelPendingTasksMutation(), onSuccess });
  const retryFailed = useMutation({ ...retryFailedTasksMutation(), onSuccess });
  const navigate = useNavigate();
  const [enqueueing, setEnqueueing] = useState(false);
  const [editing, setEditing] = useState(false);
  const remove = useMutation({
    ...deleteQueueMutation(),
    onSuccess: async () => {
      navigate('/queues');
      await qc.invalidateQueries();
    },
  });
  const path = { path: { queue: queue.name } };
  const pending = (queue.stats?.due ?? 0) + (queue.stats?.scheduled ?? 0);
  const failed = queue.stats?.failed ?? 0;
  const error = pause.error ?? resume.error ?? cancelPending.error ?? retryFailed.error ?? remove.error;
  const retried = retryFailed.data?.retried;

  return (
    <>
      {error && <span className="text-xs text-destructive">{errorMessage(error)}</span>}
      {retried !== undefined && (
        <span className="text-xs text-muted-foreground">
          {retried === 0 ? 'No failed tasks' : `Retrying ${formatCount(retried)} failed`}
        </span>
      )}
      <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
        <Pencil />
        Edit
      </Button>
      <EditPolicy queue={queue} open={editing} onClose={() => setEditing(false)} />
      {/* Workflow steps are retried by starting a new run, never one task at a time. */}
      {queue.name !== WORKFLOW_QUEUE && (
        <DisabledReason reason={failed === 0 ? 'No failed tasks to retry.' : undefined}>
          <Button
            variant="ghost"
            size="sm"
            title="Retry every failed task in this queue with a fresh set of attempts"
            onClick={() => retryFailed.mutate(path)}
            disabled={failed === 0 || retryFailed.isPending}
          >
            <RotateCcw />
            Retry failed
          </Button>
        </DisabledReason>
      )}
      {queue.paused ? (
        <Button variant="ghost" size="sm" onClick={() => resume.mutate(path)} disabled={resume.isPending}>
          <Play />
          Resume
        </Button>
      ) : (
        <Button variant="ghost" size="sm" onClick={() => pause.mutate(path)} disabled={pause.isPending}>
          <Pause />
          Pause
        </Button>
      )}
      {/* The API refuses tasks for system queues. */}
      {!isSystemQueue(queue.name) && (
        <>
          <Button onClick={() => setEnqueueing(true)}>
            <Send />
            Enqueue task
          </Button>
          <EnqueueTask queue={queue.name} open={enqueueing} onClose={() => setEnqueueing(false)} />
        </>
      )}
      <MoreActions label="More queue actions">
        <ConfirmItem
          icon={<Ban />}
          title="Cancel every pending task, including scheduled ones. They stay in the history and can be retried."
          confirmLabel={`Cancel ${formatCount(pending)} pending?`}
          onConfirm={() => cancelPending.mutate(path)}
          disabledReason={pending === 0 ? 'No pending tasks to cancel.' : undefined}
        >
          Cancel pending tasks
        </ConfirmItem>
        <ConfirmItem
          icon={<Trash2 />}
          title="Delete the queue and every task in it, history included. This can't be undone."
          confirmLabel="Delete queue and all its tasks?"
          onConfirm={() => remove.mutate(path)}
        >
          Delete queue
        </ConfirmItem>
      </MoreActions>
    </>
  );
}

/** Failed tasks grouped by error, so one broken endpoint or bad payload is one line. */
function Failures({ queue }: { queue: Queue }) {
  const failed = queue.stats?.failed ?? 0;
  const { data, error } = useQuery({
    ...getQueueFailuresOptions({ path: { queue: queue.name } }),
    enabled: failed > 0,
    refetchInterval: 15_000,
  });
  const failures = usePage(data?.groups ?? []);
  if (failed === 0) return null;

  return (
    <Section title="Failures by error">
      {error ? (
        <ErrorText error={error} />
      ) : !data ? (
        <Muted>Loading…</Muted>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
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
            {failures.items.map((g) => (
              <FailureRow key={g.error} queue={queue.name} group={g} />
            ))}
          </TableBody>
        </Table>
      )}
      <ListPager list={failures} noun="errors" />
    </Section>
  );
}

const TASKS_ID = 'queue-tasks';

function FailureRow({ queue, group: g }: { queue: string; group: FailureGroup }) {
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const from = useFromHere();
  const retry = useMutation({ ...retryFailedTasksMutation(), onSuccess: () => qc.invalidateQueries() });
  const body = g.response_body?.replace(/\s+/g, ' ').trim();
  const selected = params.get('last_error') === g.error;
  const show = () => {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set('state', 'failed');
        next.set('last_error', g.error);
        return next;
      },
      { replace: true },
    );
    // After the filtered list has rendered, so the scroll isn't cut short.
    setTimeout(() => document.getElementById(TASKS_ID)?.scrollIntoView({ behavior: 'smooth' }), 50);
  };

  return (
    <TableRow
      className="cursor-pointer data-[state=selected]:bg-muted/40"
      data-state={selected ? 'selected' : undefined}
      title="Show these tasks"
      onClick={show}
    >
      <TableCell className="text-right font-mono text-xs text-destructive">{formatCount(g.count)}</TableCell>
      <TableCell className="max-w-80 truncate" title={g.error}>
        {g.error || <span className="text-faint">No error recorded</span>}
      </TableCell>
      <TableCell className="max-w-0 truncate font-mono text-xs text-muted-foreground">
        <Link
          to={`/tasks/${g.latest_task_id}`}
          state={from}
          title={`Open the latest task${body ? `: ${body}` : ''}`}
          className="hover:text-foreground hover:underline"
          onClick={(e) => e.stopPropagation()}
        >
          {body || <span className="text-faint">No response body</span>}
        </Link>
      </TableCell>
      <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
        <RelativeTime iso={g.last_failed_at} />
      </TableCell>
      <TableCell className="py-0" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-end gap-2">
          {retry.error && <span className="text-xs text-destructive">{errorMessage(retry.error)}</span>}
          {retry.data && (
            <span className="text-xs whitespace-nowrap text-muted-foreground">Retrying {retry.data.retried}</span>
          )}
          {queue !== WORKFLOW_QUEUE && (
            <Button
              variant="ghost"
              size="sm"
              title="Retry the failed tasks with this error, each with a fresh set of attempts"
              onClick={() => retry.mutate({ path: { queue }, query: { last_error: g.error } })}
              disabled={retry.isPending}
            >
              <RotateCcw />
              Retry {formatCount(g.count)}
            </Button>
          )}
        </div>
      </TableCell>
    </TableRow>
  );
}

/** One line of label–value pairs: compact enough to sit under the title. */
function Policy({ queue }: { queue: Queue }) {
  // The generated type keeps QueueConfig's fields optional, but a queue
  // always has its full policy.
  const q = queue as Queue & Required<Pick<Queue, 'min_backoff_seconds' | 'max_backoff_seconds' | 'timeout_seconds'>>;
  const items = [
    { label: 'Max attempts', value: String(q.max_attempts) },
    {
      label: 'Backoff',
      value: `${formatSeconds(q.min_backoff_seconds)} → ${formatSeconds(q.max_backoff_seconds)}, ×${q.backoff_multiplier}`,
    },
    { label: 'Timeout', value: formatSeconds(q.timeout_seconds) },
    { label: 'Retry 4xx', value: q.retry_on_4xx ? 'yes' : 'only 408 and 429', prose: true },
    q.rate_per_second === undefined
      ? { label: 'Rate', value: 'unlimited', prose: true }
      : { label: 'Rate', value: `${q.rate_per_second}/s` },
    { label: 'Concurrency', value: String(q.max_concurrency) },
  ];
  return <InlineFields items={items} />;
}

/** Tab counts for the states the stats cover; finished tasks aren't counted. */
function stateCounts(queue: Queue): Partial<Record<TaskState, number>> {
  const s = queue.stats;
  if (!s) return {};
  return { pending: Math.min(COUNT_LIMIT, s.due + s.scheduled), running: s.running, failed: s.failed };
}

function QueueTasks({ queue }: { queue: Queue }) {
  const [params, setParams] = useSearchParams();
  const state = (params.get('state') ?? 'all') as TaskState | 'all';
  const lastError = params.get('last_error');
  // The counts are the whole queue's, so they'd contradict a filtered list.
  const counts = lastError === null ? stateCounts(queue) : {};
  const sorting = useTaskSort(state);
  const query: ListTasksData['query'] = { ...sorting.query, limit: PAGE_SIZE };
  if (state !== 'all') query.state = state;
  if (lastError !== null) query.last_error = lastError;
  const pages = useInfiniteQuery({
    ...listTasksInfiniteOptions({ path: { queue: queue.name }, query }),
    ...taskPaging,
    ...LIVE,
    // Keep the rows while a new filter loads, so the page doesn't collapse (and cut a scroll short).
    placeholderData: keepPreviousData,
  });
  const set = (name: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === 'all') next.delete(name);
    else next.set(name, value);
    setParams(next, { replace: true });
  };

  return (
    <div id={TASKS_ID} className="scroll-mt-4">
      <Section title="Tasks">
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          <Tabs value={state} onValueChange={(value) => set('state', String(value))}>
            <TabsList>
              {STATES.map((s) => (
                <TabsTab key={s} value={s} className="flex items-center gap-1.5">
                  {s === 'all' ? 'All' : s.charAt(0).toUpperCase() + s.slice(1)}
                  {s !== 'all' && counts[s] !== undefined && counts[s] > 0 && (
                    <span className={cn('font-mono text-xs', s === 'failed' ? 'text-destructive' : 'text-faint')}>
                      {formatCount(counts[s])}
                    </span>
                  )}
                </TabsTab>
              ))}
            </TabsList>
          </Tabs>
        </div>
        {lastError !== null && (
          <div className="flex min-w-0 items-center gap-2 text-xs">
            <span className="shrink-0 text-muted-foreground">Last error</span>
            <Badge variant="danger" className="min-w-0 truncate" title={lastError}>
              {lastError || 'none recorded'}
            </Badge>
            <Button variant="ghost" size="sm" onClick={() => set('last_error', null)}>
              <X />
              Clear
            </Button>
          </div>
        )}
        <TaskTable
          pages={pages}
          sorting={sorting}
          paused={() => queue.paused}
          empty={
            lastError !== null
              ? 'No tasks with this error.'
              : state === 'all'
                ? 'No tasks in this queue.'
                : `No ${state} tasks.`
          }
        />
      </Section>
    </div>
  );
}
