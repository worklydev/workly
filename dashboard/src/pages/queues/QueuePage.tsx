import { cn } from 'cn';
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Info, Pause, Pencil, Play, RotateCcw, Send, Trash2, X } from 'lucide-react';
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
import { ConfirmButton } from '../../components/ConfirmButton';
import { ListPager } from '../../components/ListControls';
import { usePage } from '../../hooks/usePage';
import { useRange, useRangeSince } from '../../hooks/useRange';
import { RangeTabs } from '../../components/RangeTabs';
import { DisabledReason } from '../../components/DisabledReason';
import { Field, Fields } from '../../components/Field';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { Section } from '../../components/Section';
import { StatCard } from '../../components/StatCard';
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
import { SortSummary } from '../../components/SortSummary';
import { useTaskSort } from '../../lib/taskSort';
import { EditPolicy } from './EditPolicy';
import { EnqueueTask } from './EnqueueTask';

const STATES: (TaskState | 'all')[] = ['all', 'pending', 'running', 'succeeded', 'failed', 'cancelled'];

export function QueuePage() {
  const name = useParams().queue!;
  const { data: queue, error, isPending } = useQuery({ ...getQueueOptions({ path: { queue: name } }), ...LIVE });

  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;

  return (
    <>
      <PageHeader title={queue.name}>
        {queue.paused && <Badge variant="warning">paused</Badge>}
        <QueueActions queue={queue} />
      </PageHeader>
      <Section title="Backlog">
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
          <PendingCard due={queue.stats?.due ?? 0} scheduled={queue.stats?.scheduled ?? 0} to="?state=pending" />
          <StatCard
            label="Running"
            value={formatCount(queue.stats?.running ?? 0)}
            to={queue.stats?.running ? '?state=running' : undefined}
          />
          <StatCard
            label="Longest wait"
            value={<LongestWait since={queue.stats?.oldest_due_at} />}
            detail={queue.stats?.oldest_due_at ? 'oldest task past its run time' : 'nothing waiting'}
            title={LONGEST_WAIT_TITLE}
          />
        </div>
      </Section>
      <Activity queue={queue.name} />
      <Failures queue={queue} />
      <Policy queue={queue} />
      <QueueTasks queue={queue} />
    </>
  );
}

/** Finished tasks over the page's range (useRange), or as much of it as retention keeps. */
function Activity({ queue }: { queue: string }) {
  const { value: range, seconds, query } = useRange();
  const since = useRangeSince();
  const { data, error } = useQuery({
    ...getQueueActivityOptions({ path: { queue }, query: { since } }),
    refetchInterval: 15_000,
    placeholderData: keepPreviousData,
  });
  const retention = data?.retention_seconds;
  const finished = data ? data.succeeded + data.failed : 0;
  // Keeps the range when filtering the task list below.
  const tasks = (state: TaskState) => `${query ? `${query}&` : '?'}state=${state}`;

  return (
    <Section title={`Activity · last ${range}`} actions={<RangeTabs />}>
      {error ? (
        <ErrorText error={error} />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <StatCard
              label="Succeeded"
              value={data ? formatCount(data.succeeded) : '…'}
              to={data?.succeeded ? tasks('succeeded') : undefined}
            />
            <StatCard
              label="Failed"
              value={data ? formatCount(data.failed) : '…'}
              tone={data?.failed ? 'danger' : undefined}
              to={data?.failed ? tasks('failed') : undefined}
            />
            <StatCard
              label="Cancelled"
              value={data ? formatCount(data.cancelled) : '…'}
              to={data?.cancelled ? tasks('cancelled') : undefined}
            />
            <StatCard label="Success rate" value={finished > 0 ? `${formatRate(data!.succeeded / finished)}` : '—'} />
          </div>
          {data && (
            <p className="flex items-center gap-1.5 text-xs text-faint">
              <Info className="size-3.5 shrink-0" />
              {retention === undefined
                ? 'Finished tasks are kept forever (WORKLY_RETENTION).'
                : retention < seconds
                  ? `Finished tasks are kept for ${formatDays(retention)} (WORKLY_RETENTION), so older activity isn't counted.`
                  : `Finished tasks are kept for ${formatDays(retention)} (WORKLY_RETENTION).`}
            </p>
          )}
        </>
      )}
    </Section>
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
      <DisabledReason reason={pending === 0 ? 'No pending tasks to cancel.' : undefined}>
        <ConfirmButton
          title="Cancel every pending task, including scheduled ones. They stay in the history and can be retried."
          confirmLabel={`Cancel ${formatCount(pending)} pending?`}
          onConfirm={() => cancelPending.mutate(path)}
          disabled={pending === 0 || cancelPending.isPending}
        >
          Cancel pending
        </ConfirmButton>
      </DisabledReason>
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
      {queue.paused ? (
        <Button onClick={() => resume.mutate(path)} disabled={resume.isPending}>
          <Play />
          Resume
        </Button>
      ) : (
        <Button onClick={() => pause.mutate(path)} disabled={pause.isPending}>
          <Pause />
          Pause
        </Button>
      )}
      <Button onClick={() => setEnqueueing(true)}>
        <Send />
        Enqueue task
      </Button>
      <EnqueueTask queue={queue.name} open={enqueueing} onClose={() => setEnqueueing(false)} />
      <ConfirmButton
        title="Delete the queue and every task in it, history included. This can't be undone."
        confirmLabel="Delete queue and all its tasks"
        onConfirm={() => remove.mutate(path)}
        disabled={remove.isPending}
      >
        <Trash2 />
        Delete
      </ConfirmButton>
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
          <Button
            variant="ghost"
            size="sm"
            title="Retry the failed tasks with this error, each with a fresh set of attempts"
            onClick={() => retry.mutate({ path: { queue }, query: { last_error: g.error } })}
            disabled={retry.isPending}
          >
            <RotateCcw />
            Retry these
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function Policy({ queue }: { queue: Queue }) {
  // The generated type keeps QueueConfig's fields optional, but a queue
  // always has its full policy.
  const q = queue as Queue & Required<Pick<Queue, 'min_backoff_seconds' | 'max_backoff_seconds' | 'timeout_seconds'>>;
  const backoff = `${formatSeconds(q.min_backoff_seconds)} → ${formatSeconds(q.max_backoff_seconds)}, ×${q.backoff_multiplier}`;
  const rate = q.rate_per_second === undefined ? 'unlimited' : `${q.rate_per_second}/s`;
  const [editing, setEditing] = useState(false);
  return (
    <Section
      title="Policy"
      actions={
        <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
          <Pencil />
          Edit
        </Button>
      }
    >
      <EditPolicy queue={queue} open={editing} onClose={() => setEditing(false)} />
      <Fields>
        <Field label="Max attempts">
          <span className="font-mono text-xs">{q.max_attempts}</span>
        </Field>
        <Field label="Backoff">
          <span className="font-mono text-xs">{backoff}</span>
        </Field>
        <Field label="Timeout">
          <span className="font-mono text-xs">{formatSeconds(q.timeout_seconds)}</span>
        </Field>
        <Field label="Retry 4xx">{q.retry_on_4xx ? 'yes' : 'only 408 and 429'}</Field>
        <Field label="Rate">
          <span className="font-mono text-xs">{rate}</span>
        </Field>
        <Field label="Concurrency">
          <span className="font-mono text-xs">{q.max_concurrency}</span>
        </Field>
      </Fields>
    </Section>
  );
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
  const counts = stateCounts(queue);
  const sorting = useTaskSort(state);
  const query: ListTasksData['query'] = { ...sorting.query };
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
          <SortSummary sorting={sorting} />
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
