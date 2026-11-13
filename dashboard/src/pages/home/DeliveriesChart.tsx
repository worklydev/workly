import { useState } from 'react';
import { cn } from 'cn';
import type { Activity } from '../../api';
import { ChartAxis, ChartEmpty, ChartFrame, ChartSkeleton } from '../../components/Chart';
import { ErrorText } from '../../components/QueryState';
import { useSetting } from '../../context';
import { useActivity } from '../../hooks/useActivity';
import { useRange } from '../../hooks/useRange';
import { axisTicks, BARS, niceCeil, RANGE_SECONDS, span, sum } from '../../lib/activity';
import { formatCount } from '../../lib/format';

// Succeeded recedes so failures, in red on top, are what the eye finds.
const SUCCEEDED = 'bg-muted-foreground/35';
const FAILED = 'bg-destructive';

/**
 * Tasks finished over time, succeeded and failed: how deliveries are going,
 * and when they went wrong. `fresh` says nothing has been delivered yet, so
 * a longer range won't help.
 */
export function DeliveriesChart({ fresh = false }: { fresh?: boolean }) {
  const { value: range, per, setRange } = useRange();
  const { data, error, outcomes } = useActivity();
  const [hovered, setHovered] = useState<number>();
  const setting = useSetting();

  const succeeded = data?.buckets.reduce((n, b) => n + b.succeeded, 0) ?? 0;
  const failed = data?.buckets.reduce((n, b) => n + b.failed, 0) ?? 0;
  const total = succeeded + failed;
  const scale = niceCeil(Math.max(5, ...(data?.buckets.map((b) => b.succeeded + b.failed) ?? [])));
  const deleted = data?.retention_seconds !== undefined && data.retention_seconds < RANGE_SECONDS[range];

  return (
    <section className="flex flex-col gap-3">
      <h3 className="font-medium">
        Deliveries
        {total > 0 && (
          <span className="text-xs font-normal text-faint">
            {' '}
            · {formatCount(total)} finished in the last {range}, per {per}
          </span>
        )}
      </h3>
      {error ? (
        <ErrorText error={error} />
      ) : !data ? (
        <>
          <div className="h-5" />
          <ChartSkeleton bars={BARS[range]} />
          <ChartAxis ticks={['', '', '']} />
          <div className="h-4" />
        </>
      ) : !outcomes ? (
        <>
          <div className="h-5" />
          <ChartEmpty
            bars={data.buckets.length}
            title="This server doesn't report deliveries yet"
            hint="Update Workly to see the tasks that succeeded and failed over time."
          />
          <ChartAxis ticks={axisTicks(data)} />
          <div className="h-4" />
        </>
      ) : total === 0 ? (
        <>
          <div className="h-5" />
          <ChartEmpty
            bars={data.buckets.length}
            title={fresh ? 'No deliveries yet' : `No tasks finished in the last ${range}`}
            hint={
              fresh
                ? 'Every task Workly delivers shows up here: succeeded, and failed in red.'
                : deleted
                  ? `Finished tasks are deleted after ${Math.round(data.retention_seconds! / 3600)}h${setting('WORKLY_RETENTION')}, so older ones aren't counted.`
                  : 'Delivered tasks show up here as they finish, failures in red.'
            }
            action={
              fresh || range === '7d' ? undefined : { label: 'Show the last 7 days', onClick: () => setRange('7d') }
            }
          />
          <ChartAxis ticks={axisTicks(data)} />
          <div className="h-4" />
        </>
      ) : (
        <>
          <Detail data={data} index={hovered} scale={scale} per={per} />
          <ChartFrame onMouseLeave={() => setHovered(undefined)}>
            {data.buckets.map((b, i) => {
              const n = b.succeeded + b.failed;
              return (
                <div
                  key={b.start}
                  className={cn('flex h-full flex-1 flex-col-reverse rounded-xs', hovered === i && 'bg-muted')}
                  onMouseEnter={() => setHovered(i)}
                >
                  {/* An empty slot keeps a stub, so a quiet range still reads as a timeline. */}
                  <div
                    className={cn('flex w-full flex-col-reverse overflow-hidden rounded-t-xs', n === 0 && 'bg-muted')}
                    style={{ height: n === 0 ? 2 : `${(n / scale) * 100}%` }}
                  >
                    {b.succeeded > 0 && <div className={SUCCEEDED} style={{ height: `${(b.succeeded / n) * 100}%` }} />}
                    {b.failed > 0 && <div className={FAILED} style={{ height: `${(b.failed / n) * 100}%` }} />}
                  </div>
                </div>
              );
            })}
          </ChartFrame>
          <ChartAxis ticks={axisTicks(data)} />
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <Legend color={SUCCEEDED} label="succeeded" n={succeeded} />
            <Legend color={FAILED} label="failed" n={failed} tone={failed > 0 ? 'text-destructive' : undefined} />
          </div>
          {deleted && (
            <p className="text-xs text-faint">
              Finished tasks are deleted after {Math.round(data.retention_seconds! / 3600)}h
              {setting('WORKLY_RETENTION')}, so older bars count only what's left.
            </p>
          )}
        </>
      )}
    </section>
  );
}

function Legend({ color, label, n, tone }: { color: string; label: string; n: number; tone?: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className={cn('size-2 rounded-xs', color)} />
      {label}
      <span className={cn('font-mono tabular-nums', tone ?? 'text-faint')}>{formatCount(n)}</span>
    </span>
  );
}

/** The hovered bar's time and counts, or the scale, labelling the dashed line at the chart's top. */
function Detail({ data, index, scale, per }: { data: Activity; index?: number; scale: number; per: string }) {
  if (index === undefined) {
    return (
      <div className="flex h-5 items-end justify-end text-xs text-faint">
        <span className="font-mono tabular-nums">
          {formatCount(scale)} per {per}
        </span>
      </div>
    );
  }
  const b = data.buckets[index];
  const end = new Date(Date.parse(b.start) + data.bucket_seconds * 1000);
  return (
    <div className="flex h-5 flex-wrap items-center gap-x-3 font-mono text-xs tabular-nums">
      <span className="text-muted-foreground">{span(new Date(b.start), end, data.range)}</span>
      <span className="text-faint">{formatCount(b.succeeded)} succeeded</span>
      <span className={b.failed > 0 ? 'text-destructive' : 'text-faint'}>{formatCount(b.failed)} failed</span>
      <span className="text-faint">{formatCount(sum(b.counts))} created</span>
    </div>
  );
}
