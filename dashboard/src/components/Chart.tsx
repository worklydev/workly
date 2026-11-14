import type { ReactNode } from 'react';
import { ArrowRight, ChartNoAxesColumn } from 'lucide-react';
import { Link } from 'react-router';
import { cn } from 'cn';

/**
 * Building blocks every bar chart shares, so they all look the same and,
 * more importantly, so they all look the same with nothing to show: the
 * frame stays, ghost bars stand in for the data and a message says what
 * would appear here and how to make it appear.
 */

// Room on the left for the y-axis labels, kept on every chart, labelled or not, so plots line up and nothing jumps.
const GUTTER = 'pl-9';

const compact = new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 });

/**
 * The plot area: bars stand on a hairline baseline, with dashed gridlines at
 * half and full `scale`, labelled on the y-axis. Without a scale (loading,
 * empty) the gridlines stay and the labels are left out.
 */
export function ChartFrame({
  children,
  className,
  scale,
  onMouseLeave,
}: {
  children: ReactNode;
  className?: string;
  /** The value at the top of the plot; see `niceCeil`. */
  scale?: number;
  onMouseLeave?: () => void;
}) {
  return (
    <div className={cn('relative', GUTTER)}>
      {scale !== undefined && (
        <div
          aria-hidden
          className="absolute inset-y-0 left-0 w-9 pr-2 text-right font-mono text-xs text-faint tabular-nums"
        >
          {[
            [scale, 'top-0'],
            [scale / 2, 'top-1/2'],
            [0, 'top-full'],
          ].map(([v, at]) => (
            <span key={at} className={cn('absolute right-2 -translate-y-1/2 leading-none', at)}>
              {compact.format(v as number)}
            </span>
          ))}
        </div>
      )}
      <div
        className={cn('relative flex h-28 items-end gap-px border-b border-border', className)}
        style={GRIDLINES}
        onMouseLeave={onMouseLeave}
      >
        {children}
      </div>
    </div>
  );
}

// Dashed hairlines at the top and the middle, under the bars.
const DASH = 'linear-gradient(to right, var(--border) 50%, transparent 0)';
const GRIDLINES = {
  backgroundImage: `${DASH}, ${DASH}`,
  backgroundSize: '6px 1px',
  backgroundPosition: '0 0, 0 50%',
  backgroundRepeat: 'repeat-x',
};

/** Times under the chart: its start, middle and end. */
export function ChartAxis({ ticks }: { ticks: ReactNode[] }) {
  return (
    <div className={cn('flex justify-between font-mono text-xs text-faint', GUTTER)}>
      {ticks.map((t, i) => (
        <span key={i}>{t}</span>
      ))}
    </div>
  );
}

/**
 * A fixed, gentle wave, the same on every render, so a placeholder never
 * flickers or looks like a real (random) series.
 */
function ghostHeight(i: number, n: number): number {
  const x = i / Math.max(1, n - 1);
  const h =
    0.42 + 0.22 * Math.sin(x * Math.PI * 2.2 + 0.6) + 0.12 * Math.sin(x * Math.PI * 7.3) + 0.06 * Math.sin(i * 1.7);
  return Math.min(0.9, Math.max(0.1, h));
}

function GhostBars({ count, className }: { count: number; className?: string }) {
  return (
    <>
      {Array.from({ length: count }, (_, i) => (
        <div
          key={i}
          aria-hidden
          className={cn('flex-1 rounded-t-xs bg-muted', className)}
          style={{ height: `${ghostHeight(i, count) * 100}%` }}
        />
      ))}
    </>
  );
}

// Clear in the middle, where the message is, and visible towards the sides.
const FADE = 'radial-gradient(ellipse 48% 95% at 50% 55%, transparent 45%, black 100%)';

export type ChartEmptyAction = { label: string; to?: string; onClick?: () => void };

/**
 * A chart with nothing to show. It keeps the chart's frame, so the page
 * doesn't jump when data arrives, and says what would appear and how to make
 * it appear, rather than drawing a flat line that reads as broken.
 */
export function ChartEmpty({
  title,
  hint,
  action,
  bars = 48,
}: {
  title: ReactNode;
  hint?: ReactNode;
  action?: ChartEmptyAction;
  /** As many as the chart would have, so the ghost bars are as wide as real ones. */
  bars?: number;
}) {
  return (
    <ChartFrame>
      <div
        aria-hidden
        className="absolute inset-0 flex items-end gap-px"
        style={{ maskImage: FADE, WebkitMaskImage: FADE }}
      >
        <GhostBars count={bars} className="opacity-70 dark:opacity-60" />
      </div>
      <div className="absolute inset-0 flex items-center justify-center px-4">
        <div className="flex max-w-sm flex-col items-center gap-1.5 text-center">
          <span className="mb-0.5 flex size-7 items-center justify-center rounded-md border border-border bg-card text-faint">
            <ChartNoAxesColumn className="size-3.5" />
          </span>
          <p className="text-sm">{title}</p>
          {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
          {action && <EmptyAction action={action} />}
        </div>
      </div>
    </ChartFrame>
  );
}

function EmptyAction({ action }: { action: ChartEmptyAction }) {
  const className =
    'mt-0.5 inline-flex items-center gap-1 rounded-sm text-xs text-primary outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/40';
  const body = (
    <>
      {action.label}
      <ArrowRight className="size-3" />
    </>
  );
  return action.to ? (
    <Link to={action.to} className={className}>
      {body}
    </Link>
  ) : (
    <button type="button" onClick={action.onClick} className={cn('cursor-pointer', className)}>
      {body}
    </button>
  );
}

/** A chart still loading: the ghost bars, pulsing, in the chart's frame. */
export function ChartSkeleton({ bars = 48 }: { bars?: number }) {
  return (
    <ChartFrame className="animate-pulse">
      <GhostBars count={bars} className="opacity-50" />
    </ChartFrame>
  );
}
