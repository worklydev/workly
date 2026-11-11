package dispatch

import (
	"math"
	"net/http"
	"time"

	"github.com/worklydev/workly/internal/store"
)

// retryPolicy is what decides retries, for a task (from its queue) or a
// schedule run (from its schedule).
type retryPolicy struct {
	maxAttempts int
	minBackoff  time.Duration
	maxBackoff  time.Duration
	multiplier  float64
	retryOn4xx  bool
}

func queuePolicy(q store.QueueConfig, t store.Task) retryPolicy {
	return retryPolicy{t.EffectiveMaxAttempts(q), q.MinBackoff, q.MaxBackoff, q.BackoffMultiplier, q.RetryOn4xx}
}

func schedulePolicy(sc store.ScheduleConfig) retryPolicy {
	return retryPolicy{sc.MaxAttempts, sc.MinBackoff, sc.MaxBackoff, sc.BackoffMultiplier, sc.RetryOn4xx}
}

// decide turns a delivery result into the attempt record and the task's next
// state. rnd returns a float in [0, 1) and drives backoff jitter.
func decide(q store.QueueConfig, t store.Task, r result, rnd func() float64) store.AttemptResult {
	ar := store.AttemptResult{Attempt: r.attempt(t), NextState: store.TaskFailed}
	switch ok, retryAt := queuePolicy(q, t).next(t.AttemptsSinceReplay(), r, rnd); {
	case ok:
		ar.NextState = store.TaskSucceeded
	case !retryAt.IsZero():
		ar.NextState, ar.RunAt = store.TaskPending, retryAt
	}
	return ar
}

// decideRun is decide for a schedule run.
func decideRun(sc store.ScheduleConfig, run store.Run, r result, rnd func() float64) store.RunAttemptResult {
	ar := store.RunAttemptResult{Attempt: r.runAttempt(run), NextState: store.RunFailed}
	switch ok, retryAt := schedulePolicy(sc).next(run.AttemptsSinceReplay(), r, rnd); {
	case ok:
		ar.NextState = store.RunSucceeded
	case !retryAt.IsZero():
		ar.NextState, ar.RunAt = store.RunPending, retryAt
	}
	return ar
}

// next says what follows a delivery that was the attempts-th since the last
// replay: success, a retry at retryAt, or (neither) failure.
func (p retryPolicy) next(attempts int, r result, rnd func() float64) (ok bool, retryAt time.Time) {
	switch {
	case r.outcome == store.OutcomeSuccess:
		return true, time.Time{}
	case !retryable(p, r) || attempts >= p.maxAttempts:
		return false, time.Time{}
	}
	return false, r.finished.Add(retryDelay(p, attempts, r, rnd))
}

func retryable(p retryPolicy, r result) bool {
	switch r.outcome {
	case store.OutcomeTimeout, store.OutcomeNetworkError, store.OutcomeLost:
		return true
	}
	switch s := r.status; {
	case s == http.StatusRequestTimeout, s == http.StatusTooManyRequests, s >= 500:
		return true
	case s >= 400:
		// Retrying a bad request usually just wastes capacity.
		return p.retryOn4xx
	default:
		// 1xx and 3xx: redirects are not followed, so these are misconfigurations.
		return false
	}
}

// retryDelay grows exponentially with the attempt number, with jitter
// between minBackoff and the exponential ceiling so that deliveries failing
// together don't retry together. Retry-After is honoured up to maxBackoff.
func retryDelay(p retryPolicy, attempt int, r result, rnd func() float64) time.Duration {
	if r.outcome == store.OutcomeLost {
		// The endpoint never gave an answer; retry right away.
		return 0
	}
	lo := float64(p.minBackoff)
	ceiling := min(lo*math.Pow(p.multiplier, float64(attempt-1)), float64(p.maxBackoff))
	ceiling = max(ceiling, lo)
	delay := time.Duration(lo + rnd()*(ceiling-lo))
	if r.retryAfter > 0 {
		delay = max(delay, min(r.retryAfter, p.maxBackoff))
	}
	return delay
}
