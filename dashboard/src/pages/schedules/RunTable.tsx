import { useMutation, useQueryClient } from '@tanstack/react-query';
import { RotateCcw, X } from 'lucide-react';
import { Link, useNavigate } from 'react-router';
import { cancelRunMutation, retryRunMutation } from '../../api/@tanstack/react-query.gen';
import type { Run } from '../../api';
import { runFinished, SKIP_REASONS, type RunPages } from '../../lib/runs';
import { ConfirmButton } from '../../components/ConfirmButton';
import { CursorPager } from '../../components/ListControls';
import { useCursorPages } from '../../hooks/useCursorPages';
import { DisabledReason } from '../../components/DisabledReason';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { StatusCode, TaskStateBadge } from '../../components/states';
import { useNow } from '../../hooks/useNow';
import { errorMessage } from '../../lib/api';
import { formatDate } from '../../lib/format';
import { useFromHere } from '../../lib/from';
import { displayState } from '../../lib/states';
import { Button } from '../../primitives/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';

export function RunTable({ pages, empty }: { pages: RunPages; empty: string }) {
  const navigate = useNavigate();
  const from = useFromHere();
  const now = useNow(5_000);
  const page = useCursorPages(pages, (p) => p.runs);
  if (pages.isPending) return <Muted>Loading…</Muted>;
  if (pages.error) return <ErrorText error={pages.error} />;
  const runs = page.items;
  if (runs.length === 0) return <Muted>{empty}</Muted>;

  return (
    <div className="flex flex-col gap-3">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Run</TableHead>
            <TableHead>State</TableHead>
            <TableHead title="The cron slot, or when the run was started by hand">Scheduled for</TableHead>
            <TableHead className="text-right">Attempts</TableHead>
            <TableHead>Last result</TableHead>
            <TableHead>Finished</TableHead>
            <TableHead className="w-0">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((r) => (
            <TableRow key={r.id} className="cursor-pointer" onClick={() => navigate(`/runs/${r.id}`, { state: from })}>
              <TableCell className="font-mono text-xs">
                <Link to={`/runs/${r.id}`} state={from} onClick={(e) => e.stopPropagation()}>
                  {r.id}
                </Link>
              </TableCell>
              <TableCell>
                <span className="flex items-center gap-2">
                  <TaskStateBadge state={displayState(r, now)} />
                  {r.trigger === 'manual' && <span className="text-xs text-faint">manual</span>}
                </span>
              </TableCell>
              <TableCell className="text-xs text-muted-foreground" title={formatDate(r.scheduled_for)}>
                <RelativeTime iso={r.scheduled_for} />
              </TableCell>
              <TableCell className="text-right font-mono text-xs">{r.attempt_count}</TableCell>
              <TableCell className="max-w-56 truncate text-xs">
                <LastResult run={r} />
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {r.finished_at ? <RelativeTime iso={r.finished_at} /> : <span className="text-faint">—</span>}
              </TableCell>
              <TableCell className="py-0" onClick={(e) => e.stopPropagation()}>
                <RowActions run={r} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <CursorPager page={page} noun="runs" />
    </div>
  );
}

/** Why a run was skipped, or its latest status with the reason. */
function LastResult({ run: r }: { run: Run }) {
  if (r.state === 'skipped' && r.skip_reason) {
    return (
      <span className="text-warning" title={SKIP_REASONS[r.skip_reason]}>
        {r.skip_reason === 'overlap' ? 'previous run still going' : 'server was down'}
      </span>
    );
  }
  if (r.last_status !== undefined) {
    const reason = r.last_error?.match(/^HTTP \d{3} (.+)$/)?.[1];
    return (
      <span className="font-mono" title={r.last_error}>
        <StatusCode code={r.last_status} />
        {reason && <span className="font-sans text-faint"> {reason}</span>}
      </span>
    );
  }
  if (r.last_outcome && r.last_outcome !== 'success') {
    return (
      <span className={r.last_outcome === 'lost' ? 'text-warning' : 'text-destructive'} title={r.last_error}>
        {r.last_outcome.replace('_', ' ')}
      </span>
    );
  }
  return <span className="text-faint">—</span>;
}

function RowActions({ run }: { run: Run }) {
  const qc = useQueryClient();
  const onSuccess = () => qc.invalidateQueries();
  const retry = useMutation({ ...retryRunMutation(), onSuccess });
  const cancel = useMutation({ ...cancelRunMutation(), onSuccess });
  const path = { path: { id: run.id } };
  const error = retry.error ?? cancel.error;
  const label = runFinished(run) ? (run.state === 'skipped' ? 'Run it now' : 'Retry') : 'Run now';

  return (
    <div className="flex items-center justify-end gap-0.5">
      {error && (
        <span className="text-xs text-destructive" title={errorMessage(error)}>
          failed
        </span>
      )}
      <DisabledReason reason={run.state === 'running' ? 'An attempt is in flight; retry once it finishes.' : undefined}>
        <Button
          variant="ghost"
          size="icon"
          title={label}
          aria-label={label}
          onClick={() => retry.mutate(path)}
          disabled={run.state === 'running' || retry.isPending}
        >
          <RotateCcw />
        </Button>
      </DisabledReason>
      <DisabledReason reason={runFinished(run) ? `A ${run.state} run can't be cancelled.` : undefined}>
        <ConfirmButton
          size="icon"
          title="Cancel"
          confirmLabel="Cancel?"
          onConfirm={() => cancel.mutate(path)}
          disabled={runFinished(run) || cancel.isPending}
        >
          <X />
        </ConfirmButton>
      </DisabledReason>
    </div>
  );
}
