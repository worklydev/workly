import type { CodeTab } from '../components/CodeTabs';
import type { Examples } from '../context';

/** The docs, on workly.run whichever server this is. */
export const DOCS = 'https://workly.run/docs';

/** This server as `workly dev` runs it: no API key, and its echo endpoint. */
export function defaultExamples(): Examples {
  const origin = window.location.origin;
  return { apiUrl: origin, echoUrl: `${origin}/_dev/echo`, apiKey: false };
}

/** How each SDK finds the server, as the first lines of its example. */
function env(e: Examples, comment: string): string {
  const vars = e.apiKey ? `WORKLY_URL=${e.apiUrl} and WORKLY_API_KEY` : `WORKLY_URL=${e.apiUrl}`;
  return `${comment} Reads ${vars}`;
}

function curl(e: Examples, method: string, path: string, body: string): string {
  const auth = e.apiKey ? `\n  -H "Authorization: Bearer $WORKLY_API_KEY" \\` : '';
  return `curl -X ${method} ${e.apiUrl}${path} \\${auth}
  -H 'Content-Type: application/json' \\
  -d '${body}'`;
}

/** Enqueueing a first task, which creates its queue. */
export function enqueueExamples(e: Examples): CodeTab[] {
  return [
    {
      label: 'Python',
      lang: 'python',
      code: `# pip install workly
from workly import Workly

workly = Workly()  ${env(e, '#')}
task = workly.tasks.enqueue(
    "hello",
    url="${e.echoUrl}",
    payload={"hello": "world"},
)`,
    },
    {
      label: 'Node',
      lang: 'typescript',
      code: `// npm install @worklydev/sdk
import { Workly } from '@worklydev/sdk';

const workly = new Workly(); ${env(e, '//')}
const task = await workly.tasks.enqueue('hello', {
  url: '${e.echoUrl}',
  payload: { hello: 'world' },
});`,
    },
    {
      label: 'curl',
      lang: 'bash',
      code: curl(e, 'POST', '/v1/queues/hello/tasks', `{"url": "${e.echoUrl}", "payload": {"hello": "world"}}`),
    },
  ];
}

/** A queue's limits in workly.yaml: queues are created on first enqueue, and configured here. */
export const QUEUE_YAML = `version: 1

queues:
  hello:
    max_concurrency: 10    # deliveries in flight at once
    rate_per_second: 5     # deliveries started per second
    max_attempts: 5        # including the first
    timeout_seconds: 30`;

/** Defining a workflow and starting a run of it. */
export function workflowExamples(e: Examples): CodeTab[] {
  return [
    {
      label: 'Python',
      lang: 'python',
      code: `# pip install workly
from workly import Workly

workly = Workly()  ${env(e, '#')}
workly.workflows.put(
    "hello",
    steps=[
        {"name": "prices", "url": "${e.echoUrl}"},
        {"name": "fx", "url": "${e.echoUrl}"},
        {
            "name": "report",
            "url": "${e.echoUrl}",
            "depends_on": ["prices", "fx"],
        },
    ],
)
run = workly.workflows.trigger("hello")`,
    },
    {
      label: 'Node',
      lang: 'typescript',
      code: `// npm install @worklydev/sdk
import { Workly } from '@worklydev/sdk';

const workly = new Workly(); ${env(e, '//')}
await workly.workflows.put('hello', {
  steps: [
    { name: 'prices', url: '${e.echoUrl}' },
    { name: 'fx', url: '${e.echoUrl}' },
    {
      name: 'report',
      url: '${e.echoUrl}',
      dependsOn: ['prices', 'fx'],
    },
  ],
});
const run = await workly.workflows.trigger('hello');`,
    },
    {
      label: 'curl',
      lang: 'bash',
      code: `${curl(
        e,
        'PUT',
        '/v1/workflows/hello',
        `{"steps": [
    {"name": "prices", "url": "${e.echoUrl}"},
    {"name": "fx", "url": "${e.echoUrl}"},
    {"name": "report", "url": "${e.echoUrl}", "depends_on": ["prices", "fx"]}
  ]}`,
      )}

${curl(e, 'POST', '/v1/workflows/hello/runs', '{}')}`,
    },
  ];
}

export function workflowYaml(e: Examples): string {
  return `version: 1

workflows:
  hello:
    steps:
      - name: prices
        url: ${e.echoUrl}
      - name: fx
        url: ${e.echoUrl}
      - name: report
        url: ${e.echoUrl}
        depends_on: [prices, fx]   # runs once both have succeeded`;
}
