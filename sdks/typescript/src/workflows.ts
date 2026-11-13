import type { RequestOptions, Workly } from './client.ts';
import { seg } from './util.ts';
import type {
  StartWorkflowRunOptions,
  Workflow,
  WorkflowConfig,
  WorkflowRun,
  WorkflowRunPage,
  WorkflowRunStatus,
  WorkflowRunSummary,
} from './types.ts';

export interface WorkflowRunsOptions extends RequestOptions {
  /** Only this workflow's runs. */
  workflow?: string | undefined;
  status?: WorkflowRunStatus | undefined;
  /** Runs per request, 1–200 (server default 50). */
  pageSize?: number | undefined;
}

export interface WorkflowRunsPageOptions extends RequestOptions {
  /** Only this workflow's runs. */
  workflow?: string | undefined;
  status?: WorkflowRunStatus | undefined;
  /** The previous page's `nextCursor`. */
  cursor?: string | undefined;
  /** 1–200 (server default 50). */
  limit?: number | undefined;
}

/**
 * Workflows: DAGs of HTTP steps, each delivered as a task once its
 * dependencies have succeeded.
 *
 * A step's endpoint can hand its completion to a task group by answering 2xx
 * with `Workly-Wait-For: group/<id>`; the step then finishes when the group
 * does. Define workflows in workly.yaml (`workly deploy`) or with `put`.
 */
export class Workflows {
  #client: Workly;

  constructor(client: Workly) {
    this.#client = client;
  }

  /** All workflows, by name, with their latest run. */
  async list(options: RequestOptions = {}): Promise<Workflow[]> {
    const res = await this.#client.send<{ workflows: Workflow[] }>({
      method: 'GET',
      path: '/v1/workflows',
      idempotent: true,
      signal: options.signal,
    });
    return res.workflows;
  }

  async get(name: string, options: RequestOptions = {}): Promise<Workflow> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/workflows/${seg(name)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /**
   * Creates or updates a workflow with exactly these steps. Runs already
   * started keep the definition they started with.
   */
  async put(name: string, config: WorkflowConfig, options: RequestOptions = {}): Promise<Workflow> {
    for (const step of config.steps) {
      if (step.payload !== undefined && step.body !== undefined)
        throw new TypeError(`step ${step.name}: pass payload or body, not both`);
    }
    return this.#client.send({
      method: 'PUT',
      path: `/v1/workflows/${seg(name)}`,
      body: config,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Deletes the definition. Its runs are kept, and those going finish. */
  async delete(name: string, options: RequestOptions = {}): Promise<void> {
    await this.#client.send({
      method: 'DELETE',
      path: `/v1/workflows/${seg(name)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /**
   * Starts a run. `input` (any JSON) is sent to every step without a body of
   * its own. With an `idempotencyKey` already used for a run of this
   * workflow, that run is returned and nothing starts.
   */
  async trigger(name: string, run: StartWorkflowRunOptions = {}, options: RequestOptions = {}): Promise<WorkflowRun> {
    return this.#client.send({
      method: 'POST',
      path: `/v1/workflows/${seg(name)}/runs`,
      body: run,
      // Without a key, resending could start a second run.
      idempotent: run.idempotencyKey !== undefined,
      signal: options.signal,
    });
  }
}

/** Runs of workflows, with each step's status, task and what it waits for. */
export class WorkflowRuns {
  #client: Workly;

  constructor(client: Workly) {
    this.#client = client;
  }

  /** Runs of every workflow, or of one, newest first, fetching pages as needed. */
  async *list(options: WorkflowRunsOptions = {}): AsyncGenerator<WorkflowRunSummary, void, undefined> {
    const { pageSize, ...rest } = options;
    let cursor: string | undefined;
    do {
      const page = await this.listPage({ ...rest, ...(cursor && { cursor }), ...(pageSize && { limit: pageSize }) });
      yield* page.workflowRuns;
      cursor = page.nextCursor;
    } while (cursor);
  }

  /** One page of `list`. Pass the previous page's `nextCursor` as `cursor`. */
  async listPage(options: WorkflowRunsPageOptions = {}): Promise<WorkflowRunPage> {
    return this.#client.send({
      method: 'GET',
      path: '/v1/workflow-runs',
      query: {
        workflow: options.workflow,
        status: options.status,
        cursor: options.cursor,
        limit: options.limit?.toString(),
      },
      idempotent: true,
      signal: options.signal,
    });
  }

  async get(id: string, options: RequestOptions = {}): Promise<WorkflowRun> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/workflow-runs/${seg(id)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Stops a run: steps not yet started are skipped. Requests in flight finish. */
  async cancel(id: string, options: RequestOptions = {}): Promise<WorkflowRun> {
    return this.#client.send({
      method: 'POST',
      path: `/v1/workflow-runs/${seg(id)}/cancel`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /**
   * Reopens a failed or cancelled run under the definition it started with.
   * Steps that succeeded stay; the rest run again, each on its old task with
   * a fresh set of attempts. A step that was waiting for a group follows the
   * group.
   */
  async retry(id: string, options: RequestOptions = {}): Promise<WorkflowRun> {
    // Not idempotent: once the run is going again, retrying is invalid_state.
    return this.#client.send({ method: 'POST', path: `/v1/workflow-runs/${seg(id)}/retry`, signal: options.signal });
  }
}
