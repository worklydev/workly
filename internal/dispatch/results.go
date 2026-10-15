package dispatch

import (
	"context"
	"time"

	"github.com/worklydev/workly/internal/store"
)

// maxResultBatch caps how many attempt results one transaction records.
const maxResultBatch = 500

// resultWriter records attempt results in batches. Results that arrive
// while a write is in flight go into the next one, so an idle dispatcher
// writes each result at once and a busy one commits once per batch instead
// of once per delivery: with many small commits, Postgres spends its time
// waiting on its write-ahead log.
//
// A delivery waits for its result to be written before it frees its slot,
// as it did when it wrote the result itself. If the process dies with
// results unwritten, their tasks are recovered by the lease reaper and
// delivered again, as after any crash mid-delivery.
type resultWriter struct {
	store store.Store
	reqs  chan resultReq
	done  chan struct{}
}

type resultReq struct {
	result store.FinishedAttempt
	reply  chan<- store.FinishOutcome
}

func startResultWriter(s store.Store) *resultWriter {
	w := &resultWriter{store: s, reqs: make(chan resultReq, maxResultBatch), done: make(chan struct{})}
	go w.run()
	return w
}

// finish records r and returns the task's resulting state, as
// store.FinishAttempt does.
func (w *resultWriter) finish(r store.FinishedAttempt) (store.TaskState, error) {
	reply := make(chan store.FinishOutcome, 1)
	w.reqs <- resultReq{result: r, reply: reply}
	o := <-reply
	return o.State, o.Err
}

// stop writes what is queued and returns. Nothing may call finish after.
func (w *resultWriter) stop() {
	close(w.reqs)
	<-w.done
}

func (w *resultWriter) run() {
	defer close(w.done)
	for req := range w.reqs {
		batch := []resultReq{req}
	collect:
		for len(batch) < maxResultBatch {
			select {
			case req, ok := <-w.reqs:
				if !ok {
					break collect
				}
				batch = append(batch, req)
			default:
				break collect
			}
		}
		w.write(batch)
	}
}

func (w *resultWriter) write(batch []resultReq) {
	// Record results even during shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rs := make([]store.FinishedAttempt, len(batch))
	for i, req := range batch {
		rs[i] = req.result
	}
	out, err := w.store.FinishAttempts(ctx, rs)
	if err != nil && len(batch) > 1 {
		// Write one at a time, so a result the database rejects doesn't
		// take the rest of the batch with it.
		for _, req := range batch {
			state, err := w.store.FinishAttempt(ctx, req.result.Workspace, req.result.AttemptResult)
			req.reply <- store.FinishOutcome{State: state, Err: err}
		}
		return
	}
	for i, req := range batch {
		if err != nil {
			req.reply <- store.FinishOutcome{Err: err}
			continue
		}
		req.reply <- out[i]
	}
}
