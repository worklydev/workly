import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router';
import { listWorkflowsOptions } from '../../api/@tanstack/react-query.gen';
import type { Workflow } from '../../api';
import { StartHere } from '../../components/StartHere';
import { workflowExamples, workflowYaml } from '../../lib/examples';
import { ListPager, ListSearch } from '../../components/ListControls';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { RowHint } from '../../components/RowHint';
import { WorkflowRunStatusBadge } from '../../components/states';
import { useListPage } from '../../hooks/useListPage';
import { LIVE } from '../../lib/api';
import { formatSeconds } from '../../lib/format';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { RecentWorkflowRuns } from './RecentWorkflowRuns';
import { useRecentWorkflowRuns } from './useRecentWorkflowRuns';

export function WorkflowsPage() {
  const { data, error, isPending } = useQuery({ ...listWorkflowsOptions(), ...LIVE });
  const workflows = data?.workflows ?? [];
  return (
    <>
      <PageHeader title="Workflows" description="HTTP steps run in dependency order, each delivered as a task." />
      {isPending ? (
        <Muted>Loading…</Muted>
      ) : error ? (
        <ErrorText error={error} />
      ) : workflows.length === 0 ? (
        <GettingStarted />
      ) : (
        <WorkflowList workflows={workflows} />
      )}
    </>
  );
}

function WorkflowList({ workflows }: { workflows: Workflow[] }) {
  const list = useListPage(workflows, (w) => [w.name, ...w.steps.map((s) => s.name)]);
  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end empty:hidden">
        <ListSearch list={list} placeholder="Search" />
      </div>
      {list.items.length === 0 ? (
        <Muted>No workflows match “{list.search}”.</Muted>
      ) : (
        <WorkflowTable workflows={list.items} />
      )}
      <ListPager list={list} noun="workflows" />
    </div>
  );
}

const WIDE = 'hidden sm:table-cell';

function WorkflowTable({ workflows }: { workflows: Workflow[] }) {
  const navigate = useNavigate();
  const recent = useRecentWorkflowRuns(workflows);
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Workflow</TableHead>
          <TableHead className={`text-right ${WIDE}`}>Steps</TableHead>
          <TableHead className={WIDE}>Max duration</TableHead>
          <TableHead>Last run</TableHead>
          <TableHead title="The latest runs, oldest first">Recent</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {workflows.map((w) => (
          <TableRow
            key={w.name}
            className="cursor-pointer"
            onClick={() => navigate(`/workflows/${encodeURIComponent(w.name)}`)}
          >
            <TableCell className="py-2">
              <div className="flex items-center gap-2">
                <Link
                  to={`/workflows/${encodeURIComponent(w.name)}`}
                  className="font-medium"
                  onClick={(e) => e.stopPropagation()}
                >
                  {w.name}
                </Link>
                <RowHint />
              </div>
              <div className="font-mono text-xs text-faint">v{w.version}</div>
            </TableCell>
            <TableCell className={`text-right font-mono text-xs ${WIDE}`}>{w.steps.length}</TableCell>
            <TableCell className={`font-mono text-xs text-muted-foreground ${WIDE}`}>
              {formatSeconds(w.max_duration_seconds)}
            </TableCell>
            <TableCell>
              {w.last_run ? (
                <span className="flex items-center gap-2 text-xs text-muted-foreground">
                  <WorkflowRunStatusBadge status={w.last_run.status} />
                  <RelativeTime iso={w.last_run.created_at} />
                </span>
              ) : (
                <span className="text-xs text-faint">never</span>
              )}
            </TableCell>
            <TableCell>
              <RecentWorkflowRuns runs={recent.get(w.name)} phoneSlots={8} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function GettingStarted() {
  return (
    <StartHere
      title="No workflows yet"
      intro="A workflow runs HTTP steps in dependency order: each step is delivered as a task once the steps it depends on have succeeded."
      code={workflowExamples}
      yaml={workflowYaml}
      yamlIntro={
        <>
          Or keep it in <span className="font-mono text-xs">workly.yaml</span> with your queues and schedules, applied
          with <span className="font-mono text-xs">workly deploy</span>.
        </>
      }
      docs={[
        ['Workflows', 'workflows'],
        ['Configuration as code', 'deploy'],
      ]}
    />
  );
}
