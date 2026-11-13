import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Layers, Plus } from 'lucide-react';
import {
  listQueuesOptions,
  listSchedulesOptions,
  listWorkflowsOptions,
  searchTasksOptions,
} from '../../api/@tanstack/react-query.gen';
import type { Queue, Schedule } from '../../api';
import { EmptyState } from '../../components/EmptyState';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText } from '../../components/QueryState';
import { RangeTabs } from '../../components/RangeTabs';
import { StatCard, StatStrip } from '../../components/StatCard';
import { useSetting } from '../../context';
import { useActivity } from '../../hooks/useActivity';
import { useQueueFailureGroups, useRunFailureGroups } from '../../hooks/useFailureGroups';
import { useNow } from '../../hooks/useNow';
import { useRange } from '../../hooks/useRange';
import { LIVE } from '../../lib/api';
import { COUNT_LIMIT, formatCount, formatDuration, formatRate } from '../../lib/format';
import { BEHIND_MS, workspaceStatus } from '../../lib/home';
import { LOW_SUCCESS } from '../../lib/queues';
import { Button } from '../../primitives/button';
import { EditPolicy } from '../queues/EditPolicy';
import { EditSchedule } from '../schedules/EditSchedule';
import { DeliveriesChart } from './DeliveriesChart';
import { GroupsOverview } from './GroupsOverview';
import { Onboarding } from './Onboarding';
import { QueueOverview } from './QueueOverview';
import { ScheduleOverview } from './ScheduleOverview';
import { Status } from './Status';

/**
 * The workspace at a glance, in the order you'd ask: does anything need me,
 * what's happening now, how have deliveries gone, and what's in the queues
 * and on the schedule.
 */
export function HomePage() {
  const queues = useQuery({ ...listQueuesOptions(), ...LIVE });
  const schedules = useQuery({ ...listSchedulesOptions(), ...LIVE });
  // Whether a delivery has ever worked here (as far as retention remembers).
  const succeeded = useQuery({ ...searchTasksOptions({ query: { state: 'succeeded', limit: 1 } }), ...LIVE });
  const activity = useActivity();
  const [creating, setCreating] = useState<'queue' | 'schedule'>();
  const now = useNow(15_000);

  const error = queues.error ?? schedules.error;
  const q = queues.data?.queues ?? [];
  const s = schedules.data?.schedules ?? [];
  const loaded = queues.data && schedules.data && succeeded.data && activity.data;
  const retention = activity.data?.retention_seconds;
  // Until something has been delivered, the page is the way there. A
  // workspace older than retention may have had its successes deleted, so
  // it isn't new just because none are left.
  const onboarding =
    loaded &&
    succeeded.data!.tasks.length === 0 &&
    !s.some((x) => x.last_run?.state === 'succeeded') &&
    q.every((x) => retention === undefined || now - Date.parse(x.created_at) < retention * 1000);

  return (
    <>
      <PageHeader title="Home" description="How your queues and schedules are doing.">
        <Button variant="ghost" onClick={() => setCreating('queue')}>
          <Plus />
          New queue
        </Button>
        <Button onClick={() => setCreating('schedule')}>
          <Plus />
          New schedule
        </Button>
      </PageHeader>
      <EditPolicy open={creating === 'queue'} onClose={() => setCreating(undefined)} />
      <EditSchedule open={creating === 'schedule'} onClose={() => setCreating(undefined)} />

      {error ? (
        <ErrorText error={error} />
      ) : !loaded ? (
        <HomeSkeleton />
      ) : onboarding ? (
        <Onboarding schedules={s} />
      ) : (
        <>
          <WorkspaceStatus queues={q} schedules={s} now={now} />
          <Stats queues={q} now={now} />
          <DeliveriesChart />
          <div className="grid gap-x-8 gap-y-8 lg:grid-cols-2">
            {q.length > 0 ? (
              <QueueOverview queues={q} />
            ) : (
              <EmptyState
                icon={Layers}
                title="No queues yet"
                hint="Enqueue a task and its queue is created, with its own retries, rate and concurrency limits."
                action={
                  <Button size="sm" onClick={() => setCreating('queue')}>
                    <Plus />
                    New queue
                  </Button>
                }
              />
            )}
            <ScheduleOverview schedules={s} />
          </div>
          <GroupsOverview />
        </>
      )}
    </>
  );
}

function WorkspaceStatus({ queues, schedules, now }: { queues: Queue[]; schedules: Schedule[]; now: number }) {
  const { value: range } = useRange();
  const { data } = useActivity();
  const queueFailures = useQueueFailureGroups();
  const runFailures = useRunFailureGroups();
  const workflows = useQuery({ ...listWorkflowsOptions(), ...LIVE });
  const created = data?.buckets.reduce((n, b) => n + Object.values(b.counts).reduce((a, c) => a + c, 0), 0);
  const status = workspaceStatus({
    queues,
    schedules,
    workflows: workflows.data?.workflows,
    queueFailures: queueFailures.groups,
    runFailures: runFailures.groups,
    created,
    range,
    now,
  });
  // Only asked when it's said: when the workspace has gone quiet.
  const latest = useQuery({
    ...searchTasksOptions({ query: { limit: 1 } }),
    enabled: status.tone === 'idle',
  });
  return <Status status={status} lastTaskAt={latest.data?.tasks[0]?.created_at} />;
}

/**
 * What's happening now, beside how the range went. Now is the queues' work
 * at this moment; the range counts tasks created in it and tasks that
 * finished in it, from the same activity the chart draws.
 */
function Stats({ queues, now }: { queues: Queue[]; now: number }) {
  const { value: range } = useRange();
  const { data } = useActivity();
  const setting = useSetting();
  const stat = (key: 'due' | 'scheduled' | 'running') =>
    Math.min(
      COUNT_LIMIT,
      queues.reduce((n, q) => n + (q.stats?.[key] ?? 0), 0),
    );
  const due = stat('due');
  const scheduled = stat('scheduled');
  const running = stat('running');
  const oldest = queues
    .filter((q) => !q.paused)
    .map((q) => q.stats?.oldest_due_at)
    .filter((t): t is string => t !== undefined)
    .sort()[0];
  const waited = oldest ? Math.max(0, now - Date.parse(oldest)) : 0;

  const total = (pick: (b: NonNullable<typeof data>['buckets'][number]) => number) =>
    data ? data.buckets.reduce((n, b) => n + pick(b), 0) : undefined;
  const created = total((b) => Object.values(b.counts).reduce((a, c) => a + c, 0));
  const succeeded = total((b) => b.succeeded);
  const failed = total((b) => b.failed);
  const finished = (succeeded ?? 0) + (failed ?? 0);
  const rate = finished > 0 ? succeeded! / finished : undefined;
  const kept = `Counts tasks still kept${setting('WORKLY_RETENTION')}.`;
  const show = (n: number | undefined) => (n === undefined ? '…' : formatCount(n));

  return (
    <div className="grid gap-x-6 gap-y-8 xl:grid-cols-[minmax(0,3fr)_minmax(0,4fr)]">
      <section className="flex flex-col gap-3">
        {/* As tall as the range tabs beside it, so the two strips line up. */}
        <div className="flex h-8 items-center">
          <h3 className="font-medium">Now</h3>
        </div>
        <StatStrip className="flex-1 grid-cols-3">
          <StatCard
            label="Due now"
            value={formatCount(due)}
            detail={oldest ? `oldest ${formatDuration(waited)}` : 'nothing waiting'}
            tone={waited > BEHIND_MS ? 'warning' : undefined}
            to={due > 0 ? '/tasks?state=pending' : undefined}
            title="Tasks ready to deliver, waiting for a free slot (concurrency or rate limit)"
          />
          <StatCard
            label="Scheduled"
            value={formatCount(scheduled)}
            detail="delayed or retrying"
            to={scheduled > 0 ? '/tasks?state=pending' : undefined}
            title="Tasks that run later: enqueued with a delay, or waiting out the backoff before a retry"
          />
          <StatCard
            label="Running"
            value={formatCount(running)}
            detail="deliveries in flight"
            to={running > 0 ? '/tasks?state=running' : undefined}
            title="Tasks whose delivery has started and not finished yet"
          />
        </StatStrip>
      </section>
      <section className="flex flex-col gap-3">
        <div className="flex h-8 items-center justify-between gap-4">
          <h3 className="font-medium">Last {range}</h3>
          <RangeTabs />
        </div>
        <StatStrip className="flex-1 grid-cols-2 md:grid-cols-4">
          <StatCard
            label="Created"
            value={show(created)}
            detail="enqueued"
            title={`Tasks enqueued in the last ${range}`}
          />
          <StatCard
            label="Succeeded"
            value={show(succeeded)}
            detail="finished"
            to={succeeded ? '/tasks?state=succeeded' : undefined}
            title={`Tasks that finished in the last ${range} and succeeded. ${kept}`}
          />
          <StatCard
            label="Failed"
            value={show(failed)}
            detail="finished"
            tone={failed ? 'danger' : undefined}
            to={failed ? '/failures' : undefined}
            title={`Tasks that finished in the last ${range} and failed, attempts used up or a response Workly doesn't retry. ${kept}`}
          />
          <StatCard
            label="Success rate"
            value={data === undefined ? '…' : rate === undefined ? '—' : formatRate(rate)}
            detail={data && rate === undefined ? 'nothing finished' : 'of finished'}
            tone={rate !== undefined && rate < LOW_SUCCESS ? 'danger' : undefined}
            title={`Succeeded out of succeeded and failed, in the last ${range}. ${kept}`}
          />
        </StatStrip>
      </section>
    </div>
  );
}

/** The page's shape while the first lists load, so nothing jumps when they arrive. */
function HomeSkeleton() {
  return (
    <div aria-hidden className="flex animate-pulse flex-col gap-8">
      <div className="h-12 rounded-lg bg-muted/60" />
      <div className="grid gap-6 xl:grid-cols-2">
        <div className="h-28 rounded-lg bg-muted/60" />
        <div className="h-28 rounded-lg bg-muted/60" />
      </div>
      <div className="h-44 rounded-lg bg-muted/40" />
    </div>
  );
}
