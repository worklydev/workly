import { useMutation, useQueryClient } from '@tanstack/react-query';
import { RotateCcw, X } from 'lucide-react';
import { Link, useNavigate } from 'react-router';
import { cancelRunMutation, retryRunMutation } from '../../api/@tanstack/react-query.gen';
import type { Run } from '../../api';
import { runFinished, type RunPages } from '../../lib/runs';
import { ConfirmButton } from '../../components/ConfirmButton';
import { CursorPager } from '../../components/ListControls';
import { useCursorPages } from '../../hooks/useCursorPages';
import { DisabledReason } from '../../components/DisabledReason';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { TaskStateBadge } from '../../components/states';
import { useNow } from '../../hooks/useNow';
import { errorMessage } from '../../lib/api';
import { formatDate, formatRelative, shortId } from '../../lib/format';
import { LastResult } from '../../components/LastResult';
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
                <Link to={`/runs/${r.id}`} state={from} title={r.id} onClick={(e) => e.stopPropagation()}>
                  {shortId(r.id)}
                </Link>
              </TableCell>
              <TableCell>
                <span className="flex items-center gap-2">
                  <TaskStateBadge state={displayState(r, now)} />
                  {r.trigger === 'manual' && <span className="text-xs text-faint">manual</span>}
                </span>
              </TableCell>
              {/* A cron slot is a time on the timetable, so the time itself names the run; how long ago is on hover. */}
              <TableCell className="font-mono text-xs text-muted-foreground">
                <span title={formatRelative(r.scheduled_for, now)}>
                  {formatDate(r.scheduled_for, { seconds: false })}
                </span>
              </TableCell>
              <TableCell className="text-right font-mono text-xs">{r.attempt_count}</TableCell>
              <TableCell className="max-w-56 truncate text-xs">
                <LastResult result={r} />
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
