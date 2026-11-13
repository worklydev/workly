// Response and request types. They mirror the schemas in api/openapi.yaml
// field for field, with names in camelCase (`retry_on_4xx` is `retryOn4xx`).
// Timestamps are RFC 3339 strings. Fields added by newer servers come through
// too, just not typed yet. Payloads and headers are passed through as they are.

export type TaskState = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled';
/** What tasks are listed by. `runAt` is when a task runs next: pending tasks run in that order. */
export type TaskSort = 'createdAt' | 'runAt' | 'lastAttemptAt' | 'attemptCount';
export type SortOrder = 'asc' | 'desc';
export type HTTPMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
export type AttemptOutcome = 'success' | 'http_error' | 'timeout' | 'network_error' | 'lost';
export type RunState = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'skipped';
export type RunTrigger = 'schedule' | 'manual';
export type SkipReason = 'overlap' | 'missed';
export type OverlapPolicy = 'skip' | 'allow';
export type MissedPolicy = 'skip' | 'catch_up';
/** `open` accepts tasks; `running` is closed with tasks unfinished; `succeeded` and `failed` are final. */
export type GroupStatus = 'open' | 'running' | 'succeeded' | 'failed';

/** A queue's delivery policy. For `queues.put`, omitted settings take their defaults. */
export interface QueueConfig {
  /** Total delivery attempts before a task fails (default 5). Tasks can override it. */
  maxAttempts?: number | undefined;
  /** Default 2. */
  minBackoffSeconds?: number | undefined;
  /** Default 3600. */
  maxBackoffSeconds?: number | undefined;
  /**
   * Retry n waits a random time between `minBackoffSeconds` and
   * `minBackoffSeconds * backoffMultiplier^(n-1)`, capped at `maxBackoffSeconds`. Default 2.
   */
  backoffMultiplier?: number | undefined;
  /** How long to wait for a response (default 30). Tasks can override it. */
  timeoutSeconds?: number | undefined;
  /** Retry 4xx responses. By default only 408 and 429 are retried. */
  retryOn4xx?: boolean | undefined;
  /** Most deliveries started per second. Omit for no limit. */
  ratePerSecond?: number | undefined;
  /** Most deliveries in flight at once (default 3). */
  maxConcurrency?: number | undefined;
}

/** A queue's current work. Counts stop at 10,000. */
export interface QueueStats {
  /** Pending tasks that are due now. */
  due: number;
  /** Pending tasks due later, including ones waiting to retry. */
  scheduled: number;
  /** Tasks with a delivery in flight. */
  running: number;
  /** Failed tasks still retained by the server. */
  failed: number;
  /** When the longest-waiting due task became due. Absent when nothing is due. */
  oldestDueAt?: string;
}

export interface Queue {
  name: string;
  paused: boolean;
  maxAttempts: number;
  minBackoffSeconds: number;
  maxBackoffSeconds: number;
  backoffMultiplier: number;
  timeoutSeconds: number;
  retryOn4xx: boolean;
  ratePerSecond?: number;
  maxConcurrency: number;
  stats?: QueueStats;
  createdAt: string;
  updatedAt: string;
}

/** A queue's finished tasks since `since`, by final state. Counts stop at 10,000. */
export interface QueueActivity {
  since: string;
  succeeded: number;
  failed: number;
  cancelled: number;
  /** Median duration of the latest attempt of the succeeded and failed tasks counted. */
  p50DurationMs?: number;
  /** 95th percentile of the same durations. */
  p95DurationMs?: number;
  /** How many durations the percentiles are taken over; absent from older servers. */
  durationSamples?: number;
  /** How long the server keeps finished tasks; absent if forever. Older ones aren't counted. */
  retentionSeconds?: number;
}

/** When a cron timetable would run, from now. */
export interface CronPreview {
  nextRuns: string[];
  /** The shortest time between two of its runs. */
  intervalSeconds: number;
  /** With `from` and `until`: every run in the window, earliest first, as runs at a fixed interval. */
  spans?: CronSpan[];
}

/** `count` runs, `everySeconds` apart, the first at `start`. */
export interface CronSpan {
  start: string;
  /** 0 when `count` is 1. */
  everySeconds: number;
  count: number;
}

export type ActivityRange = '1h' | '24h' | '7d';

export interface ActivityBucket {
  start: string;
  /** Tasks created, by queue; queues with none are left out. */
  counts: Record<string, number>;
  /** Tasks of every queue that finished in the bucket and succeeded. */
  succeeded: number;
  /** Tasks of every queue that finished in the bucket and failed. */
  failed: number;
}

/** The workspace's tasks created and finished over time: every bucket of the range, oldest first. */
export interface Activity {
  range: ActivityRange;
  bucketSeconds: number;
  buckets: ActivityBucket[];
  /** How long the server keeps finished tasks; absent if forever. Older ones aren't counted. */
  retentionSeconds?: number;
}

/** Failed tasks in a queue whose latest attempt ended with the same error. */
export interface FailureGroup {
  /** The tasks' `lastError`. Pass it as `lastError` to list or retry them. */
  error: string;
  count: number;
  outcome?: AttemptOutcome;
  /** Absent if there was no response. */
  statusCode?: number;
  lastFailedAt: string;
  /** The group's most recently failed task. */
  latestTaskId: string;
  /** The start of the latest task's last response body, if it had one. */
  responseBody?: string;
}

export interface Task {
  id: string;
  queue: string;
  state: TaskState;
  method: HTTPMethod;
  url: string;
  headers: Record<string, string>;
  /** The JSON body, decoded. Absent when the task has a raw `body` (or no body). */
  payload?: unknown;
  /** The body, when it is not JSON. */
  body?: string;
  /** When the task is next eligible for delivery. */
  runAt: string;
  attemptCount: number;
  /** Per-task override, if set. */
  maxAttempts?: number;
  /** Per-task override, if set. */
  timeoutSeconds?: number;
  idempotencyKey?: string;
  /** HTTP status of the latest attempt, if it got a response. */
  lastStatus?: number;
  /** How the latest attempt ended; absent before the first attempt. */
  lastOutcome?: AttemptOutcome;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
  /** When the task succeeded, failed or was cancelled. */
  finishedAt?: string;
  /** When the latest attempt finished. */
  lastAttemptAt?: string;
  /** When the first delivery attempt started; minus `createdAt`, the time spent waiting. */
  startedAt?: string;
  /** How long the latest finished attempt took. */
  lastDurationMs?: number;
  /** When the task was last retried by hand. */
  retriedAt?: string;
  /** The group the task belongs to, if any. */
  groupId?: string;
  onComplete?: Callback;
  /** The callback task enqueued when the task last finished, if it has `onComplete`. */
  callbackTaskId?: string;
  /** The workflow step the task delivers, if it is one. */
  workflowStep?: TaskWorkflowStep;
}

/** The workflow step a task delivers. Its task can't be retried alone: start a new run instead. */
export interface TaskWorkflowStep {
  /** The step, `wfs_…`. */
  id: string;
  name: string;
  workflow: string;
  /** The workflow run, `wfr_…`. */
  runId: string;
}

export interface Attempt {
  id: string;
  number: number;
  /** `lost` means the result is unknown and the task is delivered again. */
  outcome: AttemptOutcome;
  statusCode?: number;
  error?: string;
  /** The first 4 KB of the response body. */
  responseBody?: string;
  /** Selected response headers (Content-Type, Retry-After, Location). */
  responseHeaders: Record<string, string>;
  startedAt: string;
  finishedAt: string;
  durationMs: number;
  /** Set when a manual retry came just before this attempt: when it happened. */
  retriedAt?: string;
}

/** The HTTP request that delivers a task or run, freshly signed. */
export interface Delivery {
  method: HTTPMethod;
  url: string;
  /** Every header Workly sets, including the task's own and `Workly-Signature`. */
  headers: Record<string, string>;
  /** The body exactly as sent; the signature covers it. */
  body: string;
}

export interface TaskPage {
  tasks: Task[];
  /** Pass as `cursor` to get the next page. Absent on the last page. */
  nextCursor?: string;
}

export interface EnqueueResult {
  /** False if a task with the same idempotency key already existed. */
  created: boolean;
  task: Task;
}

/** A task to enqueue. */
export interface TaskSpec {
  /** The http(s) URL to deliver the task to. */
  url: string;
  /** Sent as JSON. Use this or `body`. */
  payload?: unknown | undefined;
  /** Sent as raw text. Use this or `payload`. */
  body?: string | undefined;
  /** Content type of `body`. Defaults to `text/plain; charset=utf-8`. */
  contentType?: string | undefined;
  /** Default `POST`. */
  method?: HTTPMethod | undefined;
  /** Sent with every delivery. `Workly-*` headers are reserved. */
  headers?: Record<string, string> | undefined;
  /** Deliver after this delay. Use this or `runAt`. */
  delaySeconds?: number | undefined;
  /** Deliver at this time. Use this or `delaySeconds`. */
  runAt?: Date | string | undefined;
  /** If a task in the queue has this key, it is returned and nothing is enqueued. */
  idempotencyKey?: string | undefined;
  /** Overrides the queue's setting. */
  maxAttempts?: number | undefined;
  /** Overrides the queue's setting. */
  timeoutSeconds?: number | undefined;
  /** Adds the task to this group, which must be open. */
  groupId?: string | undefined;
  /**
   * Called each time the task succeeds, fails or is cancelled (again after a
   * retry by hand), with a `TaskSummary` as its JSON body.
   */
  onComplete?: Callback | undefined;
}

/** Filters for `tasks.list`. All are optional and combine. */
export interface TaskFilter {
  queue?: string | undefined;
  state?: TaskState | undefined;
  /** Only the tasks of this group. */
  groupId?: string | undefined;
  idempotencyKey?: string | undefined;
  /** Only tasks whose `lastError` is exactly this, e.g. a `FailureGroup`'s error. */
  lastError?: string | undefined;
  createdAfter?: Date | string | undefined;
  createdBefore?: Date | string | undefined;
  /** Defaults to `createdAt`. List pending tasks by `runAt` to see them in the order they run. */
  sort?: TaskSort | undefined;
  /** Defaults to `asc` (soonest first) for `runAt` and to `desc` (newest or most first) for the rest. */
  order?: SortOrder | undefined;
}

/**
 * A schedule's settings, for `schedules.put`. Omitted settings take their
 * defaults. The request fields work as in `TaskSpec`.
 */
export interface ScheduleConfig {
  /**
   * When to run: five fields (minute hour day-of-month month day-of-week), e.g.
   * `0 3 * * *`, or `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`.
   */
  cron: string;
  /** IANA timezone the cron expression is read in. Default `UTC`. */
  timezone?: string | undefined;
  /** The http(s) URL to call. Use this or `workflow`. */
  url?: string | undefined;
  /**
   * A workflow to start a run of instead of calling a URL, with `payload` as
   * the run's input. Use this or `url`.
   */
  workflow?: string | undefined;
  /** Sent as JSON. Use this or `body`. */
  payload?: unknown | undefined;
  /** Sent as raw text. Use this or `payload`. */
  body?: string | undefined;
  /** Content type of `body`. Defaults to `text/plain; charset=utf-8`. */
  contentType?: string | undefined;
  /** Default `POST`. */
  method?: HTTPMethod | undefined;
  /** Sent with every run. `Workly-*` headers are reserved. */
  headers?: Record<string, string> | undefined;
  /** How long to wait for a response (default 30). */
  timeoutSeconds?: number | undefined;
  /** Total delivery attempts before a run fails (default 3). */
  maxAttempts?: number | undefined;
  /** Default 5. */
  minBackoffSeconds?: number | undefined;
  /** Default 300. */
  maxBackoffSeconds?: number | undefined;
  /** Default 2. Retries back off as for queues. */
  backoffMultiplier?: number | undefined;
  /** Retry 4xx responses. By default only 408 and 429 are retried. */
  retryOn4xx?: boolean | undefined;
  /** A slot that comes while the previous run is still going: `skip` (default) or `allow`. */
  overlap?: OverlapPolicy | undefined;
  /** Slots missed while the server was down: `skip` (default) or `catch_up` (run the latest once). */
  missed?: MissedPolicy | undefined;
  /**
   * Called each time a run succeeds, fails or is cancelled (not when it is
   * skipped), with a `RunSummary` as its JSON body.
   */
  onComplete?: Callback | undefined;
}

/** A schedule's most recent run. */
export interface LastRun {
  id: string;
  state: RunState;
  trigger: RunTrigger;
  scheduledFor: string;
  finishedAt?: string;
  lastStatus?: number;
  lastError?: string;
}

/** A cron schedule that calls `url`. Separate from queues: its calls are runs. */
export interface Schedule {
  name: string;
  cron: string;
  timezone: string;
  method: HTTPMethod;
  url: string;
  headers: Record<string, string>;
  /** The JSON body, decoded. Absent when the schedule has a raw `body` (or no body). */
  payload?: unknown;
  body?: string;
  timeoutSeconds: number;
  maxAttempts: number;
  minBackoffSeconds: number;
  maxBackoffSeconds: number;
  backoffMultiplier: number;
  retryOn4xx: boolean;
  overlap: OverlapPolicy;
  missed: MissedPolicy;
  paused: boolean;
  /** When a paused schedule was paused. Absent while it runs. */
  pausedAt?: string;
  /** When the schedule next fires. Absent while paused. */
  nextRunAt?: string;
  /** The next five slots. Empty while paused; see `nextRunsIfResumed`. */
  nextRuns: string[];
  /** While paused, the five slots it would run at if resumed now. Absent while it runs. */
  nextRunsIfResumed?: string[];
  lastRun?: LastRun;
  onComplete?: Callback;
  /** The workflow each run starts, for a schedule that starts one instead of calling `url`. */
  workflow?: string;
  createdAt: string;
  updatedAt: string;
}

/** One firing of a schedule, delivered and retried like a task. */
export interface Run {
  id: string;
  schedule: string;
  state: RunState;
  trigger: RunTrigger;
  /** The cron slot, or when a manual run was triggered. */
  scheduledFor: string;
  /** Why a `skipped` run wasn't delivered. */
  skipReason?: SkipReason;
  method: HTTPMethod;
  url: string;
  headers: Record<string, string>;
  payload?: unknown;
  body?: string;
  runAt: string;
  attemptCount: number;
  lastStatus?: number;
  lastOutcome?: AttemptOutcome;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
  finishedAt?: string;
  lastAttemptAt?: string;
  /** When the first delivery attempt started. */
  startedAt?: string;
  /** How long the latest finished attempt took. */
  lastDurationMs?: number;
  retriedAt?: string;
  /** The callback task enqueued when the run last finished, if its schedule has `onComplete`. */
  callbackTaskId?: string;
  /** The workflow run it started, for a schedule that starts a workflow. */
  workflowRunId?: string;
}

export interface RunPage {
  runs: Run[];
  /** Pass as `cursor` to get the next page. Absent on the last page. */
  nextCursor?: string;
}

export type AlertChannelType = 'email' | 'slack';

/** Where the workspace's alerts about failed tasks and runs go. */
export interface AlertChannel {
  id: string;
  type: AlertChannelType;
  /** The email address, or the Slack webhook URL with its secret part hidden. */
  target: string;
  createdAt: string;
}

export interface AlertChannelList {
  channels: AlertChannel[];
  /** Whether the server can send email, so email channels can be added. */
  emailEnabled: boolean;
}

/**
 * A task Workly enqueues when something completes: a `POST` to `url` saying
 * what completed. A group's callback has the group's summary as its JSON body.
 * It is delivered like any task in its queue, with that queue's retries.
 */
/**
 * A `POST` Workly sends when a task, a schedule's run or a group finishes, as
 * a task in the `_callbacks` queue (configure it like any queue to change its
 * retries). Its JSON body is a `TaskSummary`, `RunSummary` or `GroupSummary`.
 */
export interface Callback {
  /** The http(s) URL to deliver the callback to. */
  url: string;
  /** Headers sent with the callback. `Workly-*` headers are reserved. */
  headers?: Record<string, string> | undefined;
}

/** @deprecated Use `Callback`. */
export type GroupCallback = Callback;

/** A group to create. */
export interface GroupSpec {
  /** A label, e.g. the job the group is one run of. Names needn't be unique. */
  name?: string | undefined;
  /** If a group has this key, it is returned and nothing is created. */
  idempotencyKey?: string | undefined;
  onComplete?: Callback | undefined;
}

/** A set of tasks tracked together, with a callback once all have finished. */
export interface Group {
  id: string;
  name?: string;
  status: GroupStatus;
  idempotencyKey?: string;
  total: number;
  succeeded: number;
  failed: number;
  cancelled: number;
  /** Tasks still pending or running. */
  remaining: number;
  onComplete?: Callback;
  /** The callback task, once the group has completed. */
  callbackTaskId?: string;
  createdAt: string;
  closedAt?: string;
  /** When the group completed. */
  finishedAt?: string;
}

export interface GroupPage {
  groups: Group[];
  /** Pass as `cursor` to get the next page. Absent on the last page. */
  nextCursor?: string;
}

/**
 * The JSON body of a group's callback. It is the task's payload, so its
 * field names are as sent: snake_case.
 */
export interface GroupSummary {
  group_id: string;
  name?: string;
  status: GroupStatus;
  total: number;
  succeeded: number;
  failed: number;
  cancelled: number;
}

/**
 * The JSON body of a task's callback. It is the task's payload, so its field
 * names are as sent: snake_case.
 */
export interface TaskSummary {
  task_id: string;
  queue: string;
  state: TaskState;
  attempt_count: number;
  last_status?: number;
  last_outcome?: AttemptOutcome;
  last_error?: string;
  idempotency_key?: string;
  group_id?: string;
  finished_at: string;
}

/** The JSON body of a schedule's callback, in snake_case as sent. */
export interface RunSummary {
  run_id: string;
  schedule: string;
  state: RunState;
  trigger: RunTrigger;
  scheduled_for: string;
  attempt_count: number;
  last_status?: number;
  last_outcome?: AttemptOutcome;
  last_error?: string;
  finished_at: string;
}

export type WorkflowRunStatus = 'running' | 'succeeded' | 'failed' | 'cancelled';

/**
 * `blocked` waits for its dependencies; `pending` has its task queued or
 * retrying; `running` is being delivered; `waiting` succeeded and waits for
 * its `waitFor` group; `skipped` never ran because the run stopped.
 */
export type StepStatus = 'blocked' | 'pending' | 'running' | 'waiting' | 'succeeded' | 'failed' | 'skipped';

/**
 * A step of a workflow: an HTTP request, sent as a task once the steps in
 * `dependsOn` have succeeded. The request fields work as in `TaskSpec`.
 * Without `payload` or `body`, the step is sent the run's context as JSON
 * (`workflow_run_id`, `workflow`, `step`, `step_id`, `input`).
 */
export interface WorkflowStepConfig {
  /** Unique within the workflow. */
  name: string;
  url: string;
  dependsOn?: string[] | undefined;
  method?: HTTPMethod | undefined;
  headers?: Record<string, string> | undefined;
  payload?: unknown | undefined;
  body?: string | undefined;
  contentType?: string | undefined;
  /** The queue the step's task goes to, for its retry policy and limits. Default `_workflows`. */
  queue?: string | undefined;
  /** Overrides the queue's timeout. */
  timeoutSeconds?: number | undefined;
  /** Overrides the queue's max attempts. */
  maxAttempts?: number | undefined;
}

/** A workflow's definition, for `workflows.put`. */
export interface WorkflowConfig {
  steps: WorkflowStepConfig[];
  /** How long a run may take before it fails. Default a day. */
  maxDurationSeconds?: number | undefined;
}

/** A DAG of HTTP steps, each delivered as a task once its dependencies have succeeded. */
export interface Workflow {
  name: string;
  steps: WorkflowStepConfig[];
  maxDurationSeconds: number;
  /** Counts changes to the definition, from 1. */
  version: number;
  lastRun?: WorkflowRunSummary;
  createdAt: string;
  updatedAt: string;
}

export interface StartWorkflowRunOptions {
  /** JSON passed to every step without a body of its own. */
  input?: unknown;
  /** Deduplicates runs of the workflow: an existing run with this key is returned. */
  idempotencyKey?: string | undefined;
}

/** A workflow run without its steps, as lists show it. */
export interface WorkflowRunSummary {
  id: string;
  workflow: string;
  /** The definition's version the run started with. */
  version: number;
  status: WorkflowRunStatus;
  /** Why the run failed or was cancelled. */
  error?: string;
  idempotencyKey?: string;
  /** The schedule run that started it, if any. */
  scheduleRunId?: string;
  createdAt: string;
  /** When a run still going fails (`maxDurationSeconds`). */
  deadlineAt?: string;
  finishedAt?: string;
  /** From start to finish, or to now while running. */
  durationMs?: number;
}

/** One execution of a workflow, under the definition it started with. */
export interface WorkflowRun extends WorkflowRunSummary {
  input?: unknown;
  /** In definition order. */
  steps: WorkflowStep[];
}

export interface WorkflowRunPage {
  workflowRuns: WorkflowRunSummary[];
  /** Pass as `cursor` to get the next page. Absent on the last page. */
  nextCursor?: string;
}

/** The resource a step handed its completion to with `Workly-Wait-For`. */
export interface WaitFor {
  type: 'group';
  id: string;
}

/** A step's task, which records its delivery attempts (`tasks.attempts`). */
export interface WorkflowStepTask {
  id: string;
  queue: string;
  state: TaskState;
  attemptCount: number;
  lastStatus?: number;
  lastOutcome?: AttemptOutcome;
  lastError?: string;
  /** When a pending task is next delivered. */
  runAt?: string;
}

/** A step of a run: where it is, and why. */
export interface WorkflowStep {
  /** Stable across retries of its request: use it as an idempotency key. */
  id: string;
  name: string;
  status: StepStatus;
  dependsOn: string[];
  /** Dependencies that haven't succeeded yet. */
  blockedBy: string[];
  taskId?: string;
  task?: WorkflowStepTask;
  waitFor?: WaitFor;
  error?: string;
  createdAt?: string;
  /** When its task was enqueued. */
  readyAt?: string;
  /** When its request succeeded. */
  httpDoneAt?: string;
  finishedAt?: string;
  blockedMs?: number;
  waitingMs?: number;
  durationMs?: number;
}
