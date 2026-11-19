import { cn } from 'cn';
import { TriangleAlert } from 'lucide-react';

/** A warning or failure at the top of a page: why something isn't done. */
export function Notice({
  tone,
  detail,
  children,
}: {
  tone: 'warning' | 'danger';
  detail?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        'flex items-start gap-2 rounded-lg px-3 py-2',
        tone === 'warning' ? 'bg-warning/12 text-warning' : 'bg-destructive/12 text-destructive',
      )}
    >
      <TriangleAlert className="mt-0.5 size-4 shrink-0" />
      <div className="flex min-w-0 flex-col gap-0.5 break-words">
        <span>{children}</span>
        {detail && <span className="text-xs text-foreground/70">{detail}</span>}
      </div>
    </div>
  );
}
