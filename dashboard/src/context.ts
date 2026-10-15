import { createContext, useContext, type ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';

export type NavItem = { to: string; label: string; icon: LucideIcon };

/** Extension points a wrapping app fills in; see <Dashboard>. */
export type Slots = {
  nav: NavItem[];
  header?: ReactNode;
  gettingStarted?: GettingStarted;
};

/** What the Queues and Schedules pages show while there are none. */
export type GettingStarted = { queues?: ReactNode; schedules?: ReactNode };

export const SlotsContext = createContext<Slots>({ nav: [] });

export const useSlots = () => useContext(SlotsContext);
