import { Fragment } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useLocation } from 'react-router';
import { cn } from 'cn';
import {
  getGroupOptions,
  getHealthzOptions,
  getRunOptions,
  getWorkflowRunOptions,
} from '../api/@tanstack/react-query.gen';
import { useSlots, type NavItem } from '../context';
import { useFrom } from '../lib/from';
import { Badge } from '../primitives/badge';
import { CommandPalette } from './CommandPalette';
import { MobileNav } from './Sidebar';
import { ThemeToggle } from './ThemeToggle';

const LABELS: Record<string, string> = {
  home: 'Home',
  queues: 'Queues',
  tasks: 'Tasks',
  groups: 'Groups',
  failures: 'Failures',
  schedules: 'Schedules',
  workflows: 'Workflows',
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

/** A workflow run belongs to its workflow: Workflows / daily-refresh / wfr_…. */
function useWorkflowRunCrumbs(path: string) {
  const id = path.startsWith('/workflow-runs/') ? decodeURIComponent(path.slice('/workflow-runs/'.length)) : '';
  // Shares the run page's query.
  const { data: run } = useQuery({ ...getWorkflowRunOptions({ path: { id } }), enabled: id !== '' });
  if (!id) return undefined;
  const before = crumbs(run ? `/workflows/${encodeURIComponent(run.workflow)}` : '/workflows');
  return [...before, { to: path, label: id, mono: true }];
}

/** A group is named in the crumbs when it has a name: Groups / nightly-export. */
function useGroupCrumbs(path: string) {
  const id = path.startsWith('/groups/') ? decodeURIComponent(path.slice('/groups/'.length)) : '';
  // Shares the group page's query.
  const { data: group } = useQuery({ ...getGroupOptions({ path: { id } }), enabled: id !== '' });
  if (!id || !group?.name) return undefined;
  return [...crumbs('/groups'), { to: path, label: group.name, mono: false }];
}

export function Header() {
  const { header, nav } = useSlots();
  const path = useLocation().pathname;
  const from = useFrom();
  const runItems = useRunCrumbs(path, from);
  const groupItems = useGroupCrumbs(path);
  const workflowRunItems = useWorkflowRunCrumbs(path);
  const items =
    runItems ??
    groupItems ??
    workflowRunItems ??
    (path.startsWith('/tasks/') ? taskCrumbs(path, from) : crumbs(path, nav));

  return (
    <header className="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-border px-4 sm:gap-4 sm:px-6">
      <MobileNav />
      {/* A top-level page's own title names it, so its single crumb is for screen readers only. */}
      <h1 className="flex min-w-0 flex-1 items-center gap-2 text-sm">
        {items.map((crumb, i) => {
          // On phones only the parent shows, as a way back: the page's title names the page.
          const phone = items.length === 1 ? 'sr-only' : i === items.length - 2 ? '' : 'hidden sm:inline';
          return (
            <Fragment key={crumb.to}>
              {i > 0 && <span className="hidden text-faint sm:inline">/</span>}
              {i === items.length - 1 ? (
                <span className={cn(crumb.mono ? 'truncate font-mono text-xs' : 'truncate font-medium', phone)}>
                  {crumb.label}
                </span>
              ) : (
                <Link to={crumb.to} className={cn('shrink-0 text-muted-foreground hover:text-foreground', phone)}>
                  {crumb.label}
                </Link>
              )}
            </Fragment>
          );
        })}
      </h1>
      <div className="flex shrink-0 items-center gap-2 sm:gap-3">
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
  // On phones the dot alone says it, leaving room for the page's name.
  return isError ? (
    <Badge variant="danger" dot title="The Workly API is unreachable">
      <span className="hidden sm:inline">offline</span>
    </Badge>
  ) : (
    <Badge
      variant="success"
      dot
      title="Connected; updates live"
      className="motion-safe:[&>span:first-child]:animate-pulse"
    >
      <span className="hidden sm:inline">live</span>
    </Badge>
  );
}
