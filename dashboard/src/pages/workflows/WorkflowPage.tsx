import { useState } from 'react';
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Play, Trash2 } from 'lucide-react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router';
import {
  deleteWorkflowMutation,
  getWorkflowOptions,
  listWorkflowRunsInfiniteOptions,
} from '../../api/@tanstack/react-query.gen';
import type { ListWorkflowRunsData, Workflow, WorkflowRunStatus } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { InlineFields } from '../../components/InlineFields';
import { CursorPager } from '../../components/ListControls';
import { ConfirmItem, MoreActions } from '../../components/MoreActions';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { RowHint } from '../../components/RowHint';
import { Section } from '../../components/Section';
import { WorkflowRunStatusBadge } from '../../components/states';
import { useCursorPages } from '../../hooks/useCursorPages';
import { PAGE_SIZE } from '../../hooks/useListPage';
import { errorMessage, LIVE } from '../../lib/api';
import { formatDuration, formatSeconds, formatTarget, sharedHost, shortId } from '../../lib/format';
import { WORKFLOW_QUEUE, workflowRunPaging } from '../../lib/workflows';
import { Button } from '../../primitives/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { Tabs, TabsList, TabsTab } from '../../primitives/tabs';
import { TriggerWorkflow } from './TriggerWorkflow';
import { WorkflowDag } from './WorkflowDag';

export function WorkflowPage() {
  const name = useParams().workflow!;
  const {
    data: workflow,
    error,
    isPending,
  } = useQuery({ ...getWorkflowOptions({ path: { workflow: name } }), ...LIVE });
  const [triggering, setTriggering] = useState(false);

  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;
  const queues = [...new Set(workflow.steps.map((s) => s.queue ?? WORKFLOW_QUEUE))];

  return (
    <>
      <div className="flex flex-col gap-3">
        <PageHeader
          title={workflow.name}
          description={
            <span className="font-mono text-xs">
              {workflow.steps.length} {workflow.steps.length === 1 ? 'step' : 'steps'} · v{workflow.version}
            </span>
          }
        >
          <Button variant="default" onClick={() => setTriggering(true)}>
            <Play />
            Run now
          </Button>
          <DeleteWorkflow workflow={workflow} />
        </PageHeader>
        <InlineFields
          items={[
            { label: 'Max duration', value: formatSeconds(workflow.max_duration_seconds) },
            {
              label: queues.length === 1 ? 'Queue' : 'Queues',
              value: queues.map((q, i) => (
                <span key={q}>
                  {i > 0 && ', '}
                  <Link to={`/queues/${encodeURIComponent(q)}`} className="hover:underline">
                    {q}
                  </Link>
                </span>
              )),
            },
            { label: 'Updated', value: <RelativeTime iso={workflow.updated_at} /> },
          ]}
        />
      </div>
      <TriggerWorkflow workflow={workflow.name} open={triggering} onClose={() => setTriggering(false)} />
      <Steps workflow={workflow} />
      <Runs workflow={workflow} />
    </>
  );
}

function Steps({ workflow: w }: { workflow: Workflow }) {
  const host = sharedHost(w.steps.map((s) => s.url));
  return (
    <Section title="Steps">
      <WorkflowDag
        nodes={w.steps.map((s) => ({
          name: s.name,
          depends_on: s.depends_on,
          detail: (
            <span className="font-mono" title={s.url}>
              {formatTarget(s.method ?? 'POST', s.url, host)}
            </span>
          ),
        }))}
      />
      <details className="group">
        <summary className="cursor-pointer text-xs text-muted-foreground hover:text-foreground">Definition</summary>
        <CodePanel className="mt-2">{JSON.stringify({ steps: w.steps }, null, 2)}</CodePanel>
      </details>
    </Section>
  );
}

function DeleteWorkflow({ workflow }: { workflow: Workflow }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const del = useMutation({
    ...deleteWorkflowMutation(),
    onSuccess: async () => {
      navigate('/workflows');
      await qc.invalidateQueries();
    },
  });
  return (
    <>
      {del.error && <span className="text-xs text-destructive">{errorMessage(del.error)}</span>}
      <MoreActions>
        <ConfirmItem
          icon={<Trash2 className="size-4" />}
          confirmLabel="Click again to delete"
          title="Runs are kept, and runs still going finish under the definition they started with."
          onConfirm={() => del.mutate({ path: { workflow: workflow.name } })}
        >
          Delete workflow
        </ConfirmItem>
      </MoreActions>
    </>
  );
}

const STATUSES: (WorkflowRunStatus | 'all')[] = ['all', 'running', 'succeeded', 'failed', 'cancelled'];

function Runs({ workflow }: { workflow: Workflow }) {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const status = (params.get('status') ?? 'all') as WorkflowRunStatus | 'all';
  const query: ListWorkflowRunsData['query'] = { workflow: workflow.name, limit: PAGE_SIZE };
  if (status !== 'all') query.status = status;
  const pages = useInfiniteQuery({
    ...listWorkflowRunsInfiniteOptions({ query }),
    ...workflowRunPaging,
    ...LIVE,
    placeholderData: keepPreviousData,
  });
  const page = useCursorPages(pages, (p) => p.workflow_runs);

  return (
    <Section title="Runs">
      <Tabs
        value={status}
        onValueChange={(value) => {
          const next = new URLSearchParams(params);
          if (value === 'all') next.delete('status');
          else next.set('status', String(value));
          setParams(next, { replace: true });
        }}
      >
        <TabsList>
          {STATUSES.map((s) => (
            <TabsTab key={s} value={s}>
              {s === 'all' ? 'All' : s.charAt(0).toUpperCase() + s.slice(1)}
            </TabsTab>
          ))}
        </TabsList>
      </Tabs>
      {pages.isPending ? (
        <Muted>Loading…</Muted>
      ) : pages.error ? (
        <ErrorText error={pages.error} />
      ) : page.items.length === 0 ? (
        <Muted>
          {status === 'all' ? 'No runs yet. Run it now, or start one from a schedule.' : `No ${status} runs.`}
        </Muted>
      ) : (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Run</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Started</TableHead>
                <TableHead className="text-right">Took</TableHead>
                <TableHead className="hidden sm:table-cell">Error</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {page.items.map((r) => (
                <TableRow key={r.id} className="cursor-pointer" onClick={() => navigate(`/workflow-runs/${r.id}`)}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Link
                        to={`/workflow-runs/${r.id}`}
                        title={r.id}
                        className="font-mono text-xs"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {shortId(r.id)}
                      </Link>
                      {r.schedule_run_id && <span className="text-xs text-faint">scheduled</span>}
                      {r.version !== workflow.version && (
                        <span className="font-mono text-xs text-faint" title="Ran under an earlier definition">
                          v{r.version}
                        </span>
                      )}
                      <RowHint />
                    </div>
                  </TableCell>
                  <TableCell>
                    <WorkflowRunStatusBadge status={r.status} />
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    <RelativeTime iso={r.created_at} />
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs text-muted-foreground">
                    {r.duration_ms !== undefined ? (
                      formatDuration(r.duration_ms)
                    ) : (
                      <span className="text-faint">—</span>
                    )}
                  </TableCell>
                  <TableCell
                    className="hidden max-w-80 truncate text-xs text-muted-foreground sm:table-cell"
                    title={r.error}
                  >
                    {r.error ?? <span className="text-faint">—</span>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <CursorPager page={page} noun="runs" />
        </>
      )}
    </Section>
  );
}
