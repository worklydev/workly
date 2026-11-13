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

/**
 * The plot area: bars stand on a hairline baseline under a dashed top line,
 * which is the scale the chart labels above it.
 */
export function ChartFrame({
  children,
  className,
  onMouseLeave,
}: {
  children: ReactNode;
  className?: string;
  onMouseLeave?: () => void;
}) {
  return (
    <div
      className={cn(
        'relative flex h-28 items-end gap-px border-t border-b border-border border-t-transparent bg-[linear-gradient(to_right,var(--border)_50%,transparent_0)] bg-size-[6px_1px] bg-top bg-repeat-x',
        className,
      )}
      onMouseLeave={onMouseLeave}
    >
      {children}
    </div>
  );
}

/** Times under the chart: its start, middle and end. */
export function ChartAxis({ ticks }: { ticks: ReactNode[] }) {
  return (
    <div className="flex justify-between font-mono text-xs text-faint">
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

/**
 * A small bar chart for a table row, scaled to its own tallest bar. With
 * nothing in it, a dotted baseline: a row has no room for a message, and its
 * counts already say zero.
 */
export function Sparkline({
  values,
  color = 'bg-chart-1',
  className,
  title,
}: {
  values: number[] | undefined;
  color?: string;
  className?: string;
  title?: string;
}) {
  const frame = cn('flex h-6 w-28 items-end gap-px', className);
  if (values === undefined)
    return (
      <div className={cn(frame, 'animate-pulse')}>
        <div className="h-px w-full bg-muted" />
      </div>
    );
  const max = Math.max(0, ...values);
  if (max === 0) {
    return (
      <div className={frame} title={title}>
        <div className="w-full border-b border-dotted border-faint/60" />
      </div>
    );
  }
  return (
    <div className={frame} title={title} aria-hidden={!title}>
      {values.map((v, i) => (
        <div
          key={i}
          className={cn('flex-1 rounded-t-[1px]', v === 0 ? 'bg-muted' : color)}
          style={{ height: v === 0 ? 1 : `${Math.max(8, (v / max) * 100)}%` }}
        />
      ))}
    </div>
  );
}
