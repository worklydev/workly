import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { cn } from 'cn';
import { ChevronDown, Copy, RotateCcw, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { Link, useParams } from 'react-router';
import {
  cancelTaskMutation,
  getQueueOptions,
  getTaskDeliveryOptions,
  getTaskOptions,
  listAttemptsOptions,
  retryTaskMutation,
} from '../../api/@tanstack/react-query.gen';
import type { Attempt, Queue, Task } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { ConfirmButton } from '../../components/ConfirmButton';
import { DisabledReason } from '../../components/DisabledReason';
import { CopyButton } from '../../components/CopyButton';
import { Properties, Property, PropertyGroups } from '../../components/Properties';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { Section } from '../../components/Section';
import { StatusCode, TaskStateBadge } from '../../components/states';
import { NextRun } from '../../components/TaskTable';
import { EnqueueTask } from '../queues/EnqueueTask';
import { hasDoubleSlash, toCurl } from '../../lib/curl';
import { useNow } from '../../hooks/useNow';
import { displayState } from '../../lib/states';
import { errorMessage, LIVE } from '../../lib/api';
import { formatDuration, prettyBody } from '../../lib/format';
import { Button } from '../../primitives/button';
import { AttemptTable } from './AttemptTable';

export function TaskPage() {
  const id = useParams().id!;
  const { data: task, error, isPending } = useQuery({ ...getTaskOptions({ path: { id } }), ...LIVE });
  const { data: queue } = useQuery({
    ...getQueueOptions({ path: { queue: task?.queue ?? '' } }),
    enabled: task !== undefined,
    ...LIVE,
  });

  // Shares its query with the attempt table.
  const { data: attempts } = useQuery({
    ...listAttemptsOptions({ path: { id } }),
    ...LIVE,
  });
  const firstAttempt = attempts?.attempts[0];
  const now = useNow(5_000);

  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;

  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-1">
            <span className="truncate font-mono text-base">{task.id}</span>
            <CopyButton text={task.id} title="Copy task ID" />
          </span>
        }
      >
        <TaskStateBadge state={displayState(task, now)} />
        <TaskActions task={task} />
      </PageHeader>
      <Notices task={task} queue={queue} attempts={attempts?.attempts} />
      <PropertyGroups>
        <Properties title="Timing">
          <Property label="Created">
            <RelativeTime iso={task.created_at} />
          </Property>
          <Property label="First attempt">
            {firstAttempt ? <RelativeTime iso={firstAttempt.started_at} /> : <None />}
          </Property>
          <Property label="Last run finished">
            {task.last_attempt_at ? <RelativeTime iso={task.last_attempt_at} /> : <None />}
          </Property>
          <Property label="Next run">
            <NextRun task={task} now={now} paused={queue?.paused} />
          </Property>
          <Property label="Total time">
            <Duration task={task} />
          </Property>
        </Properties>
        <Properties title="Delivery">
          <Property label="Queue">
            <Link
              to={`/queues/${task.queue}`}
              className="underline decoration-border underline-offset-4 hover:text-primary hover:decoration-primary"
            >
              {task.queue}
            </Link>
          </Property>
          <Property label="Attempts">
            <span className="font-mono text-xs">{task.attempt_count}</span>
          </Property>
          {/* Not "n of max": a retried task gets a fresh budget but keeps counting attempts. */}
          <Property label="Max attempts">
            <span className="font-mono text-xs">{task.max_attempts ?? queue?.max_attempts ?? '…'}</span>
          </Property>
          <Property label="Last status">
            <span className="font-mono text-xs">
              <StatusCode code={task.last_status} />
            </span>
          </Property>
          <Property label="Timeout">
            <span className="font-mono text-xs">{task.timeout_seconds ?? queue?.timeout_seconds ?? '…'}s</span>
          </Property>
          <Property label="Idempotency key">
            {task.idempotency_key ? (
              <span className="font-mono text-xs" title={task.idempotency_key}>
                {task.idempotency_key}
              </span>
            ) : (
              <None />
            )}
          </Property>
        </Properties>
      </PropertyGroups>
      <Request task={task} />
      <Section title="Attempts">
        <Attempts task={task} />
      </Section>
    </>
  );
}

function None() {
  return <span className="text-faint">—</span>;
}

/** From enqueue, or the latest manual retry, until the task finished. */
function Duration({ task }: { task: Task }) {
  if (!task.finished_at) return <None />;
  const from = task.retried_at ?? task.created_at;
  return (
    <span
      className="font-mono text-xs"
      title={
        task.retried_at
          ? 'From the latest manual retry until the task finished'
          : 'From enqueue until the task finished'
      }
    >
      {formatDuration(new Date(task.finished_at).getTime() - new Date(from).getTime())}
    </span>
  );
}

function TaskActions({ task }: { task: Task }) {
  const qc = useQueryClient();
  const [duplicating, setDuplicating] = useState(false);
  const onSuccess = () => qc.invalidateQueries();
  const cancel = useMutation({ ...cancelTaskMutation(), onSuccess });
  const retry = useMutation({ ...retryTaskMutation(), onSuccess });
  const path = { path: { id: task.id } };
  const error = cancel.error ?? retry.error;
  const finished = task.state === 'succeeded' || task.state === 'failed' || task.state === 'cancelled';

  return (
    <>
      {error && <span className="text-xs text-destructive">{errorMessage(error)}</span>}
      <DisabledReason reason={finished ? `A ${task.state} task can't be cancelled.` : undefined}>
        <ConfirmButton
          confirmLabel="Cancel task?"
          onConfirm={() => cancel.mutate(path)}
          disabled={finished || cancel.isPending}
        >
          Cancel
        </ConfirmButton>
      </DisabledReason>
      <DisabledReason
        reason={task.state === 'running' ? 'An attempt is in flight; retry once it finishes.' : undefined}
      >
        <Button
          variant={task.state === 'failed' ? 'default' : 'secondary'}
          onClick={() => retry.mutate(path)}
          disabled={task.state === 'running' || retry.isPending}
        >
          <RotateCcw />
          {finished ? 'Retry' : 'Run now'}
        </Button>
      </DisabledReason>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setDuplicating(true)}
        title="Enqueue a new task with this request"
      >
        <Copy />
        Duplicate
      </Button>
      <EnqueueTask queue={task.queue} from={task} open={duplicating} onClose={() => setDuplicating(false)} />
    </>
  );
}

/** The answer to "why isn't this task done?", when there is one. */
function Notices({ task, queue, attempts }: { task: Task; queue?: Queue; attempts?: Attempt[] }) {
  if (task.state === 'pending' && queue?.paused) {
    return <Notice tone="warning">Queue {queue.name} is paused. This task won't run until it's resumed.</Notice>;
  }
  if (task.state === 'failed') {
    const n = attempts ? attemptsSinceRetry(attempts) : task.attempt_count;
    return (
      <Notice tone="danger" detail={attempts && queue && whyFailed(task, queue, attempts)}>
        Failed after {n} attempt{n === 1 ? '' : 's'}
        {task.retried_at && attempts ? ' since the last manual retry' : ''}
        {task.last_error ? `: ${task.last_error}` : '.'}
      </Notice>
    );
  }
  return null;
}

/** Attempts count against max attempts from the latest manual retry on. */
function attemptsSinceRetry(attempts: Attempt[]): number {
  for (let i = attempts.length - 1; i >= 0; i--) {
    if (attempts[i].retried_at) return attempts.length - i;
  }
  return attempts.length;
}

/**
 * Why Workly stopped retrying: the attempts ran out, or the last response
 * was one it doesn't retry (see internal/dispatch/policy.go).
 */
function whyFailed(task: Task, queue: Queue, attempts: Attempt[]): React.ReactNode {
  const status = attempts.at(-1)?.status_code;
  const max = task.max_attempts ?? queue.max_attempts ?? Infinity;
  const exhausted = attemptsSinceRetry(attempts) >= max;
  const policy = (
    <Link to={`/queues/${task.queue}`} className="underline underline-offset-4">
      the queue's policy
    </Link>
  );

  if (status !== undefined && status >= 300 && status < 400) {
    return <>Redirects aren't followed, and aren't retried. Point the task at the final URL.</>;
  }
  if (!exhausted && status !== undefined && status >= 400 && status < 500 && status !== 408 && status !== 429) {
    return queue.retry_on_4xx ? (
      <>It wasn't retried because 4xx responses weren't retried when it failed. They are now (Retry 4xx).</>
    ) : (
      <>
        4xx responses other than 408 and 429 aren't retried, since the request itself is usually the problem. To retry
        them, turn on Retry 4xx in {policy}.
      </>
    );
  }
  if (exhausted) {
    return task.max_attempts !== undefined ? (
      <>It used all {max} attempts, the limit set on this task.</>
    ) : (
      <>
        It used all {max} attempts, the limit (Max attempts) in {policy}.
      </>
    );
  }
  return null;
}

function Notice({
  tone,
  detail,
  children,
}: {
  tone: 'warning' | 'danger';
  detail?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        'flex items-start gap-2 rounded-lg px-3 py-2',
        tone === 'warning' ? 'bg-warning/12 text-warning' : 'bg-destructive/12 text-destructive',
      )}
    >
      <TriangleAlert className="mt-0.5 size-4 shrink-0" />
      <div className="flex min-w-0 flex-col gap-0.5 break-words">
        <span>{children}</span>
        {detail && <span className="text-xs text-foreground/70">{detail}</span>}
      </div>
    </div>
  );
}

function Request({ task }: { task: Task }) {
  const qc = useQueryClient();
  // A failed task's payload is usually the next thing to check.
  const [open, setOpen] = useState(task.state === 'failed');
  const deliveryOptions = getTaskDeliveryOptions({ path: { id: task.id } });
  const { data: delivery } = useQuery({ ...deliveryOptions, enabled: open });
  const body = task.payload !== undefined ? JSON.stringify(task.payload, null, 2) : task.body && prettyBody(task.body);
  const headers = Object.entries(task.headers)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
  // The headers Workly adds, shown fainter than the task's own.
  const own = new Set(Object.keys(task.headers).map((k) => k.toLowerCase()));
  const added = Object.entries(delivery?.headers ?? {})
    .filter(([k]) => !own.has(k.toLowerCase()))
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
  // Fetched on click, so the signature is fresh.
  const curl = async () => toCurl(await qc.fetchQuery({ ...deliveryOptions, staleTime: 0 }));

  return (
    <Section
      title="Request"
      actions={
        <CopyButton
          text={curl}
          label="Copy as curl"
          title="The exact request with a fresh signature, which verifies for 5 minutes"
        />
      }
    >
      <CodePanel
        title={
          <div className="flex items-center justify-between gap-4">
            <span className="min-w-0 truncate font-mono" title={task.url}>
              {task.method} {task.url}
            </span>
            <button
              type="button"
              className="flex shrink-0 cursor-pointer items-center gap-1 text-faint hover:text-muted-foreground"
              aria-expanded={open}
              onClick={() => setOpen(!open)}
            >
              <ChevronDown className={cn('size-3.5 transition-transform', open && 'rotate-180')} />
              {open ? 'Hide request details' : 'Show request details'}
            </button>
          </div>
        }
      >
        {open ? (
          <>
            {headers}
            {headers && added ? '\n' : ''}
            {added && <span className="text-faint">{added}</span>}
            {(headers || added) && body ? '\n\n' : ''}
            {body || (headers || added ? '' : <span className="text-faint">No headers or body</span>)}
          </>
        ) : undefined}
      </CodePanel>
      {hasDoubleSlash(task.url) && (
        <p className="text-xs text-warning">
          The URL's path has a double slash (//). Many servers route that differently, often to a 404.
        </p>
      )}
    </Section>
  );
}

function Attempts({ task }: { task: Task }) {
  const { data, error, isPending } = useQuery({ ...listAttemptsOptions({ path: { id: task.id } }), ...LIVE });
  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;
  return <AttemptTable task={task} attempts={data.attempts} />;
}
