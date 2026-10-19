import { Fragment } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useLocation } from 'react-router';
import { getHealthzOptions, getRunOptions } from '../api/@tanstack/react-query.gen';
import { useSlots, type NavItem } from '../context';
import { useFrom } from '../lib/from';
import { Badge } from '../primitives/badge';
import { CommandPalette } from './CommandPalette';
import { ThemeToggle } from './ThemeToggle';

const LABELS: Record<string, string> = {
  queues: 'Queues',
  tasks: 'Tasks',
  groups: 'Groups',
  failures: 'Failures',
  schedules: 'Schedules',
};

/**
 * Breadcrumbs from the path: /queues/emails is Queues / emails. Pages a
 * wrapping app adds are named by their nav item.
 */
function crumbs(path: string, nav: NavItem[] = []) {
  const parts = path.split('/').filter(Boolean);
  return parts.map((part, i) => ({
    to: '/' + parts.slice(0, i + 1).join('/'),
    label:
      i === 0
        ? (LABELS[part] ?? nav.find((n) => n.to === '/' + part)?.label ?? part.charAt(0).toUpperCase() + part.slice(1))
        : decodeURIComponent(part),
    mono: i > 0,
  }));
}

/**
 * A task page opened from a queue or a filtered list keeps that place:
 * Queues / emails / task_…, with the queue crumb linking back to the same tab.
 */
function taskCrumbs(path: string, from: string | undefined) {
  if (!from || !path.startsWith('/tasks/')) return crumbs(path);
  const [fromPath] = from.split('?');
  const before = crumbs(fromPath);
  if (before.length === 0) return crumbs(path);
  before[before.length - 1].to = from;
  const id = path.slice('/tasks/'.length);
  return [...before, { to: path, label: decodeURIComponent(id), mono: true }];
}

/** A run belongs to its schedule: Schedules / nightly / run_…, keeping the runs tab it was opened from. */
function useRunCrumbs(path: string, from: string | undefined) {
  const id = path.startsWith('/runs/') ? decodeURIComponent(path.slice('/runs/'.length)) : '';
  // Shares the run page's query.
  const { data: run } = useQuery({ ...getRunOptions({ path: { id } }), enabled: id !== '' });
  if (!id) return undefined;
  const schedule = run && `/schedules/${encodeURIComponent(run.schedule)}`;
  const before = crumbs(schedule ?? '/schedules');
  if (schedule && from?.split('?')[0] === schedule) before[before.length - 1].to = from;
  return [...before, { to: path, label: id, mono: true }];
}

export function Header() {
  const { header, nav } = useSlots();
  const path = useLocation().pathname;
  const from = useFrom();
  const runItems = useRunCrumbs(path, from);
  const items = runItems ?? (path.startsWith('/tasks/') ? taskCrumbs(path, from) : crumbs(path, nav));

  return (
    <header className="flex h-14 shrink-0 items-center justify-between gap-4 border-b border-border px-6">
      <h1 className="flex min-w-0 items-center gap-2 text-sm">
        {items.map((crumb, i) => (
          <Fragment key={crumb.to}>
            {i > 0 && <span className="text-faint">/</span>}
            {i === items.length - 1 ? (
              <span className={crumb.mono ? 'truncate font-mono text-xs' : 'font-medium'}>{crumb.label}</span>
            ) : (
              <Link to={crumb.to} className="text-muted-foreground hover:text-foreground">
                {crumb.label}
              </Link>
            )}
          </Fragment>
        ))}
      </h1>
      <div className="flex items-center gap-3">
        <CommandPalette />
        {header}
        <ServerStatus />
        <ThemeToggle />
      </div>
    </header>
  );
}

function ServerStatus() {
  const { isPending, isError } = useQuery({ ...getHealthzOptions(), refetchInterval: 10_000, retry: false });
  if (isPending) return null;
  return isError ? (
    <Badge variant="danger" dot>
      offline
    </Badge>
  ) : (
    <Badge variant="success" dot>
      live
    </Badge>
  );
}
