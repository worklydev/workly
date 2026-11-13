package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/worklydev/workly/internal/store"
)

// Workflow runs advance in the store, in the transactions that finish
// their steps' tasks. The dispatcher only stops runs that outlived their
// max_duration, and sweeps finished ones past retention.

// expireInterval is how often runs past their deadline are failed; a run
// may overstay its max_duration by up to this much.
const expireInterval = time.Minute

func (d *Dispatcher) expireWorkflowRuns(ctx context.Context) {
	ticker := time.NewTicker(expireInterval)
	defer ticker.Stop()
	for {
		for {
			n, err := d.store.ExpireWorkflowRuns(ctx, time.Now(), sweepBatch)
			if err != nil {
				if ctx.Err() == nil {
					d.log.Warn("expiring workflow runs failed", "err", err)
				}
				break
			}
			if n > 0 {
				d.log.Info("failed workflow runs past their max_duration", "count", n)
			}
			if n < sweepBatch {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweepWorkflowRuns deletes the finished workflow runs sw covers.
func (d *Dispatcher) sweepWorkflowRuns(ctx context.Context, sw store.Sweep, retention time.Duration) {
	total := d.sweepBatches(ctx, "workflow runs", func() (int, error) {
		return d.store.DeleteFinishedWorkflowRuns(ctx, sw, sweepBatch)
	})
	if total > 0 {
		d.log.Info("deleted workflow runs past retention", "count", total, "retention", retention.String(), "workspace", sw.Workspace)
	}
}

// startWorkflow is a run's "delivery" for a schedule that starts a
// workflow: it starts a workflow run, with the schedule run's body as its
// input. The schedule run's ID is the idempotency key, so a retry, or a
// second leader claiming the run, starts the same workflow run, and its
// slot gets one however often it is attempted.
func (s *scheduler) startWorkflow(ctx context.Context, c store.RunClaim) result {
	r := result{started: time.Now(), headers: map[string]string{}}
	nr := store.NewWorkflowRun{IdempotencyKey: c.Run.ID, ScheduleRunID: c.Run.ID}
	if len(c.Run.Body) > 0 {
		if !json.Valid(c.Run.Body) {
			r.finished, r.outcome = time.Now(), store.OutcomeNetworkError
			r.err = "the schedule's body is not JSON, so it can't be a workflow run's input"
			return r
		}
		nr.Input = json.RawMessage(c.Run.Body)
	}
	wr, _, err := s.d.store.StartWorkflowRun(ctx, c.Run.WorkspaceID, c.Schedule.Workflow, nr, time.Now())
	r.finished = time.Now()
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Retried, as a failed delivery is: the workflow may be deployed
		// after the schedule.
		r.outcome, r.err = store.OutcomeNetworkError, fmt.Sprintf("workflow %q does not exist", c.Schedule.Workflow)
	case err != nil:
		r.outcome, r.err = store.OutcomeNetworkError, "starting workflow run: "+err.Error()
	default:
		r.outcome = store.OutcomeSuccess
		r.headers["Content-Type"] = "application/json"
		b, _ := json.Marshal(map[string]string{"workflow_run_id": wr.ID, "workflow": wr.Workflow})
		r.body = string(b)
		s.d.opts.Changed(c.Run.WorkspaceID)
	}
	return r
}
