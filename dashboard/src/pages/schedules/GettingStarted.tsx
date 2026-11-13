import { CodePanel } from '../../components/CodePanel';
import { useSlots } from '../../context';

/** A wrapping app's own first steps, or the `workly dev` example. */
export function SchedulesGettingStarted() {
  const custom = useSlots().gettingStarted?.schedules;
  return custom ?? <GettingStarted />;
}

function GettingStarted() {
  const origin = window.location.origin;
  return (
    <div className="flex max-w-2xl flex-col gap-3">
      <p className="text-muted-foreground">
        No schedules yet. A schedule calls a URL on a cron timetable, with retries and a history of every run. With{' '}
        <span className="font-mono text-xs">workly dev</span>, the built-in echo endpoint makes a handy target:
      </p>
      <CodePanel title="Terminal">
        {`curl -X PUT ${origin}/v1/schedules/hello \\
  -H 'Content-Type: application/json' \\
  -d '{"cron": "* * * * *", "url": "${origin}/_dev/echo", "payload": {"hello": "world"}}'`}
      </CodePanel>
    </div>
  );
}
