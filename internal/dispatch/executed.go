package dispatch

import (
	"context"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// Kind is what an execution delivered.
type Kind string

const (
	KindTask         Kind = "task"
	KindWorkflowStep Kind = "workflow_step"
	KindCallback     Kind = "callback"
	KindScheduleRun  Kind = "schedule_run"
)

// taskKind tells a task's kind: callbacks always go to their own queue,
// while a workflow step may name any queue.
func taskKind(t store.Task) Kind {
	switch {
	case t.WorkflowStepID != "":
		return KindWorkflowStep
	case t.Queue == store.CallbackQueue:
		return KindCallback
	default:
		return KindTask
	}
}

// executed reports a delivered attempt to Options.Executed if it was the
// first since the task or run was created or retried by hand. Retries with
// backoff, and redeliveries after a lost lease, aren't new executions.
func (d *Dispatcher) executed(ctx context.Context, ws workspace.ID, kind Kind, attemptsSinceReplay int) {
	if d.opts.Executed != nil && attemptsSinceReplay == 1 {
		d.opts.Executed(ctx, ws, kind)
	}
}
