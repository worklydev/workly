package dispatch

import (
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
)

// A replayed task gets a fresh attempt budget and backoff, while its
// attempt numbers keep counting.
func TestDecideAfterReplay(t *testing.T) {
	q := store.QueueConfig{MaxAttempts: 2, MinBackoff: time.Second, MaxBackoff: time.Hour, BackoffMultiplier: 10}
	finished := time.Unix(1790000000, 0)
	r := result{finished: finished, status: 500, outcome: store.OutcomeHTTPError}
	rnd := func() float64 { return 0.999 }

	task := store.Task{ID: "task_1", AttemptCount: 5, ReplayedAttempts: 4}
	ar := decide(q, task, r, rnd)
	if ar.NextState != store.TaskPending || ar.Number != 5 {
		t.Fatalf("first attempt after replay = %s #%d, want pending #5", ar.NextState, ar.Number)
	}
	// Backoff restarts too: the first retry waits at most MinBackoff.
	if d := ar.RunAt.Sub(finished); d > time.Second {
		t.Fatalf("retry delay = %v, want <= 1s", d)
	}
	task.AttemptCount = 6
	if ar := decide(q, task, r, rnd); ar.NextState != store.TaskFailed {
		t.Fatalf("second attempt after replay = %s, want failed", ar.NextState)
	}
}

func TestDecide(t *testing.T) {
	q := store.QueueConfig{MaxAttempts: 3, MinBackoff: time.Second, MaxBackoff: time.Minute, BackoffMultiplier: 2}
	retry4xx := q
	retry4xx.RetryOn4xx = true
	finished := time.Unix(1790000000, 0)
	httpResult := func(status int) result {
		o := store.OutcomeHTTPError
		if status >= 200 && status < 300 {
			o = store.OutcomeSuccess
		}
		return result{finished: finished, status: status, outcome: o}
	}

	tests := []struct {
		name    string
		q       store.QueueConfig
		attempt int
		r       result
		want    store.TaskState
	}{
		{"2xx", q, 1, httpResult(204), store.TaskSucceeded},
		{"2xx on last attempt", q, 3, httpResult(200), store.TaskSucceeded},
		{"500 retries", q, 1, httpResult(500), store.TaskPending},
		{"503 on last attempt fails", q, 3, httpResult(503), store.TaskFailed},
		{"429 retries", q, 1, httpResult(429), store.TaskPending},
		{"408 retries", q, 1, httpResult(408), store.TaskPending},
		{"400 fails", q, 1, httpResult(400), store.TaskFailed},
		{"404 fails", q, 1, httpResult(404), store.TaskFailed},
		{"404 retries with retry_on_4xx", retry4xx, 1, httpResult(404), store.TaskPending},
		{"301 fails", retry4xx, 1, httpResult(301), store.TaskFailed},
		{"timeout retries", q, 1, result{finished: finished, outcome: store.OutcomeTimeout}, store.TaskPending},
		{"network error retries", q, 2, result{finished: finished, outcome: store.OutcomeNetworkError}, store.TaskPending},
		{"lost retries", q, 1, result{finished: finished, outcome: store.OutcomeLost}, store.TaskPending},
		{"lost on last attempt fails", q, 3, result{finished: finished, outcome: store.OutcomeLost}, store.TaskFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := store.Task{ID: "task_1", AttemptCount: tt.attempt}
			ar := decide(tt.q, task, tt.r, func() float64 { return 0 })
			if ar.NextState != tt.want {
				t.Fatalf("NextState = %s, want %s", ar.NextState, tt.want)
			}
			if ar.Number != tt.attempt || ar.TaskID != "task_1" {
				t.Fatalf("attempt = %+v", ar.Attempt)
			}
			if tt.want == store.TaskPending && ar.RunAt.Before(finished) {
				t.Fatalf("RunAt %v before finish %v", ar.RunAt, finished)
			}
		})
	}

	// A per-task max_attempts overrides the queue's.
	five := 5
	ar := decide(q, store.Task{AttemptCount: 3, MaxAttempts: &five}, httpResult(500), func() float64 { return 0 })
	if ar.NextState != store.TaskPending {
		t.Fatalf("task override: NextState = %s, want pending", ar.NextState)
	}
}

func TestRetryDelay(t *testing.T) {
	q := store.QueueConfig{MinBackoff: time.Second, MaxBackoff: 10 * time.Second, BackoffMultiplier: 2}
	lo, hi := func() float64 { return 0 }, func() float64 { return 0.999999999 }
	failed := result{outcome: store.OutcomeHTTPError, status: 500}

	tests := []struct {
		name    string
		attempt int
		r       result
		rnd     func() float64
		want    time.Duration
	}{
		{"first retry, low jitter", 1, failed, lo, time.Second},
		{"first retry, high jitter", 1, failed, hi, time.Second},
		{"third retry, low jitter", 3, failed, lo, time.Second},
		{"third retry, high jitter", 3, failed, hi, 4 * time.Second},
		{"capped at max", 10, failed, hi, 10 * time.Second},
		{"Retry-After raises delay", 1, result{outcome: store.OutcomeHTTPError, status: 429, retryAfter: 7 * time.Second}, lo, 7 * time.Second},
		{"Retry-After capped at max", 1, result{outcome: store.OutcomeHTTPError, status: 429, retryAfter: time.Hour}, lo, 10 * time.Second},
		{"Retry-After below backoff ignored", 3, result{outcome: store.OutcomeHTTPError, status: 503, retryAfter: time.Millisecond}, hi, 4 * time.Second},
		{"lost retries immediately", 5, result{outcome: store.OutcomeLost}, hi, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := retryDelay(queuePolicy(q, store.Task{}), tt.attempt, tt.r, tt.rnd)
			if (got - tt.want).Abs() > time.Millisecond {
				t.Fatalf("retryDelay = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"30", 30 * time.Second},
		{" 5 ", 5 * time.Second},
		{"-3", 0},
		{"Sun, 27 Sep 2026 12:01:00 GMT", time.Minute},
		{"Sun, 27 Sep 2026 11:00:00 GMT", 0},
		{"soon", 0},
	}
	for _, tt := range tests {
		if got := parseRetryAfter(tt.in, now); got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
