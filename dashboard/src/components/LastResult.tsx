import type { Run, Task } from '../api';
import { SKIP_REASONS, SKIP_SHORT } from '../lib/runs';
import { StatusCode } from './states';

type Result = Pick<Task, 'last_status' | 'last_error' | 'last_outcome'> & Partial<Pick<Run, 'state' | 'skip_reason'>>;

/**
 * How a task or run last went: the status code (mono, red from 400) with its
 * reason, or in words how it ended without a response, or why a run was
 * skipped. Sets its own fonts, so it reads the same in any cell.
 */
export function LastResult({ result: r }: { result: Result }) {
  if (r.state === 'skipped' && r.skip_reason) {
    return (
      <span className="font-sans text-warning" title={SKIP_REASONS[r.skip_reason]}>
        {SKIP_SHORT[r.skip_reason]}
      </span>
    );
  }
  if (r.last_status !== undefined) {
    const reason = r.last_error?.match(/^HTTP \d{3} (.+)$/)?.[1];
    return (
      <span title={r.last_error}>
        <span className="font-mono">
          <StatusCode code={r.last_status} />
        </span>
        {reason && <span className="font-sans text-faint"> {reason}</span>}
      </span>
    );
  }
  if (r.last_outcome && r.last_outcome !== 'success') {
    return (
      <span
        className={r.last_outcome === 'lost' ? 'font-sans text-warning' : 'font-sans text-destructive'}
        title={r.last_error}
      >
        {r.last_outcome.replace('_', ' ')}
      </span>
    );
  }
  return <span className="text-faint">—</span>;
}
