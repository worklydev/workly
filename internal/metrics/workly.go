package metrics

// Metric names and labels are stable API: people alert on them.

// latencyBuckets span a fast endpoint (5ms) to a long-running one (30min),
// in seconds.
var latencyBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300, 1800}

// Workly is the set of metrics the server exports.
type Workly struct {
	*Registry

	// Counted by the replica that served the request.
	TasksEnqueued *Counter

	// Counted by the leader, which does all delivery.
	Attempts        *Counter
	TasksCompleted  *Counter
	AttemptDuration *Histogram
	DispatchLag     *Histogram
	IsLeader        *Gauge

	// Read from the database at scrape time by the leader, so that
	// replicas don't report the same queues.
	QueueDue          *Gauge
	QueueScheduled    *Gauge
	QueueRunning      *Gauge
	QueueFailed       *Gauge
	QueuePaused       *Gauge
	QueueOldestDueAge *Gauge

	// Schedules, counted by the leader.
	ScheduleRuns            *Counter
	ScheduleAttempts        *Counter
	ScheduleAttemptDuration *Histogram
	ScheduleFireLag         *Histogram

	// Read at scrape time by the leader, like the queue gauges.
	SchedulePaused      *Gauge
	ScheduleLastSuccess *Gauge
}

func New() *Workly {
	r := NewRegistry()
	q := []string{"workspace", "queue"}
	sc := []string{"workspace", "schedule"}
	return &Workly{
		Registry: r,
		TasksEnqueued: r.Counter("workly_tasks_enqueued_total",
			"Tasks enqueued. Idempotent repeats aren't counted.", q...),
		Attempts: r.Counter("workly_attempts_total",
			"Delivery attempts by outcome: success, http_error, timeout, network_error or lost.",
			"workspace", "queue", "outcome"),
		TasksCompleted: r.Counter("workly_tasks_completed_total",
			"Tasks that finished delivery, by state: succeeded or failed (attempts exhausted or not retryable).",
			"workspace", "queue", "state"),
		AttemptDuration: r.Histogram("workly_attempt_duration_seconds",
			"Duration of delivery attempts, from request to response.", latencyBuckets, q...),
		DispatchLag: r.Histogram("workly_dispatch_lag_seconds",
			"Time from when a task was due to when its delivery started, including waits for rate and concurrency limits.",
			latencyBuckets, q...),
		IsLeader: r.Gauge("workly_dispatcher_is_leader",
			"1 if this replica is the dispatcher delivering tasks, 0 if it is standing by."),
		QueueDue: r.Gauge("workly_queue_due",
			"Pending tasks that are due now. Stops counting at 10000.", q...),
		QueueScheduled: r.Gauge("workly_queue_scheduled",
			"Pending tasks due later, including ones waiting to retry. Stops counting at 10000.", q...),
		QueueRunning: r.Gauge("workly_queue_running",
			"Tasks with a delivery in flight. Stops counting at 10000.", q...),
		QueueFailed: r.Gauge("workly_queue_failed",
			"Failed tasks still retained. Stops counting at 10000.", q...),
		QueuePaused: r.Gauge("workly_queue_paused",
			"1 if the queue is paused.", q...),
		QueueOldestDueAge: r.Gauge("workly_queue_oldest_due_age_seconds",
			"How long the longest-waiting due task has been due; 0 when nothing is due. The metric to alert on.", q...),
		ScheduleRuns: r.Counter("workly_schedule_runs_total",
			"Schedule runs that finished, by state: succeeded, failed or skipped.",
			"workspace", "schedule", "state"),
		ScheduleAttempts: r.Counter("workly_schedule_attempts_total",
			"Delivery attempts of schedule runs by outcome: success, http_error, timeout, network_error or lost.",
			"workspace", "schedule", "outcome"),
		ScheduleAttemptDuration: r.Histogram("workly_schedule_attempt_duration_seconds",
			"Duration of schedule run attempts, from request to response.", latencyBuckets, sc...),
		ScheduleFireLag: r.Histogram("workly_schedule_fire_lag_seconds",
			"Time from a schedule's slot to when its run was created.", latencyBuckets, sc...),
		SchedulePaused: r.Gauge("workly_schedule_paused",
			"1 if the schedule is paused.", sc...),
		ScheduleLastSuccess: r.Gauge("workly_schedule_last_success_timestamp_seconds",
			"When the schedule's latest successful run finished, as a Unix timestamp; absent if none is retained. Alert when time() minus it exceeds the schedule's period.", sc...),
	}
}
