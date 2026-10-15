import type { ReactNode } from 'react';
import { errorMessage } from '../lib/api';

/** Placeholder text for loading, error and empty states. */
export function Muted({ children }: { children: ReactNode }) {
  return <div className="py-10 text-center text-muted-foreground">{children}</div>;
}

export function ErrorText({ error }: { error: unknown }) {
  return <Muted>{errorMessage(error, "Couldn't reach the Workly API.")}</Muted>;
}
