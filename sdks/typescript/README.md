# Workly for TypeScript

The TypeScript SDK for [Workly](https://github.com/worklydev/workly), the open-source HTTP task queue. Enqueue a task, and Workly delivers it to your endpoint over HTTP, retrying with backoff until it succeeds.

```sh
npm install @workly/sdk
```

No dependencies. It uses `fetch` and Web Crypto, so it runs on Node 22+, Bun, Deno and edge runtimes (Cloudflare Workers, Vercel Edge). ESM, with types included.

## Quickstart

Start a local server with `workly dev` (SQLite, no auth, on :7337; `pip install "workly[cli]"` or `curl -fsSL https://raw.githubusercontent.com/worklydev/workly/main/install.sh | sh`), then:

```ts
import { Workly } from '@workly/sdk';

const workly = new Workly(); // WORKLY_URL, else http://localhost:7337; WORKLY_API_KEY
const task = await workly.tasks.enqueue('emails', {
  url: 'https://api.example.com/send-email',
  payload: { userId: 123 },
});
console.log(task.id, task.state);
```

Your endpoint receives a `POST` with the JSON payload. Any 2xx marks the task succeeded. A 5xx, 408, 429, timeout or connection error is retried with backoff. Delivery is at-least-once, so make handlers safe to run twice. `Workly-Task-Id` is stable across attempts and works as a deduplication key.

## Verifying deliveries

Every delivery is signed: with the server's `WORKLY_SIGNING_SECRET` when you run Workly yourself, or with your workspace's secret on [workly.run](#hosted-workly). Check the signature against the **raw** body, before parsing it:

```ts
import { SignatureVerificationError, verifySignature } from '@workly/sdk';

// Next.js route handler, Hono, Cloudflare Workers: anything with a Fetch API Request
export async function POST(request: Request) {
  const body = await request.text();
  try {
    await verifySignature(body, request.headers); // secret from WORKLY_SIGNING_SECRET
  } catch (err) {
    if (err instanceof SignatureVerificationError) return new Response(null, { status: 401 });
    throw err;
  }
  const payload = JSON.parse(body);
  // ...
}

// Express: keep the raw body
app.post('/send-email', express.raw({ type: '*/*' }), async (req, res) => {
  await verifySignature(req.body, req.headers, secret);
  // ...
});
```

By default, signatures older than 5 minutes are rejected (`{ toleranceSeconds }`). `workly dev` signs with `workly-dev-secret`. To test your handler without a server, `await sign(body, secret)` returns the headers Workly would send.

## Hosted Workly

On [workly.run](https://workly.run), point the client at the hosted API and use the keys from the [console](https://console.workly.run):

```sh
export WORKLY_URL=https://api.workly.run
export WORKLY_API_KEY=...                # API keys page
export WORKLY_SIGNING_SECRET=whsec_...   # same page; each workspace has its own
```

Nothing else changes. Hosted workspaces have a request rate limit and a monthly task quota: requests over the rate limit are retried for you, and an enqueue over the quota throws a `PermissionDeniedError` with the code `quota_exceeded`.

## Tasks

```ts
await workly.tasks.enqueue('emails', {
  url: 'https://api.example.com/send-email',
  payload: { userId: 123 }, // JSON, sent as is; or body: 'raw text', contentType: 'text/csv'
  method: 'POST', // default
  headers: { 'X-Tenant': 'acme' }, // sent with every delivery
  delaySeconds: 60, // or runAt: new Date(...)
  idempotencyKey: 'welcome-123', // an existing task with this key is returned instead
  maxAttempts: 5, // overrides the queue's setting
  timeoutSeconds: 10, // likewise
});

// Up to 100 at once, all or nothing.
await workly.tasks.enqueueMany(
  'emails',
  users.map((u) => ({ url: 'https://api.example.com/send-email', payload: { userId: u.id } })),
);

const task = await workly.tasks.get('task_01J9...');
for (const attempt of await workly.tasks.attempts(task.id)) {
  console.log(attempt.number, attempt.outcome, attempt.statusCode, attempt.durationMs);
}

await workly.tasks.retry(task.id); // run now; replays a finished task
const delivery = await workly.tasks.delivery(task.id); // the signed request, to replay by hand
await workly.tasks.cancel(task.id);

// Newest first, across pages. All filters are optional.
for await (const task of workly.tasks.list({
  queue: 'emails',
  state: 'failed',
  createdAfter: new Date(Date.now() - 24 * 3600 * 1000),
})) {
  // ...
}
```

Field names are camelCase: the SDK converts them to and from the [API](https://github.com/worklydev/workly/blob/main/api/openapi.yaml)'s snake_case (`retry_on_4xx` is `retryOn4xx`). Payloads and headers are your own data, so they are sent and returned exactly as they are. Timestamps are RFC 3339 strings, and fields added by newer servers come through too.

## Queues

Queues are created automatically on first enqueue. `put` sets a queue's configuration declaratively, which makes it easy to keep in code: settings you leave out are reset to their defaults.

```ts
await workly.queues.put('emails', {
  maxAttempts: 10,
  minBackoffSeconds: 1,
  maxBackoffSeconds: 3600,
  backoffMultiplier: 2,
  timeoutSeconds: 30,
  ratePerSecond: 50,
  maxConcurrency: 20,
});

const queue = await workly.queues.get('emails');
console.log(queue.stats?.due, queue.stats?.oldestDueAt);

await workly.queues.pause('emails'); // stop delivering; enqueueing still works
await workly.queues.resume('emails');
await workly.queues.cancelPending('emails'); // cancel pending tasks
await workly.queues.retryFailed('emails'); // replay failed tasks, e.g. after an outage
for (const group of await workly.queues.failures('emails')) {
  console.log(group.count, group.error); // failed tasks grouped by error
}
await workly.queues.retryFailed('emails', { lastError: 'HTTP 503 Service Unavailable' }); // replay one group
await workly.queues.activity('emails'); // succeeded/failed/cancelled in the last 7 days
await workly.queues.delete('emails'); // delete the queue and all its tasks
```

## Schedules

A schedule calls a URL on a cron timetable; each call is a run, retried and recorded like a task. Schedules are separate from queues. `put` is declarative, like `queues.put`. See [Schedules](https://docs.workly.run/schedules/).

```ts
const schedule = await workly.schedules.put('nightly-report', {
  cron: '0 3 * * *',
  timezone: 'Europe/Stockholm',
  url: 'https://api.example.com/reports/nightly',
  payload: { kind: 'daily' },
  maxAttempts: 3,
  overlap: 'skip', // skip a slot while the previous run is still going
});
console.log(schedule.nextRuns); // the next five times

const run = await workly.schedules.trigger('nightly-report'); // run it now
for await (const r of workly.schedules.runs('nightly-report', { state: 'failed' })) {
  console.log(r.id, r.scheduledFor, r.lastError);
}
await workly.runs.attempts(run.id);
await workly.runs.retry(run.id); // deliver it again, e.g. a failed or skipped run
await workly.schedules.pause('nightly-report');
await workly.schedules.resume('nightly-report'); // from the next scheduled time
await workly.schedules.delete('nightly-report'); // with all its runs
```

Runs are delivered with `Workly-Run-Id`, `Workly-Schedule` and `Workly-Scheduled-For` headers and the same signature, so `verifySignature` works unchanged.

## Task groups

A group tracks a set of tasks and enqueues a callback task once it's closed and they've all finished: a `POST` of the group's status and counts. See [Task groups](https://docs.workly.run/groups/).

```ts
const group = await workly.groups.create({
  name: 'daily-stock-prices',
  idempotencyKey: 'stock-prices-2026-10-19', // creating it again returns this group
  onComplete: { queue: 'jobs', url: 'https://api.example.com/jobs/refresh-views' },
});
for (const company of companies) {
  await workly.tasks.enqueue('stock-prices', {
    url: 'https://api.example.com/jobs/update-stock-price',
    payload: { symbol: company.symbol },
    groupId: group.id,
  });
}
await workly.groups.close(group.id); // no more tasks; it completes once they've finished

const g = await workly.groups.get(group.id);
console.log(g.status, g.total, g.succeeded, g.failed, g.remaining);
for await (const task of workly.tasks.list({ groupId: group.id, state: 'failed' })) {
  console.log(task.id, task.lastError);
}
```

The callback's body is a `GroupSummary`. It arrives as the task's payload, so its fields are snake_case: `group_id`, `status`, `total`, `succeeded`, `failed`, `cancelled`.

## Errors

| Error                   | When                                                                    |
| ----------------------- | ----------------------------------------------------------------------- |
| `BadRequestError`       | 400, invalid request                                                    |
| `AuthenticationError`   | 401, missing or wrong API key                                           |
| `PermissionDeniedError` | 403, not allowed, or a hosted limit was reached (e.g. `quota_exceeded`) |
| `NotFoundError`         | 404, no such queue or task                                              |
| `ConflictError`         | 409, e.g. retrying a running task                                       |
| `PayloadTooLargeError`  | 413                                                                     |
| `RateLimitError`        | 429, still rate limited after the retries                               |
| `APIError`              | base class of the above; has `.status`, `.code` (stable) and `.message` |
| `APIConnectionError`    | the server couldn't be reached, or the request timed out                |

All of them extend `WorklyError`. Requests are retried up to `maxRetries` times (2 by default), honouring `Retry-After`. A 429 is always retried, since the server refused the request without doing anything. Connection errors and 502–504 are retried for requests that are safe to repeat: an enqueue only when it has an `idempotencyKey`, or when the connection failed before the request was sent.

## Options

```ts
const workly = new Workly({
  url: 'https://workly.internal', // default WORKLY_URL, then http://localhost:7337
  apiKey: '...', // default WORKLY_API_KEY
  timeoutMs: 30_000, // per call, including retries
  maxRetries: 2,
  fetch: customFetch,
});

// Every method takes an AbortSignal last.
await workly.tasks.get(id, { signal: AbortSignal.timeout(1000) });
```

## License

[Apache-2.0](https://github.com/worklydev/workly/blob/main/LICENSE)
