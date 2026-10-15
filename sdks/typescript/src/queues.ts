import type { RequestOptions, Workly } from './client.ts';
import { seg, timestamp } from './util.ts';
import type { FailureGroup, Queue, QueueActivity, QueueConfig } from './types.ts';

export class Queues {
  #client: Workly;

  constructor(client: Workly) {
    this.#client = client;
  }

  /** All queues, by name, with their stats. */
  async list(options: RequestOptions = {}): Promise<Queue[]> {
    const res = await this.#client.send<{ queues: Queue[] }>({
      method: 'GET',
      path: '/v1/queues',
      idempotent: true,
      signal: options.signal,
    });
    return res.queues;
  }

  async get(name: string, options: RequestOptions = {}): Promise<Queue> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/queues/${seg(name)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /**
   * Creates or updates a queue.
   *
   * Declarative: the queue ends up with exactly this configuration, and
   * omitted settings are reset to their defaults. It doesn't change whether
   * the queue is paused.
   */
  async put(name: string, config: QueueConfig = {}, options: RequestOptions = {}): Promise<Queue> {
    return this.#client.send({
      method: 'PUT',
      path: `/v1/queues/${seg(name)}`,
      body: config,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Deletes a queue and all its tasks. */
  async delete(name: string, options: RequestOptions = {}): Promise<void> {
    await this.#client.send({
      method: 'DELETE',
      path: `/v1/queues/${seg(name)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Stops dispatching. In-flight deliveries finish; enqueueing still works. */
  async pause(name: string, options: RequestOptions = {}): Promise<Queue> {
    return this.#post(name, 'pause', options);
  }

  async resume(name: string, options: RequestOptions = {}): Promise<Queue> {
    return this.#post(name, 'resume', options);
  }

  /**
   * Cancels the queue's pending tasks, including scheduled ones. Running
   * tasks finish. Cancelled tasks stay in the history and can be retried.
   * Returns how many were cancelled.
   */
  async cancelPending(name: string, options: RequestOptions = {}): Promise<number> {
    return (await this.#post<{ cancelled: number }>(name, 'cancel-pending', options)).cancelled;
  }

  /**
   * Retries the failed tasks in the queue now, e.g. after an outage. Each
   * task gets a fresh set of attempts. With `lastError`, only tasks that
   * failed with exactly that error (a group from `failures`) are retried.
   * Returns how many were retried.
   */
  async retryFailed(name: string, options: RequestOptions & { lastError?: string | undefined } = {}): Promise<number> {
    const query = { last_error: options.lastError };
    return (await this.#post<{ retried: number }>(name, 'retry-failed', options, query)).retried;
  }

  /** The queue's failed tasks grouped by error, most common first (default: 10 groups). */
  async failures(name: string, options: RequestOptions & { limit?: number | undefined } = {}): Promise<FailureGroup[]> {
    const res = await this.#client.send<{ groups: FailureGroup[] }>({
      method: 'GET',
      path: `/v1/queues/${seg(name)}/failures`,
      query: { limit: options.limit?.toString() },
      idempotent: true,
      signal: options.signal,
    });
    return res.groups;
  }

  /** Counts the queue's tasks that finished since `since` (default: 7 days ago), by state. */
  async activity(
    name: string,
    options: RequestOptions & { since?: Date | string | undefined } = {},
  ): Promise<QueueActivity> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/queues/${seg(name)}/activity`,
      query: { since: timestamp(options.since, 'since') },
      idempotent: true,
      signal: options.signal,
    });
  }

  async #post<T = Queue>(
    name: string,
    action: string,
    options: RequestOptions,
    query?: Record<string, string | undefined>,
  ): Promise<T> {
    return this.#client.send({
      method: 'POST',
      path: `/v1/queues/${seg(name)}/${action}`,
      ...(query && { query }),
      idempotent: true,
      signal: options.signal,
    });
  }
}
