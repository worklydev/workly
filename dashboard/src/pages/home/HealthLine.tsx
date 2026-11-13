import { ArrowRight } from 'lucide-react';
import { Link } from 'react-router';
import { cn } from 'cn';
import type { Health, Part } from '../../lib/home';
import { formatCount } from '../../lib/format';

const TONE = {
  ok: { panel: 'border-border bg-card/50 dark:bg-card', dot: 'bg-success' },
  new: { panel: 'border-border bg-card/50 dark:bg-card', dot: 'bg-primary' },
  warning: { panel: 'border-warning/20 bg-warning/6', dot: 'bg-warning-fill' },
  danger: { panel: 'border-destructive/20 bg-destructive/6', dot: 'bg-destructive' },
} as const;

/**
 * The page's headline: one sentence on how the workspace is doing, with a
 * dot in its tone, and what's in flight right now beside it.
 */
export function HealthLine({ health }: { health: Health }) {
  const { tone, clauses, running } = health;
  return (
    <div
      role="status"
      className={cn('flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border px-4 py-3', TONE[tone].panel)}
    >
      <div className="flex min-w-0 grow items-center gap-3">
        <span className="relative flex size-2 shrink-0">
          {tone !== 'ok' && tone !== 'new' && (
            <span className={cn('absolute inset-0 animate-ping rounded-full opacity-60', TONE[tone].dot)} />
          )}
          <span className={cn('relative size-2 rounded-full', TONE[tone].dot)} />
        </span>
        <p className="min-w-0">
          {tone === 'new' ? (
            <>
              Welcome to Workly.{' '}
              <span className="text-muted-foreground">Queue a task or set up a schedule to start.</span>
            </>
          ) : (
            <Sentence clauses={clauses} />
          )}
        </p>
      </div>
      <div className="flex items-center gap-4 text-xs">
        {running > 0 && (
          <Link
            to="/tasks?state=running"
            className="flex items-center gap-1.5 text-muted-foreground hover:text-foreground"
          >
            <span className="size-1.5 animate-pulse rounded-full bg-primary" />
            <span className="font-mono tabular-nums">{formatCount(running)}</span> running now
          </Link>
        )}
        {tone === 'danger' && (
          <Link to="/failures" className="group/link flex items-center gap-1 text-destructive hover:underline">
            See failures
            <ArrowRight className="size-3 transition-transform group-hover/link:translate-x-0.5" />
          </Link>
        )}
      </div>
    </div>
  );
}

/** "3 failed tasks in emails, and nightly failed its last run." */
function Sentence({ clauses }: { clauses: Part[][] }) {
  return (
    <>
      {clauses.map((parts, i) => (
        <span key={i}>
          {i > 0 && ', and '}
          {parts.map((p, j) =>
            typeof p === 'string' ? (
              <span key={j}>{i === 0 && j === 0 ? capitalize(p) : p}</span>
            ) : (
              <Link
                key={j}
                to={p.to}
                className={cn(
                  'font-medium underline decoration-faint underline-offset-3 hover:decoration-current',
                  p.mono && 'font-mono text-[0.8125rem]',
                )}
              >
                {p.text}
              </Link>
            ),
          )}
        </span>
      ))}
      .
    </>
  );
}

const capitalize = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);
