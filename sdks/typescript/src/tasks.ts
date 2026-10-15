import type { RequestOptions, Workly } from './client.ts';
import { seg, timestamp } from './util.ts';
import type {
  Activity,
  ActivityRange,
  Attempt,
  Delivery,
  EnqueueResult,
  Task,
  TaskFilter,
  TaskPage,
  TaskSpec,
} from './types.ts';

export interface EnqueueOptions extends RequestOptions {
  /** W3C trace context, forwarded on every delivery so the handler joins your trace. */
  traceparent?: string | undefined;
}

export interface ListOptions extends TaskFilter, RequestOptions {
  /** Tasks per request, 1–200 (server default 50). */
  pageSize?: number | undefined;
}

export interface ListPageOptions extends TaskFilter, RequestOptions {
  /** The previous page's `nextCursor`. */
  cursor?: string | undefined;
  /** 1–200 (server default 50). */
  limit?: number | undefined;
}

export class Tasks {
  #client: Workly;

  constructor(client: Workly) {
    this.#client = client;
  }

  /**
   * Enqueues a task to deliver to `spec.url`.
   *
   * The queue is created with default settings if it doesn't exist. If
   * `idempotencyKey` matches a task already in the queue, that task is
   * returned and nothing is enqueued.
   */
  async enqueue(queue: string, spec: TaskSpec, options: EnqueueOptions = {}): Promise<Task> {
    const body = taskBody(spec);
    return this.#client.send({
      method: 'POST',
      path: `/v1/queues/${seg(queue)}/tasks`,
      body,
      headers: traceHeaders(options.traceparent),
      // Without a key, resending could enqueue the task twice.
      idempotent: body.idempotencyKey !== undefined,
      signal: options.signal,
    });
  }

  /** Enqueues up to 100 tasks at once: all of them, or none if any is invalid. */
  async enqueueMany(queue: string, specs: Iterable<TaskSpec>, options: EnqueueOptions = {}): Promise<EnqueueResult[]> {
    const tasks = Array.from(specs, taskBody);
    const res = await this.#client.send<{ results: EnqueueResult[] }>({
      method: 'POST',
      path: `/v1/queues/${seg(queue)}/tasks:batch`,
      body: { tasks },
      headers: traceHeaders(options.traceparent),
      idempotent: tasks.every((t) => t.idempotencyKey !== undefined),
      signal: options.signal,
    });
    return res.results;
  }

  async get(id: string, options: RequestOptions = {}): Promise<Task> {
    return this.#client.send({ method: 'GET', path: `/v1/tasks/${seg(id)}`, idempotent: true, signal: options.signal });
  }

  /** The task's delivery attempts, oldest first. */
  async attempts(id: string, options: RequestOptions = {}): Promise<Attempt[]> {
    const res = await this.#client.send<{ attempts: Attempt[] }>({
      method: 'GET',
      path: `/v1/tasks/${seg(id)}/attempts`,
      idempotent: true,
      signal: options.signal,
    });
    return res.attempts;
  }

  /**
   * The request that delivers the task, as it would be sent now and freshly
   * signed. Nothing is sent. Useful to replay a delivery by hand while fixing
   * an endpoint.
   */
  async delivery(id: string, options: RequestOptions = {}): Promise<Delivery> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/tasks/${seg(id)}/delivery`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Cancels a pending or running task. A running attempt finishes but isn't retried. */
  async cancel(id: string, options: RequestOptions = {}): Promise<Task> {
    return this.#client.send({
      method: 'POST',
      path: `/v1/tasks/${seg(id)}/cancel`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Makes the task due now. A finished task is replayed with fresh attempts. */
  async retry(id: string, options: RequestOptions = {}): Promise<Task> {
    // Not idempotent: resending could replay a task that already finished again.
    return this.#client.send({ method: 'POST', path: `/v1/tasks/${seg(id)}/retry`, signal: options.signal });
  }

  /**
   * Tasks matching every filter given, newest first, fetching pages as needed.
   *
   * ```ts
   * for await (const task of workly.tasks.list({ queue: 'emails', state: 'failed' })) { ... }
   * ```
   */
  async *list(options: ListOptions = {}): AsyncGenerator<Task, void, undefined> {
    const { pageSize, ...rest } = options;
    let cursor: string | undefined;
    do {
      const page = await this.listPage({ ...rest, ...(cursor && { cursor }), ...(pageSize && { limit: pageSize }) });
      yield* page.tasks;
      cursor = page.nextCursor;
    } while (cursor);
  }

  /** One page of `list`. Pass the previous page's `nextCursor` as `cursor`. */
  async listPage(options: ListPageOptions = {}): Promise<TaskPage> {
    return this.#client.send({
      method: 'GET',
      path: '/v1/tasks',
      query: {
        queue: options.queue,
        state: options.state,
        idempotency_key: options.idempotencyKey,
        last_error: options.lastError,
        created_after: timestamp(options.createdAfter, 'createdAfter'),
        created_before: timestamp(options.createdBefore, 'createdBefore'),
        cursor: options.cursor,
        limit: options.limit?.toString(),
      },
      idempotent: true,
      signal: options.signal,
    });
  }

  /**
   * Counts the workspace's tasks created over time, by queue (default range: 24h).
   * Buckets are 5 minutes for 1h, 1 hour for 24h and 6 hours for 7d, aligned to UTC.
   */
  async activity(options: RequestOptions & { range?: ActivityRange | undefined } = {}): Promise<Activity> {
    return this.#client.send({
      method: 'GET',
      path: '/v1/activity',
      query: { range: options.range },
      idempotent: true,
      signal: options.signal,
    });
  }
}

function taskBody(spec: TaskSpec): Record<string, unknown> {
  if (spec.payload !== undefined && spec.body !== undefined) throw new TypeError('pass payload or body, not both');
  if (spec.delaySeconds !== undefined && spec.runAt !== undefined) {
    throw new TypeError('pass delaySeconds or runAt, not both');
  }
  return { ...spec, runAt: timestamp(spec.runAt, 'runAt') };
}

function traceHeaders(traceparent: string | undefined): Record<string, string> {
  return traceparent ? { traceparent } : {};
}
