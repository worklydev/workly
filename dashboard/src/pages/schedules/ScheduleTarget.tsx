import { Globe, Workflow } from 'lucide-react';
import { cn } from 'cn';
import type { Schedule } from '../../api';
import { formatTarget } from '../../lib/format';

/**
 * What a schedule's runs do: the request they make, or the workflow they
 * start. The icon tells the two apart at a glance, so lists need no type
 * column.
 */
export function ScheduleTarget({
  schedule: s,
  host,
  className,
}: {
  schedule: Schedule;
  /** The host every listed schedule calls, so only the path is shown. */
  host?: string;
  className?: string;
}) {
  const Icon = s.workflow ? Workflow : Globe;
  return (
    <span
      className={cn('flex min-w-0 items-center gap-1.5 text-xs text-faint', className)}
      title={s.workflow ? `Starts a run of workflow ${s.workflow}` : `${s.method} ${s.url}`}
    >
      <Icon className="size-3 shrink-0" aria-label={s.workflow ? 'Workflow' : 'HTTP request'} />
      <span className="min-w-0 truncate font-mono">
        {s.workflow ? s.workflow : formatTarget(s.method, s.url, host)}
      </span>
    </span>
  );
}
