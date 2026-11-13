import { Bell, Boxes, CalendarClock, House, Layers, ListChecks, TriangleAlert } from 'lucide-react';
import type { NavItem } from '../context';

/** The built-in pages, in the sidebar and the command palette. */
export const ITEMS: NavItem[] = [
  { to: '/home', label: 'Home', icon: House },
  { to: '/queues', label: 'Queues', icon: Layers },
  { to: '/tasks', label: 'Tasks', icon: ListChecks },
  { to: '/groups', label: 'Groups', icon: Boxes },
  { to: '/schedules', label: 'Schedules', icon: CalendarClock },
  { to: '/failures', label: 'Failures', icon: TriangleAlert },
  { to: '/alerts', label: 'Alerts', icon: Bell },
];
