// Package dispatch delivers due tasks over HTTP.
//
// Exactly one dispatcher is active at a time (see store.Dispatch.TryLead);
// other replicas stand by and take over if the leader dies. The leader runs
// many deliveries concurrently. Delivery is at-least-once: a task whose
// delivery outcome is unknown (crash, shutdown) is delivered again.
package dispatch

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/worklydev/workly/internal/metrics"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

type Options struct {
	Store         store.Store
	Logger        *slog.Logger
	SigningSecret []byte
	// PreviousSigningSecret, if set, also signs deliveries while endpoints
	// move to SigningSecret.
	PreviousSigningSecret []byte
	// SigningSecrets, if set, signs each workspace's deliveries with its own
	// secrets instead.
	SigningSecrets SecretsFunc
	UserAgent      string

	// MaxInFlight caps concurrent deliveries across all queues. Queues can
	// set lower limits of their own. Default 100.
	MaxInFlight int
	// WorkspaceMaxInFlight, if set, returns a workspace's own cap on
	// concurrent deliveries, within MaxInFlight; 0 means none.
	WorkspaceMaxInFlight func(ctx context.Context, ws workspace.ID) (int, error)
	// MaxRunsInFlight caps concurrent deliveries of schedule runs,
	// separately from MaxInFlight. Default 20.
	MaxRunsInFlight int
	// PollInterval is the longest the dispatcher sleeps without checking for
	// due tasks, as a fallback for missed wake-ups. Default 1s.
	PollInterval time.Duration
	// LeaseGrace is added to a task's timeout to form its lease. If the lease
	// expires the task is presumed lost and redelivered. A leader never reaps
	// its own deliveries, so this only needs to cover a previous leader
	// finishing up after losing leadership. Default 10s.
	LeaseGrace time.Duration
	// ReapInterval is how often expired leases are checked. Default 10s.
	ReapInterval time.Duration
	// LeaderRetryInterval is how often a standby tries to take over. Default 5s.
	LeaderRetryInterval time.Duration
	// ShutdownTimeout bounds how long in-flight deliveries may finish after
	// shutdown starts; the rest are interrupted and redelivered. Default 25s.
	ShutdownTimeout time.Duration
	// Retention is how long finished tasks (succeeded, failed or cancelled)
	// are kept before they are deleted with their attempts. 0 keeps them
	// forever.
	Retention time.Duration
	// RunRetention is how long finished schedule runs are kept; each
	// schedule's latest run is kept regardless. 0 keeps them forever.
	RunRetention time.Duration
	// WorkspaceRetention, if set, returns the workspaces whose finished
	// tasks, groups and runs are kept for their own duration instead (0
	// keeps them forever). Called once per sweep; if it fails, those
	// workspaces aren't known, so nothing is deleted that sweep.
	WorkspaceRetention func(ctx context.Context) (map[workspace.ID]time.Duration, error)
	// SweepInterval is how often finished tasks and runs past retention are
	// deleted. Default 1m.
	SweepInterval time.Duration
	// Client defaults to a client that doesn't follow redirects, and whose
	// connections DialControl vets.
	Client      *http.Client
	DialControl DialControl
	// Metrics defaults to a private set that nothing serves.
	Metrics *metrics.Workly
	// Alerts, if set, runs while this dispatcher leads, so alerts are sent
	// from one process.
	Alerts interface{ Run(ctx context.Context) }
	// Changed, if set, is told when a workspace's tasks or runs change
	// state, so dashboards can refresh. It must not block.
	Changed func(workspace.ID)
	// TasksCreated, if set, is told how many tasks a schedule created by
	// starting a workflow run (one per step), as the API's Admission is for
	// runs started through it. A scheduled run isn't refused: its slot has
	// already come.
	TasksCreated func(ctx context.Context, ws workspace.ID, n int)
}

type Dispatcher struct {
	opts      Options
	store     store.Store
	log       *slog.Logger
	deliverer deliverer
	inFlight  atomic.Int64
	metrics   *metrics.Workly
	leading   atomic.Bool
	// delivering holds the IDs of tasks this process is delivering, so the
	// reaper only recovers tasks abandoned by a previous leader.
	delivering sync.Map
	// results records attempt results while leading.
	results *resultWriter

	mu         sync.Mutex
	queues     map[queueKey]*queueState
	workspaces map[workspace.ID]*workspaceState
	// round rotates which workspace, and which of its queues, is served
	// first. Only lead uses it.
	round int
}

const (
	// claimBatch caps how many tasks are claimed from one queue per round.
	claimBatch = 50
	// sweepBatch is how many finished tasks one sweep statement deletes, to
	// keep transactions short.
	sweepBatch = 1000
)

func New(opts Options) *Dispatcher {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	opts.MaxInFlight = cmp.Or(opts.MaxInFlight, 100)
	opts.MaxRunsInFlight = cmp.Or(opts.MaxRunsInFlight, 20)
	opts.PollInterval = cmp.Or(opts.PollInterval, time.Second)
	opts.LeaseGrace = cmp.Or(opts.LeaseGrace, 10*time.Second)
	opts.ReapInterval = cmp.Or(opts.ReapInterval, 10*time.Second)
	opts.LeaderRetryInterval = cmp.Or(opts.LeaderRetryInterval, 5*time.Second)
	opts.ShutdownTimeout = cmp.Or(opts.ShutdownTimeout, 25*time.Second)
	opts.SweepInterval = cmp.Or(opts.SweepInterval, time.Minute)
	if opts.Client == nil {
		opts.Client = newHTTPClient(opts.MaxInFlight+opts.MaxRunsInFlight, opts.DialControl)
	}
	if opts.Metrics == nil {
		opts.Metrics = metrics.New()
	}
	if opts.Changed == nil {
		opts.Changed = func(workspace.ID) {}
	}
	d := &Dispatcher{
		opts:       opts,
		store:      opts.Store,
		log:        opts.Logger,
		metrics:    opts.Metrics,
		queues:     map[queueKey]*queueState{},
		workspaces: map[workspace.ID]*workspaceState{},
		deliverer: deliverer{
			client: opts.Client,
			signer: NewSigner(opts.SigningSecret, opts.PreviousSigningSecret, opts.UserAgent, opts.SigningSecrets),
		},
	}
	d.metrics.IsLeader.Set(0)
	d.metrics.OnCollect(d.collectQueueMetrics)
	d.metrics.OnCollect(d.collectScheduleMetrics)
	return d
}

// collectQueueMetrics refreshes the per-queue gauges before a scrape. Only
// the leader reports them, so replicas don't duplicate every queue's series
// or multiply the queries.
func (d *Dispatcher) collectQueueMetrics(ctx context.Context) {
	m := d.metrics
	gauges := []*metrics.Gauge{m.QueueDue, m.QueueScheduled, m.QueueRunning, m.QueueFailed, m.QueuePaused, m.QueueOldestDueAge}
	for _, g := range gauges {
		g.Reset()
	}
	if !d.leading.Load() {
		return
	}
	now := time.Now()
	queues, err := d.store.AllQueueStats(ctx, now)
	if err != nil {
		d.log.Warn("could not collect queue metrics", "err", err)
		return
	}
	for _, q := range queues {
		labels := []string{string(q.WorkspaceID), q.Queue}
		m.QueueDue.Set(float64(q.Due), labels...)
		m.QueueScheduled.Set(float64(q.Scheduled), labels...)
		m.QueueRunning.Set(float64(q.Running), labels...)
		m.QueueFailed.Set(float64(q.Failed), labels...)
		m.QueuePaused.Set(boolFloat(q.Paused), labels...)
		var age time.Duration
		if q.OldestDueAt != nil {
			age = max(0, now.Sub(*q.OldestDueAt))
		}
		m.QueueOldestDueAge.Set(age.Seconds(), labels...)
	}
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// Run dispatches while leader, and stands by otherwise, until ctx is
// cancelled. It then lets in-flight deliveries finish (up to ShutdownTimeout)
// before returning.
func (d *Dispatcher) Run(ctx context.Context) error {
	standingBy := false
	for {
		leader, err := d.store.TryLead(ctx)
		switch {
		case err == nil:
			standingBy = false
			d.lead(ctx, leader)
			leader.Release()
		case errors.Is(err, store.ErrNotLeader):
			if !standingBy {
				d.log.Info("dispatcher standing by: another replica is leader")
				standingBy = true
			}
		case ctx.Err() == nil:
			d.log.Warn("dispatcher could not acquire leadership", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(d.opts.LeaderRetryInterval):
		}
	}
}

// lead dispatches until ctx is cancelled or leadership is lost.
func (d *Dispatcher) lead(ctx context.Context, leader store.Leader) {
	d.log.Info("dispatcher is leader")
	d.leading.Store(true)
	d.metrics.IsLeader.Set(1)
	defer func() {
		d.leading.Store(false)
		d.metrics.IsLeader.Set(0)
	}()

	if d.opts.Retention > 0 || d.opts.RunRetention > 0 || d.opts.WorkspaceRetention != nil {
		sweepCtx, stopSweep := context.WithCancel(ctx)
		var sweeper sync.WaitGroup
		sweeper.Go(func() { d.sweep(sweepCtx) })
		defer sweeper.Wait()
		defer stopSweep()
	}

	expireCtx, stopExpiring := context.WithCancel(ctx)
	var expirer sync.WaitGroup
	expirer.Go(func() { d.expireWorkflowRuns(expireCtx) })
	defer expirer.Wait()
	defer stopExpiring()

	if d.opts.Alerts != nil {
		alertsCtx, stopAlerts := context.WithCancel(ctx)
		var alerts sync.WaitGroup
		alerts.Go(func() { d.opts.Alerts.Run(alertsCtx) })
		defer alerts.Wait()
		defer stopAlerts()
	}

	// Deliveries outlive ctx so shutdown can drain them, and their results
	// are written after.
	d.results = startResultWriter(d.store)
	defer d.results.stop()
	deliverCtx, interrupt := context.WithCancel(context.WithoutCancel(ctx))
	defer interrupt()
	var wg sync.WaitGroup
	defer d.drain(&wg, interrupt)

	// The schedule loop runs alongside, until this loop returns. Each loop
	// gets every wake-up.
	loopCtx, stopLoops := context.WithCancel(ctx)
	var loops sync.WaitGroup
	defer loops.Wait()
	defer stopLoops()
	wake, scheduleWake := make(chan struct{}, 1), make(chan struct{}, 1)
	loops.Go(func() {
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-leader.Wake():
				for _, c := range []chan struct{}{wake, scheduleWake} {
					select {
					case c <- struct{}{}:
					default:
					}
				}
			}
		}
	})
	loops.Go(func() { d.runSchedules(loopCtx, deliverCtx, &wg, scheduleWake) })

	// done is signalled when a delivery finishes and frees a slot.
	done := make(chan struct{}, 1)
	var lastReap time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-leader.Lost():
			d.log.Warn("dispatcher lost leadership")
			return
		default:
		}

		if time.Since(lastReap) >= d.opts.ReapInterval {
			d.reap(ctx)
			d.pruneQueues(time.Now())
			lastReap = time.Now()
		}

		more, wait, err := d.dispatch(ctx, deliverCtx, &wg, done)
		if err != nil && ctx.Err() == nil {
			d.log.Warn("dispatch failed", "err", err)
		}
		if more && d.free() > 0 {
			continue
		}

		// Sleep until a scheduled task or retry is due, a rate-limited queue
		// has tokens, a task is enqueued, or a delivery frees a slot.
		wait = min(cmp.Or(wait, d.opts.PollInterval), d.opts.PollInterval)
		now := time.Now()
		if next, ok, err := d.store.NextRunAt(ctx, now); err == nil && ok {
			wait = min(wait, next.Sub(now))
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
		case <-leader.Lost():
		case <-wake:
		case <-done:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (d *Dispatcher) free() int {
	return d.opts.MaxInFlight - int(d.inFlight.Load())
}

// dispatch claims due tasks, shared fairly among queues and within their
// limits, and starts their deliveries. more reports that some queue may have
// more due tasks it could deliver now; wait, if non-zero, is when a
// rate-limited queue will have tokens again.
func (d *Dispatcher) dispatch(ctx, deliverCtx context.Context, wg *sync.WaitGroup, done chan<- struct{}) (more bool, wait time.Duration, err error) {
	free := d.free()
	if free <= 0 {
		return true, 0, nil
	}
	now := time.Now()
	queues, err := d.store.DueQueues(ctx, now)
	if err != nil || len(queues) == 0 {
		return false, 0, err
	}
	later := func(w time.Duration) {
		if w > 0 && (wait == 0 || w < wait) {
			wait = w
		}
	}

	// Only queues that can start a delivery now share the free slots: one
	// at its concurrency limit or out of rate tokens would otherwise take a
	// share it can't use, and the rest would be claimed a task or two per
	// round. Slots are shared by workspace first, then by queue, so on a
	// server shared by many workspaces none gets more for having more
	// queues. A different workspace, and queue within it, goes first each
	// round, so none starves the others when slots are scarce.
	d.refreshLimits(ctx, queues, now)
	var (
		ready  []store.Queue
		room   []int
		byWS   [][]store.Queue
		wsSeen = map[workspace.ID]int{}
	)
	for _, q := range queues {
		g, ok := wsSeen[q.WorkspaceID]
		if !ok {
			g = len(byWS)
			wsSeen[q.WorkspaceID] = g
			byWS = append(byWS, nil)
		}
		byWS[g] = append(byWS[g], q)
	}
	var (
		groups [][]int
		caps   []int
	)
	round := d.round
	d.round++
	for g := range byWS {
		qs := byWS[(round+g)%len(byWS)]
		var group []int
		for j := range qs {
			q := qs[(round+j)%len(qs)]
			n, w := d.headroom(q, now)
			if n == 0 {
				later(w)
				continue
			}
			group = append(group, len(ready))
			ready, room = append(ready, q), append(room, min(n, claimBatch))
		}
		if len(group) > 0 {
			groups = append(groups, group)
			caps = append(caps, d.workspaceRoom(qs[0].WorkspaceID))
		}
	}
	for i, allot := range allotByWorkspace(groups, caps, room, free) {
		q := ready[i]
		if allot == 0 {
			// Out of slots this round.
			more = true
			continue
		}
		n, w := d.reserve(q, allot, now)
		if n == 0 {
			later(w)
			continue
		}
		tasks, err := d.store.ClaimTasks(ctx, q, n, now, d.opts.LeaseGrace)
		if err != nil {
			d.unreserve(q, n)
			return more, wait, err
		}
		d.unreserve(q, n-len(tasks))
		if len(tasks) > 0 {
			d.opts.Changed(q.WorkspaceID)
		}
		if len(tasks) == n {
			if n < room[i] || n == claimBatch {
				// Held back by the slots or the batch size, not by the
				// queue's own limits.
				more = true
			} else {
				later(d.tokenWait(q, now))
			}
		}
		for _, t := range tasks {
			d.metrics.DispatchLag.Observe(max(0, now.Sub(t.RunAt)).Seconds(), string(t.WorkspaceID), t.Queue)
			d.inFlight.Add(1)
			d.delivering.Store(t.ID, struct{}{})
			wg.Go(func() {
				defer func() {
					d.delivering.Delete(t.ID)
					d.released(q)
					d.inFlight.Add(-1)
					select {
					case done <- struct{}{}:
					default:
					}
				}()
				d.deliver(deliverCtx, q, t)
			})
		}
	}
	return more, wait, nil
}

func (d *Dispatcher) deliver(ctx context.Context, q store.Queue, t store.Task) {
	r := d.deliverer.deliver(ctx, t, t.EffectiveTimeout(q.QueueConfig))
	d.finish(q, t, decide(q.QueueConfig, t, r, rand.Float64))
}

// finish records the attempt and logs it: one line per attempt.
func (d *Dispatcher) finish(q store.Queue, t store.Task, ar store.AttemptResult) {
	state, err := d.results.finish(store.FinishedAttempt{Workspace: t.WorkspaceID, AttemptResult: ar})
	if err != nil {
		state = ar.NextState
	} else {
		d.count(t, ar, state)
		d.opts.Changed(t.WorkspaceID)
	}

	attrs := attemptAttrs([]any{"task_id", t.ID, "queue", t.Queue}, t.WorkspaceID, ar.Attempt, string(state))
	if state == store.TaskPending {
		attrs = append(attrs, "retry_in", retryIn(ar.RunAt))
	}

	switch {
	case errors.Is(err, store.ErrLeaseLost):
		// The reaper already recovered the task (or its queue was deleted);
		// this result is stale.
		d.log.Warn("attempt result discarded: task lease was lost", attrs...)
	case err != nil:
		// The task stays running until its lease expires, then is redelivered.
		d.log.Error("could not record attempt", append(attrs, "err", err)...)
	case state == store.TaskFailed:
		d.log.Warn("task failed", attrs...)
	default:
		d.log.Info("task attempt", attrs...)
	}
}

// attemptAttrs are the log attributes of an attempt of the task or run that
// subject identifies, and what follows it.
func attemptAttrs(subject []any, ws workspace.ID, a store.Attempt, next string) []any {
	attrs := append(subject,
		"attempt", a.Number,
		"outcome", a.Outcome,
		"duration_ms", a.Duration().Milliseconds(),
		"next", next,
	)
	if ws != workspace.Default {
		attrs = append(attrs, "workspace", ws)
	}
	if a.StatusCode != nil {
		attrs = append(attrs, "status", *a.StatusCode)
	}
	if a.Error != "" {
		attrs = append(attrs, "error", a.Error)
	}
	return attrs
}

func retryIn(at time.Time) string {
	return max(time.Until(at), 0).Round(time.Millisecond).String()
}

// count updates the metrics for a recorded attempt.
func (d *Dispatcher) count(t store.Task, ar store.AttemptResult, state store.TaskState) {
	ws, q := string(t.WorkspaceID), t.Queue
	d.metrics.Attempts.Inc(ws, q, string(ar.Outcome))
	// A lost attempt's duration is when the reaper noticed, not a response time.
	if ar.Outcome != store.OutcomeLost {
		d.metrics.AttemptDuration.Observe(ar.Duration().Seconds(), ws, q)
	}
	if state == store.TaskSucceeded || state == store.TaskFailed {
		d.metrics.TasksCompleted.Inc(ws, q, string(state))
	}
}

// reap recovers tasks whose dispatcher died mid-delivery.
func (d *Dispatcher) reap(ctx context.Context) {
	now := time.Now()
	tasks, err := d.store.ExpiredLeases(ctx, now, 100)
	if err != nil {
		if ctx.Err() == nil {
			d.log.Warn("lease reaper failed", "err", err)
		}
		return
	}
	for _, t := range tasks {
		if _, ok := d.delivering.Load(t.ID); ok {
			// Still ours: a slow delivery or a slow write of its result.
			continue
		}
		q, err := d.store.GetQueue(ctx, t.WorkspaceID, t.Queue)
		if err != nil {
			continue // deleted with its queue, or retried next round
		}
		r := result{
			started:  t.UpdatedAt, // when it was claimed
			finished: now,
			outcome:  store.OutcomeLost,
			err:      "lease expired: the dispatcher stopped before the delivery finished",
		}
		d.finish(q, t, decide(q.QueueConfig, t, r, rand.Float64))
	}
}

// sweep deletes finished tasks and runs past retention until ctx is
// cancelled.
func (d *Dispatcher) sweep(ctx context.Context) {
	ticker := time.NewTicker(d.opts.SweepInterval)
	defer ticker.Stop()
	for {
		d.sweepOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweepOnce sweeps every workspace on the default retention, then each one
// with its own.
func (d *Dispatcher) sweepOnce(ctx context.Context) {
	var own map[workspace.ID]time.Duration
	if d.opts.WorkspaceRetention != nil {
		var err error
		if own, err = d.opts.WorkspaceRetention(ctx); err != nil {
			if ctx.Err() == nil {
				d.log.Warn("retention sweep skipped: workspace retention unknown", "err", err)
			}
			return
		}
	}
	except := slices.Sorted(maps.Keys(own))
	now := time.Now()
	if d.opts.Retention > 0 {
		d.sweepTasks(ctx, store.Sweep{Before: now.Add(-d.opts.Retention), Except: except}, d.opts.Retention)
	}
	if d.opts.RunRetention > 0 {
		d.sweepRuns(ctx, store.Sweep{Before: now.Add(-d.opts.RunRetention), Except: except}, d.opts.RunRetention)
		d.sweepWorkflowRuns(ctx, store.Sweep{Before: now.Add(-d.opts.RunRetention), Except: except}, d.opts.RunRetention)
	}
	for _, ws := range except {
		if r := own[ws]; r > 0 && ctx.Err() == nil {
			sw := store.Sweep{Before: now.Add(-r), Workspace: ws}
			d.sweepTasks(ctx, sw, r)
			d.sweepRuns(ctx, sw, r)
			d.sweepWorkflowRuns(ctx, sw, r)
		}
	}
}

// sweepBatches calls del until a batch comes back short, and returns how
// many it deleted.
func (d *Dispatcher) sweepBatches(ctx context.Context, what string, del func() (int, error)) int {
	total := 0
	for {
		n, err := del()
		if err != nil {
			if ctx.Err() == nil {
				d.log.Warn("retention sweep failed", "of", what, "err", err)
			}
			return total
		}
		total += n
		if n < sweepBatch || ctx.Err() != nil {
			return total
		}
	}
}

func (d *Dispatcher) sweepTasks(ctx context.Context, sw store.Sweep, retention time.Duration) {
	total := d.sweepBatches(ctx, "tasks", func() (int, error) { return d.store.DeleteFinishedTasks(ctx, sw, sweepBatch) })
	if total > 0 {
		d.log.Info("deleted finished tasks past retention", "count", total, "retention", retention.String(), "workspace", sw.Workspace)
	}
	// A completed group's tasks finished before it did, so they are gone by
	// the time it is.
	d.sweepBatches(ctx, "groups", func() (int, error) { return d.store.DeleteFinishedGroups(ctx, sw, sweepBatch) })
}

// drain waits for in-flight deliveries, interrupting them after
// ShutdownTimeout. Interrupted deliveries are recorded as lost and retried.
func (d *Dispatcher) drain(wg *sync.WaitGroup, interrupt context.CancelFunc) {
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	if n := d.inFlight.Load(); n > 0 {
		d.log.Info("waiting for in-flight deliveries", "count", n)
	}
	select {
	case <-finished:
		return
	case <-time.After(d.opts.ShutdownTimeout):
	}
	d.log.Warn("interrupting in-flight deliveries", "count", d.inFlight.Load())
	interrupt()
	<-finished
}
