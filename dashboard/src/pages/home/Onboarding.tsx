import { useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ArrowRight, Check, Plus } from 'lucide-react';
import { Link } from 'react-router';
import { cn } from 'cn';
import { listAlertChannelsOptions, searchTasksOptions } from '../../api/@tanstack/react-query.gen';
import type { Schedule, Task } from '../../api';
import { LastResult } from '../../components/LastResult';
import { TaskStateBadge } from '../../components/states';
import { LIVE } from '../../lib/api';
import { formatTarget, shortId } from '../../lib/format';
import { displayState } from '../../lib/states';
import { Button } from '../../primitives/button';
import { QueuesGettingStarted } from '../queues/GettingStarted';
import { EditSchedule } from '../schedules/EditSchedule';
import { DeliveriesChart } from './DeliveriesChart';

/**
 * Until a first task has been delivered: the steps to get there, checked off
 * as they happen. The first one watches the newest task, so a delivery that
 * fails says why right here instead of somewhere else.
 */
export function Onboarding({ schedules }: { schedules: Schedule[] }) {
  const latest = useQuery({ ...searchTasksOptions({ query: { limit: 1 } }), ...LIVE });
  const channels = useQuery(listAlertChannelsOptions());
  const [creating, setCreating] = useState(false);
  const task = latest.data?.tasks[0];
  const alerting = (channels.data?.channels.length ?? 0) > 0;

  return (
    <>
      <EditSchedule open={creating} onClose={() => setCreating(false)} />
      <section className="overflow-hidden rounded-lg border border-border bg-card/50 dark:bg-card">
        <div className="flex items-center gap-3 border-b border-border px-4 py-3">
          <span className="size-2 rounded-full bg-primary" />
          <p>
            Welcome to Workly.{' '}
            <span className="text-muted-foreground">Deliver a first task and this page turns into your dashboard.</span>
          </p>
        </div>
        <ol className="flex flex-col">
          <Step n={1} title="Deliver your first task" state={task ? 'active' : 'todo'}>
            <QueuesGettingStarted />
            <FirstTask task={task} loading={latest.isPending} />
          </Step>
          <Step
            n={2}
            title="Run something on a schedule"
            optional
            state={schedules.length > 0 ? 'done' : 'todo'}
            action={
              schedules.length === 0 && (
                <Button size="sm" variant="ghost" onClick={() => setCreating(true)}>
                  <Plus />
                  New schedule
                </Button>
              )
            }
          >
            <p className="text-sm text-muted-foreground">
              A schedule calls a URL on a cron timetable; each call is a run, retried and recorded like a task.
            </p>
          </Step>
          <Step
            n={3}
            title="Get alerted when deliveries fail"
            optional
            state={alerting ? 'done' : 'todo'}
            action={
              !alerting && (
                <Link to="/alerts" className="group/link flex items-center gap-1 text-xs text-primary hover:underline">
                  Set up alerts
                  <ArrowRight className="size-3 transition-transform group-hover/link:translate-x-0.5" />
                </Link>
              )
            }
          >
            <p className="text-sm text-muted-foreground">
              Email or Slack, at most once per queue or schedule in a while, with what failed and why.
            </p>
          </Step>
        </ol>
      </section>
      <DeliveriesChart fresh />
    </>
  );
}

function Step({
  n,
  title,
  state,
  optional,
  action,
  children,
}: {
  n: number;
  title: string;
  state: 'todo' | 'active' | 'done';
  optional?: boolean;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <li className="flex gap-3 border-t border-border px-4 py-4 first:border-t-0">
      <span
        className={cn(
          'mt-px flex size-5 shrink-0 items-center justify-center rounded-full font-mono text-xs',
          state === 'done' ? 'bg-success/12 text-success' : 'bg-primary/12 text-primary',
        )}
      >
        {state === 'done' ? <Check className="size-3" /> : n}
      </span>
      <div className="flex min-w-0 grow flex-col gap-3">
        <div className="flex min-h-5 flex-wrap items-center justify-between gap-2">
          <h3 className={cn('font-medium', state === 'done' && 'text-muted-foreground line-through decoration-faint')}>
            {title}
            {optional && <span className="ml-2 text-xs font-normal text-faint">optional</span>}
          </h3>
          {action}
        </div>
        {state !== 'done' && <div className="flex min-w-0 flex-col gap-3 [&>div]:max-w-none">{children}</div>}
      </div>
    </li>
  );
}

/** How the newest task is doing: waiting for one, delivering, or why it failed. */
function FirstTask({ task, loading }: { task?: Task; loading: boolean }) {
  if (loading) return null;
  if (!task) {
    return (
      <p className="flex items-center gap-2 text-xs text-muted-foreground">
        <span className="size-1.5 animate-pulse rounded-full bg-primary" />
        Waiting for a task…
      </p>
    );
  }
  const state = displayState(task);
  const failing = task.state === 'failed' || (task.state === 'pending' && task.attempt_count > 0);
  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-border bg-surface px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <TaskStateBadge state={state} />
        <Link
          to={`/tasks/${task.id}`}
          title={task.id}
          className="font-mono text-muted-foreground hover:text-foreground hover:underline"
        >
          {shortId(task.id)}
        </Link>
        <span className="min-w-0 truncate font-mono text-faint" title={task.url}>
          {formatTarget(task.method, task.url)}
        </span>
      </div>
      {failing ? (
        <>
          <p className="min-w-0 truncate" title={task.last_error}>
            <LastResult result={task} />
            {task.last_error && task.last_status === undefined && (
              <span className="font-mono text-muted-foreground"> {task.last_error}</span>
            )}
          </p>
          <p className="text-muted-foreground">
            Fix the URL or the handler, then retry it from the task, or send another.
          </p>
        </>
      ) : (
        <p className="text-muted-foreground">Delivering… this page switches over as soon as it succeeds.</p>
      )}
    </div>
  );
}
