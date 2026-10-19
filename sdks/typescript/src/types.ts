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
  /** How long the server keeps finished tasks; absent if forever. Older ones aren't counted. */
  retentionSeconds?: number;
}

/** When a cron timetable would run, from now. */
export interface CronPreview {
  nextRuns: string[];
  /** The shortest time between two of its runs. */
  intervalSeconds: number;
}

export type ActivityRange = '1h' | '24h' | '7d';

/** The workspace's tasks created over time: every bucket of the range, oldest first. */
export interface Activity {
  range: ActivityRange;
  bucketSeconds: number;
  buckets: { start: string; /** By queue; queues with none are left out. */ counts: Record<string, number> }[];
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
  /** When the task was last retried by hand. */
  retriedAt?: string;
  /** The group the task belongs to, if any. */
  groupId?: string;
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
  /** The http(s) URL to call. */
  url: string;
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
  /** When the schedule next fires. Absent while paused. */
  nextRunAt?: string;
  /** The next five slots (as if resumed now, while paused). */
  nextRuns: string[];
  lastRun?: LastRun;
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
  retriedAt?: string;
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
export interface Callback {
  /** The http(s) URL to deliver the callback to. */
  url: string;
  /**
   * The queue to enqueue the callback into. `_callbacks`, the default, is
   * created by Workly; configure it like any queue to change its retries.
   */
  queue?: string | undefined;
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
  /** Its `queue` is always set. */
  onComplete?: Callback & { queue: string };
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
