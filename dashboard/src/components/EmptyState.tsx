import type { ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';
import { cn } from 'cn';

/**
 * A section with nothing in it: an icon, what would be here, and how to get
 * it there. `tone="success"` for an empty that is good news (nothing failing).
 */
export function EmptyState({
  icon: Icon,
  title,
  hint,
  action,
  tone,
  className,
}: {
  icon: LucideIcon;
  title: ReactNode;
  hint?: ReactNode;
  action?: ReactNode;
  tone?: 'success';
  className?: string;
}) {
  return (
    <div className={cn('flex flex-col items-center gap-1.5 px-4 py-8 text-center', className)}>
      <span
        className={cn(
          'mb-0.5 flex size-8 items-center justify-center rounded-md',
          tone === 'success' ? 'bg-success/12 text-success' : 'border border-border bg-card text-faint',
        )}
      >
        <Icon className="size-4" />
      </span>
      <p className="text-sm">{title}</p>
      {hint && <p className="max-w-xs text-xs text-muted-foreground">{hint}</p>}
      {action && <div className="mt-1.5">{action}</div>}
    </div>
  );
}

/** Rows still loading: faint bars as tall as the rows they stand in for. */
export function RowsSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div aria-hidden className="flex animate-pulse flex-col">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex h-10 items-center gap-3 border-b border-border px-1">
          <div className="h-2 w-10 rounded-full bg-muted" />
          <div className="h-2 rounded-full bg-muted" style={{ width: `${40 - i * 8}%` }} />
        </div>
      ))}
    </div>
  );
}
