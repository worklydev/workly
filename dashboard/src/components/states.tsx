import { Badge } from '../primitives/badge';
import type { Attempt, GroupStatus } from '../api';
import { outcomeVariant, type DisplayState } from '../lib/states';

const STATE = {
  pending: { variant: 'neutral', dot: false, title: undefined },
  scheduled: { variant: 'neutral', dot: false, title: 'Pending, due later' },
  retrying: { variant: 'warning', dot: false, title: 'Pending: an attempt failed and it waits to retry' },
  running: { variant: 'accent', dot: true, title: undefined },
  succeeded: { variant: 'success', dot: false, title: undefined },
  failed: { variant: 'danger', dot: false, title: undefined },
  cancelled: { variant: 'neutral', dot: false, title: undefined },
  skipped: { variant: 'warning', dot: false, title: "This schedule slot wasn't delivered" },
} as const;

/** A task or run state badge; pass displayState() to tell scheduled and retrying ones apart from due ones. */
export function TaskStateBadge({ state }: { state: DisplayState }) {
  const { variant, dot, title } = STATE[state];
  return (
    <Badge variant={variant} dot={dot} title={title} className={state === 'cancelled' ? 'text-faint' : undefined}>
      {state}
    </Badge>
  );
}

const GROUP_STATUS = {
  open: { variant: 'neutral', dot: false, title: 'Accepting tasks' },
  running: { variant: 'accent', dot: true, title: 'Closed, with tasks still to finish' },
  succeeded: { variant: 'success', dot: false, title: 'Every task succeeded' },
  failed: { variant: 'danger', dot: false, title: 'Every task finished, and some failed or were cancelled' },
} as const;

export function GroupStatusBadge({ status }: { status: GroupStatus }) {
  const { variant, dot, title } = GROUP_STATUS[status];
  return (
    <Badge variant={variant} dot={dot} title={title}>
      {status}
    </Badge>
  );
}

export function OutcomeBadge({ outcome }: { outcome: Attempt['outcome'] }) {
  return <Badge variant={outcomeVariant(outcome)}>{outcome.replace('_', ' ')}</Badge>;
}

/** An HTTP status code, red from 400 up. */
export function StatusCode({ code }: { code?: number }) {
  if (code === undefined) return <span className="text-faint">—</span>;
  return <span className={code >= 400 ? 'text-destructive' : undefined}>{code}</span>;
}
