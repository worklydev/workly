import type { Attempt, RunState, Task, TaskState } from '../api';

const OUTCOME = {
  success: 'success',
  http_error: 'danger',
  timeout: 'danger',
  network_error: 'danger',
  lost: 'warning',
} as const;

/** Badge variant for an attempt outcome; lost is a warning because the result is unknown. */
export const outcomeVariant = (outcome: Attempt['outcome']) => OUTCOME[outcome];

/**
 * A task's or run's state as shown: the API's `pending` covers ones due now
 * and ones due later, which the backlog counts separately as due and
 * scheduled.
 */
export type DisplayState = TaskState | RunState | 'scheduled' | 'retrying';

/** A pending task or run due later is scheduled, or retrying once an attempt has failed. */
export function displayState(
  item: Pick<Task, 'run_at' | 'attempt_count'> & { state: TaskState | RunState },
  now = Date.now(),
): DisplayState {
  if (item.state !== 'pending' || new Date(item.run_at).getTime() <= now) return item.state;
  return item.attempt_count > 0 ? 'retrying' : 'scheduled';
}
