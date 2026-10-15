import { useRef, useState } from 'react';
import { ChevronDown, ChevronUp, RotateCcw } from 'lucide-react';
import type { Attempt, RunState, Task } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { ListPager } from '../../components/ListControls';
import { usePage } from '../../hooks/usePage';
import { Properties, Property } from '../../components/Properties';
import { RelativeTime } from '../../components/RelativeTime';
import { OutcomeBadge, StatusCode } from '../../components/states';
import { formatDate, formatDuration, prettyBody } from '../../lib/format';
import { Badge } from '../../primitives/badge';
import { Button } from '../../primitives/button';
import { Dialog, DialogContent, DialogTitle } from '../../primitives/dialog';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';

const ms = (iso: string) => new Date(iso).getTime();

/** What the attempt table needs of a task or a schedule run. */
export type AttemptOwner = Pick<Task, 'attempt_count' | 'run_at' | 'retried_at' | 'last_attempt_at'> & {
  state: Task['state'] | RunState;
};

/** Attempts newest first, with manual retries as divider rows. A row opens the attempt's response. */
export function AttemptTable({ task, attempts }: { task: AttemptOwner; attempts: Attempt[] }) {
  const [openId, setOpenId] = useState<string | null>(null);
  const newest = [...attempts].reverse();
  const list = usePage(newest);
  const upcoming = task.state === 'pending' || task.state === 'running';
  if (attempts.length === 0 && !upcoming) {
    return <p className="text-muted-foreground">No attempts were made.</p>;
  }

  const newestFirst = newest;
  // The wait before each automatic retry: from the previous attempt's end to this one's start.
  const waits = new Map(
    newestFirst.slice(0, -1).map((a, i) => [a.id, ms(a.started_at) - ms(newestFirst[i + 1].finished_at)]),
  );
  // A retry the next attempt hasn't picked up yet.
  const retryPending =
    upcoming &&
    task.retried_at !== undefined &&
    (!task.last_attempt_at || ms(task.retried_at) > ms(task.last_attempt_at));
  const openIndex = newestFirst.findIndex((a) => a.id === openId);

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Attempt</TableHead>
            <TableHead>Result</TableHead>
            <TableHead className="text-right">Status</TableHead>
            <TableHead className="w-full">Response</TableHead>
            <TableHead className="text-right">Duration</TableHead>
            <TableHead className="text-right" title="Time between the previous attempt ending and this one starting">
              Waited
            </TableHead>
            <TableHead className="text-right">Started</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.page === 0 && upcoming && <UpcomingRow task={task} />}
          {list.page === 0 && retryPending && <RetryRow at={task.retried_at!} first={attempts.length === 0} />}
          {list.items.map((a) => (
            <AttemptRows key={a.id} attempt={a} wait={waits.get(a.id)} onOpen={() => setOpenId(a.id)} />
          ))}
        </TableBody>
      </Table>
      <ListPager list={list} noun="attempts" />
      <Dialog open={openIndex >= 0} onOpenChange={(open) => !open && setOpenId(null)}>
        {openIndex >= 0 && (
          <AttemptDialog
            attempt={newestFirst[openIndex]}
            newer={newestFirst[openIndex - 1]}
            older={newestFirst[openIndex + 1]}
            onSelect={(a) => setOpenId(a.id)}
          />
        )}
      </Dialog>
    </>
  );
}

function UpcomingRow({ task }: { task: AttemptOwner }) {
  const running = task.state === 'running';
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell className="text-muted-foreground">
        Attempt {running ? task.attempt_count : task.attempt_count + 1}
      </TableCell>
      <TableCell>
        {running ? (
          <Badge variant="accent" dot>
            in flight
          </Badge>
        ) : (
          <Badge variant="neutral">scheduled</Badge>
        )}
      </TableCell>
      <TableCell colSpan={4} />
      <TableCell className="text-right text-xs text-muted-foreground">
        {running ? 'now' : <RelativeTime iso={task.run_at} />}
      </TableCell>
    </TableRow>
  );
}

function AttemptRows({ attempt: a, wait, onOpen }: { attempt: Attempt; wait?: number; onOpen: () => void }) {
  const preview = responsePreview(a);
  return (
    <>
      <TableRow className="cursor-pointer" onClick={onOpen}>
        <TableCell>
          {/* The row handles the click; the button makes it reachable by keyboard. */}
          <button type="button" className="cursor-pointer font-medium">
            Attempt {a.number}
          </button>
        </TableCell>
        <TableCell>
          <OutcomeBadge outcome={a.outcome} />
        </TableCell>
        <TableCell className="text-right font-mono text-xs">
          <StatusCode code={a.status_code} />
        </TableCell>
        <TableCell className="max-w-0 truncate font-mono text-xs text-muted-foreground" title={preview}>
          {preview ?? <span className="text-faint">—</span>}
        </TableCell>
        <TableCell className="text-right font-mono text-xs text-muted-foreground">
          {formatDuration(a.duration_ms)}
        </TableCell>
        <TableCell className="text-right font-mono text-xs text-muted-foreground">
          {wait !== undefined && !a.retried_at ? (
            formatDuration(Math.max(0, wait))
          ) : (
            <span className="text-faint">—</span>
          )}
        </TableCell>
        <TableCell className="text-right text-xs text-muted-foreground">
          <RelativeTime iso={a.started_at} />
        </TableCell>
      </TableRow>
      {a.retried_at && <RetryRow at={a.retried_at} first={a.number === 1} />}
    </>
  );
}

/** Where someone retried the task by hand, between two attempts. */
function RetryRow({ at, first }: { at: string; first: boolean }) {
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell colSpan={7} className="h-8 bg-muted/40 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <RotateCcw className="size-3.5" />
          {first ? 'Run manually' : 'Retried manually'}
          <span className="text-faint">
            · <RelativeTime iso={at} />
          </span>
        </span>
      </TableCell>
    </TableRow>
  );
}

/** "HTTP 500 Internal Server Error" only repeats the status code. */
function redundant(a: Attempt): boolean {
  return a.outcome === 'http_error' && a.error !== undefined && /^HTTP \d{3}( [\w' -]*)?$/.test(a.error);
}

/** One line on why the attempt ended as it did: the error, else the start of the response body. */
function responsePreview(a: Attempt): string | undefined {
  if (a.error && !redundant(a)) return a.error;
  const body = a.response_body?.replace(/\s+/g, ' ').trim();
  return body || undefined;
}

function AttemptDialog({
  attempt: a,
  newer,
  older,
  onSelect,
}: {
  attempt: Attempt;
  newer?: Attempt;
  older?: Attempt;
  onSelect: (a: Attempt) => void;
}) {
  const headers = Object.entries(a.response_headers)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
  const hasResponse = a.response_body !== undefined || headers !== '';
  // Focus the dialog itself, not its first button, so the arrow keys work
  // without a focus ring on "Newer".
  const popup = useRef<HTMLDivElement>(null);

  const onKeyDown = (e: React.KeyboardEvent) => {
    const next =
      e.key === 'ArrowUp' || e.key === 'ArrowLeft'
        ? newer
        : e.key === 'ArrowDown' || e.key === 'ArrowRight'
          ? older
          : undefined;
    if (next) {
      e.preventDefault();
      onSelect(next);
    }
  };

  return (
    <DialogContent ref={popup} initialFocus={popup} onKeyDown={onKeyDown}>
      <div className="flex flex-wrap items-center gap-3 pr-8">
        <DialogTitle className="text-base font-medium">Attempt {a.number}</DialogTitle>
        <OutcomeBadge outcome={a.outcome} />
        <span className="font-mono text-xs">
          <StatusCode code={a.status_code} />
        </span>
      </div>
      <Properties title="Details">
        <Property label="Started">{formatDate(a.started_at)}</Property>
        <Property label="Duration">
          <span className="font-mono text-xs">{formatDuration(a.duration_ms)}</span>
        </Property>
        {a.retried_at && <Property label="Retried manually">{formatDate(a.retried_at)}</Property>}
        {a.error && (
          <Property label="Error">
            <span className="whitespace-normal">{a.error}</span>
          </Property>
        )}
      </Properties>
      <section className="flex flex-col gap-2">
        <h3 className="text-xs text-muted-foreground">Response</h3>
        {hasResponse ? (
          <CodePanel>
            {headers}
            {headers && a.response_body ? '\n\n' : ''}
            {a.response_body && prettyBody(a.response_body)}
          </CodePanel>
        ) : (
          <p className="text-muted-foreground">No response was received.</p>
        )}
        {a.response_body !== undefined && <p className="text-xs text-faint">The first 4 KB of the body are kept.</p>}
      </section>
      <div className="flex items-center justify-between gap-2 border-t border-border pt-4">
        <span className="text-xs text-faint">↑ ↓ to move between attempts</span>
        <div className="flex gap-2">
          <Button variant="ghost" size="sm" disabled={!newer} onClick={() => newer && onSelect(newer)}>
            <ChevronUp />
            Newer
          </Button>
          <Button variant="ghost" size="sm" disabled={!older} onClick={() => older && onSelect(older)}>
            <ChevronDown />
            Older
          </Button>
        </div>
      </div>
    </DialogContent>
  );
}
