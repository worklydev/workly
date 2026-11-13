import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Layers, Plus } from 'lucide-react';
import { listQueuesOptions, listSchedulesOptions } from '../../api/@tanstack/react-query.gen';
import type { Queue } from '../../api';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText } from '../../components/QueryState';
import { RangeTabs } from '../../components/RangeTabs';
import { StatCard, StatStrip } from '../../components/StatCard';
import { useSetting } from '../../context';
import { useNow } from '../../hooks/useNow';
import { useQueueActivity } from '../../hooks/useQueueActivity';
import { useRange } from '../../hooks/useRange';
import { LIVE } from '../../lib/api';
import { COUNT_LIMIT, formatCount, formatRate } from '../../lib/format';
import { workspaceHealth } from '../../lib/home';
import { LOW_SUCCESS } from '../../lib/queues';
import { Button } from '../../primitives/button';
import { ActivityChart } from '../queues/ActivityChart';
import { useActivity } from '../../hooks/useActivity';
import { EditPolicy } from '../queues/EditPolicy';
import { QueuesGettingStarted } from '../queues/GettingStarted';
import { Totals } from '../queues/Totals';
import { EditSchedule } from '../schedules/EditSchedule';
import { SchedulesGettingStarted } from '../schedules/GettingStarted';
import { ComingUp } from './ComingUp';
import { GroupsOverview } from './GroupsOverview';
import { HealthLine } from './HealthLine';
import { NeedsAttention } from './NeedsAttention';
import { QueueOverview } from './QueueOverview';
import { ScheduleOverview } from './ScheduleOverview';
import { EmptyState } from '../../components/EmptyState';

/**
 * Everything in the workspace at a glance, in the order you'd ask about it:
 * is anything wrong, what's happening now, what broke, what's next.
 */
export function HomePage() {
  const queues = useQuery({ ...listQueuesOptions(), ...LIVE });
  const schedules = useQuery({ ...listSchedulesOptions(), ...LIVE });
  const [creating, setCreating] = useState<'queue' | 'schedule'>();
  const now = useNow(15_000);

  const error = queues.error ?? schedules.error;
  const loaded = queues.data && schedules.data;
  const q = queues.data?.queues ?? [];
  const s = schedules.data?.schedules ?? [];
  const health = loaded ? workspaceHealth(q, s, now) : undefined;

  return (
    <>
      <PageHeader title="Home" description="Everything in this workspace at a glance, live.">
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
      ) : !health ? (
        <HomeSkeleton />
      ) : health.tone === 'new' ? (
        <>
          <HealthLine health={health} />
          <Welcome />
        </>
      ) : (
        <>
          <HealthLine health={health} />
          <Stats queues={q} />
          <ActivityChart tabs={false} />
          <div className="grid gap-x-8 gap-y-8 lg:grid-cols-2">
            <NeedsAttention />
            <ComingUp schedules={s} />
          </div>
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
          {s.length > 0 && <ScheduleOverview schedules={s} />}
          <GroupsOverview />
        </>
      )}
    </>
  );
}

/** Range stats count over this many queues at most, one request each. */
const ACTIVITY_QUEUES = 50;

/** What's happening now, beside how the range went, as on a queue's page. */
function Stats({ queues }: { queues: Queue[] }) {
  const { value: range } = useRange();
  const { series } = useActivity();
  const counted = queues.slice(0, ACTIVITY_QUEUES);
  const activity = useQueueActivity(counted);
  const setting = useSetting();
  const ready = activity.size === counted.length;
  const sum = (key: 'succeeded' | 'failed') =>
    Math.min(
      COUNT_LIMIT,
      [...activity.values()].reduce((n, a) => n + a[key], 0),
    );
  const succeeded = sum('succeeded');
  const failed = sum('failed');
  const rate = succeeded + failed > 0 ? succeeded / (succeeded + failed) : undefined;
  const partial = queues.length > counted.length ? ` Counts the first ${ACTIVITY_QUEUES} queues.` : '';
  const kept = `Finished tasks are kept for a while${setting('WORKLY_RETENTION')}.${partial}`;

  return (
    <div className="grid gap-x-6 gap-y-8 xl:grid-cols-2">
      <section className="flex flex-col gap-3">
        {/* As tall as the range tabs beside it, so the two strips line up. */}
        <div className="flex h-8 items-center">
          <h3 className="font-medium">Now</h3>
        </div>
        <Totals queues={queues} />
      </section>
      <section className="flex flex-col gap-3">
        <div className="flex h-8 items-center justify-between gap-4">
          <h3 className="font-medium">Last {range}</h3>
          <RangeTabs />
        </div>
        <StatStrip className="flex-1 grid-cols-2 md:grid-cols-4">
          <StatCard label="Created" value={series ? formatCount(series.total) : '…'} />
          <StatCard
            label="Succeeded"
            value={ready ? formatCount(succeeded) : '…'}
            to={ready && succeeded > 0 ? '/tasks?state=succeeded' : undefined}
            title={kept}
          />
          <StatCard
            label="Failed"
            value={ready ? formatCount(failed) : '…'}
            tone={ready && failed > 0 ? 'danger' : undefined}
            to={ready && failed > 0 ? '/failures' : undefined}
            title={kept}
          />
          <StatCard
            label="Success rate"
            value={!ready ? '…' : rate === undefined ? '—' : formatRate(rate)}
            detail={ready && rate === undefined ? 'nothing finished' : undefined}
            tone={rate !== undefined && rate < LOW_SUCCESS ? 'danger' : undefined}
            title={kept}
          />
        </StatStrip>
      </section>
    </div>
  );
}

/** A brand-new workspace: the two ways to start, side by side, and where activity will show. */
function Welcome() {
  return (
    <>
      <div className="grid gap-4 lg:grid-cols-2">
        <WelcomeCard step="1" title="Queue a task">
          <QueuesGettingStarted />
        </WelcomeCard>
        <WelcomeCard step="2" title="Run something on a schedule">
          <SchedulesGettingStarted />
        </WelcomeCard>
      </div>
      <ActivityChart tabs={false} fresh />
    </>
  );
}

function WelcomeCard({ step, title, children }: { step: string; title: string; children: React.ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-3 rounded-lg border border-border bg-card/50 p-4 dark:bg-card">
      <h3 className="flex items-center gap-2.5 font-medium">
        <span className="flex size-5 items-center justify-center rounded-sm bg-primary/12 font-mono text-xs text-primary">
          {step}
        </span>
        {title}
      </h3>
      {/* The getting-started blocks size themselves for a page; in a card they fill it. */}
      <div className="min-w-0 [&>div]:max-w-none">{children}</div>
    </section>
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
