import { StartHere } from '../../components/StartHere';
import { useSlots } from '../../context';
import { enqueueExamples, QUEUE_YAML } from '../../lib/examples';

/** A wrapping app's own first steps, or how to enqueue a first task. */
export function QueuesGettingStarted() {
  const custom = useSlots().gettingStarted?.queues;
  return custom ?? <FirstQueue />;
}

function FirstQueue() {
  return (
    <StartHere
      title="No queues yet"
      intro="Enqueue a task and its queue is created. Workly delivers the task to its URL, and retries with backoff until it answers 2xx."
      code={enqueueExamples}
      yaml={() => QUEUE_YAML}
      yamlIntro={
        <>
          Set a queue's limits and retries in <span className="font-mono text-xs">workly.yaml</span>, and apply it with{' '}
          <span className="font-mono text-xs">workly deploy</span>.
        </>
      }
      docs={[
        ['Quickstart', 'quickstart'],
        ['Configuration as code', 'deploy'],
      ]}
    />
  );
}

/** For the Tasks page: the same first task. */
export function FirstTask() {
  return (
    <StartHere
      title="No tasks yet"
      intro="A task is an HTTP request Workly delivers for you, with retries, in a queue. Enqueue one and it shows up here, with every delivery attempt."
      code={enqueueExamples}
      yaml={() => QUEUE_YAML}
      yamlIntro={
        <>
          Tasks come from your code, but their queue's limits and retries live in{' '}
          <span className="font-mono text-xs">workly.yaml</span>, applied with{' '}
          <span className="font-mono text-xs">workly deploy</span>.
        </>
      }
      docs={[
        ['Concepts', 'concepts'],
        ['Configuration as code', 'deploy'],
      ]}
    />
  );
}
