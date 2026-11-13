import { Link, useNavigate } from 'react-router';
import { cn } from 'cn';
import type { Queue } from '../../api';
import { Sparkline } from '../../components/Chart';
import { Count } from '../../components/Count';
import { RowHint } from '../../components/RowHint';
import { Section } from '../../components/Section';
import { useQueueActivity } from '../../hooks/useQueueActivity';
import { useRange } from '../../hooks/useRange';
import { formatRate } from '../../lib/format';
import { LOW_SUCCESS } from '../../lib/queues';
import { Badge } from '../../primitives/badge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { useActivity } from '../../hooks/useActivity';
import { LongestWait } from '../queues/LongestWait';

const SHOWN = 5;

const busy = (q: Queue) => (q.stats?.due ?? 0) + (q.stats?.scheduled ?? 0) + (q.stats?.running ?? 0);

/** The busiest queues: most tasks created in the range, then most work right now. */
export function QueueOverview({ queues }: { queues: Queue[] }) {
  const navigate = useNavigate();
  const { value: range, query } = useRange();
  const { data, series } = useActivity();
  const created = series?.totals ?? {};
  const top = [...queues]
    .sort(
      (a, b) =>
        (created[b.name] ?? 0) - (created[a.name] ?? 0) ||
        busy(b) - busy(a) ||
        (b.stats?.failed ?? 0) - (a.stats?.failed ?? 0) ||
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
            <TableHead className="hidden md:table-cell" title={`Tasks created in the last ${range}`}>
              Created {range}
            </TableHead>
            <TableHead className="text-right" title="Tasks not run yet: due now, or scheduled for later">
              Pending
            </TableHead>
            <TableHead className="text-right">Running</TableHead>
            <TableHead className="text-right">Failed</TableHead>
            <TableHead className="hidden text-right sm:table-cell">Longest wait</TableHead>
            <TableHead className="text-right" title={`Share of the tasks finished in the last ${range} that succeeded`}>
              Success
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {top.map((q) => {
            const a = done.get(q.name);
            const finished = a ? a.succeeded + a.failed : 0;
            const rate = finished > 0 ? a!.succeeded / finished : undefined;
            const values = data?.buckets.map((b) => b.counts[q.name] ?? 0);
            const total = created[q.name] ?? 0;
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
                    {q.paused && <Badge variant="warning">paused</Badge>}
                    <RowHint />
                  </div>
                </TableCell>
                <TableCell className="hidden md:table-cell">
                  <div className="flex items-center gap-2">
                    <Sparkline
                      values={values}
                      color={series?.color[q.name] ?? 'bg-chart-other'}
                      title={`${total.toLocaleString()} created in the last ${range}`}
                    />
                    <span className="w-10 font-mono text-xs tabular-nums">
                      <Count n={total} />
                    </span>
                  </div>
                </TableCell>
                <TableCell className="text-right font-mono text-xs">
                  <Count n={(q.stats?.due ?? 0) + (q.stats?.scheduled ?? 0)} />
                </TableCell>
                <TableCell className="text-right font-mono text-xs">
                  <Count n={q.stats?.running ?? 0} />
                </TableCell>
                <TableCell className="text-right font-mono text-xs">
                  <Count n={q.stats?.failed ?? 0} tone="danger" />
                </TableCell>
                <TableCell className="hidden text-right font-mono text-xs sm:table-cell">
                  <LongestWait since={q.stats?.oldest_due_at} />
                </TableCell>
                <TableCell
                  className={cn(
                    'text-right font-mono text-xs',
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
