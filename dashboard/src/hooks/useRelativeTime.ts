import { useEffect, useState } from 'react';
import { formatRelative, relativeRefreshMs } from '../lib/format';

/** `formatRelative(iso)`, re-rendered as often as the label can change. */
export function useRelativeTime(iso: string): string {
  const [now, setNow] = useState(Date.now);
  const refreshMs = relativeRefreshMs(iso, now);

  useEffect(() => {
    if (refreshMs === null) return;
    const timer = setTimeout(() => setNow(Date.now()), refreshMs);
    return () => clearTimeout(timer);
  }, [now, refreshMs]);

  return formatRelative(iso, now);
}
