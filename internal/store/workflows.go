package store

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/worklydev/workly/internal/workspace"
)

// Workflows (docs/workflows.md) run a fixed DAG of HTTP steps. Each step is
// an ordinary task, so delivery, retries, signing and queue limits are the
// queues'. A run advances in the transactions that finish its steps' tasks,
// and that complete the groups its steps wait for: there is no engine to
// crash between a step finishing and its successors being enqueued.

// WorkflowQueue is the queue steps go to unless they name one. Its name is
// reserved, so only Workly enqueues into it.
const WorkflowQueue = "_workflows"

// WaitForHeader is the response header with which a step's endpoint hands
// its completion to another resource: "group/<id>".
const WaitForHeader = "Workly-Wait-For"

// ErrWorkflowStepTask is returned by RetryTask for a task that is a
// workflow step: its run has moved on, so replaying it alone would deliver
// it outside the workflow.
var ErrWorkflowStepTask = errors.New("task is a workflow step")

// WorkflowConfig is a workflow's definition. The API validates it; the
// store keeps it as JSON, and each run keeps the one it started with.
type WorkflowConfig struct {
	Steps []WorkflowStepConfig `json:"steps"`
	// MaxDuration bounds a run: one still running after it fails.
	MaxDuration time.Duration `json:"max_duration"`
}

// DefaultWorkflowMaxDuration is MaxDuration when it isn't set.
const DefaultWorkflowMaxDuration = 24 * time.Hour

// MaxWorkflowSteps bounds a workflow's size.
const MaxWorkflowSteps = 100

func (c WorkflowConfig) EffectiveMaxDuration() time.Duration {
	if c.MaxDuration <= 0 {
		return DefaultWorkflowMaxDuration
	}
	return c.MaxDuration
}

// WorkflowStepConfig is one step: the request its task sends, once all of
// DependsOn have succeeded.
type WorkflowStepConfig struct {
	Name      string   `json:"name"`
	DependsOn []string `json:"depends_on,omitempty"`
	// Queue is the queue the step's task goes to, WorkflowQueue by default.
	Queue   string            `json:"queue"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	// Body is the request body. When nil the step is sent the run's
	// context instead (a WorkflowStepBody).
	Body []byte `json:"body,omitempty"`
	// MaxAttempts and Timeout override the queue's policy when non-nil.
	MaxAttempts *int           `json:"max_attempts,omitempty"`
	Timeout     *time.Duration `json:"timeout,omitempty"`
}

// Headers on a step's task, set when the task is created: they tell the
// endpoint which step it is serving, and the task which step it delivers.
const (
	HeaderWorkflow       = "Workly-Workflow"
	HeaderWorkflowRunID  = "Workly-Workflow-Run-Id"
	HeaderWorkflowStep   = "Workly-Workflow-Step"
	HeaderWorkflowStepID = "Workly-Workflow-Step-Id"
	// HeaderWorkflowRunAttempt is the run's attempt, 1 until it is retried:
	// with the step ID, a key for work that a retried step should redo.
	HeaderWorkflowRunAttempt = "Workly-Workflow-Run-Attempt"
)

// TaskWorkflowStep is the workflow step a task delivers.
type TaskWorkflowStep struct {
	ID, Name, Workflow, RunID string
}

// WorkflowStep is the step the task delivers, read from the headers it was
// created with; ok is false for a task that isn't a step.
func (t Task) WorkflowStep() (step TaskWorkflowStep, ok bool) {
	if t.WorkflowStepID == "" {
		return TaskWorkflowStep{}, false
	}
	return TaskWorkflowStep{
		ID:       t.WorkflowStepID,
		Name:     t.Headers[HeaderWorkflowStep],
		Workflow: t.Headers[HeaderWorkflow],
		RunID:    t.Headers[HeaderWorkflowRunID],
	}, true
}

// WorkflowStepBody is the body of a step that has none of its own.
type WorkflowStepBody struct {
	WorkflowRunID string          `json:"workflow_run_id"`
	Workflow      string          `json:"workflow"`
	Step          string          `json:"step"`
	StepID        string          `json:"step_id"`
	RunAttempt    int             `json:"run_attempt"`
	Input         json.RawMessage `json:"input"`
}

type Workflow struct {
	WorkspaceID workspace.ID
	Name        string
	WorkflowConfig
	// Version counts the changes to the definition, from 1.
	Version   int
	LastRun   *WorkflowRunSummary
	CreatedAt time.Time
	UpdatedAt time.Time
}

type WorkflowRunStatus string

const (
	WorkflowRunning   WorkflowRunStatus = "running"
	WorkflowSucceeded WorkflowRunStatus = "succeeded"
	WorkflowFailed    WorkflowRunStatus = "failed"
	WorkflowCancelled WorkflowRunStatus = "cancelled"
)

func (s WorkflowRunStatus) Finished() bool { return s != WorkflowRunning }

// WorkflowRunSummary describes a workflow's latest run.
type WorkflowRunSummary struct {
	ID         string
	Status     WorkflowRunStatus
	CreatedAt  time.Time
	FinishedAt *time.Time
	Error      string
}

type NewWorkflowRun struct {
	Input          json.RawMessage
	IdempotencyKey string
	// ScheduleRunID is the schedule run that started it, if any.
	ScheduleRunID string
}

// WorkflowRun is one execution of a workflow, under the definition (and
// version) it started with.
type WorkflowRun struct {
	ID             string
	WorkspaceID    workspace.ID
	Workflow       string
	Version        int
	Config         WorkflowConfig
	Input          json.RawMessage
	Status         WorkflowRunStatus
	Error          string
	IdempotencyKey string
	ScheduleRunID  string
	DeadlineAt     time.Time
	CreatedAt      time.Time
	FinishedAt     *time.Time
	// Attempt is 1, plus one for each time the run was retried; RetriedAt
	// is when it last was.
	Attempt   int
	RetriedAt *time.Time
	// Steps are in definition order. Only GetWorkflowRun sets them.
	Steps []WorkflowStep
}

// StepStatus is where a step is. A pending step has a task; whether it is
// being delivered or retried is the task's state.
type StepStatus string

const (
	// StepBlocked waits for its dependencies.
	StepBlocked StepStatus = "blocked"
	// StepPending has its task enqueued.
	StepPending StepStatus = "pending"
	// StepWaiting's request succeeded, and handed completion to WaitFor.
	StepWaiting   StepStatus = "waiting"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	// StepSkipped never ran, or was dropped, because the run stopped.
	StepSkipped StepStatus = "skipped"
)

func (s StepStatus) Finished() bool {
	return s == StepSucceeded || s == StepFailed || s == StepSkipped
}

type WorkflowStep struct {
	ID        string
	RunID     string
	Name      string
	DependsOn []string
	Status    StepStatus
	TaskID    string
	// Task is the step's task, if it has one and it is still retained.
	Task *Task
	// WaitType and WaitID are the resource a waiting step waits for.
	WaitType  string
	WaitID    string
	Error     string
	CreatedAt time.Time
	// ReadyAt is when its task was enqueued; HTTPDoneAt when that task
	// succeeded; FinishedAt when the step finished.
	ReadyAt    *time.Time
	HTTPDoneAt *time.Time
	FinishedAt *time.Time
}

// WorkflowRunFilter selects runs. Zero fields match everything.
type WorkflowRunFilter struct {
	Workflow string
	Status   WorkflowRunStatus
	Cursor   string
	Limit    int
}
