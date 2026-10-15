package dispatch

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/worklydev/workly/internal/cron"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// missedAfter is how late a slot may fire and still count as on time. A
// leader failover takes seconds; anything later means no dispatcher was
// running, and the schedule's missed policy applies.
const missedAfter = time.Minute

// scheduler fires schedules and delivers their runs. It runs on the leader
// next to the queue dispatch loop, with its own in-flight cap, so neither
// can hold up the other.
type scheduler struct {
	d        *Dispatcher
	inFlight atomic.Int64
	// broken remembers schedules whose cron or timezone no longer parses
	// (e.g. zoneinfo changed), so the error is logged once, not every tick.
	broken map[string]string
}

// runSchedules fires due schedules and delivers runs until ctx is
// cancelled. Deliveries run on deliverCtx and are tracked by wg, so the
// dispatcher's drain covers them.
func (d *Dispatcher) runSchedules(ctx, deliverCtx context.Context, wg *sync.WaitGroup, wake <-chan struct{}) {
	s := &scheduler{d: d, broken: map[string]string{}}
	done := make(chan struct{}, 1)
	var lastReap time.Time
	for ctx.Err() == nil {
		if time.Since(lastReap) >= d.opts.ReapInterval {
			s.reap(ctx)
			lastReap = time.Now()
		}
		if err := s.fireDue(ctx, time.Now()); err != nil && ctx.Err() == nil {
			d.log.Warn("firing schedules failed", "err", err)
		}
		more, err := s.claim(ctx, deliverCtx, wg, done)
		if err != nil && ctx.Err() == nil {
			d.log.Warn("claiming schedule runs failed", "err", err)
		}
		if more {
			continue
		}

		wait := d.opts.PollInterval
		now := time.Now()
		if next, ok, err := d.store.NextScheduleWork(ctx, now); err == nil && ok {
			wait = min(wait, next.Sub(now))
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
		case <-wake:
		case <-done:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func scheduleKey(ws workspace.ID, name string) string { return string(ws) + "/" + name }

// fireDue creates the runs of every schedule whose next slot has come.
func (s *scheduler) fireDue(ctx context.Context, now time.Time) error {
	d := s.d
	due, err := d.store.DueSchedules(ctx, now)
	if err != nil {
		return err
	}
	for _, sc := range due {
		expr, err := cron.Parse(sc.Cron)
		var loc *time.Location
		if err == nil {
			loc, err = cron.LoadLocation(sc.Timezone)
		}
		key := scheduleKey(sc.WorkspaceID, sc.Name)
		if err != nil {
			if s.broken[key] != err.Error() {
				d.log.Error("schedule can't fire", "schedule", sc.Name, "workspace", sc.WorkspaceID, "err", err)
				s.broken[key] = err.Error()
			}
			continue
		}
		delete(s.broken, key)

		// The latest slot that has come; earlier ones passed while no
		// dispatcher was running, and get at most this one run.
		slot := *sc.NextRunAt
		if latest := expr.Prev(now, loc); latest.After(slot) {
			slot = latest
		}
		next := expr.Next(slot, loc)
		if next.IsZero() {
			// The cron parser guarantees a match within eight years.
			next = slot.Add(366 * 24 * time.Hour)
		}
		f := store.Fire{WorkspaceID: sc.WorkspaceID, Schedule: sc.Name, Due: *sc.NextRunAt, Slot: slot, NextRunAt: next}
		late := now.Sub(slot) > missedAfter
		if late && sc.Missed == store.MissedSkip {
			f.SkipReason = store.SkipMissed
		}
		run, fired, err := d.store.FireSchedule(ctx, f, now)
		if err != nil {
			return err
		}
		if !fired {
			continue // changed since it was read; the next round sees it
		}
		attrs := []any{"schedule", sc.Name, "run_id", run.ID, "slot", slot.Format(time.RFC3339)}
		if sc.WorkspaceID != workspace.Default {
			attrs = append(attrs, "workspace", sc.WorkspaceID)
		}
		ws := string(sc.WorkspaceID)
		if run.State == store.RunSkipped {
			d.metrics.ScheduleRuns.Inc(ws, sc.Name, string(store.RunSkipped))
			d.log.Warn("schedule run skipped", append(attrs, "reason", run.SkipReason)...)
			continue
		}
		d.metrics.ScheduleFireLag.Observe(max(0, now.Sub(slot)).Seconds(), ws, sc.Name)
		if late {
			attrs = append(attrs, "late_by", now.Sub(slot).Round(time.Second).String())
		}
		d.log.Info("schedule fired", attrs...)
	}
	return nil
}

// claim starts deliveries of due runs, up to the in-flight cap. more
// reports that the cap was the limit, so more runs may be due.
func (s *scheduler) claim(ctx, deliverCtx context.Context, wg *sync.WaitGroup, done chan<- struct{}) (more bool, err error) {
	d := s.d
	free := d.opts.MaxRunsInFlight - int(s.inFlight.Load())
	if free <= 0 {
		return false, nil
	}
	claims, err := d.store.ClaimRuns(ctx, free, time.Now(), d.opts.LeaseGrace)
	if err != nil {
		return false, err
	}
	for _, c := range claims {
		s.inFlight.Add(1)
		d.delivering.Store(c.Run.ID, struct{}{})
		wg.Go(func() {
			defer func() {
				d.delivering.Delete(c.Run.ID)
				s.inFlight.Add(-1)
				select {
				case done <- struct{}{}:
				default:
				}
			}()
			r := d.deliverer.deliverRun(deliverCtx, c.Run, c.Schedule.Timeout)
			s.finish(c.Run, decideRun(c.Schedule.ScheduleConfig, c.Run, r, rand.Float64))
		})
	}
	return len(claims) == free, nil
}

// finish records a run's attempt and logs it, one line per attempt.
func (s *scheduler) finish(run store.Run, ar store.RunAttemptResult) {
	d := s.d
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := d.store.FinishRunAttempt(ctx, run.WorkspaceID, ar)
	ws := string(run.WorkspaceID)
	if err != nil {
		state = ar.NextState
	} else {
		d.metrics.ScheduleAttempts.Inc(ws, run.Schedule, string(ar.Outcome))
		if ar.Outcome != store.OutcomeLost {
			d.metrics.ScheduleAttemptDuration.Observe(ar.Duration().Seconds(), ws, run.Schedule)
		}
		if state == store.RunSucceeded || state == store.RunFailed {
			d.metrics.ScheduleRuns.Inc(ws, run.Schedule, string(state))
		}
	}

	attrs := []any{
		"run_id", run.ID,
		"schedule", run.Schedule,
		"attempt", ar.Number,
		"outcome", ar.Outcome,
		"duration_ms", ar.Duration().Milliseconds(),
		"next", state,
	}
	if run.WorkspaceID != workspace.Default {
		attrs = append(attrs, "workspace", run.WorkspaceID)
	}
	if ar.StatusCode != nil {
		attrs = append(attrs, "status", *ar.StatusCode)
	}
	if ar.Error != "" {
		attrs = append(attrs, "error", ar.Error)
	}
	if state == store.RunPending {
		attrs = append(attrs, "retry_in", max(time.Until(ar.RunAt), 0).Round(time.Millisecond).String())
	}
	switch {
	case errors.Is(err, store.ErrLeaseLost):
		d.log.Warn("run attempt result discarded: lease was lost", attrs...)
	case err != nil:
		d.log.Error("could not record run attempt", append(attrs, "err", err)...)
	case state == store.RunFailed:
		d.log.Warn("schedule run failed", attrs...)
	default:
		d.log.Info("schedule run attempt", attrs...)
	}
}

// reap recovers runs whose dispatcher died mid-delivery, as for tasks.
func (s *scheduler) reap(ctx context.Context) {
	d := s.d
	now := time.Now()
	runs, err := d.store.ExpiredRunLeases(ctx, now, 100)
	if err != nil {
		if ctx.Err() == nil {
			d.log.Warn("run lease reaper failed", "err", err)
		}
		return
	}
	for _, run := range runs {
		if _, ok := d.delivering.Load(run.ID); ok {
			continue
		}
		sc, err := d.store.GetSchedule(ctx, run.WorkspaceID, run.Schedule)
		if err != nil {
			continue
		}
		r := result{
			started:  run.UpdatedAt,
			finished: now,
			outcome:  store.OutcomeLost,
			err:      "lease expired: the dispatcher stopped before the delivery finished",
		}
		s.finish(run, decideRun(sc.ScheduleConfig, run, r, rand.Float64))
	}
}

// collectScheduleMetrics refreshes the per-schedule gauges before a scrape,
// on the leader only.
func (d *Dispatcher) collectScheduleMetrics(ctx context.Context) {
	m := d.metrics
	m.SchedulePaused.Reset()
	m.ScheduleLastSuccess.Reset()
	if !d.leading.Load() {
		return
	}
	status, err := d.store.AllScheduleStatus(ctx)
	if err != nil {
		d.log.Warn("could not collect schedule metrics", "err", err)
		return
	}
	for _, st := range status {
		labels := []string{string(st.WorkspaceID), st.Schedule}
		m.SchedulePaused.Set(boolFloat(st.Paused), labels...)
		if st.LastSuccessAt != nil {
			m.ScheduleLastSuccess.Set(float64(st.LastSuccessAt.UnixMilli())/1000, labels...)
		}
	}
}

// sweepRuns deletes finished runs older than RunRetention.
func (d *Dispatcher) sweepRuns(ctx context.Context) {
	before := time.Now().Add(-d.opts.RunRetention)
	total := 0
	for {
		n, err := d.store.DeleteFinishedRuns(ctx, before, sweepBatch)
		if err != nil {
			if ctx.Err() == nil {
				d.log.Warn("run retention sweep failed", "err", err)
			}
			break
		}
		total += n
		if n < sweepBatch || ctx.Err() != nil {
			break
		}
	}
	if total > 0 {
		d.log.Info("deleted schedule runs past retention", "count", total, "retention", d.opts.RunRetention.String())
	}
}
