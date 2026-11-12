import { createContext, useContext, type ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';

export type NavItem = { to: string; label: string; icon: LucideIcon };

/** Extension points a wrapping app fills in; see <Dashboard>. */
export type Slots = {
  nav: NavItem[];
  header?: ReactNode;
  banner?: ReactNode;
  gettingStarted?: GettingStarted;
  /** False hides server settings (environment variables) from explanations; see <Dashboard>. */
  serverSettings?: boolean;
};

/** What the Queues and Schedules pages show while there are none. */
export type GettingStarted = { queues?: ReactNode; schedules?: ReactNode };

export const SlotsContext = createContext<Slots>({ nav: [] });

export const useSlots = () => useContext(SlotsContext);

/**
 * Names the server setting behind a limit, as " (WORKLY_RETENTION)", where
 * the people reading can change it; "" where they can't.
 */
export function useSetting(): (name: string) => string {
  const { serverSettings = true } = useSlots();
  return (name) => (serverSettings ? ` (${name})` : '');
}
