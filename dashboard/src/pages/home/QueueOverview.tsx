import { Link, useNavigate } from 'react-router';
import { cn } from 'cn';
import type { Queue } from '../../api';
import { Count } from '../../components/Count';
import { RowHint } from '../../components/RowHint';
import { Section } from '../../components/Section';
import { useActivity } from '../../hooks/useActivity';
import { useNow } from '../../hooks/useNow';
import { useQueueActivity } from '../../hooks/useQueueActivity';
import { useRange } from '../../hooks/useRange';
import { formatRate } from '../../lib/format';
import { behindBy } from '../../lib/home';
import { LOW_SUCCESS } from '../../lib/queues';
import { Badge } from '../../primitives/badge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';

const SHOWN = 5;

/**
 * The queues worth a look: the ones with failures or falling behind first,
 * then the busiest. What's wrong with them is in Status; this says how much
 * work they hold and how it's going.
 */
export function QueueOverview({ queues }: { queues: Queue[] }) {
  const navigate = useNavigate();
  const now = useNow(15_000);
  const { value: range, query } = useRange();
  const { series } = useActivity();
  const created = series?.totals ?? {};
  const pending = (q: Queue) => (q.stats?.due ?? 0) + (q.stats?.scheduled ?? 0);
  const trouble = (q: Queue) => ((q.stats?.failed ?? 0) > 0 ? 2 : behindBy(q, now) ? 1 : 0);
  const top = [...queues]
    .sort(
      (a, b) =>
        trouble(b) - trouble(a) ||
        (created[b.name] ?? 0) - (created[a.name] ?? 0) ||
        pending(b) - pending(a) ||
        a.name.localeCompare(b.name),
    )
    .slice(0, SHOWN);
  const done = useQueueActivity(top);

  return (
    <Section
      title="Queues"
      link={{ to: `/queues${query}`, label: queues.length > SHOWN ? `All ${queues.length} queues` : 'Queues' }}
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Queue</TableHead>
            <TableHead className="text-right" title="Tasks not run yet: due now, or scheduled for later">
              Pending
            </TableHead>
            <TableHead className="text-right" title="Tasks with a delivery in flight">
              Running
            </TableHead>
            <TableHead
              className="hidden text-right sm:table-cell"
              title={`Share of the tasks that finished in the last ${range} that succeeded`}
            >
              Success {range}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {top.map((q) => {
            const a = done.get(q.name);
            const finished = a ? a.succeeded + a.failed : 0;
            const rate = finished > 0 ? a!.succeeded / finished : undefined;
            return (
              <TableRow key={q.name} className="cursor-pointer" onClick={() => navigate(`/queues/${q.name}${query}`)}>
                <TableCell className="w-full max-w-0 min-w-32">
                  <div className="flex items-center gap-2">
                    <Link
                      to={`/queues/${q.name}${query}`}
                      title={q.name}
                      className="truncate font-medium"
                      onClick={(e) => e.stopPropagation()}
                    >
                      {q.name}
                    </Link>
                    {q.paused ? (
                      <Badge variant="warning">paused</Badge>
                    ) : (
                      behindBy(q, now) && <Badge variant="warning">behind</Badge>
                    )}
                    <RowHint />
                  </div>
                </TableCell>
                <TableCell className="text-right font-mono text-xs">
                  <Count n={pending(q)} />
                </TableCell>
                <TableCell className="text-right font-mono text-xs">
                  <Count n={q.stats?.running ?? 0} />
                </TableCell>
                <TableCell
                  className={cn(
                    'hidden text-right font-mono text-xs sm:table-cell',
                    rate !== undefined && rate < LOW_SUCCESS && 'text-destructive',
                  )}
                  title={a && `${a.succeeded.toLocaleString()} succeeded, ${a.failed.toLocaleString()} failed`}
                >
                  {rate === undefined ? <span className="text-faint">—</span> : formatRate(rate)}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </Section>
  );
}
