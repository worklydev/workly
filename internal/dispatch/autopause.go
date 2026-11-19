package dispatch

import (
	"context"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// probeDelay is the wait before the probe that follows failed failed ones:
// first, doubling with each, up to most.
func probeDelay(failed int, first, most time.Duration) time.Duration {
	d := first
	for range failed {
		d *= 2
		if d >= most {
			return most
		}
	}
	return min(d, most)
}

type autoPauseSignal int

const (
	// signalNone neither counts nor resets: the result says nothing about
	// whether the endpoint is up.
	signalNone autoPauseSignal = iota
	signalFailure
	signalSuccess
)

// autoPauseSignalOf says what a delivery result means for its queue's
// auto-pause. Only outcomes that say the endpoint itself is unwell count:
// 5xx, timeouts and network errors. Other 4xx responses are usually about
// that one task, 429 means the endpoint is pacing us (Retry-After handles
// it), a lost attempt is our side, and a refused connection is
// configuration.
func autoPauseSignalOf(r result) autoPauseSignal {
	switch {
	case r.outcome == store.OutcomeSuccess:
		return signalSuccess
	case r.final:
		return signalNone
	case r.outcome == store.OutcomeTimeout, r.outcome == store.OutcomeNetworkError:
		return signalFailure
	case r.outcome == store.OutcomeHTTPError && r.status >= 500:
		return signalFailure
	}
	return signalNone
}

// autoPause updates queue q's auto-pause after a delivery to it: counting
// failures in a row, pausing the queue at its threshold, scheduling the next
// probe after a failed one, and resuming it on a success. q is the queue as
// it was when the dispatcher listed it, at claimed; probe says the task was
// claimed as its probe.
//
// The count is in memory: a new leader starts from zero, so at worst a queue
// pauses a little later after a failover.
func (d *Dispatcher) autoPause(ctx context.Context, q store.Queue, claimed time.Time, r result, probe bool) {
	if q.AutoPauseAfter == 0 && q.AutoPaused == nil {
		return
	}
	k := queueKey{q.WorkspaceID, q.Name}
	signal := autoPauseSignalOf(r)

	d.mu.Lock()
	// A delivery listed before this process paused the queue has a snapshot
	// that doesn't say so. One listed after, whose snapshot still doesn't,
	// means the queue was resumed by hand since.
	pausedAt, pausedHere := d.autoPaused[k]
	paused := q.AutoPaused != nil || (pausedHere && claimed.Before(pausedAt))
	if pausedHere && !paused {
		delete(d.autoPaused, k)
	}
	n := 0
	switch {
	case signal == signalSuccess:
		delete(d.failures, k)
		delete(d.autoPaused, k)
	case signal == signalFailure && !paused:
		d.failures[k]++
		n = d.failures[k]
		if n >= q.AutoPauseAfter {
			delete(d.failures, k)
			d.autoPaused[k] = time.Now()
		}
	}
	d.mu.Unlock()

	log := d.log.With("queue", q.Name)
	if q.WorkspaceID != workspace.Default {
		log = log.With("workspace", q.WorkspaceID)
	}
	now := time.Now()
	switch {
	case signal == signalSuccess && paused:
		cleared, err := d.store.AutoResumeQueue(ctx, q.WorkspaceID, q.Name)
		if err != nil {
			log.Error("could not resume auto-paused queue", "err", err)
			return
		}
		if cleared == nil {
			return // resumed by hand, or by another success
		}
		log.Info("queue resumed: delivery succeeded", "auto_paused_for", now.Sub(cleared.Since).Round(time.Second).String())
		d.opts.Changed(q.WorkspaceID)
		d.notifyAutoPause(store.AutoPauseEvent{Workspace: q.WorkspaceID, Queue: q.Name, Resumed: true, Since: cleared.Since, Error: cleared.Error})

	case probe && signal != signalSuccess:
		failed := 1
		if q.AutoPaused != nil {
			failed = q.AutoPaused.Probes + 1
		}
		next := now.Add(probeDelay(failed, d.opts.FirstProbeDelay, d.opts.MaxProbeDelay))
		if err := d.store.ProbeFailed(ctx, q.WorkspaceID, q.Name, r.err, next); err != nil {
			log.Error("could not record failed probe", "err", err)
			return
		}
		log.Info("auto-paused queue's probe failed", "err", r.err, "next_probe_in", next.Sub(now).Round(time.Second).String())
		d.opts.Changed(q.WorkspaceID)

	case n >= q.AutoPauseAfter && n > 0:
		ok, err := d.store.AutoPauseQueue(ctx, q.WorkspaceID, q.Name, r.err, now, now.Add(probeDelay(0, d.opts.FirstProbeDelay, d.opts.MaxProbeDelay)))
		if err != nil || !ok {
			d.mu.Lock()
			delete(d.autoPaused, k)
			d.mu.Unlock()
			if err != nil {
				log.Error("could not auto-pause queue", "err", err)
			}
			return
		}
		log.Warn("queue auto-paused: failures in a row", "failures", n, "err", r.err,
			"next_probe_in", probeDelay(0, d.opts.FirstProbeDelay, d.opts.MaxProbeDelay).String())
		d.metrics.QueueAutoPauses.Inc(string(q.WorkspaceID), q.Name)
		d.opts.Changed(q.WorkspaceID)
		d.notifyAutoPause(store.AutoPauseEvent{Workspace: q.WorkspaceID, Queue: q.Name, Since: now, Error: r.err, Failures: n})
	}
}

func (d *Dispatcher) notifyAutoPause(ev store.AutoPauseEvent) {
	if d.opts.AutoPaused != nil {
		d.opts.AutoPaused(ev)
	}
}
