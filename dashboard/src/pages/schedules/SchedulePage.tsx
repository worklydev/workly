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
import { ConfirmButton } from '../../components/ConfirmButton';
import { PageHeader } from '../../components/PageHeader';
import { Properties, Property, PropertyGroups } from '../../components/Properties';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { Section } from '../../components/Section';
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
      <PageHeader title={schedule.name}>
        {schedule.paused && <Badge variant="warning">paused</Badge>}
        <ScheduleActions schedule={schedule} />
      </PageHeader>
      <PropertyGroups>
        <Properties title="Timetable">
          <Property label="Runs">{describeCron(schedule.cron) ?? <None />}</Property>
          <Property label="Cron">
            <span className="font-mono text-xs">{schedule.cron}</span>
          </Property>
          <Property label="Timezone">
            <span className="font-mono text-xs">{schedule.timezone}</span>
          </Property>
          <Property label="Next run">
            {schedule.next_run_at ? (
              <RelativeTime iso={schedule.next_run_at} />
            ) : (
              <span className="text-warning">paused</span>
            )}
          </Property>
          <Property label="If still running">
            {schedule.overlap === 'skip' ? 'skip the slot' : 'run alongside'}
          </Property>
          <Property label="If missed">{schedule.missed === 'skip' ? 'skip, record it' : 'catch up once'}</Property>
        </Properties>
        <Properties title="Delivery">
          <Property label="Max attempts">
            <span className="font-mono text-xs">{schedule.max_attempts}</span>
          </Property>
          <Property label="Backoff">
            <span className="font-mono text-xs">
              {formatSeconds(schedule.min_backoff_seconds)} → {formatSeconds(schedule.max_backoff_seconds)}, ×
              {schedule.backoff_multiplier}
            </span>
          </Property>
          <Property label="Timeout">
            <span className="font-mono text-xs">{formatSeconds(schedule.timeout_seconds)}</span>
          </Property>
          <Property label="Retry 4xx">{schedule.retry_on_4xx ? 'yes' : 'only 408 and 429'}</Property>
          <Property label="Recent runs">
            <RecentRunsOf schedule={schedule.name} />
          </Property>
        </Properties>
      </PropertyGroups>
      <Upcoming schedule={schedule} />
      <Request schedule={schedule} />
      <Runs schedule={schedule.name} />
    </>
  );
}

function None() {
  return <span className="text-faint">—</span>;
}

function RecentRunsOf({ schedule }: { schedule: string }) {
  const { data } = useQuery({
    ...listRunsOptions({ path: { schedule }, query: { limit: 12 } }),
    ...LIVE,
  });
  return <RecentRuns runs={data?.runs} />;
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
      <ConfirmButton
        title="Delete the schedule and all its runs. A run in flight finishes, but its result isn't recorded."
        confirmLabel="Delete schedule and runs"
        onConfirm={() => remove.mutate(path)}
        disabled={remove.isPending}
      >
        <Trash2 />
        Delete
      </ConfirmButton>
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
            <span className="w-44 font-mono text-xs">{formatDate(t)}</span>
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
