// End-to-end tests against a real `workly dev` server (see server.ts).

import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { after, afterEach, beforeEach, describe, test } from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';

import {
  APIConnectionError,
  BadRequestError,
  ConflictError,
  NotFoundError,
  SignatureVerificationError,
  type Task,
  type TaskSpec,
  verifySignature,
  Workly,
} from '../src/index.ts';
import { startServer, type Server } from './server.ts';

const DEV_SIGNING_SECRET = 'workly-dev-secret';
const FINISHED = new Set(['succeeded', 'failed', 'cancelled']);

const server = await startServer();

describe('integration', { skip: server ? false : 'set WORKLY_BIN or install Go' }, () => {
  const { url } = server ?? ({ url: '' } as Server);
  const workly = new Workly({ url });
  let queue: string;

  after(() => server?.stop());
  beforeEach(() => {
    queue = `test-${randomUUID().slice(0, 8)}`;
  });
  afterEach(() => workly.queues.delete(queue).catch(() => {}));

  async function waitFinished(id: string, timeoutMs = 10_000): Promise<Task> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const task = await workly.tasks.get(id);
      if (FINISHED.has(task.state)) return task;
      if (Date.now() > deadline) assert.fail(`task still ${task.state} after ${timeoutMs}ms`);
      await delay(50);
    }
  }

  test('delivery', async () => {
    let task = await workly.tasks.enqueue(queue, { url: `${url}/_dev/echo`, payload: { user_id: 1 } });
    assert.ok(['pending', 'running'].includes(task.state));
    assert.deepEqual(task.payload, { user_id: 1 });

    task = await waitFinished(task.id);
    assert.equal(task.state, 'succeeded');
    assert.equal(task.lastStatus, 200);
    const [attempt, ...rest] = await workly.tasks.attempts(task.id);
    assert.equal(rest.length, 0);
    assert.deepEqual([attempt?.number, attempt?.outcome, attempt?.statusCode], [1, 'success', 200]);
    assert.equal(task.lastAttemptAt, attempt?.finishedAt);
    const activity = await workly.queues.activity(queue);
    assert.deepEqual([activity.succeeded, activity.failed], [1, 0]);
  });

  test('task delivery', async () => {
    await workly.queues.put(queue);
    await workly.queues.pause(queue);
    const task = await workly.tasks.enqueue(queue, {
      url: 'http://127.0.0.1:9/hook',
      payload: { b: 1 },
      headers: { 'X-Tenant': 'acme' },
    });
    const d = await workly.tasks.delivery(task.id);
    assert.deepEqual(
      [d.method, d.url, d.headers['X-Tenant'], d.headers['Workly-Attempt']],
      ['POST', 'http://127.0.0.1:9/hook', 'acme', '1'],
    );
    await verifySignature(d.body, d.headers, DEV_SIGNING_SECRET);
  });

  test('signed delivery', async () => {
    const received: { body: Buffer; headers: Record<string, string | string[] | undefined> }[] = [];
    const target = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on('data', (c: Buffer) => chunks.push(c));
      req.on('end', () => {
        received.push({ body: Buffer.concat(chunks), headers: req.headers });
        res.writeHead(204).end();
      });
    });
    await new Promise<void>((resolve) => target.listen(0, '127.0.0.1', resolve));
    try {
      const { port } = target.address() as AddressInfo;
      const task = await workly.tasks.enqueue(queue, {
        url: `http://127.0.0.1:${port}/hook`,
        payload: { hello: 'world' },
      });
      assert.equal((await waitFinished(task.id)).state, 'succeeded');
      const [delivery] = received;
      assert.ok(delivery);
      await verifySignature(delivery.body, delivery.headers, DEV_SIGNING_SECRET);
      assert.equal(delivery.headers['workly-task-id'], task.id);
      await assert.rejects(
        verifySignature(Buffer.concat([delivery.body, Buffer.from(' ')]), delivery.headers, DEV_SIGNING_SECRET),
        SignatureVerificationError,
      );
    } finally {
      target.closeAllConnections();
      target.close();
    }
  });

  test('retry and attempts', async () => {
    await workly.queues.put(queue, { maxAttempts: 2, minBackoffSeconds: 0, maxBackoffSeconds: 0 });
    let task = await workly.tasks.enqueue(queue, { url: `${url}/_dev/echo?status=500` });
    task = await waitFinished(task.id);
    assert.deepEqual(
      [task.state, task.attemptCount, task.lastStatus, task.lastOutcome],
      ['failed', 2, 500, 'http_error'],
    );
    const [group] = await workly.queues.failures(queue);
    assert.deepEqual([group?.count, group?.statusCode, group?.latestTaskId], [1, 500, task.id]);
    const listed = await workly.tasks.listPage({ queue, lastError: group?.error });
    assert.deepEqual(
      listed.tasks.map((t) => t.id),
      [task.id],
    );
    assert.deepEqual(
      (await workly.tasks.attempts(task.id)).map((a) => a.outcome),
      ['http_error', 'http_error'],
    );

    assert.equal(task.retriedAt, undefined);
    task = await workly.tasks.retry(task.id);
    assert.equal(task.state, 'pending');
    assert.ok(task.retriedAt);
    assert.equal((await waitFinished(task.id)).attemptCount, 4);
    // Attempt 3 followed the manual retry; attempt 4 was automatic.
    assert.deepEqual(
      (await workly.tasks.attempts(task.id)).map((a) => a.retriedAt),
      [undefined, undefined, task.retriedAt, undefined],
    );
  });

  test('cancel', async () => {
    let task = await workly.tasks.enqueue(queue, { url: `${url}/_dev/echo`, delaySeconds: 3600 });
    assert.equal((await workly.tasks.cancel(task.id)).state, 'cancelled');
    task = await workly.tasks.retry(task.id);
    assert.equal((await waitFinished(task.id)).state, 'succeeded');
    const err = await workly.tasks.cancel(task.id).catch((e: unknown) => e);
    assert.ok(err instanceof ConflictError);
    assert.equal(err.code, 'invalid_state');
  });

  test('queues', async () => {
    let q = await workly.queues.put(queue, { maxAttempts: 3, ratePerSecond: 5 });
    assert.deepEqual([q.name, q.maxAttempts, q.ratePerSecond, q.paused], [queue, 3, 5, false]);
    // Declarative: omitted settings go back to their defaults.
    q = await workly.queues.put(queue, { maxAttempts: 3 });
    assert.equal(q.ratePerSecond, undefined);

    assert.equal((await workly.queues.pause(queue)).paused, true);
    for (let i = 0; i < 3; i++) await workly.tasks.enqueue(queue, { url: `${url}/_dev/echo` });
    const stats = (await workly.queues.get(queue)).stats;
    assert.deepEqual([stats?.due, stats?.failed], [3, 0]);
    assert.ok(stats?.oldestDueAt);
    assert.ok((await workly.queues.list()).some((x) => x.name === queue));

    assert.equal(await workly.queues.cancelPending(queue), 3);
    assert.deepEqual(
      (await workly.tasks.listPage({ queue })).tasks.map((t) => t.state),
      Array(3).fill('cancelled'),
    );
    assert.equal((await workly.queues.resume(queue)).paused, false);
    assert.equal(await workly.queues.retryFailed(queue), 0);
    assert.equal(await workly.queues.retryFailed(queue, { lastError: 'HTTP 500 Internal Server Error' }), 0);

    await workly.queues.delete(queue);
    const err = await workly.queues.get(queue).catch((e: unknown) => e);
    assert.ok(err instanceof NotFoundError);
    assert.equal(err.code, 'queue_not_found');
  });

  test('enqueueMany and list', async () => {
    await workly.queues.put(queue);
    await workly.queues.pause(queue);
    const specs: TaskSpec[] = Array.from({ length: 5 }, (_, i) => ({
      url: 'https://example.com/hook',
      idempotencyKey: `k${i}`,
      body: String(i),
    }));
    const results = await workly.tasks.enqueueMany(queue, specs);
    assert.ok(results.every((r) => r.created));
    const again = await workly.tasks.enqueueMany(queue, specs.slice(0, 2));
    assert.deepEqual(
      again.map((r) => [r.created, r.task.id]),
      results.slice(0, 2).map((r) => [false, r.task.id]),
    );

    // IDs only order by millisecond, so a batch's tasks come back in any order.
    const found: string[] = [];
    for await (const t of workly.tasks.list({ queue, pageSize: 2 })) found.push(t.id);
    assert.deepEqual(found.sort(), results.map((r) => r.task.id).sort());

    const page = await workly.tasks.listPage({ queue, limit: 2 });
    assert.equal(page.tasks.length, 2);
    assert.ok(page.nextCursor);

    // Pending tasks by runAt come in the order they run, soonest first.
    const later = await workly.tasks.enqueue(queue, { url: 'https://example.test', delaySeconds: 60 });
    const soon = await workly.tasks.enqueue(queue, { url: 'https://example.test', delaySeconds: 30 });
    const byRunAt: string[] = [];
    for await (const t of workly.tasks.list({ queue, sort: 'runAt', pageSize: 2 })) byRunAt.push(t.id);
    assert.deepEqual(byRunAt.slice(-2), [soon.id, later.id]);

    const byKey: Task[] = [];
    for await (const t of workly.tasks.list({ queue, idempotencyKey: 'k3' })) byKey.push(t);
    assert.deepEqual(
      byKey.map((t) => t.body),
      ['3'],
    );
  });

  test('schedules', async () => {
    const name = `test-${randomUUID().slice(0, 8)}`;
    try {
      const schedule = await workly.schedules.put(name, {
        cron: '0 3 * * *',
        timezone: 'Europe/Stockholm',
        url: `${url}/_dev/echo?status=500`,
        payload: { kind: 'daily' },
        maxAttempts: 2,
        minBackoffSeconds: 0,
        maxBackoffSeconds: 0,
      });
      assert.equal(schedule.nextRuns.length, 5);
      assert.equal(schedule.nextRunAt, schedule.nextRuns[0]);
      assert.deepEqual(schedule.payload, { kind: 'daily' });

      assert.equal((await workly.schedules.pause(name)).nextRunAt, undefined);
      let run = await workly.schedules.trigger(name); // runs even while paused
      const deadline = Date.now() + 10_000;
      while (!FINISHED.has(run.state)) {
        if (Date.now() > deadline) assert.fail(`run still ${run.state}`);
        await delay(50);
        run = await workly.runs.get(run.id);
      }
      assert.deepEqual([run.state, run.trigger, run.attemptCount, run.lastStatus], ['failed', 'manual', 2, 500]);
      const delivery = await workly.runs.delivery(run.id);
      assert.equal(delivery.headers['Workly-Run-Id'], run.id);
      await verifySignature(delivery.body, delivery.headers, DEV_SIGNING_SECRET);

      const failed: string[] = [];
      for await (const r of workly.schedules.runs(name, { state: 'failed' })) failed.push(r.id);
      assert.deepEqual(failed, [run.id]);
      assert.equal((await workly.schedules.get(name)).lastRun?.id, run.id);
      assert.ok((await workly.schedules.resume(name)).nextRunAt);

      const err = await workly.schedules.put(name, { cron: 'every day', url }).catch((e: unknown) => e);
      assert.ok(err instanceof BadRequestError);
    } finally {
      await workly.schedules.delete(name);
    }
  });

  test('alert channels', async () => {
    const hook = `https://hooks.slack.com/services/T0/B0/${randomUUID()}`;
    const channel = await workly.alerts.create('slack', hook);
    try {
      assert.ok(channel.target.endsWith(hook.slice(-4)) && channel.target !== hook);
      const list = await workly.alerts.list();
      assert.ok(list.channels.some((c) => c.id === channel.id));
      if (!list.emailEnabled) {
        const err = await workly.alerts.create('email', 'ops@example.com').catch((e: unknown) => e);
        assert.ok(err instanceof BadRequestError);
        assert.equal(err.code, 'email_unavailable');
      }
    } finally {
      await workly.alerts.delete(channel.id);
    }
    await assert.rejects(workly.alerts.delete(channel.id), NotFoundError);
  });

  test('validation error', async () => {
    const err = await workly.tasks.enqueue(queue, { url: 'ftp://example.com' }).catch((e: unknown) => e);
    assert.ok(err instanceof BadRequestError);
    assert.equal(err.code, 'invalid_request');
  });
});

test('connection error', async () => {
  await assert.rejects(new Workly({ url: 'http://127.0.0.1:1', maxRetries: 0 }).queues.list(), APIConnectionError);
});
