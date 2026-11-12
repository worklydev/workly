import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { RotateCcw, X } from 'lucide-react';
import { Link, useNavigate } from 'react-router';
import { cn } from 'cn';
import { cancelTask, retryTask } from '../api';
import { cancelTaskMutation, retryTaskMutation } from '../api/@tanstack/react-query.gen';
import type { Task } from '../api';
import { useNow } from '../hooks/useNow';
import { errorMessage } from '../lib/api';
import { useFromHere } from '../lib/from';
import { formatTarget, sharedHost, shortId } from '../lib/format';
import { displayState } from '../lib/states';
import type { TaskPages } from '../lib/tasks';
import type { SortField, TaskSorting } from '../lib/taskSort';
import { Button } from '../primitives/button';
import { Checkbox as CheckboxPrimitive } from '../primitives/checkbox';
import { SortableTableHead, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../primitives/table';
import { ConfirmButton } from './ConfirmButton';
import { CursorPager } from './ListControls';
import { useCursorPages } from '../hooks/useCursorPages';
import { DisabledReason } from './DisabledReason';
import { SortSummary } from './SortSummary';
import { ErrorText, Muted } from './QueryState';
import { RelativeTime } from './RelativeTime';
import { TaskStateBadge } from './states';
import { LastResult } from './LastResult';

type Props = {
  pages: TaskPages;
  showQueue?: boolean;
  empty: string;
  /** Whether a queue is paused, so its pending tasks don't read as about to run. */
  paused?: (queue: string) => boolean;
  /** Makes the columns it can sort by clickable. */
  sorting?: TaskSorting;
};

export function TaskTable({ pages, showQueue, empty, paused = () => false, sorting }: Props) {
  const navigate = useNavigate();
  const from = useFromHere();
  // Scheduled tasks become due without their data changing.
  const now = useNow(5_000);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const page = useCursorPages(pages, (p) => p.tasks);
  if (pages.isPending) return <Muted>Loading…</Muted>;
  if (pages.error) return <ErrorText error={pages.error} />;
  const tasks = page.items;
  if (tasks.length === 0) return <Muted>{empty}</Muted>;

  /** A column header, clickable when the list can sort by its field. */
  const head = (field: SortField, label: string, props: React.ComponentProps<'th'> = {}) =>
    sorting?.fields.includes(field) ? (
      <SortableTableHead
        {...props}
        direction={sorting.sort.field === field ? sorting.sort.order : undefined}
        next={field === 'run_at' ? 'asc' : 'desc'}
        onSort={() => sorting.toggle(field)}
      >
        {label}
      </SortableTableHead>
    ) : (
      <TableHead {...props}>{label}</TableHead>
    );

  // When every task on the page calls the same host, the path is what differs.
  const host = sharedHost(tasks.map((t) => t.url));

  // The table is too wide for a small laptop, more so with a Queue column, so
  // Last run goes first, unless the list is sorted by it.
  const lastRun =
    sorting?.sort.field === 'last_attempt_at'
      ? undefined
      : showQueue
        ? 'hidden 2xl:table-cell'
        : 'hidden xl:table-cell';

  // With a Queue column, Attempts and Target wait for a wider screen too.
  const wide = showQueue ? 'hidden xl:table-cell' : undefined;

  // Selections of tasks no longer listed (e.g. after a filter change) don't count.
  const chosen = tasks.filter((t) => selected.has(t.id));
  const allChosen = chosen.length === tasks.length;
  const toggle = (id: string) => {
    const next = new Set(selected);
    if (!next.delete(id)) next.add(id);
    setSelected(next);
  };

  return (
    <div className="flex flex-col gap-3">
      <BulkActions tasks={chosen} sorting={sorting} onDone={() => setSelected(new Set())} />
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-0">
              <Checkbox
                checked={allChosen}
                indeterminate={chosen.length > 0 && !allChosen}
                onChange={() => setSelected(allChosen ? new Set() : new Set(tasks.map((t) => t.id)))}
                label="Select all tasks on this page"
              />
            </TableHead>
            <TableHead>Task</TableHead>
            {showQueue && <TableHead>Queue</TableHead>}
            <TableHead>State</TableHead>
            {head('attempt_count', 'Attempts', { className: cn('text-right', wide) })}
            <TableHead>Last result</TableHead>
            {head('run_at', 'Next run')}
            {head('last_attempt_at', 'Last run', { title: 'When the latest attempt finished', className: lastRun })}
            {head('created_at', 'Created')}
            <TableHead className={wide}>Target</TableHead>
            <TableHead className="w-0">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {tasks.map((t) => (
            <TableRow
              key={t.id}
              className="cursor-pointer data-[state=selected]:bg-muted/40"
              data-state={selected.has(t.id) ? 'selected' : undefined}
              onClick={() => navigate(`/tasks/${t.id}`, { state: from })}
            >
              <TableCell onClick={(e) => e.stopPropagation()}>
                <Checkbox checked={selected.has(t.id)} onChange={() => toggle(t.id)} label={`Select ${t.id}`} />
              </TableCell>
              <TableCell className="font-mono text-xs whitespace-nowrap">
                <Link to={`/tasks/${t.id}`} state={from} title={t.id} onClick={(e) => e.stopPropagation()}>
                  {shortId(t.id)}
                </Link>
              </TableCell>
              {showQueue && (
                <TableCell className="max-w-32 truncate" title={t.queue}>
                  {t.queue}
                </TableCell>
              )}
              <TableCell>
                <TaskStateBadge state={displayState(t, now)} />
              </TableCell>
              <TableCell className={cn('text-right font-mono text-xs', wide)}>{t.attempt_count}</TableCell>
              <TableCell className="max-w-64 truncate text-xs">
                <LastResult result={t} />
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                <NextRun task={t} now={now} paused={paused(t.queue)} />
              </TableCell>
              <TableCell className={cn('text-xs text-muted-foreground', lastRun)}>
                {t.last_attempt_at ? <RelativeTime iso={t.last_attempt_at} /> : <span className="text-faint">—</span>}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                <RelativeTime iso={t.created_at} />
              </TableCell>
              {/* Takes the width left over, at least 5rem, rather than widening the table. */}
              <TableCell
                className={cn('w-full max-w-0 min-w-20 truncate font-mono text-xs text-faint', wide)}
                title={t.url}
              >
                {formatTarget(t.method, t.url, host)}
              </TableCell>
              <TableCell className="py-0" onClick={(e) => e.stopPropagation()}>
                <RowActions task={t} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <CursorPager page={page} noun="tasks" />
    </div>
  );
}

function Checkbox({
  checked,
  indeterminate = false,
  onChange,
  label,
}: {
  checked: boolean;
  indeterminate?: boolean;
  onChange: () => void;
  label: string;
}) {
  return <CheckboxPrimitive aria-label={label} checked={checked} indeterminate={indeterminate} onChange={onChange} />;
}

/** When a pending task runs next; a running one is in flight, a finished one won't run. */
export function NextRun({ task, now, paused = false }: { task: Task; now: number; paused?: boolean }) {
  if (task.state === 'pending') {
    if (!paused) return <RelativeTime iso={task.run_at} />;
    const later = new Date(task.run_at).getTime() > now;
    return (
      <span className="text-warning" title="The queue is paused. The task runs once it's resumed.">
        {later ? (
          <>
            <RelativeTime iso={task.run_at} />, paused
          </>
        ) : (
          'paused'
        )}
      </span>
    );
  }
  if (task.state === 'running') return <span className="text-primary">running</span>;
  return <span className="text-faint">—</span>;
}

const finished = (t: Task) => t.state === 'succeeded' || t.state === 'failed' || t.state === 'cancelled';

function RowActions({ task }: { task: Task }) {
  const qc = useQueryClient();
  const onSuccess = () => qc.invalidateQueries();
  const retry = useMutation({ ...retryTaskMutation(), onSuccess });
  const cancel = useMutation({ ...cancelTaskMutation(), onSuccess });
  const path = { path: { id: task.id } };
  const error = retry.error ?? cancel.error;

  return (
    <div className="flex items-center justify-end gap-0.5 opacity-60 transition-opacity group-hover/row:opacity-100 focus-within:opacity-100">
      {error && (
        <span className="text-xs text-destructive" title={errorMessage(error)}>
          failed
        </span>
      )}
      <DisabledReason
        reason={task.state === 'running' ? 'An attempt is in flight; retry once it finishes.' : undefined}
      >
        <Button
          variant="ghost"
          size="icon"
          title={finished(task) ? 'Retry' : 'Run now'}
          aria-label={finished(task) ? 'Retry' : 'Run now'}
          onClick={() => retry.mutate(path)}
          disabled={task.state === 'running' || retry.isPending}
        >
          <RotateCcw />
        </Button>
      </DisabledReason>
      <DisabledReason reason={finished(task) ? `A ${task.state} task can't be cancelled.` : undefined}>
        <ConfirmButton
          size="icon"
          title="Cancel"
          confirmLabel="Cancel?"
          onConfirm={() => cancel.mutate(path)}
          disabled={finished(task) || cancel.isPending}
        >
          <X />
        </ConfirmButton>
      </DisabledReason>
    </div>
  );
}

/** Runs fn over ids, a few at a time, and returns how many failed. */
async function forEachLimit(ids: string[], fn: (id: string) => Promise<unknown>, limit = 5): Promise<number> {
  let next = 0;
  let failed = 0;
  const worker = async () => {
    while (next < ids.length) {
      const id = ids[next++];
      await fn(id).catch(() => failed++);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, ids.length) }, worker));
  return failed;
}

type Bulk = { action: 'retry' | 'cancel'; ids: string[] };

/** Retry or cancel the selected tasks; each applies only to the tasks it can act on. */
/** The list's sort sits in the same row, so the table has one line of controls above it, not two. */
function BulkActions({ tasks, sorting, onDone }: { tasks: Task[]; sorting?: TaskSorting; onDone: () => void }) {
  const qc = useQueryClient();
  const [result, setResult] = useState<string>();
  const retryable = tasks.filter((t) => t.state !== 'running');
  const cancellable = tasks.filter((t) => !finished(t));
  const run = useMutation({
    mutationFn: async ({ action, ids }: Bulk) => {
      const call = action === 'retry' ? retryTask : cancelTask;
      const failed = await forEachLimit(ids, (id) => call({ path: { id }, throwOnError: true }));
      return { action, done: ids.length - failed, failed };
    },
    onSuccess: ({ action, done, failed }) => {
      const verb = action === 'retry' ? 'Retried' : 'Cancelled';
      setResult(`${verb} ${done} ${done === 1 ? 'task' : 'tasks'}${failed ? `; ${failed} failed` : ''}.`);
      onDone();
      qc.invalidateQueries();
    },
  });
  const none = tasks.length === 0 ? 'Select tasks to act on several at once.' : undefined;

  return (
    <div className="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-1 text-xs">
      <span className={tasks.length ? 'text-foreground' : 'text-faint'}>
        {tasks.length ? `${tasks.length} selected` : 'None selected'}
      </span>
      {result && !tasks.length && <span className="text-muted-foreground">{result}</span>}
      <div className="ml-auto flex flex-wrap items-center gap-x-1 gap-y-1">
        {/* On phones the column headers' arrows say it; the row is for the actions. */}
        {sorting && (
          <div className="mr-3 hidden sm:block">
            <SortSummary sorting={sorting} />
          </div>
        )}
        <DisabledReason
          reason={none ?? (retryable.length ? undefined : 'Running tasks retry once their attempt finishes.')}
        >
          <Button
            variant="ghost"
            size="sm"
            onClick={() => run.mutate({ action: 'retry', ids: retryable.map((t) => t.id) })}
            disabled={!retryable.length || run.isPending}
          >
            <RotateCcw />
            {retryable.length ? `Retry ${retryable.length}` : 'Retry'}
          </Button>
        </DisabledReason>
        <DisabledReason reason={none ?? (cancellable.length ? undefined : 'Finished tasks can’t be cancelled.')}>
          <ConfirmButton
            confirmLabel={`Cancel ${cancellable.length}?`}
            onConfirm={() => run.mutate({ action: 'cancel', ids: cancellable.map((t) => t.id) })}
            disabled={!cancellable.length || run.isPending}
          >
            {cancellable.length ? `Cancel ${cancellable.length}` : 'Cancel'}
          </ConfirmButton>
        </DisabledReason>
      </div>
    </div>
  );
}
