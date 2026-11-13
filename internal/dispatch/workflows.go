package dispatch

import (
	"context"
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
