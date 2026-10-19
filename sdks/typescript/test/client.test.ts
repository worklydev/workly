import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, test } from 'node:test';

import {
  APIConnectionError,
  APIError,
  AuthenticationError,
  BadRequestError,
  ConflictError,
  NotFoundError,
  PayloadTooLargeError,
  PermissionDeniedError,
  RateLimitError,
  VERSION,
  Workly,
} from '../src/index.ts';

interface Sent {
  method: string;
  url: URL;
  headers: Record<string, string>;
  body: unknown;
}

type Reply = Response | Error | (() => Response | Error);

/** A fetch that records requests and answers from a script. */
function fakeFetch(...replies: Reply[]) {
  const sent: Sent[] = [];
  const fetch = async (input: string | URL | Request, init: RequestInit = {}): Promise<Response> => {
    const body = typeof init.body === 'string' ? JSON.parse(init.body) : undefined;
    sent.push({
      method: init.method ?? 'GET',
      url: new URL(String(input)),
      headers: init.headers as Record<string, string>,
      body,
    });
    const reply = replies.shift() ?? json({});
    const value = typeof reply === 'function' ? reply() : reply;
    if (value instanceof Error) throw value;
    return value;
  };
  return { fetch: fetch as typeof globalThis.fetch, sent };
}

function json(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } });
}

function apiErr(status: number, code: string, message = 'nope'): Response {
  return json({ error: { code, message } }, status);
}

function refused(): Error {
  return new TypeError('fetch failed', {
    cause: Object.assign(new Error('connect ECONNREFUSED'), { code: 'ECONNREFUSED' }),
  });
}

function reset(): Error {
  return new TypeError('fetch failed', { cause: Object.assign(new Error('socket hang up'), { code: 'ECONNRESET' }) });
}

const task = { id: 'task_1', queue: 'emails', state: 'pending' };

describe('configuration', () => {
  let saved: Record<string, string | undefined>;
  beforeEach(() => {
    saved = { url: process.env.WORKLY_URL, key: process.env.WORKLY_API_KEY };
    delete process.env.WORKLY_URL;
    delete process.env.WORKLY_API_KEY;
  });
  afterEach(() => {
    for (const [name, value] of [
      ['WORKLY_URL', saved.url],
      ['WORKLY_API_KEY', saved.key],
    ] as const) {
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
    }
  });

  test('defaults', async () => {
    const { fetch, sent } = fakeFetch(json({ queues: [] }));
    const workly = new Workly({ fetch });
    assert.equal(workly.url, 'http://localhost:7337');
    await workly.queues.list();
    assert.equal(sent[0]?.url.href, 'http://localhost:7337/v1/queues');
    assert.equal(sent[0]?.headers.Authorization, undefined);
    assert.equal(sent[0]?.headers['User-Agent'], `workly-typescript/${VERSION}`);
  });

  test('environment', async () => {
    process.env.WORKLY_URL = 'https://workly.example.com/';
    process.env.WORKLY_API_KEY = 'k1';
    const { fetch, sent } = fakeFetch(json({ queues: [] }));
    const workly = new Workly({ fetch });
    assert.equal(workly.url, 'https://workly.example.com');
    await workly.queues.list();
    assert.equal(sent[0]?.headers.Authorization, 'Bearer k1');
  });

  test('options override the environment', async () => {
    process.env.WORKLY_URL = 'https://env.example.com';
    process.env.WORKLY_API_KEY = 'k1';
    const { fetch, sent } = fakeFetch(json({ queues: [] }));
    await new Workly({ url: 'http://other:1', apiKey: 'k2', fetch }).queues.list();
    assert.equal(sent[0]?.url.origin, 'http://other:1');
    assert.equal(sent[0]?.headers.Authorization, 'Bearer k2');
  });
});

describe('tasks', () => {
  test('enqueue', async () => {
    const { fetch, sent } = fakeFetch(json(task, 201));
    const workly = new Workly({ fetch });
    const got = await workly.tasks.enqueue(
      'emails',
      {
        url: 'https://api.example.com/send',
        payload: { user_id: 1 },
        headers: { 'X-Tenant': 'acme' },
        runAt: new Date('2030-01-02T03:04:05Z'),
        idempotencyKey: 'k',
        maxAttempts: undefined,
      },
      { traceparent: '00-abc-def-01' },
    );
    assert.deepEqual(got, task);
    const req = sent[0]!;
    assert.equal(req.method, 'POST');
    assert.equal(req.url.pathname, '/v1/queues/emails/tasks');
    assert.equal(req.headers.traceparent, '00-abc-def-01');
    assert.equal(req.headers['Content-Type'], 'application/json');
    assert.deepEqual(req.body, {
      url: 'https://api.example.com/send',
      payload: { user_id: 1 },
      headers: { 'X-Tenant': 'acme' },
      run_at: '2030-01-02T03:04:05.000Z',
      idempotency_key: 'k',
    });
  });

  test('enqueue validation', async () => {
    const workly = new Workly({ fetch: fakeFetch().fetch });
    await assert.rejects(workly.tasks.enqueue('q', { url: 'u', payload: {}, body: 'x' }), /payload or body/);
    await assert.rejects(workly.tasks.enqueue('q', { url: 'u', delaySeconds: 1, runAt: 'x' }), /delaySeconds or runAt/);
    await assert.rejects(workly.tasks.enqueue('q', { url: 'u', runAt: new Date('nope') }), /invalid Date/);
  });

  test('path segments are escaped', async () => {
    const { fetch, sent } = fakeFetch(json(task), json({ attempts: [] }));
    const workly = new Workly({ fetch });
    await workly.tasks.get('a/b?c');
    await workly.tasks.attempts('../x');
    assert.equal(sent[0]?.url.pathname, '/v1/tasks/a%2Fb%3Fc');
    assert.equal(sent[1]?.url.pathname, '/v1/tasks/..%2Fx/attempts');
  });

  test('alert channels', async () => {
    const channel = { id: 'ach_1', type: 'slack', target: 'https://hooks.slack.com/services/…abcd' };
    const { fetch, sent } = fakeFetch(
      json({ channels: [channel], email_enabled: false }),
      json(channel, 201),
      new Response(null, { status: 204 }),
      new Response(null, { status: 204 }),
    );
    const workly = new Workly({ fetch });
    const list = await workly.alerts.list();
    assert.deepEqual([list.emailEnabled, list.channels.map((c) => c.id)], [false, ['ach_1']]);
    await workly.alerts.create('slack', 'https://hooks.slack.com/services/T0/B0/abcd');
    await workly.alerts.test('ach_1');
    await workly.alerts.delete('ach_1');
    assert.deepEqual(
      sent.map((s) => `${s.method} ${s.url.pathname}`),
      [
        'GET /v1/alert-channels',
        'POST /v1/alert-channels',
        'POST /v1/alert-channels/ach_1/test',
        'DELETE /v1/alert-channels/ach_1',
      ],
    );
    assert.deepEqual(sent[1]?.body, { type: 'slack', target: 'https://hooks.slack.com/services/T0/B0/abcd' });
  });

  test('list paginates', async () => {
    const { fetch, sent } = fakeFetch(
      json({ tasks: [{ id: 't1' }, { id: 't2' }], next_cursor: 'c1' }),
      json({ tasks: [{ id: 't3' }] }),
    );
    const workly = new Workly({ fetch });
    const ids: string[] = [];
    for await (const t of workly.tasks.list({ queue: 'emails', state: 'failed', pageSize: 2 })) ids.push(t.id);
    assert.deepEqual(ids, ['t1', 't2', 't3']);
    assert.equal(sent[0]?.url.search, '?queue=emails&state=failed&limit=2');
    assert.equal(sent[1]?.url.search, '?queue=emails&state=failed&cursor=c1&limit=2');
  });

  test('listPage filters', async () => {
    const { fetch, sent } = fakeFetch(json({ tasks: [] }));
    await new Workly({ fetch }).tasks.listPage({
      idempotencyKey: 'k',
      lastError: 'HTTP 500',
      createdAfter: new Date('2030-01-01T00:00:00Z'),
      createdBefore: '2030-02-01T00:00:00Z',
      sort: 'lastAttemptAt',
      order: 'asc',
      limit: 10,
    });
    assert.deepEqual(Object.fromEntries(sent[0]!.url.searchParams), {
      idempotency_key: 'k',
      last_error: 'HTTP 500',
      created_after: '2030-01-01T00:00:00.000Z',
      created_before: '2030-02-01T00:00:00Z',
      sort: 'last_attempt_at',
      order: 'asc',
      limit: '10',
    });
  });

  test('enqueueMany', async () => {
    const { fetch, sent } = fakeFetch(json({ results: [{ created: true, task }] }, 201));
    const results = await new Workly({ fetch }).tasks.enqueueMany('emails', [
      { url: 'u', payload: 1 },
      { url: 'u', payload: 2 },
    ]);
    assert.deepEqual(results, [{ created: true, task }]);
    assert.equal(sent[0]?.url.pathname, '/v1/queues/emails/tasks:batch');
    assert.deepEqual(sent[0]?.body, {
      tasks: [
        { url: 'u', payload: 1 },
        { url: 'u', payload: 2 },
      ],
    });
  });

  test('actions', async () => {
    const delivery = { method: 'POST', url: 'u', headers: { 'Workly-Attempt': '1' }, body: '{}' };
    const { fetch, sent } = fakeFetch(json(task), json(task), json(delivery));
    const workly = new Workly({ fetch });
    await workly.tasks.cancel('task_1');
    await workly.tasks.retry('task_1');
    assert.deepEqual(await workly.tasks.delivery('task_1'), delivery);
    assert.deepEqual(
      sent.map((r) => `${r.method} ${r.url.pathname}`),
      ['POST /v1/tasks/task_1/cancel', 'POST /v1/tasks/task_1/retry', 'GET /v1/tasks/task_1/delivery'],
    );
  });
});

describe('groups', () => {
  const group = {
    id: 'grp_1',
    name: 'daily-prices',
    status: 'open',
    total: 0,
    succeeded: 0,
    failed: 0,
    cancelled: 0,
    remaining: 0,
    on_complete: { queue: 'jobs', url: 'u', headers: { 'X-Job': 'prices' } },
    callback_task_id: 'task_9',
    created_at: '2026-10-19T10:00:00Z',
  };

  test('create, close and enqueue into a group', async () => {
    const { fetch, sent } = fakeFetch(json(group, 201), json(group), json(task, 201), json({ tasks: [] }));
    const workly = new Workly({ fetch });
    const g = await workly.groups.create({
      name: 'daily-prices',
      onComplete: { queue: 'jobs', url: 'u', headers: { 'X-Job': 'prices' } },
    });
    assert.equal(g.callbackTaskId, 'task_9');
    assert.deepEqual(g.onComplete, { queue: 'jobs', url: 'u', headers: { 'X-Job': 'prices' } });
    await workly.groups.close(g.id);
    await workly.tasks.enqueue('prices', { url: 'u', groupId: g.id });
    await workly.tasks.listPage({ groupId: g.id });
    assert.deepEqual(sent[0]?.body, {
      name: 'daily-prices',
      on_complete: { queue: 'jobs', url: 'u', headers: { 'X-Job': 'prices' } },
    });
    assert.equal(`${sent[1]?.method} ${sent[1]?.url.pathname}`, 'POST /v1/groups/grp_1/close');
    assert.deepEqual(sent[2]?.body, { url: 'u', group_id: 'grp_1' });
    assert.equal(sent[3]?.url.search, '?group_id=grp_1');
  });

  test('the callback queue defaults on the server', async () => {
    const { fetch, sent } = fakeFetch(json(group, 201));
    await new Workly({ fetch }).groups.create({ onComplete: { url: 'u' } });
    assert.deepEqual(sent[0]?.body, { on_complete: { url: 'u' } });
  });

  test('list paginates', async () => {
    const { fetch, sent } = fakeFetch(json({ groups: [group], next_cursor: 'c1' }), json({ groups: [group] }));
    const ids: string[] = [];
    for await (const g of new Workly({ fetch }).groups.list({ status: 'running', pageSize: 1 })) ids.push(g.id);
    assert.deepEqual(ids, ['grp_1', 'grp_1']);
    assert.equal(sent[1]?.url.search, '?status=running&cursor=c1&limit=1');
  });

  test('create is retried only with a key', async () => {
    let { fetch, sent } = fakeFetch(json({}, 503), json(group, 201));
    await assert.rejects(new Workly({ fetch }).groups.create({ name: 'x' }));
    assert.equal(sent.length, 1);
    ({ fetch, sent } = fakeFetch(json({}, 503), json(group, 201)));
    await new Workly({ fetch }).groups.create({ idempotencyKey: 'k' });
    assert.equal(sent.length, 2);
  });
});

describe('field names', () => {
  test('camelCase in the SDK, snake_case on the wire; payloads and headers as they are', async () => {
    const apiTask = {
      id: 'task_1',
      attempt_count: 2,
      last_error: 'HTTP 500',
      headers: { 'X-Tenant': 'acme', x_custom: '1' },
      payload: { user_id: 1, nested: { created_at: 'x' } },
    };
    const { fetch, sent } = fakeFetch(
      json(apiTask),
      json({ attempts: [{ status_code: 500, response_headers: { 'Retry-After': '1', x_y: '2' } }] }),
    );
    const workly = new Workly({ fetch });
    const got = await workly.tasks.enqueue('q', {
      url: 'u',
      payload: { userId: 1, nested: { createdAt: 'x' } },
      headers: { x_custom: '1' },
      timeoutSeconds: 5,
    });
    assert.deepEqual(sent[0]?.body, {
      url: 'u',
      payload: { userId: 1, nested: { createdAt: 'x' } },
      headers: { x_custom: '1' },
      timeout_seconds: 5,
    });
    assert.deepEqual(got, {
      id: 'task_1',
      attemptCount: 2,
      lastError: 'HTTP 500',
      headers: { 'X-Tenant': 'acme', x_custom: '1' },
      payload: { user_id: 1, nested: { created_at: 'x' } },
    });
    assert.deepEqual(await workly.tasks.attempts('t'), [
      { statusCode: 500, responseHeaders: { 'Retry-After': '1', x_y: '2' } },
    ]);
  });
});

describe('queues', () => {
  test('put sends only the settings given', async () => {
    const { fetch, sent } = fakeFetch(json({ name: 'emails' }));
    await new Workly({ fetch }).queues.put('emails', { maxAttempts: 3, ratePerSecond: 5, retryOn4xx: true });
    assert.equal(sent[0]?.method, 'PUT');
    assert.deepEqual(sent[0]?.body, { max_attempts: 3, rate_per_second: 5, retry_on_4xx: true });
  });

  test('put without settings resets to defaults', async () => {
    const { fetch, sent } = fakeFetch(json({ name: 'emails' }));
    await new Workly({ fetch }).queues.put('emails');
    assert.deepEqual(sent[0]?.body, {});
  });

  test('actions', async () => {
    const { fetch, sent } = fakeFetch(
      json({ name: 'q', paused: true }),
      json({ name: 'q', paused: false }),
      json({ cancelled: 3 }),
      json({ retried: 2 }),
      json({ retried: 1 }),
      json({
        groups: [
          {
            error: 'HTTP 503 Service Unavailable',
            count: 7,
            last_failed_at: '2030-01-01T00:00:00Z',
            latest_task_id: 'task_1',
          },
        ],
      }),
      new Response(null, { status: 204 }),
    );
    const workly = new Workly({ fetch });
    assert.equal((await workly.queues.pause('q')).paused, true);
    assert.equal((await workly.queues.resume('q')).paused, false);
    assert.equal(await workly.queues.cancelPending('q'), 3);
    assert.equal(await workly.queues.retryFailed('q'), 2);
    assert.equal(await workly.queues.retryFailed('q', { lastError: 'HTTP 503 Service Unavailable' }), 1);
    assert.deepEqual(
      (await workly.queues.failures('q', { limit: 5 })).map((g) => [g.error, g.count]),
      [['HTTP 503 Service Unavailable', 7]],
    );
    assert.equal(await workly.queues.delete('q'), undefined);
    assert.deepEqual(
      sent.map((r) => `${r.method} ${r.url.pathname}${r.url.search}`),
      [
        'POST /v1/queues/q/pause',
        'POST /v1/queues/q/resume',
        'POST /v1/queues/q/cancel-pending',
        'POST /v1/queues/q/retry-failed',
        'POST /v1/queues/q/retry-failed?last_error=HTTP+503+Service+Unavailable',
        'GET /v1/queues/q/failures?limit=5',
        'DELETE /v1/queues/q',
      ],
    );
  });

  test('activity', async () => {
    const activity = { since: '2030-01-01T00:00:00Z', succeeded: 5, failed: 1, cancelled: 0 };
    const { fetch, sent } = fakeFetch(json(activity), json(activity));
    const workly = new Workly({ fetch });
    assert.deepEqual(await workly.queues.activity('q'), activity);
    await workly.queues.activity('q', { since: new Date('2030-01-01T00:00:00Z') });
    assert.equal(sent[0]?.url.search, '');
    assert.equal(sent[1]?.url.searchParams.get('since'), '2030-01-01T00:00:00.000Z');
  });
});

describe('task activity', () => {
  test('tasks.activity', async () => {
    const activity = {
      range: '1h',
      bucket_seconds: 300,
      buckets: [{ start: '2030-01-01T00:00:00Z', counts: { q: 2 } }],
    };
    const { fetch, sent } = fakeFetch(json(activity), json(activity));
    const workly = new Workly({ fetch });
    assert.deepEqual(await workly.tasks.activity({ range: '1h' }), {
      range: '1h',
      bucketSeconds: 300,
      buckets: [{ start: '2030-01-01T00:00:00Z', counts: { q: 2 } }],
    });
    await workly.tasks.activity();
    assert.equal(`${sent[0]?.url.pathname}${sent[0]?.url.search}`, '/v1/activity?range=1h');
    assert.equal(sent[1]?.url.search, '');
  });
});

describe('cron preview', () => {
  test('previewCron', async () => {
    const preview = { next_runs: ['2030-01-01T03:00:00Z'], interval_seconds: 86400 };
    const { fetch, sent } = fakeFetch(json(preview));
    const workly = new Workly({ fetch });
    assert.deepEqual(await workly.schedules.previewCron('0 3 * * *', { timezone: 'UTC' }), {
      nextRuns: ['2030-01-01T03:00:00Z'],
      intervalSeconds: 86400,
    });
    assert.equal(sent[0]?.url.pathname, '/v1/cron/preview');
    assert.equal(sent[0]?.url.searchParams.get('cron'), '0 3 * * *');
    assert.equal(sent[0]?.url.searchParams.get('timezone'), 'UTC');
  });
});

describe('errors', () => {
  for (const [status, code, cls] of [
    [400, 'invalid_request', BadRequestError],
    [401, 'unauthorized', AuthenticationError],
    [403, 'quota_exceeded', PermissionDeniedError],
    [404, 'task_not_found', NotFoundError],
    [409, 'invalid_state', ConflictError],
    [413, 'payload_too_large', PayloadTooLargeError],
    [429, 'rate_limited', RateLimitError],
    [500, 'internal', APIError],
  ] as const) {
    test(`${status} ${code}`, async () => {
      const workly = new Workly({ fetch: fakeFetch(apiErr(status, code)).fetch, maxRetries: 0 });
      const err = await workly.tasks.get('t').catch((e: unknown) => e);
      assert.ok(err instanceof cls);
      assert.ok(err instanceof APIError);
      assert.equal(err.status, status);
      assert.equal(err.code, code);
      assert.equal(err.message, 'nope');
    });
  }

  test('without a JSON body', async () => {
    const workly = new Workly({ fetch: fakeFetch(new Response('bad gateway', { status: 502 })).fetch, maxRetries: 0 });
    const err = await workly.tasks.get('t').catch((e: unknown) => e);
    assert.ok(err instanceof APIError);
    assert.deepEqual([err.status, err.code, err.message], [502, 'unknown', 'bad gateway']);
  });
});

describe('retries', () => {
  test('idempotent requests are retried on 429 and 5xx gateways', async () => {
    const { fetch, sent } = fakeFetch(apiErr(503, 'unavailable'), json(task, 200, {}), json(task));
    const workly = new Workly({ fetch });
    const retryAfter = fakeFetch(json({}, 429, { 'Retry-After': '0' }), json(task));
    assert.deepEqual(await workly.tasks.get('t'), task);
    assert.equal(sent.length, 2);
    assert.deepEqual(await new Workly({ fetch: retryAfter.fetch }).tasks.get('t'), task);
    assert.equal(retryAfter.sent.length, 2);
  });

  test('gives up after maxRetries', async () => {
    const { fetch, sent } = fakeFetch(apiErr(503, 'a'), apiErr(503, 'b'), apiErr(503, 'c'));
    const err = await new Workly({ fetch, maxRetries: 2 }).tasks.get('t').catch((e: unknown) => e);
    assert.ok(err instanceof APIError);
    assert.equal(err.code, 'c');
    assert.equal(sent.length, 3);
  });

  test('enqueue without a key is not retried', async () => {
    const { fetch, sent } = fakeFetch(apiErr(503, 'unavailable'), json(task));
    await assert.rejects(new Workly({ fetch }).tasks.enqueue('q', { url: 'u' }), APIError);
    assert.equal(sent.length, 1);
  });

  test('enqueue with a key is retried', async () => {
    const { fetch, sent } = fakeFetch(apiErr(503, 'unavailable'), json(task));
    await new Workly({ fetch }).tasks.enqueue('q', { url: 'u', idempotencyKey: 'k' });
    assert.equal(sent.length, 2);
  });

  test('429 is retried, even for enqueue', async () => {
    const { fetch, sent } = fakeFetch(
      json({ error: { code: 'rate_limited', message: 'slow down' } }, 429, { 'Retry-After': '0' }),
      json(task),
    );
    await new Workly({ fetch }).tasks.enqueue('q', { url: 'u' });
    assert.equal(sent.length, 2);
  });

  test('connection refused is retried, even for enqueue', async () => {
    const { fetch, sent } = fakeFetch(refused(), json(task));
    await new Workly({ fetch }).tasks.enqueue('q', { url: 'u' });
    assert.equal(sent.length, 2);
  });

  test('errors after sending are not retried for enqueue', async () => {
    const { fetch, sent } = fakeFetch(reset(), json(task));
    const err = await new Workly({ fetch }).tasks.enqueue('q', { url: 'u' }).catch((e: unknown) => e);
    assert.ok(err instanceof APIConnectionError);
    assert.match(err.message, /POST \/v1\/queues\/q\/tasks: fetch failed: socket hang up/);
    assert.equal(sent.length, 1);
  });

  test('retry is not idempotent', async () => {
    const { fetch, sent } = fakeFetch(reset(), json(task));
    await assert.rejects(new Workly({ fetch }).tasks.retry('t'), APIConnectionError);
    assert.equal(sent.length, 1);
  });
});

describe('cancellation', () => {
  /** A fetch that never answers, until aborted. */
  const hang = (async (_: unknown, init: RequestInit = {}) =>
    new Promise((_, reject) => {
      // Stands in for the open socket, which keeps the process alive.
      const socket = setTimeout(() => {}, 10_000);
      init.signal?.addEventListener('abort', () => {
        clearTimeout(socket);
        reject(init.signal?.reason);
      });
    })) as typeof fetch;

  test('timeout', async () => {
    const err = await new Workly({ fetch: hang, timeoutMs: 20 }).tasks.get('t').catch((e: unknown) => e);
    assert.ok(err instanceof APIConnectionError);
    assert.match(err.message, /timed out after 20ms/);
  });

  test("the caller's signal", async () => {
    const controller = new AbortController();
    const pending = new Workly({ fetch: hang }).tasks.get('t', { signal: controller.signal });
    controller.abort(new Error('stop'));
    await assert.rejects(pending, /stop/);
  });

  test('abort during a backoff', async () => {
    const controller = new AbortController();
    const { fetch } = fakeFetch(() => {
      setTimeout(() => controller.abort(new Error('stop')), 5);
      return json({}, 503, { 'Retry-After': '5' });
    });
    await assert.rejects(new Workly({ fetch }).tasks.get('t', { signal: controller.signal }), /stop/);
  });
});

describe('schedules', () => {
  const run = {
    id: 'run_1',
    schedule: 'nightly',
    state: 'pending',
    trigger: 'manual',
    scheduled_for: '2026-10-01T01:00:00Z',
    method: 'POST',
    url: 'https://example.com/report',
    headers: {},
    run_at: '2026-10-01T01:00:00Z',
    attempt_count: 0,
    created_at: '2026-10-01T01:00:00Z',
    updated_at: '2026-10-01T01:00:00Z',
  };

  test('put sends only the settings given', async () => {
    const { fetch, sent } = fakeFetch(json({ name: 'nightly' }, 201));
    await new Workly({ fetch }).schedules.put('nightly', {
      cron: '0 3 * * *',
      url: 'https://example.com/report',
      timezone: 'Europe/Stockholm',
      payload: { kind: 'daily' },
      overlap: undefined,
    });
    assert.equal(sent[0]!.method, 'PUT');
    assert.equal(sent[0]!.url.pathname, '/v1/schedules/nightly');
    assert.deepEqual(sent[0]!.body, {
      cron: '0 3 * * *',
      url: 'https://example.com/report',
      timezone: 'Europe/Stockholm',
      payload: { kind: 'daily' },
    });
    await assert.rejects(
      new Workly({ fetch }).schedules.put('n', { cron: '@daily', url: 'https://x', payload: {}, body: 'x' }),
      TypeError,
    );
  });

  test('trigger is not idempotent', async () => {
    const { fetch, sent } = fakeFetch(json({}, 503), json(run, 201));
    await assert.rejects(new Workly({ fetch }).schedules.trigger('nightly'), APIError);
    assert.equal(sent.length, 1);
  });

  test('runs paginate', async () => {
    const { fetch, sent } = fakeFetch(json({ runs: [run], next_cursor: 'c1' }), json({ runs: [run] }));
    const ids: string[] = [];
    for await (const r of new Workly({ fetch }).schedules.runs('nightly', { state: 'failed' })) ids.push(r.id);
    assert.deepEqual(ids, ['run_1', 'run_1']);
    assert.equal(sent[0]!.url.pathname, '/v1/schedules/nightly/runs');
    assert.equal(sent[0]!.url.searchParams.get('state'), 'failed');
    assert.equal(sent[1]!.url.searchParams.get('cursor'), 'c1');
  });

  test('runs', async () => {
    const { fetch, sent } = fakeFetch(json(run), json({ attempts: [] }), json(run));
    const workly = new Workly({ fetch });
    await workly.runs.get('run_1');
    assert.deepEqual(await workly.runs.attempts('run_1'), []);
    await workly.runs.cancel('run_1');
    assert.deepEqual(
      sent.map((s) => `${s.method} ${s.url.pathname}`),
      ['GET /v1/runs/run_1', 'GET /v1/runs/run_1/attempts', 'POST /v1/runs/run_1/cancel'],
    );
  });
});
