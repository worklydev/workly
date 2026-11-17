import { createContext, useContext, type ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';

export type NavItem = { to: string; label: string; icon: LucideIcon };

/** Extension points a wrapping app fills in; see <Dashboard>. */
export type Slots = {
  nav: NavItem[];
  header?: ReactNode;
  banner?: ReactNode;
  gettingStarted?: GettingStarted;
  examples?: Examples;
  /** False hides server settings (environment variables) from explanations; see <Dashboard>. */
  serverSettings?: boolean;
};

/**
 * Where the empty states' code examples point. Defaults to this server, with
 * no API key and `workly dev`'s echo endpoint as the target.
 */
export type Examples = {
  /** The API's base URL, which the SDKs read from WORKLY_URL. */
  apiUrl: string;
  /** A URL that answers any request, for a first task to call. */
  echoUrl: string;
  /** Whether requests need WORKLY_API_KEY. */
  apiKey: boolean;
  /** Shown before the examples, e.g. where to get an API key. */
  setup?: ReactNode;
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
