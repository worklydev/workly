import { CodePanel } from '../../components/CodePanel';
import { useSlots } from '../../context';

/** A wrapping app's own first steps, or the `workly dev` example. */
export function QueuesGettingStarted() {
  const custom = useSlots().gettingStarted?.queues;
  return custom ?? <GettingStarted />;
}

function GettingStarted() {
  const origin = window.location.origin;
  return (
    <div className="flex max-w-2xl flex-col gap-3">
      <p className="text-muted-foreground">
        No queues yet. Enqueue a task and its queue is created. With{' '}
        <span className="font-mono text-xs">workly dev</span>, the built-in echo endpoint makes a handy target:
      </p>
      <CodePanel title="Terminal">
        {`curl -X POST ${origin}/v1/queues/hello/tasks \\
  -H 'Content-Type: application/json' \\
  -d '{"url": "${origin}/_dev/echo", "payload": {"hello": "world"}}'`}
      </CodePanel>
    </div>
  );
}
