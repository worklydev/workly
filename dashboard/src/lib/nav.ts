import { Bell, CalendarClock, Layers, ListChecks, TriangleAlert } from 'lucide-react';
import type { NavItem } from '../context';

/** The built-in pages, in the sidebar and the command palette. */
export const ITEMS: NavItem[] = [
  { to: '/queues', label: 'Queues', icon: Layers },
  { to: '/tasks', label: 'Tasks', icon: ListChecks },
  { to: '/failures', label: 'Failures', icon: TriangleAlert },
  { to: '/schedules', label: 'Schedules', icon: CalendarClock },
  { to: '/alerts', label: 'Alerts', icon: Bell },
];
