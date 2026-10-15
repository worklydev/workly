import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getActivityOptions } from '../../api/@tanstack/react-query.gen';
import type { Activity, ActivityBucket } from '../../api';
import { ErrorText } from '../../components/QueryState';
import { RangeTabs } from '../../components/RangeTabs';
import { useRange } from '../../hooks/useRange';
import { formatCount } from '../../lib/format';
import { cn } from 'cn';

type Range = Activity['range'];

/** The busiest queues get a color each; the rest share one. */
const COLORS = ['bg-chart-1', 'bg-chart-2', 'bg-chart-3', 'bg-chart-4', 'bg-chart-5'];
const OTHER = 'other';

/** Tasks created over time, stacked by queue: which queues drive the load, and when. */
export function ActivityChart() {
  const { value: range, per } = useRange();
  const { data, error } = useQuery({ ...getActivityOptions({ query: { range } }), refetchInterval: 15_000 });
  const [hovered, setHovered] = useState<number>();
  const [focus, setFocus] = useState<string>();

  const series = data ? toSeries(data.buckets) : undefined;

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-4">
        <h3 className="text-xs text-muted-foreground">
          Tasks created
          {series && (
            <span className="text-faint">
              {' '}
              · {formatCount(series.total)} in the last {range}, per {per}
            </span>
          )}
        </h3>
        <RangeTabs />
      </div>
      {error ? (
        <ErrorText error={error} />
      ) : !data || !series ? (
        <div className="h-36" />
      ) : (
        <>
          <Detail data={data} series={series} index={hovered} />
          <div className="flex h-28 items-end gap-px" onMouseLeave={() => setHovered(undefined)}>
            {data.buckets.map((b, i) => {
              const total = sum(b.counts);
              return (
                <div
                  key={b.start}
                  className={cn('flex h-full flex-1 flex-col-reverse rounded-xs', hovered === i && 'bg-muted')}
                  onMouseEnter={() => setHovered(i)}
                >
                  <div
                    className="flex w-full flex-col-reverse overflow-hidden rounded-xs"
                    style={{ height: `${(total / series.max) * 100}%` }}
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
          </div>
          <div className="flex justify-between font-mono text-xs text-faint">
            <span>{tick(data.buckets[0].start, range)}</span>
            <span>{tick(data.buckets[Math.floor(data.buckets.length / 2)].start, range)}</span>
            <span>now</span>
          </div>
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
          {data.retention_seconds !== undefined && data.retention_seconds < RANGE_SECONDS[range] && (
            <p className="text-xs text-faint">
              Finished tasks are deleted after {Math.round(data.retention_seconds / 3600)}h (WORKLY_RETENTION), so older
              bars count only what's left.
            </p>
          )}
        </>
      )}
    </section>
  );
}

const RANGE_SECONDS: Record<Range, number> = { '1h': 3600, '24h': 86_400, '7d': 7 * 86_400 };

type Series = {
  /** The top queues by total, then OTHER if there are more. */
  keys: string[];
  color: Record<string, string>;
  totals: Record<string, number>;
  total: number;
  /** The tallest bar, at least 1. */
  max: number;
};

function toSeries(buckets: ActivityBucket[]): Series {
  const totals: Record<string, number> = {};
  for (const b of buckets) for (const [q, n] of Object.entries(b.counts)) totals[q] = (totals[q] ?? 0) + n;
  const ranked = Object.keys(totals).sort((a, b) => totals[b] - totals[a] || a.localeCompare(b));
  const top = ranked.length > COLORS.length ? ranked.slice(0, COLORS.length - 1) : ranked;
  const keys = ranked.length > top.length ? [...top, OTHER] : top;
  const color: Record<string, string> = {};
  top.forEach((q, i) => (color[q] = COLORS[i]));
  if (keys.includes(OTHER)) {
    color[OTHER] = 'bg-chart-other';
    totals[OTHER] = ranked.slice(top.length).reduce((n, q) => n + totals[q], 0);
  }
  const total = ranked.reduce((n, q) => n + totals[q], 0);
  return { keys, color, totals, total, max: Math.max(1, ...buckets.map((b) => sum(b.counts))) };
}

function countFor(b: ActivityBucket, key: string, keys: string[]): number {
  if (key !== OTHER) return b.counts[key] ?? 0;
  return Object.entries(b.counts).reduce((n, [q, c]) => (keys.includes(q) ? n : n + c), 0);
}

const sum = (counts: Record<string, number>) => Object.values(counts).reduce((a, b) => a + b, 0);

/** The hovered bar's time and counts, or the tallest bar's height as a scale. */
function Detail({ data, series, index }: { data: Activity; series: Series; index?: number }) {
  if (index === undefined) {
    return (
      <div className="h-5 text-xs text-faint">
        {series.total === 0 ? 'No tasks created in this range.' : `Tallest bar: ${formatCount(series.max)}`}
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

const time = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });
const dayTime = new Intl.DateTimeFormat(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' });

function tick(iso: string, range: Range) {
  return (range === '7d' ? dayTime : time).format(new Date(iso));
}

function span(start: Date, end: Date, range: Range) {
  const f = range === '7d' ? dayTime : time;
  return `${f.format(start)}–${time.format(end)}`;
}
