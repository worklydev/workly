import type { RequestOptions, Workly } from './client.ts';
import { seg } from './util.ts';
import type { Attempt, CronPreview, Delivery, Run, RunPage, RunState, Schedule, ScheduleConfig } from './types.ts';

export interface RunsOptions extends RequestOptions {
  state?: RunState | undefined;
  /** Runs per request, 1–200 (server default 50). */
  pageSize?: number | undefined;
}

export interface RunsPageOptions extends RequestOptions {
  state?: RunState | undefined;
  /** The previous page's `nextCursor`. */
  cursor?: string | undefined;
  /** 1–200 (server default 50). */
  limit?: number | undefined;
}

/** Cron schedules that call an endpoint. Separate from queues: each call is a run. */
export class Schedules {
  #client: Workly;

  constructor(client: Workly) {
    this.#client = client;
  }

  /** All schedules, by name, with their next and last run. */
  async list(options: RequestOptions = {}): Promise<Schedule[]> {
    const res = await this.#client.send<{ schedules: Schedule[] }>({
      method: 'GET',
      path: '/v1/schedules',
      idempotent: true,
      signal: options.signal,
    });
    return res.schedules;
  }

  async get(name: string, options: RequestOptions = {}): Promise<Schedule> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/schedules/${seg(name)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /**
   * Creates or updates a schedule that calls `config.url` on `config.cron`.
   *
   * Declarative: the schedule ends up with exactly these settings, and
   * omitted ones are reset to their defaults. It doesn't change whether the
   * schedule is paused. `nextRuns` on the result shows when it runs.
   */
  async put(name: string, config: ScheduleConfig, options: RequestOptions = {}): Promise<Schedule> {
    if (config.payload !== undefined && config.body !== undefined)
      throw new TypeError('pass payload or body, not both');
    return this.#client.send({
      method: 'PUT',
      path: `/v1/schedules/${seg(name)}`,
      body: config,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Deletes a schedule and all its runs. */
  async delete(name: string, options: RequestOptions = {}): Promise<void> {
    await this.#client.send({
      method: 'DELETE',
      path: `/v1/schedules/${seg(name)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Stops the schedule from firing. Runs in flight finish; `trigger` still works. */
  async pause(name: string, options: RequestOptions = {}): Promise<Schedule> {
    return this.#post(name, 'pause', options, true);
  }

  /** Fires again from the next slot. Slots that passed while paused aren't run. */
  async resume(name: string, options: RequestOptions = {}): Promise<Schedule> {
    return this.#post(name, 'resume', options, true);
  }

  /** The next 5 times a schedule with this cron and timezone (default UTC) would run, without creating it. */
  async previewCron(
    cron: string,
    options: RequestOptions & { timezone?: string | undefined } = {},
  ): Promise<CronPreview> {
    return this.#client.send({
      method: 'GET',
      path: '/v1/cron/preview',
      query: { cron, timezone: options.timezone },
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Runs the schedule now, even if paused. Doesn't change when it next fires. */
  async trigger(name: string, options: RequestOptions = {}): Promise<Run> {
    // Not idempotent: resending would create a second run.
    return this.#post(name, 'trigger', options, false);
  }

  /**
   * The schedule's runs, newest first, fetching pages as needed.
   *
   * ```ts
   * for await (const run of workly.schedules.runs('nightly', { state: 'failed' })) { ... }
   * ```
   */
  async *runs(name: string, options: RunsOptions = {}): AsyncGenerator<Run, void, undefined> {
    const { pageSize, ...rest } = options;
    let cursor: string | undefined;
    do {
      const page = await this.runsPage(name, {
        ...rest,
        ...(cursor && { cursor }),
        ...(pageSize && { limit: pageSize }),
      });
      yield* page.runs;
      cursor = page.nextCursor;
    } while (cursor);
  }

  /** One page of `runs`. Pass the previous page's `nextCursor` as `cursor`. */
  async runsPage(name: string, options: RunsPageOptions = {}): Promise<RunPage> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/schedules/${seg(name)}/runs`,
      query: { state: options.state, cursor: options.cursor, limit: options.limit?.toString() },
      idempotent: true,
      signal: options.signal,
    });
  }

  async #post<T>(name: string, action: string, options: RequestOptions, idempotent: boolean): Promise<T> {
    return this.#client.send({
      method: 'POST',
      path: `/v1/schedules/${seg(name)}/${action}`,
      idempotent,
      signal: options.signal,
    });
  }
}

/** Runs of schedules: one per firing, with its own attempts. */
export class Runs {
  #client: Workly;

  constructor(client: Workly) {
    this.#client = client;
  }

  async get(id: string, options: RequestOptions = {}): Promise<Run> {
    return this.#client.send({ method: 'GET', path: `/v1/runs/${seg(id)}`, idempotent: true, signal: options.signal });
  }

  /** The run's delivery attempts, oldest first. */
  async attempts(id: string, options: RequestOptions = {}): Promise<Attempt[]> {
    const res = await this.#client.send<{ attempts: Attempt[] }>({
      method: 'GET',
      path: `/v1/runs/${seg(id)}/attempts`,
      idempotent: true,
      signal: options.signal,
    });
    return res.attempts;
  }

  /** The request that delivers the run, as it would be sent now and freshly signed. Nothing is sent. */
  async delivery(id: string, options: RequestOptions = {}): Promise<Delivery> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/runs/${seg(id)}/delivery`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Cancels a pending or running run. A running attempt finishes but isn't retried. */
  async cancel(id: string, options: RequestOptions = {}): Promise<Run> {
    return this.#client.send({
      method: 'POST',
      path: `/v1/runs/${seg(id)}/cancel`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Delivers the run now. A finished or skipped run gets fresh attempts. */
  async retry(id: string, options: RequestOptions = {}): Promise<Run> {
    // Not idempotent, like tasks.retry.
    return this.#client.send({ method: 'POST', path: `/v1/runs/${seg(id)}/retry`, signal: options.signal });
  }
}
