// Package store defines Workly's persistence interface.
//
// There are two implementations, postgres and sqlite, and both must pass the
// conformance suite in storetest. Every method that touches workspace data
// takes the workspace ID explicitly: there is no way to query across
// workspaces by accident. The exception is the dispatcher methods, which
// schedule work for all workspaces and are grouped under Dispatch.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/worklydev/workly/internal/workspace"
)

var (
	// ErrNotFound is returned when a requested record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNotLeader is returned by TryLead when another process holds leadership.
	ErrNotLeader = errors.New("another dispatcher is leader")
	// ErrLeaseLost is returned by FinishAttempt (FinishRunAttempt) when the
	// task (run) is no longer running under the given attempt, e.g. because
	// the lease reaper already recovered it or its queue (schedule) was
	// deleted.
	ErrLeaseLost = errors.New("lease lost")
	// ErrInvalidState is returned when a task or run can't make the requested
	// transition, e.g. cancelling a task that already succeeded.
	ErrInvalidState = errors.New("invalid task or run state for this operation")
	// ErrInvalidCursor is returned by ListTasks for a cursor it didn't
	// issue, or issued for another sort.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrGroupNotFound is returned, in a GroupError, by CreateTask and
	// CreateTasks for a group that doesn't exist. It wraps ErrNotFound.
	ErrGroupNotFound = fmt.Errorf("group %w", ErrNotFound)
	// ErrGroupClosed is returned, in a GroupError, by CreateTask and
	// CreateTasks for a group that no longer accepts tasks.
	ErrGroupClosed = errors.New("group is closed")
)

// GroupError says which group a task couldn't join, and why: Err is
// ErrGroupNotFound or ErrGroupClosed.
type GroupError struct {
	GroupID string
	Err     error
}

func (e *GroupError) Error() string { return e.GroupID + ": " + e.Err.Error() }
func (e *GroupError) Unwrap() error { return e.Err }

type Store interface {
	// Ping checks that the database is reachable.
	Ping(ctx context.Context) error
	// Migrate applies all pending schema migrations. It is safe to call
	// concurrently from multiple processes.
	Migrate(ctx context.Context) error
	// MigrationsPending reports whether any embedded migration has not been applied.
	MigrationsPending(ctx context.Context) (bool, error)
	Close() error

	// EnsureWorkspace creates the workspace if it doesn't exist and returns it.
	EnsureWorkspace(ctx context.Context, id workspace.ID, name string) (Workspace, error)
	GetWorkspace(ctx context.Context, id workspace.ID) (Workspace, error)

	// PutQueue creates the queue or replaces its configuration. created
	// reports whether it was new.
	PutQueue(ctx context.Context, ws workspace.ID, name string, cfg QueueConfig) (q Queue, created bool, err error)
	GetQueue(ctx context.Context, ws workspace.ID, name string) (Queue, error)
	ListQueues(ctx context.Context, ws workspace.ID) ([]Queue, error)
	// DeleteQueue deletes the queue with all its tasks and attempts.
	DeleteQueue(ctx context.Context, ws workspace.ID, name string) error
	// SetQueuePaused pauses or resumes dispatch from a queue. PutQueue
	// doesn't change whether a queue is paused.
	SetQueuePaused(ctx context.Context, ws workspace.ID, name string, paused bool) (Queue, error)
	// QueueStats returns the stats of the named queue, or of every queue in
	// the workspace if name is empty, keyed by queue name. A missing queue is
	// absent from the map.
	QueueStats(ctx context.Context, ws workspace.ID, name string, now time.Time) (map[string]QueueStats, error)
	// QueueActivity counts the queue's tasks that finished at or after
	// since, by final state. Only retained tasks count, and counts stop at
	// StatsCountLimit.
	QueueActivity(ctx context.Context, ws workspace.ID, queue string, since time.Time) (QueueActivity, error)
	// TaskActivity counts the workspace's tasks created at or after since,
	// per queue and per bucket of the given size, aligned to the Unix epoch
	// (so to UTC hours and days). Empty buckets are left out.
	TaskActivity(ctx context.Context, ws workspace.ID, since time.Time, bucket time.Duration) ([]ActivityCount, error)
	// QueueFailures groups the queue's failed tasks by how their latest
	// attempt ended, most common first, and returns up to limit groups. Only
	// the StatsCountLimit most recently failed tasks are grouped.
	QueueFailures(ctx context.Context, ws workspace.ID, queue string, limit int) ([]FailureGroup, error)
	// CancelPendingTasks cancels the queue's pending tasks (including
	// scheduled ones), as CancelTask does, and returns how many. Running
	// tasks are unaffected. It returns ErrNotFound if the queue doesn't
	// exist.
	CancelPendingTasks(ctx context.Context, ws workspace.ID, queue string) (int, error)

	// CreateTask enqueues a task. If t.IdempotencyKey matches an existing
	// task in the same queue, that task is returned with created = false.
	// If the queue doesn't exist it is created with defaults when
	// autoCreateQueue is set, and ErrNotFound is returned otherwise. A task
	// with a GroupID joins that group, which must be open (ErrGroupNotFound,
	// ErrGroupClosed).
	CreateTask(ctx context.Context, ws workspace.ID, t NewTask, autoCreateQueue bool) (task Task, created bool, err error)
	// CreateTasks enqueues tasks atomically, as CreateTask does one.
	CreateTasks(ctx context.Context, ws workspace.ID, ts []NewTask, autoCreateQueue bool) (tasks []Task, created []bool, err error)
	GetTask(ctx context.Context, ws workspace.ID, id string) (Task, error)
	// ListTasks returns tasks matching the filter, in the filter's order
	// (newest first by default). Pass the returned cursor back, with the
	// same filter, to get the next page; it is empty on the last page.
	ListTasks(ctx context.Context, ws workspace.ID, f TaskFilter) (tasks []Task, cursor string, err error)
	// ListAttempts returns a task's attempts, oldest first.
	ListAttempts(ctx context.Context, ws workspace.ID, taskID string) ([]Attempt, error)
	// CancelTask cancels a pending or running task. A running task's
	// current attempt finishes and is recorded, but the task isn't retried.
	// Cancelling a cancelled task is a no-op; cancelling a succeeded or
	// failed one returns ErrInvalidState.
	CancelTask(ctx context.Context, ws workspace.ID, id string) (Task, error)
	// RetryTask makes a task due now. A finished task (succeeded, failed or
	// cancelled) is replayed with a fresh attempt budget. It returns
	// ErrInvalidState while an attempt is in flight.
	RetryTask(ctx context.Context, ws workspace.ID, id string) (Task, error)
	// RetryFailedTasks replays the failed tasks in the queue, as RetryTask
	// does, and returns how many. With lastError set, only tasks whose
	// LastError equals it are replayed. It returns ErrNotFound if the queue
	// doesn't exist.
	RetryFailedTasks(ctx context.Context, ws workspace.ID, queue, lastError string) (int, error)

	// CreateGroup creates an open group. If g.IdempotencyKey matches an
	// existing group, that group is returned with created = false.
	CreateGroup(ctx context.Context, ws workspace.ID, g NewGroup) (group Group, created bool, err error)
	GetGroup(ctx context.Context, ws workspace.ID, id string) (Group, error)
	// ListGroups returns groups matching the filter, newest first, paged
	// like ListRuns.
	ListGroups(ctx context.Context, ws workspace.ID, f GroupFilter) (groups []Group, cursor string, err error)
	// CloseGroup stops a group from accepting tasks. A group whose tasks
	// are all finished (or that has none) completes at once; closing a
	// closed group changes nothing.
	CloseGroup(ctx context.Context, ws workspace.ID, id string) (Group, error)

	// PutSchedule creates the schedule or replaces its settings, and sets
	// when it next fires unless it is paused. created reports whether it was
	// new.
	PutSchedule(ctx context.Context, ws workspace.ID, name string, cfg ScheduleConfig, nextRunAt time.Time) (s Schedule, created bool, err error)
	GetSchedule(ctx context.Context, ws workspace.ID, name string) (Schedule, error)
	ListSchedules(ctx context.Context, ws workspace.ID) ([]Schedule, error)
	// DeleteSchedule deletes the schedule with all its runs and attempts.
	DeleteSchedule(ctx context.Context, ws workspace.ID, name string) error
	// SetSchedulePaused pauses a schedule, or resumes a paused one from
	// nextRunAt. Resuming a schedule that isn't paused changes nothing.
	SetSchedulePaused(ctx context.Context, ws workspace.ID, name string, paused bool, nextRunAt time.Time) (Schedule, error)
	// TriggerSchedule creates a manual run, due now, whatever the schedule's
	// paused state and overlap policy.
	TriggerSchedule(ctx context.Context, ws workspace.ID, name string, now time.Time) (Run, error)
	// ListRuns returns runs matching the filter, newest first, paged like
	// ListTasks.
	ListRuns(ctx context.Context, ws workspace.ID, f RunFilter) (runs []Run, cursor string, err error)
	GetRun(ctx context.Context, ws workspace.ID, id string) (Run, error)
	// ListRunAttempts returns a run's attempts, oldest first.
	ListRunAttempts(ctx context.Context, ws workspace.ID, runID string) ([]Attempt, error)
	// CancelRun cancels a pending or running run, as CancelTask does a task.
	// Cancelling a cancelled run is a no-op; any other state returns
	// ErrInvalidState.
	CancelRun(ctx context.Context, ws workspace.ID, id string) (Run, error)
	// RetryRun makes a run due now. A finished or skipped run is delivered
	// again with a fresh attempt budget. It returns ErrInvalidState while an
	// attempt is in flight.
	RetryRun(ctx context.Context, ws workspace.ID, id string) (Run, error)

	// CreateAlertChannel adds a channel. It returns ErrExists if the
	// workspace has one of the same type and target.
	CreateAlertChannel(ctx context.Context, ws workspace.ID, typ AlertChannelType, target string) (AlertChannel, error)
	GetAlertChannel(ctx context.Context, ws workspace.ID, id string) (AlertChannel, error)
	// ListAlertChannels returns the workspace's channels, oldest first.
	ListAlertChannels(ctx context.Context, ws workspace.ID) ([]AlertChannel, error)
	DeleteAlertChannel(ctx context.Context, ws workspace.ID, id string) error
	// FailuresToAlert returns, for each queue and schedule in the workspace
	// that is due an alert, its tasks or runs that failed after its last
	// alert (or w.Since, whichever is later) and up to w.Now. A subject is
	// due once w.Cooldown has passed since its last alert.
	FailuresToAlert(ctx context.Context, ws workspace.ID, w AlertWindow) ([]AlertFailures, error)
	// MarkAlerted records that the subjects were alerted about at the
	// given time.
	MarkAlerted(ctx context.Context, ws workspace.ID, subjects []AlertSubject, at time.Time) error

	Dispatch
	Changes
}

// Changes carries change notices between the replicas sharing a database,
// so each can tell the dashboards it serves that a workspace's queues,
// tasks, schedules or runs changed. Notices are hints: they say which
// workspaces changed, not what, and may be lost.
type Changes interface {
	// PublishChanges tells every replica listening, this one included, that
	// the workspaces changed.
	PublishChanges(ctx context.Context, ws []workspace.ID) error
	// ListenChanges calls fn with the workspaces of each notice published
	// from the time it is listening (it calls ready then) until ctx is done
	// or the connection fails. fn must not block.
	ListenChanges(ctx context.Context, ready func(), fn func([]workspace.ID)) error
}

// Dispatch holds the methods used by the dispatcher. Unlike the rest of the
// Store, they span all workspaces.
type Dispatch interface {
	// TryLead attempts to become the single dispatcher. It returns
	// ErrNotLeader if another process holds leadership.
	TryLead(ctx context.Context) (Leader, error)

	// DueQueues returns unpaused queues that have pending tasks due at now.
	DueQueues(ctx context.Context, now time.Time) ([]Queue, error)
	// NextRunAt returns the earliest run_at after the given time of any
	// pending task.
	NextRunAt(ctx context.Context, after time.Time) (t time.Time, ok bool, err error)
	// ClaimTasks moves up to limit due tasks of a queue to running, increments
	// their attempt count and leases them until now + their timeout + grace.
	// It claims nothing if the queue has been paused.
	ClaimTasks(ctx context.Context, q Queue, limit int, now time.Time, grace time.Duration) ([]Task, error)
	// FinishAttempt records an attempt and moves the task to r.NextState,
	// or leaves it cancelled if it was cancelled mid-attempt. It returns the
	// task's resulting state, or ErrLeaseLost if attempt r.Number is no
	// longer in flight, in which case nothing is written.
	FinishAttempt(ctx context.Context, ws workspace.ID, r AttemptResult) (TaskState, error)
	// FinishAttempts records several attempts in one transaction, each as
	// FinishAttempt would, so a busy dispatcher commits once per batch
	// rather than once per delivery. Each task may appear at most once.
	// outcomes[i] is rs[i]'s; its Err is nil or ErrLeaseLost. If err is
	// non-nil, nothing is written.
	FinishAttempts(ctx context.Context, rs []FinishedAttempt) (outcomes []FinishOutcome, err error)
	// ExpiredLeases returns up to limit tasks with an attempt in flight whose
	// lease expired before now, i.e. whose dispatcher crashed or lost its
	// database connection mid-delivery.
	ExpiredLeases(ctx context.Context, now time.Time, limit int) ([]Task, error)
	// AllQueueStats returns every queue's stats across all workspaces, for
	// metrics.
	AllQueueStats(ctx context.Context, now time.Time) ([]QueueStatus, error)
	// DeleteFinishedTasks deletes up to limit succeeded, failed or cancelled
	// tasks that finished before the given time, with their attempts. It
	// returns how many it deleted.
	DeleteFinishedTasks(ctx context.Context, before time.Time, limit int) (int, error)

	// DeleteFinishedGroups deletes up to limit groups that completed before
	// the given time. It returns how many it deleted.
	DeleteFinishedGroups(ctx context.Context, before time.Time, limit int) (int, error)

	// DueSchedules returns unpaused schedules whose next slot is at or
	// before now.
	DueSchedules(ctx context.Context, now time.Time) ([]Schedule, error)
	// FireSchedule creates the run for f.Slot and moves the schedule to
	// f.NextRunAt, if the schedule is unpaused and its next slot is still
	// f.Due; otherwise it does nothing and returns fired = false. The run is
	// skipped if f.SkipReason is set, or if the schedule's overlap policy is
	// skip and a run is still pending or running. A slot never gets two
	// runs.
	FireSchedule(ctx context.Context, f Fire, now time.Time) (run Run, fired bool, err error)
	// NextScheduleWork returns the earliest time after the given one when a
	// schedule fires or a pending run is due.
	NextScheduleWork(ctx context.Context, after time.Time) (t time.Time, ok bool, err error)
	// ClaimRuns moves up to limit due runs to running, oldest first,
	// increments their attempt count and leases them until now + their
	// schedule's timeout + grace.
	ClaimRuns(ctx context.Context, limit int, now time.Time, grace time.Duration) ([]RunClaim, error)
	// FinishRunAttempt records a run's attempt and moves it to r.NextState,
	// as FinishAttempt does for tasks.
	FinishRunAttempt(ctx context.Context, ws workspace.ID, r RunAttemptResult) (RunState, error)
	// ExpiredRunLeases returns up to limit runs with an attempt in flight
	// whose lease expired before now.
	ExpiredRunLeases(ctx context.Context, now time.Time, limit int) ([]Run, error)
	// DeleteFinishedRuns deletes up to limit runs that finished before the
	// given time, with their attempts, except each schedule's latest run.
	DeleteFinishedRuns(ctx context.Context, before time.Time, limit int) (int, error)
	// AllScheduleStatus returns every schedule's status across all
	// workspaces, for metrics.
	AllScheduleStatus(ctx context.Context) ([]ScheduleStatus, error)
	// AllAlertChannels returns every workspace's alert channels, oldest
	// first, for the alerts loop.
	AllAlertChannels(ctx context.Context) ([]AlertChannel, error)
}

// Leader is held by the one dispatcher allowed to deliver tasks.
type Leader interface {
	// Wake receives a value when a task is enqueued (due now or later), so
	// the dispatcher can dispatch it or reset its timer. It is a hint:
	// wake-ups coalesce, and the dispatcher also polls.
	Wake() <-chan struct{}
	// Lost is closed if leadership is lost, e.g. the database connection
	// holding it dropped.
	Lost() <-chan struct{}
	// Release gives up leadership.
	Release()
}

type Workspace struct {
	ID        workspace.ID
	Name      string
	CreatedAt time.Time
}

// QueueConfig is a queue's delivery policy. Tasks can override MaxAttempts
// and Timeout.
type QueueConfig struct {
	// RatePerSecond limits how many deliveries start per second. 0 means
	// unlimited.
	RatePerSecond float64
	// MaxConcurrency limits how many deliveries are in flight. The API
	// always sets it; 0, for no limit, is left to tests.
	MaxConcurrency int

	MaxAttempts       int
	MinBackoff        time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
	Timeout           time.Duration
	// RetryOn4xx retries 4xx responses other than 408 and 429, which are
	// otherwise treated as permanent failures.
	RetryOn4xx bool
}

func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		// Low, so a queue created by an enqueue can't flood its endpoint.
		MaxConcurrency:    3,
		MaxAttempts:       5,
		MinBackoff:        2 * time.Second,
		MaxBackoff:        time.Hour,
		BackoffMultiplier: 2,
		Timeout:           30 * time.Second,
	}
}

type Queue struct {
	WorkspaceID workspace.ID
	Name        string
	Paused      bool
	QueueConfig
	CreatedAt time.Time
	UpdatedAt time.Time
}

// StatsCountLimit caps the counts in QueueStats, so that stats stay cheap
// on queues with a large backlog.
const StatsCountLimit = 10000

// QueueStats describes a queue's current work. Counts stop at
// StatsCountLimit.
type QueueStats struct {
	// Due counts pending tasks whose run_at has passed.
	Due int
	// Scheduled counts pending tasks due in the future, including ones
	// waiting to retry.
	Scheduled int
	Running   int
	// Failed counts tasks that failed and are still retained.
	Failed int
	// OldestDueAt is the earliest run_at of a due task: how long the queue
	// has been behind. Nil when nothing is due.
	OldestDueAt *time.Time
}

// ActivityCount is how many tasks of a queue were created in the bucket
// starting at Start.
type ActivityCount struct {
	Queue string
	Start time.Time
	Count int
}

// QueueActivity counts a queue's finished tasks by final state.
type QueueActivity struct {
	Succeeded int
	Failed    int
	Cancelled int
}

// FailureGroup is a set of failed tasks whose latest attempt ended the same
// way.
type FailureGroup struct {
	// Outcome is empty for tasks that failed before outcomes were recorded
	// on tasks.
	Outcome    Outcome
	StatusCode *int
	Error      string
	Count      int
	LastFailed time.Time
	// LatestTaskID is the group's most recently failed task.
	LatestTaskID string
	// ResponseBody is the start of the latest task's last response body.
	ResponseBody string
}

// QueueStatus is a queue's stats with its identity, for metrics.
type QueueStatus struct {
	WorkspaceID workspace.ID
	Queue       string
	Paused      bool
	QueueStats
}

type TaskState string

const (
	TaskPending   TaskState = "pending"
	TaskRunning   TaskState = "running"
	TaskSucceeded TaskState = "succeeded"
	TaskFailed    TaskState = "failed"
	TaskCancelled TaskState = "cancelled"
)

type NewTask struct {
	Queue   string
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
	RunAt   time.Time
	// MaxAttempts and Timeout override the queue's policy when non-nil.
	MaxAttempts    *int
	Timeout        *time.Duration
	IdempotencyKey string
	// TraceContext is the W3C traceparent captured at enqueue.
	TraceContext string
	GroupID      string
}

type Task struct {
	ID          string
	WorkspaceID workspace.ID
	Queue       string
	State       TaskState
	Method      string
	URL         string
	Headers     map[string]string
	Body        []byte
	// RunAt is when the task is next eligible for dispatch.
	RunAt        time.Time
	AttemptCount int
	// ReplayedAttempts is AttemptCount when the task was last replayed. The
	// attempt budget (MaxAttempts) counts from there.
	ReplayedAttempts int
	MaxAttempts      *int
	Timeout          *time.Duration
	IdempotencyKey   string
	TraceContext     string
	LeaseExpiresAt   *time.Time
	LastStatus       *int
	LastOutcome      Outcome
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	FinishedAt       *time.Time
	// LastAttemptAt is when the latest attempt finished.
	LastAttemptAt *time.Time
	// RetriedAt is when the task was last retried by hand (RetryTask or
	// RetryFailedTasks).
	RetriedAt *time.Time
	// GroupID is the group the task belongs to, if any.
	GroupID string
}

// EffectiveMaxAttempts applies the queue default.
func (t Task) EffectiveMaxAttempts(q QueueConfig) int {
	if t.MaxAttempts != nil {
		return *t.MaxAttempts
	}
	return q.MaxAttempts
}

// AttemptsSinceReplay is the number of attempts counted against MaxAttempts.
func (t Task) AttemptsSinceReplay() int { return t.AttemptCount - t.ReplayedAttempts }

// EffectiveTimeout applies the queue default.
func (t Task) EffectiveTimeout(q QueueConfig) time.Duration {
	if t.Timeout != nil {
		return *t.Timeout
	}
	return q.Timeout
}

// TaskFilter selects tasks. Zero fields match everything.
type TaskFilter struct {
	Queue          string
	GroupID        string
	State          TaskState
	IdempotencyKey string
	LastError      string
	CreatedAfter   time.Time
	CreatedBefore  time.Time
	// Sort defaults to SortCreated. Ties are broken by ID, in the same
	// direction.
	Sort      TaskSort
	Ascending bool
	// Cursor continues a listing with the same filter, sort and direction.
	Cursor string
	Limit  int
}

// TaskSort is what tasks are listed by.
type TaskSort string

const (
	SortCreated TaskSort = "created_at"
	SortRunAt   TaskSort = "run_at"
	// SortLastAttempt orders tasks never attempted as oldest.
	SortLastAttempt TaskSort = "last_attempt_at"
	SortAttempts    TaskSort = "attempt_count"
)

type Outcome string

const (
	OutcomeSuccess      Outcome = "success"
	OutcomeHTTPError    Outcome = "http_error"
	OutcomeTimeout      Outcome = "timeout"
	OutcomeNetworkError Outcome = "network_error"
	// OutcomeLost means the result is unknown: the dispatcher crashed or was
	// shut down mid-delivery. The endpoint may have processed the request.
	OutcomeLost Outcome = "lost"
)

// Attempt is one delivery of a task or a schedule run. Exactly one of
// TaskID and RunID is set.
type Attempt struct {
	ID              string
	TaskID          string
	RunID           string
	Number          int
	StartedAt       time.Time
	FinishedAt      time.Time
	StatusCode      *int
	Outcome         Outcome
	Error           string
	ResponseBody    string
	ResponseHeaders map[string]string
	// RetriedAt is set when a manual retry came between the previous
	// attempt and this one: when the retry happened. The store sets it.
	RetriedAt *time.Time
}

func (a Attempt) Duration() time.Duration { return a.FinishedAt.Sub(a.StartedAt) }

// AttemptResult is an attempt plus the task's resulting state.
type AttemptResult struct {
	Attempt
	// NextState is pending (retry at RunAt), succeeded or failed.
	NextState TaskState
	RunAt     time.Time
}

// FinishedAttempt is an attempt result of a task in workspace Workspace.
type FinishedAttempt struct {
	Workspace workspace.ID
	AttemptResult
}

// FinishOutcome is what FinishAttempts did with one result.
type FinishOutcome struct {
	State TaskState
	Err   error
}

// GroupStatus is where a group is in its lifecycle: open (accepting tasks),
// running (closed, tasks unfinished), then succeeded or failed for good.
type GroupStatus string

const (
	GroupOpen      GroupStatus = "open"
	GroupRunning   GroupStatus = "running"
	GroupSucceeded GroupStatus = "succeeded"
	GroupFailed    GroupStatus = "failed"
)

// Finished reports whether the group has completed.
func (s GroupStatus) Finished() bool { return s == GroupSucceeded || s == GroupFailed }

// Callback is a task enqueued when something completes: a POST saying what
// completed. A group's has its summary (GroupSummary) as its JSON body.
type Callback struct {
	Queue   string
	URL     string
	Headers map[string]string
}

type NewGroup struct {
	Name           string
	IdempotencyKey string
	OnComplete     *Callback
}

// Group is a set of tasks tracked together. Its counts cover its tasks'
// current states while it is open or running, and are frozen once it
// completes: retrying a task of a completed group doesn't change them.
type Group struct {
	ID             string
	WorkspaceID    workspace.ID
	Name           string
	Status         GroupStatus
	IdempotencyKey string
	Total          int
	Succeeded      int
	Failed         int
	Cancelled      int
	OnComplete     *Callback
	// CallbackTaskID is the task enqueued for OnComplete when the group
	// completed.
	CallbackTaskID string
	CreatedAt      time.Time
	ClosedAt       *time.Time
	FinishedAt     *time.Time
}

// Remaining counts the tasks not yet succeeded, failed or cancelled.
func (g Group) Remaining() int { return g.Total - g.Succeeded - g.Failed - g.Cancelled }

// GroupSummary is the body of a group's callback.
type GroupSummary struct {
	GroupID   string      `json:"group_id"`
	Name      string      `json:"name,omitempty"`
	Status    GroupStatus `json:"status"`
	Total     int         `json:"total"`
	Succeeded int         `json:"succeeded"`
	Failed    int         `json:"failed"`
	Cancelled int         `json:"cancelled"`
}

// CallbackBody is the JSON body of g's callback.
func (g Group) CallbackBody() []byte {
	b, _ := json.Marshal(GroupSummary{
		GroupID: g.ID, Name: g.Name, Status: g.Status,
		Total: g.Total, Succeeded: g.Succeeded, Failed: g.Failed, Cancelled: g.Cancelled,
	})
	return b
}

// GroupFilter selects groups. Zero fields match everything.
type GroupFilter struct {
	Status GroupStatus
	Name   string
	Cursor string
	Limit  int
}
