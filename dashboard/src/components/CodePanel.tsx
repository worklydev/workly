import type { ReactNode } from 'react';
import { cn } from 'cn';

/** A small window for machine text: bodies, headers, commands. Without children, just its title bar. */
export function CodePanel({
  title,
  children,
  className,
}: {
  title?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('overflow-hidden rounded-lg border border-border bg-card', className)}>
      {title && (
        <div
          className={cn(
            'px-3 py-1.5 text-xs text-muted-foreground',
            children !== undefined && 'border-b border-border',
          )}
        >
          {title}
        </div>
      )}
      {children !== undefined && (
        <pre className="max-h-96 overflow-auto p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">
          {children}
        </pre>
      )}
    </div>
  );
}
