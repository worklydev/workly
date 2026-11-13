import { useState } from 'react';
import type { Activity } from '../../api';
import { ChartAxis, ChartEmpty, ChartFrame, ChartSkeleton } from '../../components/Chart';
import { useActivity } from '../../hooks/useActivity';
import { axisTicks, BARS, countFor, OTHER, RANGE_SECONDS, span, sum, type Series } from '../../lib/activity';
import { ErrorText } from '../../components/QueryState';
import { RangeTabs } from '../../components/RangeTabs';
import { useSetting } from '../../context';
import { useRange } from '../../hooks/useRange';
import { formatCount } from '../../lib/format';
import { cn } from 'cn';

/** Tasks created over time, stacked by queue: which queues drive the load, and when. */
export function ActivityChart() {
  const { value: range, per, setRange } = useRange();
  const { data, error, series } = useActivity();
  const [hovered, setHovered] = useState<number>();
  const [focus, setFocus] = useState<string>();
  const setting = useSetting();
  const deleted = data?.retention_seconds !== undefined && data.retention_seconds < RANGE_SECONDS[range];
  const ticks = data && axisTicks(data);

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-4">
        <h3 className="font-medium">
          Tasks created
          {series && series.total > 0 && (
            <span className="text-xs font-normal text-faint">
              {' '}
              · {formatCount(series.total)} in the last {range}, per {per}
            </span>
          )}
        </h3>
        <RangeTabs />
      </div>
      {error ? (
        <ErrorText error={error} />
      ) : !data || !series || !ticks ? (
        <>
          <div className="h-5" />
          <ChartSkeleton bars={BARS[range]} />
          <ChartAxis ticks={['', '', '']} />
          <div className="h-4" />
        </>
      ) : series.total === 0 ? (
        <>
          <div className="h-5" />
          <ChartEmpty
            bars={data.buckets.length}
            title={`No tasks created in the last ${range}`}
            hint={
              deleted
                ? `Finished tasks are deleted after ${Math.round(data.retention_seconds! / 3600)}h${setting('WORKLY_RETENTION')}, so older ones aren't counted.`
                : 'Tasks you enqueue show up here, stacked by queue.'
            }
            action={range === '7d' ? undefined : { label: 'Show the last 7 days', onClick: () => setRange('7d') }}
          />
          <ChartAxis ticks={ticks} />
          <div className="h-4" />
        </>
      ) : (
        <>
          <Detail data={data} series={series} index={hovered} per={per} />
          {/* The dashed top line is the scale Detail labels. */}
          <ChartFrame onMouseLeave={() => setHovered(undefined)}>
            {data.buckets.map((b, i) => {
              const total = sum(b.counts);
              return (
                <div
                  key={b.start}
                  className={cn('flex h-full flex-1 flex-col-reverse rounded-xs', hovered === i && 'bg-muted')}
                  onMouseEnter={() => setHovered(i)}
                >
                  {/* An empty slot keeps a stub, so a quiet range still reads as a timeline. */}
                  <div
                    className={cn(
                      'flex w-full flex-col-reverse overflow-hidden rounded-t-xs',
                      total === 0 && 'bg-muted',
                    )}
                    style={{ height: total === 0 ? 2 : `${(total / series.scale) * 100}%` }}
                  >
                    {series.keys.map((key) => {
                      const n = countFor(b, key, series.keys);
                      if (n === 0) return null;
                      return (
                        <div
                          key={key}
                          className={cn(
                            series.color[key],
                            'w-full transition-opacity',
                            focus && focus !== key && 'opacity-25',
                          )}
                          style={{ height: `${(n / total) * 100}%` }}
                        />
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </ChartFrame>
          <ChartAxis ticks={ticks} />
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs" onMouseLeave={() => setFocus(undefined)}>
            {series.keys.map((key) => (
              <span
                key={key}
                className="flex cursor-default items-center gap-1.5 text-muted-foreground"
                onMouseEnter={() => setFocus(key)}
              >
                <span className={cn('size-2 rounded-xs', series.color[key])} />
                <span className={key === OTHER ? 'text-faint' : 'font-mono'}>
                  {key === OTHER ? 'other queues' : key}
                </span>
                <span className="font-mono text-faint tabular-nums">{formatCount(series.totals[key])}</span>
              </span>
            ))}
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

/** The hovered bar's time and counts, or the scale, labelling the dashed line at the chart's top. */
function Detail({ data, series, index, per }: { data: Activity; series: Series; index?: number; per: string }) {
  if (index === undefined) {
    return (
      <div className="flex h-5 items-end justify-between text-xs text-faint">
        <span />
        <span className="font-mono tabular-nums">
          {formatCount(series.scale)} per {per}
        </span>
      </div>
    );
  }
  const b = data.buckets[index];
  const end = new Date(Date.parse(b.start) + data.bucket_seconds * 1000);
  return (
    <div className="flex h-5 flex-wrap items-center gap-x-3 text-xs">
      <span className="font-mono text-muted-foreground">
        {span(new Date(b.start), end, data.range)} · {formatCount(sum(b.counts))}
      </span>
      {series.keys.map((key) => {
        const n = countFor(b, key, series.keys);
        return n > 0 ? (
          <span key={key} className="flex items-center gap-1 text-faint">
            <span className={cn('size-2 rounded-xs', series.color[key])} />
            <span className="font-mono tabular-nums">{formatCount(n)}</span>
          </span>
        ) : null;
      })}
    </div>
  );
}
