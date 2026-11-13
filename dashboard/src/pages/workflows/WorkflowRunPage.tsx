import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { XCircle } from 'lucide-react';
import { Link, useParams } from 'react-router';
import { cancelWorkflowRunMutation, getGroupOptions, getWorkflowRunOptions } from '../../api/@tanstack/react-query.gen';
import type { WorkflowRun, WorkflowStep } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { CopyButton } from '../../components/CopyButton';
import { InlineFields } from '../../components/InlineFields';
import { ConfirmItem, MoreActions } from '../../components/MoreActions';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { Section } from '../../components/Section';
import { StatusCode, StepStatusBadge, WorkflowRunStatusBadge } from '../../components/states';
import { useNow } from '../../hooks/useNow';
import { errorMessage, LIVE } from '../../lib/api';
import { useFromHere } from '../../lib/from';
import { formatDuration, shortId } from '../../lib/format';
import { stepDetail, stepDuration } from '../../lib/workflows';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { GroupProgress } from '../groups/GroupProgress';
import { WorkflowDag } from './WorkflowDag';

export function WorkflowRunPage() {
  const id = useParams().id!;
  const { data: run, error, isPending } = useQuery({ ...getWorkflowRunOptions({ path: { id } }), ...LIVE });
  const from = useFromHere();
  const now = useNow(5_000);

  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;
  const waiting = run.steps.filter((s) => s.status === 'waiting' && s.wait_for);

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
          description={
            <>
              Run of{' '}
              <Link to={`/workflows/${encodeURIComponent(run.workflow)}`} className="text-foreground hover:underline">
                {run.workflow}
              </Link>{' '}
              <span className="font-mono text-xs">v{run.version}</span>
            </>
          }
          badge={<WorkflowRunStatusBadge status={run.status} />}
        >
          <CancelRun run={run} />
        </PageHeader>
        <InlineFields
          items={[
            { label: 'Started', value: <RelativeTime iso={run.created_at} /> },
            {
              label: run.finished_at ? 'Took' : 'Running for',
              value: run.duration_ms !== undefined ? formatDuration(run.duration_ms) : '—',
            },
            ...(run.status === 'running' && run.deadline_at
              ? [{ label: 'Fails', value: <RelativeTime iso={run.deadline_at} /> }]
              : []),
            ...(run.schedule_run_id
              ? [
                  {
                    label: 'Started by',
                    value: (
                      <Link to={`/runs/${run.schedule_run_id}`} title={run.schedule_run_id} className="hover:underline">
                        {shortId(run.schedule_run_id)}
                      </Link>
                    ),
                  },
                ]
              : []),
            // A scheduled run's key is its schedule run's ID, already shown as Started by.
            ...(run.idempotency_key && run.idempotency_key !== run.schedule_run_id
              ? [{ label: 'Idempotency key', value: run.idempotency_key }]
              : []),
          ]}
        />
      </div>
      {run.error && (
        <p className="text-sm text-muted-foreground">
          <span className={run.status === 'failed' ? 'text-destructive' : undefined}>{run.error}</span>
          {run.status === 'failed' && ' Steps not yet started were skipped; requests already in flight finished.'}
        </p>
      )}
      <Section title="Steps">
        <WorkflowDag
          nodes={run.steps.map((s) => ({
            name: s.name,
            depends_on: s.depends_on,
            status: s.status,
            detail: stepDetail(s, now),
            meta: stepDuration(s),
            to: s.task_id ? `/tasks/${s.task_id}` : undefined,
            state: from,
          }))}
        />
      </Section>
      {waiting.length > 0 && (
        <Section title="Waiting for">
          <div className="flex flex-col gap-2">
            {waiting.map((s) => (
              <WaitingFor key={s.id} step={s} />
            ))}
          </div>
        </Section>
      )}
      <StepTable run={run} now={now} />
      <Section title="Input">
        {run.input === undefined ? (
          <Muted>No input.</Muted>
        ) : (
          <CodePanel>{JSON.stringify(run.input, null, 2)}</CodePanel>
        )}
      </Section>
    </>
  );
}

/** A step handed its completion to a group: how far the group has got. */
function WaitingFor({ step }: { step: WorkflowStep }) {
  const id = step.wait_for!.id;
  const group = useQuery({ ...getGroupOptions({ path: { id } }), ...LIVE });
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
      <span className="font-medium">{step.name}</span>
      <span className="text-muted-foreground">
        waits for group{' '}
        <Link to={`/groups/${id}`} className="font-mono text-xs text-foreground hover:underline">
          {group.data?.name ?? id}
        </Link>
      </span>
      {group.data && <GroupProgress group={group.data} />}
      {group.data?.status === 'open' && (
        <span className="text-xs text-faint">open: it completes only once it's closed</span>
      )}
      {step.waiting_ms !== undefined && (
        <span className="font-mono text-xs text-faint">for {formatDuration(step.waiting_ms)}</span>
      )}
    </div>
  );
}

function StepTable({ run, now }: { run: WorkflowRun; now: number }) {
  const from = useFromHere();
  return (
    <Section title="Details">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Step</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="hidden text-right sm:table-cell">HTTP</TableHead>
            <TableHead className="hidden text-right sm:table-cell">Attempts</TableHead>
            <TableHead className="text-right">Took</TableHead>
            <TableHead className="hidden md:table-cell">Detail</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {run.steps.map((s) => (
            <TableRow key={s.id}>
              <TableCell className="py-2">
                <div className="font-medium">{s.name}</div>
                {s.task_id ? (
                  <Link
                    to={`/tasks/${s.task_id}`}
                    state={from}
                    title={s.task_id}
                    className="font-mono text-xs text-faint hover:text-foreground"
                  >
                    {shortId(s.task_id)}
                  </Link>
                ) : (
                  <span className="font-mono text-xs text-faint" title="The step's ID, stable across retries">
                    {shortId(s.id)}
                  </span>
                )}
              </TableCell>
              <TableCell>
                <StepStatusBadge status={s.status} />
              </TableCell>
              <TableCell className="hidden text-right font-mono text-xs sm:table-cell">
                <StatusCode code={s.task?.last_status} />
              </TableCell>
              <TableCell className="hidden text-right font-mono text-xs sm:table-cell">
                {s.task ? s.task.attempt_count : <span className="text-faint">—</span>}
              </TableCell>
              <TableCell className="text-right font-mono text-xs text-muted-foreground">
                {stepDuration(s) ?? <span className="text-faint">—</span>}
              </TableCell>
              <TableCell
                className={`hidden max-w-96 truncate text-xs md:table-cell ${s.status === 'failed' ? 'text-destructive' : 'text-muted-foreground'}`}
                title={stepDetail(s, now)}
              >
                {stepDetail(s, now) ?? <span className="text-faint">—</span>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Section>
  );
}

function CancelRun({ run }: { run: WorkflowRun }) {
  const qc = useQueryClient();
  const cancel = useMutation({
    ...cancelWorkflowRunMutation(),
    onSuccess: () => qc.invalidateQueries(),
  });
  const reason = run.status === 'running' ? undefined : `Already ${run.status}.`;
  return (
    <>
      {cancel.error && <span className="text-xs text-destructive">{errorMessage(cancel.error)}</span>}
      <MoreActions>
        <ConfirmItem
          icon={<XCircle className="size-4" />}
          confirmLabel="Click again to cancel the run"
          disabledReason={reason}
          title="Steps not yet started are skipped. Requests in flight finish."
          onConfirm={() => cancel.mutate({ path: { id: run.id } })}
        >
          Cancel run
        </ConfirmItem>
      </MoreActions>
    </>
  );
}
