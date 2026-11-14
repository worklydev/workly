import { useQuery } from '@tanstack/react-query';
import { Webhook } from 'lucide-react';
import { Link } from 'react-router';
import { cn } from 'cn';
import { getTaskOptions } from '../api/@tanstack/react-query.gen';
import type { TaskState } from '../api';
import { LIVE } from '../lib/api';
import { useFromHere } from '../lib/from';

const WORDS: Record<TaskState, { word: string; tone: string; title: string }> = {
  pending: { word: 'pending', tone: 'text-muted-foreground', title: 'Callback waiting to be delivered' },
  running: { word: 'sending', tone: 'text-primary', title: 'Callback being delivered' },
  succeeded: { word: 'delivered', tone: 'text-success', title: 'Callback delivered' },
  failed: { word: 'failed', tone: 'text-destructive', title: 'Callback failed: its attempts ran out' },
  cancelled: { word: 'cancelled', tone: 'text-faint', title: 'Callback cancelled' },
};

/**
 * Whether a callback went out, in a word, linking to its task. Pass `state`
 * when the list already has it (runs do); otherwise the task is fetched.
 */
export function CallbackMark({ taskId, state, className }: { taskId: string; state?: TaskState; className?: string }) {
  const from = useFromHere();
  const { data: task } = useQuery({
    ...getTaskOptions({ path: { id: taskId } }),
    enabled: state === undefined,
    ...LIVE,
  });
  const known = state ?? task?.state;
  const w = known ? WORDS[known] : undefined;
  return (
    <Link
      to={`/tasks/${taskId}`}
      state={from}
      title={w ? `${w.title} (${taskId})` : taskId}
      onClick={(e) => e.stopPropagation()}
      className={cn('inline-flex items-center gap-1 text-xs hover:underline', w?.tone ?? 'text-faint', className)}
    >
      <Webhook className="size-3.5 shrink-0" />
      {w?.word ?? '…'}
    </Link>
  );
}
