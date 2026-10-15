import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { Card, CardDescription } from '../primitives/card';
import { cn } from 'cn';

type Props = { label: string; value: ReactNode; tone?: 'warning' | 'danger'; to?: string; title?: string };

/** A labelled number. With `to`, the card links to the tasks behind it. */
export function StatCard({ label, value, tone, to, title }: Props) {
  const card = (
    <Card className={cn('gap-1', to && 'transition-colors hover:bg-muted')} title={title}>
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
    </Card>
  );
  return to ? (
    <Link to={to} className="rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring/40">
      {card}
    </Link>
  ) : (
    card
  );
}
