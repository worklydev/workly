import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { cn } from 'cn';
import { RotateCcw, TriangleAlert } from 'lucide-react';
import { Link, useParams } from 'react-router';
import {
  cancelRunMutation,
  getRunDeliveryOptions,
  getRunOptions,
  getScheduleOptions,
  listRunAttemptsOptions,
  retryRunMutation,
} from '../../api/@tanstack/react-query.gen';
import type { Run, Schedule } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { ConfirmButton } from '../../components/ConfirmButton';
import { CopyButton } from '../../components/CopyButton';
import { DisabledReason } from '../../components/DisabledReason';
import { PageHeader } from '../../components/PageHeader';
import { InlineFields } from '../../components/InlineFields';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { CallbackMark } from '../../components/CallbackMark';
import { CallbackSection } from '../../components/CallbackSection';
import { Section } from '../../components/Section';
import { StatCard, StatStrip } from '../../components/StatCard';
import { StatusCode, TaskStateBadge } from '../../components/states';
import { useNow } from '../../hooks/useNow';
import { errorMessage, LIVE } from '../../lib/api';
import { toCurl } from '../../lib/curl';
import { formatDate, prettyBody } from '../../lib/format';
import { displayState } from '../../lib/states';
import { Button } from '../../primitives/button';
import { AttemptTable } from '../tasks/AttemptTable';
import { runFinished, SKIP_REASONS } from '../../lib/runs';

export function RunPage() {
  const id = useParams().id!;
  const { data: run, error, isPending } = useQuery({ ...getRunOptions({ path: { id } }), ...LIVE });
  const { data: schedule } = useQuery({
    ...getScheduleOptions({ path: { schedule: run?.schedule ?? '' } }),
    enabled: run !== undefined,
    ...LIVE,
  });
  const now = useNow(5_000);

  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;

  return (
    <>
      <div className="flex flex-col gap-3">
        <PageHeader
          title={
            <span className="flex min-w-0 items-center gap-1">
              <span className="truncate font-mono text-base">{run.id}</span>
              <CopyButton text={run.id} title="Copy run ID" />
            </span>
          }
          badge={<TaskStateBadge state={displayState(run, now)} />}
        >
          <RunActions run={run} />
        </PageHeader>
        <InlineFields
          items={[
            {
              label: 'Schedule',
              value: (
                <Link
                  to={`/schedules/${run.schedule}`}
                  className="underline decoration-border underline-offset-4 hover:text-primary hover:decoration-primary"
                >
                  {run.schedule}
                </Link>
              ),
            },
            { label: 'Trigger', value: run.trigger === 'manual' ? 'run by hand' : 'timetable' },
            { label: 'Scheduled for', value: formatDate(run.scheduled_for) },
            { label: 'Created', value: <RelativeTime iso={run.created_at} /> },
            {
              label: 'Callback',
              prose: true,
              value: run.callback_task_id ? (
                <CallbackMark taskId={run.callback_task_id} state={run.callback_state} />
              ) : schedule?.on_complete && run.state !== 'skipped' ? (
                <span className="text-xs text-muted-foreground">when the run finishes</span>
              ) : (
                <span className="text-faint">none</span>
              ),
            },
          ]}
        />
      </div>
      <Notice run={run} schedule={schedule} />
      <StatStrip className="grid-cols-2 sm:grid-cols-4">
        <StatCard label="Attempts" value={run.attempt_count} detail={`max ${schedule?.max_attempts ?? '…'}`} />
        <StatCard
          label="Last status"
          value={<StatusCode code={run.last_status} />}
          detail={run.last_error?.replace(/^HTTP \d{3} /, '') ?? run.last_outcome?.replace('_', ' ')}
          title={run.last_error}
        />
        <StatCard
          label="Finished"
          value={run.finished_at ? <RelativeTime iso={run.finished_at} /> : <None />}
          detail={run.finished_at ? formatDate(run.finished_at, { seconds: false }) : undefined}
        />
        <StatCard label="Next attempt" value={run.state === 'pending' ? <RelativeTime iso={run.run_at} /> : <None />} />
      </StatStrip>
      {run.workflow_run_id || !run.url ? <StartedWorkflow run={run} /> : <Request run={run} />}
      {schedule?.on_complete && run.state !== 'skipped' && (
        <CallbackSection
          callback={schedule.on_complete}
          taskId={run.callback_task_id}
          body={runSummary(run)}
          waiting="Waiting: it is enqueued when the run succeeds, fails or is cancelled."
          note={
            <>
              The schedule's callback, enqueued into <span className="font-mono">_callbacks</span> each time a run
              finishes, again after a retry by hand, and delivered with that queue's retries.
            </>
          }
        />
      )}
      <Section title="Attempts">
        <Attempts run={run} />
      </Section>
    </>
  );
}

/** A run's callback body as it is (or will be) sent. */
function runSummary(r: Run) {
  const finished = r.finished_at !== undefined;
  return {
    run_id: r.id,
    schedule: r.schedule,
    state: finished ? r.state : '…',
    trigger: r.trigger,
    scheduled_for: r.scheduled_for,
    attempt_count: r.attempt_count,
    ...(r.last_status !== undefined && { last_status: r.last_status }),
    ...(r.last_outcome && { last_outcome: r.last_outcome }),
    ...(r.last_error && { last_error: r.last_error }),
    finished_at: r.finished_at ?? '…',
  };
}

function None() {
  return <span className="text-faint">—</span>;
}

function RunActions({ run }: { run: Run }) {
  const qc = useQueryClient();
  const onSuccess = () => qc.invalidateQueries();
  const cancel = useMutation({ ...cancelRunMutation(), onSuccess });
  const retry = useMutation({ ...retryRunMutation(), onSuccess });
  const path = { path: { id: run.id } };
  const error = cancel.error ?? retry.error;
  const finished = runFinished(run);

  return (
    <>
      {error && <span className="text-xs text-destructive">{errorMessage(error)}</span>}
      <DisabledReason reason={run.state === 'running' ? 'An attempt is in flight; retry once it finishes.' : undefined}>
        <Button
          variant={run.state === 'failed' ? 'default' : 'secondary'}
          onClick={() => retry.mutate(path)}
          disabled={run.state === 'running' || retry.isPending}
        >
          <RotateCcw />
          {run.state === 'skipped' ? 'Run it now' : finished ? 'Retry' : 'Run now'}
        </Button>
      </DisabledReason>
      <DisabledReason reason={finished ? `A ${run.state} run can't be cancelled.` : undefined}>
        <ConfirmButton
          confirmLabel="Cancel run?"
          onConfirm={() => cancel.mutate(path)}
          disabled={finished || cancel.isPending}
        >
          Cancel
        </ConfirmButton>
      </DisabledReason>
    </>
  );
}

/** Why the run didn't go as planned, when there's a reason to show. */
function Notice({ run, schedule }: { run: Run; schedule?: Schedule }) {
  let tone: 'warning' | 'danger';
  let text: React.ReactNode;
  if (run.state === 'skipped' && run.skip_reason) {
    tone = 'warning';
    text = <>Skipped: {SKIP_REASONS[run.skip_reason].toLowerCase()} Retry it to deliver this slot now.</>;
  } else if (run.state === 'failed') {
    tone = 'danger';
    text = (
      <>
        Failed after {run.attempt_count} attempt
        {run.attempt_count === 1 ? '' : 's'}
        {run.last_error ? `: ${run.last_error}` : '.'}
      </>
    );
  } else if (run.state === 'pending' && schedule?.paused && run.trigger === 'schedule') {
    tone = 'warning';
    text = <>The schedule is paused, but runs already created still go.</>;
  } else {
    return null;
  }
  return (
    <div
      className={cn(
        'flex items-start gap-2 rounded-lg px-3 py-2',
        tone === 'warning' ? 'bg-warning/12 text-warning' : 'bg-destructive/12 text-destructive',
      )}
    >
      <TriangleAlert className="mt-0.5 size-4 shrink-0" />
      <span className="min-w-0 break-words">{text}</span>
    </div>
  );
}

function Request({ run }: { run: Run }) {
  const qc = useQueryClient();
  const body = run.payload !== undefined ? JSON.stringify(run.payload, null, 2) : run.body && prettyBody(run.body);
  const headers = Object.entries(run.headers)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
  // Fetched on click, so the signature is fresh.
  const curl = async () =>
    toCurl(
      await qc.fetchQuery({
        ...getRunDeliveryOptions({ path: { id: run.id } }),
        staleTime: 0,
      }),
    );

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
          <span className="min-w-0 truncate font-mono" title={run.url}>
            {run.method} {run.url}
          </span>
        }
      >
        {headers}
        {headers && body ? '\n\n' : ''}
        {body || (headers ? '' : <span className="text-faint">No headers or body</span>)}
      </CodePanel>
      <p className="text-xs text-faint">As the schedule had it when this run was created.</p>
    </Section>
  );
}

function Attempts({ run }: { run: Run }) {
  const { data, error, isPending } = useQuery({
    ...listRunAttemptsOptions({ path: { id: run.id } }),
    ...LIVE,
  });
  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;
  if (run.state === 'skipped' && data.attempts.length === 0) {
    return <p className="text-muted-foreground">Skipped runs aren't delivered, so there are no attempts.</p>;
  }
  return <AttemptTable task={run} attempts={data.attempts} />;
}

/** A run of a schedule that starts a workflow: the workflow run it started. */
function StartedWorkflow({ run }: { run: Run }) {
  return (
    <Section title="Workflow run">
      {run.workflow_run_id ? (
        <p className="text-sm">
          Started{' '}
          <Link to={`/workflow-runs/${run.workflow_run_id}`} className="font-mono text-xs hover:underline">
            {run.workflow_run_id}
          </Link>
          <span className="text-muted-foreground">: its steps and how they went are on its page.</span>
        </p>
      ) : (
        <Muted>Not started yet.</Muted>
      )}
    </Section>
  );
}
