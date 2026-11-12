import { useState } from 'react';
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Pause, Pencil, Play, Trash2 } from 'lucide-react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router';
import {
  deleteScheduleMutation,
  getScheduleOptions,
  listRunsInfiniteOptions,
  listRunsOptions,
  pauseScheduleMutation,
  resumeScheduleMutation,
  triggerScheduleMutation,
} from '../../api/@tanstack/react-query.gen';
import type { ListRunsData, RunState, Schedule } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { ConfirmItem, MoreActions } from '../../components/MoreActions';
import { LastResult } from '../../components/LastResult';
import { TaskStateBadge } from '../../components/states';
import { PageHeader } from '../../components/PageHeader';
import { InlineFields } from '../../components/InlineFields';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { Section } from '../../components/Section';
import { StatCard, StatStrip } from '../../components/StatCard';
import { errorMessage, LIVE } from '../../lib/api';
import { describeCron } from '../../lib/cron';
import { formatDate, formatSeconds, prettyBody } from '../../lib/format';
import { useFromHere } from '../../lib/from';
import { Badge } from '../../primitives/badge';
import { Button } from '../../primitives/button';
import { Tabs, TabsList, TabsTab } from '../../primitives/tabs';
import { EditSchedule } from './EditSchedule';
import { RecentRuns } from './RecentRuns';
import { runPaging } from '../../lib/runs';
import { RunTable } from './RunTable';
import { PAGE_SIZE } from '../../hooks/useListPage';

const STATES: (RunState | 'all')[] = ['all', 'pending', 'running', 'succeeded', 'failed', 'skipped', 'cancelled'];

export function SchedulePage() {
  const name = useParams().schedule!;
  const {
    data: schedule,
    error,
    isPending,
  } = useQuery({
    ...getScheduleOptions({ path: { schedule: name } }),
    ...LIVE,
  });

  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;

  return (
    <>
      {/* What the schedule is set to sits with the title; below it, how it's doing. */}
      <div className="flex flex-col gap-3">
        <PageHeader
          title={schedule.name}
          description={
            <span className="flex flex-wrap items-baseline gap-x-2">
              <span className="text-foreground">{describeCron(schedule.cron) ?? 'Custom timetable'}</span>
              <span className="font-mono text-xs">
                {schedule.cron} · {schedule.timezone}
              </span>
            </span>
          }
          badge={schedule.paused && <Badge variant="warning">paused</Badge>}
        >
          <ScheduleActions schedule={schedule} />
        </PageHeader>
        <InlineFields
          items={[
            { label: 'Max attempts', value: schedule.max_attempts },
            {
              label: 'Backoff',
              value: `${formatSeconds(schedule.min_backoff_seconds)} → ${formatSeconds(schedule.max_backoff_seconds)}, ×${schedule.backoff_multiplier}`,
            },
            { label: 'Timeout', value: formatSeconds(schedule.timeout_seconds) },
            { label: 'Retry 4xx', value: schedule.retry_on_4xx ? 'yes' : 'only 408 and 429', prose: true },
            {
              label: 'If still running',
              value: schedule.overlap === 'skip' ? 'skip the slot' : 'run alongside',
              prose: true,
            },
            {
              label: 'If missed',
              value: schedule.missed === 'skip' ? 'skip, record it' : 'catch up once',
              prose: true,
            },
          ]}
        />
      </div>
      <Overview schedule={schedule} />
      <div className="grid gap-8 md:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Request schedule={schedule} />
        <Upcoming schedule={schedule} />
      </div>
      <Runs schedule={schedule.name} />
    </>
  );
}

/** When it runs next, how the last run went, and the latest dozen at a glance. */
function Overview({ schedule }: { schedule: Schedule }) {
  const { data } = useQuery({
    ...listRunsOptions({ path: { schedule: schedule.name }, query: { limit: 12 } }),
    ...LIVE,
  });
  const runs = data?.runs;
  const last = runs?.[0];
  const count = (state: RunState) => runs?.filter((r) => r.state === state).length ?? 0;
  const tally = (['succeeded', 'failed', 'skipped'] as const)
    .filter((s) => count(s) > 0)
    .map((s) => `${count(s)} ${s}`)
    .join(' · ');

  return (
    <StatStrip className="grid-cols-1 sm:grid-cols-3">
      <StatCard
        label="Next run"
        value={
          schedule.next_run_at ? (
            <RelativeTime iso={schedule.next_run_at} />
          ) : (
            <span className="text-warning">paused</span>
          )
        }
        detail={schedule.next_run_at ? formatDate(schedule.next_run_at, { seconds: false }) : 'resume it to run again'}
      />
      <StatCard
        label="Last run"
        value={
          last ? (
            // A state is a word, not a number: a badge, at the height of the mono values beside it.
            <span className="flex h-7 items-center gap-2 font-sans text-sm">
              <TaskStateBadge state={last.state} />
              <span className="text-muted-foreground">
                <RelativeTime iso={last.scheduled_for} />
              </span>
            </span>
          ) : (
            <span className="text-faint">—</span>
          )
        }
        detail={last ? <LastResult result={last} /> : runs ? 'no runs yet' : undefined}
        to={last ? `/runs/${last.id}` : undefined}
      />
      <StatCard
        label="Recent runs"
        value={
          <div className="flex h-7 items-center">
            <RecentRuns runs={runs} />
          </div>
        }
        detail={tally || undefined}
      />
    </StatStrip>
  );
}

function ScheduleActions({ schedule }: { schedule: Schedule }) {
  const qc = useQueryClient();
  const onSuccess = () => qc.invalidateQueries();
  const pause = useMutation({ ...pauseScheduleMutation(), onSuccess });
  const resume = useMutation({ ...resumeScheduleMutation(), onSuccess });
  const trigger = useMutation({ ...triggerScheduleMutation(), onSuccess });
  const navigate = useNavigate();
  const remove = useMutation({
    ...deleteScheduleMutation(),
    onSuccess: () => {
      // Leave first, so the page doesn't refetch a schedule that's gone.
      navigate('/schedules', { replace: true });
      return qc.invalidateQueries();
    },
  });
  const from = useFromHere();
  const path = { path: { schedule: schedule.name } };
  const error = pause.error ?? resume.error ?? trigger.error ?? remove.error;
  const [editing, setEditing] = useState(false);

  return (
    <>
      {error && <span className="text-xs text-destructive">{errorMessage(error)}</span>}
      {trigger.data && (
        <span className="text-xs text-muted-foreground">
          Started{' '}
          <Link to={`/runs/${trigger.data.id}`} state={from} className="font-mono underline underline-offset-4">
            {trigger.data.id}
          </Link>
        </span>
      )}
      <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
        <Pencil />
        Edit
      </Button>
      <EditSchedule schedule={schedule} open={editing} onClose={() => setEditing(false)} />
      {schedule.paused ? (
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
      <Button
        title="Call the endpoint now, outside the timetable. Works while paused, and doesn't move the next run."
        onClick={() => trigger.mutate(path)}
        disabled={trigger.isPending}
      >
        <Play />
        Run now
      </Button>
      <MoreActions label="More schedule actions">
        <ConfirmItem
          icon={<Trash2 />}
          title="Delete the schedule and all its runs. A run in flight finishes, but its result isn't recorded."
          confirmLabel="Delete schedule and runs?"
          onConfirm={() => remove.mutate(path)}
        >
          Delete schedule
        </ConfirmItem>
      </MoreActions>
    </>
  );
}

/** The next slots, so a cron expression can be checked at a glance. */
function Upcoming({ schedule }: { schedule: Schedule }) {
  return (
    <Section title={schedule.paused ? 'Upcoming, if resumed now' : 'Upcoming'}>
      <ol className="flex flex-col gap-1.5">
        {schedule.next_runs.map((t) => (
          <li key={t} className="flex items-center gap-3 text-sm">
            <span className="w-36 font-mono text-xs">{formatDate(t, { seconds: false })}</span>
            <span className="text-xs text-muted-foreground">
              <RelativeTime iso={t} />
            </span>
          </li>
        ))}
      </ol>
      <p className="text-xs text-faint">Times are shown in your browser's timezone.</p>
    </Section>
  );
}

function Request({ schedule: s }: { schedule: Schedule }) {
  const body = s.payload !== undefined ? JSON.stringify(s.payload, null, 2) : s.body && prettyBody(s.body);
  const headers = Object.entries(s.headers)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
  return (
    <Section title="Request">
      <CodePanel
        title={
          <span className="min-w-0 truncate font-mono" title={s.url}>
            {s.method} {s.url}
          </span>
        }
      >
        {headers}
        {headers && body ? '\n\n' : ''}
        {body || (headers ? '' : <span className="text-faint">No headers or body</span>)}
      </CodePanel>
      <p className="text-xs text-faint">
        Each run also gets Workly-Run-Id, Workly-Schedule, Workly-Scheduled-For and a signature.
      </p>
    </Section>
  );
}

function Runs({ schedule }: { schedule: string }) {
  const [params, setParams] = useSearchParams();
  const state = (params.get('state') ?? 'all') as RunState | 'all';
  const query: ListRunsData['query'] = state === 'all' ? { limit: PAGE_SIZE } : { state, limit: PAGE_SIZE };
  const pages = useInfiniteQuery({
    ...listRunsInfiniteOptions({ path: { schedule }, query }),
    ...runPaging,
    ...LIVE,
    placeholderData: keepPreviousData,
  });

  return (
    <Section title="Runs">
      <Tabs
        value={state}
        onValueChange={(value) => {
          const next = new URLSearchParams(params);
          if (value === 'all') next.delete('state');
          else next.set('state', String(value));
          setParams(next, { replace: true });
        }}
      >
        <TabsList>
          {STATES.map((s) => (
            <TabsTab key={s} value={s}>
              {s === 'all' ? 'All' : s.charAt(0).toUpperCase() + s.slice(1)}
            </TabsTab>
          ))}
        </TabsList>
      </Tabs>
      <RunTable pages={pages} empty={state === 'all' ? 'No runs yet.' : `No ${state} runs.`} />
    </Section>
  );
}
