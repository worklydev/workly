import type { ReactNode } from 'react';

/**
 * Explains on hover why the control inside is disabled. Actions stay visible
 * when they don't apply, so people can see they exist; a disabled button gets
 * no pointer events itself, hence the wrapper.
 */
export function DisabledReason({ reason, children }: { reason?: string; children: ReactNode }) {
  if (!reason) return children;
  return (
    <span title={reason} className="inline-flex cursor-not-allowed">
      {children}
    </span>
  );
}
