import { useEffect, useState, type ReactNode } from 'react';
import { cn } from 'cn';

/** How long a fetch may take before the old content is marked as stale. */
const DELAY_MS = 300;

/**
 * Content still showing a previous filter's data while the new one loads,
 * e.g. a list after switching tabs. The rows stay, so the page doesn't
 * collapse; after a moment they fade and a bar slides along the top, and a
 * fetch faster than that shows nothing at all, so there's no flicker.
 */
export function Stale({ stale, children }: { stale: boolean; children: ReactNode }) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!stale) return;
    const t = setTimeout(() => setShown(true), DELAY_MS);
    return () => {
      clearTimeout(t);
      setShown(false);
    };
  }, [stale]);

  return (
    <div className="relative" aria-busy={stale || undefined}>
      <div
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-x-0 -top-1.5 h-0.5 overflow-hidden rounded-full opacity-0 transition-opacity duration-300',
          shown && 'opacity-100',
        )}
      >
        <div className="h-full w-1/3 animate-loading rounded-full bg-primary/70" />
      </div>
      <div className={cn('transition-opacity duration-300', shown && 'pointer-events-none opacity-40')}>{children}</div>
    </div>
  );
}
