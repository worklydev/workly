import { createContext, useContext, type ReactNode } from 'react';
import { Link } from 'react-router';
import { Card, CardDescription } from '../primitives/card';
import { cn } from 'cn';

type Props = {
  label: ReactNode;
  value: ReactNode;
  /** A short line under the value, for what the number means. */
  detail?: ReactNode;
  tone?: 'warning' | 'danger';
  to?: string;
  title?: string;
  /** For the cell's place in a strip's grid, e.g. col-span-2. */
  className?: string;
};

const InStrip = createContext(false);

/**
 * Several stats as one panel: cells divided by hairlines instead of a box
 * each, so a row of numbers reads as one thing. Give it the grid's columns;
 * `filler` cells pad a last row that the stats don't fill.
 */
export function StatStrip({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <InStrip value={true}>
      {/* The gaps show the container's fill: that's the hairlines. */}
      <div className={cn('grid gap-px overflow-hidden rounded-lg border border-border bg-border', className)}>
        {children}
      </div>
    </InStrip>
  );
}

/** An empty cell, for a row the stats don't fill. */
export function StatFiller({ className }: { className?: string }) {
  return <div aria-hidden className={cn('bg-surface', className)} />;
}

/** A labelled number. With `to`, it links to the tasks behind it. Inside a StatStrip it's a cell, otherwise a card. */
export function StatCard({ label, value, detail, tone, to, title, className }: Props) {
  const strip = useContext(InStrip);
  const body = (
    <>
      <CardDescription>{label}</CardDescription>
      <div
        className={cn(
          'font-mono text-xl',
          tone === 'warning' && 'text-warning',
          tone === 'danger' && 'text-destructive',
        )}
      >
        {value}
      </div>
      {detail && <p className="text-xs text-faint">{detail}</p>}
    </>
  );
  const hover = to && 'transition-colors hover:bg-muted dark:hover:bg-muted';
  const card = strip ? (
    <div className={cn('flex h-full flex-col gap-1 bg-surface px-4 py-3', hover, !to && className)} title={title}>
      {body}
    </div>
  ) : (
    <Card className={cn('h-full gap-1 py-3', hover, !to && className)} title={title}>
      {body}
    </Card>
  );
  return to ? (
    <Link
      to={to}
      className={cn(
        'h-full outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
        strip ? 'focus-visible:ring-inset' : 'rounded-lg',
        className,
      )}
    >
      {card}
    </Link>
  ) : (
    card
  );
}
