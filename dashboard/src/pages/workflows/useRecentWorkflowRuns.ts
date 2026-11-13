import { useQueries } from '@tanstack/react-query';
import { listWorkflowRunsOptions } from '../../api/@tanstack/react-query.gen';
import type { Workflow, WorkflowRunSummary } from '../../api';
import { LIVE } from '../../lib/api';

/** How many recent runs a workflow's strip shows. */
export const RECENT = 12;

export function useRecentWorkflowRuns(workflows: Workflow[]): Map<string, WorkflowRunSummary[]> {
  const results = useQueries({
    queries: workflows.map((w) => ({
      ...listWorkflowRunsOptions({ query: { workflow: w.name, limit: RECENT } }),
      ...LIVE,
    })),
  });
  return new Map(workflows.flatMap((w, i) => (results[i].data ? [[w.name, results[i].data.workflow_runs]] : [])));
}
