import type { ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import { getTaskOptions } from '../api/@tanstack/react-query.gen';
import type { Callback } from '../api';
import { useNow } from '../hooks/useNow';
import { LIVE } from '../lib/api';
import { useFromHere } from '../lib/from';
import { displayState } from '../lib/states';
import { CodePanel } from './CodePanel';
import { Section } from './Section';
import { TaskStateBadge } from './states';

/**
 * A task's, run's or group's callback: the request Workly sends (or will send)
 * and the callback task it last enqueued for it.
 */
export function CallbackSection({
  callback,
  taskId,
  body,
  waiting,
  note,
}: {
  callback: Callback;
  /** The callback task last enqueued, if any. */
  taskId?: string;
  /** The JSON body as it is or will be sent. */
  body: object;
  /** Shown while no callback task has been enqueued yet. */
  waiting: string;
  note: ReactNode;
}) {
  const headers = Object.entries(callback.headers ?? {})
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
  return (
    <Section title="Callback">
      <CallbackStatus id={taskId} waiting={waiting} />
      <CodePanel
        title={
          <span className="min-w-0 truncate font-mono" title={callback.url}>
            POST {callback.url}
          </span>
        }
      >
        {headers && `${headers}\n\n`}
        {JSON.stringify(body, null, 2)}
      </CodePanel>
      <p className="text-xs text-faint">{note}</p>
    </Section>
  );
}

function CallbackStatus({ id, waiting }: { id?: string; waiting: string }) {
  const { data: task } = useQuery({
    ...getTaskOptions({ path: { id: id ?? '' } }),
    enabled: id !== undefined,
    ...LIVE,
  });
  const from = useFromHere();
  const now = useNow(5_000);
  if (!id) return <p className="text-sm text-muted-foreground">{waiting}</p>;
  return (
    <div className="flex items-center gap-2 text-sm">
      {task ? <TaskStateBadge state={displayState(task, now)} /> : <span className="text-faint">…</span>}
      <Link to={`/tasks/${id}`} state={from} className="font-mono text-xs underline underline-offset-4">
        {id}
      </Link>
    </div>
  );
}
