package dispatch

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

func TestAutoPauseSignal(t *testing.T) {
	tests := []struct {
		name string
		r    result
		want autoPauseSignal
	}{
		{"2xx", result{outcome: store.OutcomeSuccess, status: 200}, signalSuccess},
		{"500", result{outcome: store.OutcomeHTTPError, status: 500}, signalFailure},
		{"503", result{outcome: store.OutcomeHTTPError, status: 503}, signalFailure},
		{"timeout", result{outcome: store.OutcomeTimeout}, signalFailure},
		{"network error", result{outcome: store.OutcomeNetworkError}, signalFailure},
		{"404", result{outcome: store.OutcomeHTTPError, status: 404}, signalNone},
		{"429", result{outcome: store.OutcomeHTTPError, status: 429}, signalNone},
		{"302", result{outcome: store.OutcomeHTTPError, status: 302}, signalNone},
		{"lost", result{outcome: store.OutcomeLost}, signalNone},
		{"refused", result{outcome: store.OutcomeNetworkError, final: true}, signalNone},
	}
	for _, tt := range tests {
		if got := autoPauseSignalOf(tt.r); got != tt.want {
			t.Errorf("%s: signal = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestProbeDelay(t *testing.T) {
	first, most := 30*time.Second, 10*time.Minute
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for failed, w := range want {
		if got := probeDelay(failed, first, most); got != w {
			t.Errorf("probeDelay(%d) = %v, want %v", failed, got, w)
		}
	}
	if got := probeDelay(100, first, most); got != most {
		t.Errorf("probeDelay(100) = %v, want %v", got, most)
	}
}

// TestAutoPause takes a queue's endpoint down: the queue pauses after its
// threshold, tasks wait without using attempts, probes go one at a time,
// and the first success once it's back resumes the queue and drains it.
func TestAutoPause(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		putQueue(t, s, "q", func(c *store.QueueConfig) {
			c.AutoPauseAfter = 3
			c.MaxAttempts = 20
			c.MaxConcurrency = 1
		})
		var down atomic.Bool
		down.Store(true)
		rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
			if down.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		})
		var tasks []store.Task
		for range 5 {
			tasks = append(tasks, enqueue(t, s, store.NewTask{URL: rec.URL}))
		}
		var (
			mu     sync.Mutex
			events []store.AutoPauseEvent
		)
		start(t, s, func(o *Options) {
			o.FirstProbeDelay, o.MaxProbeDelay = 200*time.Millisecond, 400*time.Millisecond
			o.AutoPaused = func(ev store.AutoPauseEvent) {
				mu.Lock()
				events = append(events, ev)
				mu.Unlock()
			}
		})

		q := waitForQueue(t, s, "q", func(q store.Queue) bool { return q.AutoPaused != nil && q.AutoPaused.Probes >= 2 })
		if q.AutoPaused.Error != "HTTP 503 Service Unavailable" {
			t.Errorf("auto-pause error = %q", q.AutoPaused.Error)
		}
		// 3 failures paused it, then one probe at a time: nowhere near the
		// attempts the tasks had left.
		attempts := 0
		for _, task := range tasks {
			got, _ := s.GetTask(t.Context(), workspace.Default, task.ID)
			if got.State != store.TaskPending {
				t.Fatalf("task %s = %s while auto-paused, want pending", got.ID, got.State)
			}
			attempts += got.AttemptCount
		}
		if attempts < 5 || attempts > 7 {
			t.Errorf("attempts while auto-paused = %d, want 3 + the probes", attempts)
		}
		arrivals, _ := rec.snapshot()
		if len(arrivals) != attempts {
			t.Fatalf("deliveries = %d, attempts = %d", len(arrivals), attempts)
		}
		// Probes are spaced by their delay.
		for i := 4; i < len(arrivals); i++ {
			if gap := arrivals[i].Sub(arrivals[i-1]); gap < 150*time.Millisecond {
				t.Errorf("probe %d came %v after the previous delivery", i-2, gap)
			}
		}

		down.Store(false)
		waitAll(t, s, tasks, store.TaskSucceeded)
		if q, _ := s.GetQueue(t.Context(), workspace.Default, "q"); q.AutoPaused != nil {
			t.Fatalf("queue still auto-paused after the tasks succeeded: %+v", q.AutoPaused)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(events) != 2 || events[0].Resumed || events[0].Failures != 3 || !events[1].Resumed {
			t.Fatalf("events = %+v, want a pause after 3 failures, then a resume", events)
		}
	})
}

// TestAutoPauseResumedByHand checks that a queue resumed by hand counts
// failures again, and pauses again.
func TestAutoPauseResumedByHand(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		putQueue(t, s, "q", func(c *store.QueueConfig) {
			c.AutoPauseAfter = 2
			c.MaxAttempts = 20
			c.MaxConcurrency = 1
		})
		rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		enqueue(t, s, store.NewTask{URL: rec.URL})
		var pauses atomic.Int32
		start(t, s, func(o *Options) {
			o.FirstProbeDelay, o.MaxProbeDelay = time.Hour, time.Hour
			o.AutoPaused = func(ev store.AutoPauseEvent) {
				if !ev.Resumed {
					pauses.Add(1)
				}
			}
		})
		waitForQueue(t, s, "q", func(q store.Queue) bool { return q.AutoPaused != nil })
		if _, err := s.SetQueuePaused(context.Background(), workspace.Default, "q", false); err != nil {
			t.Fatal(err)
		}
		waitForQueue(t, s, "q", func(q store.Queue) bool { return q.AutoPaused != nil && pauses.Load() == 2 })
		if n := rec.count(); n != 4 {
			t.Errorf("deliveries = %d, want 2 before each pause", n)
		}
	})
}

func waitForQueue(t *testing.T, s store.Store, name string, ok func(store.Queue) bool) store.Queue {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		q, err := s.GetQueue(context.Background(), workspace.Default, name)
		if err != nil {
			t.Fatalf("GetQueue: %v", err)
		}
		if ok(q) {
			return q
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue %s after 10s: %+v (auto-paused %+v)", name, q, q.AutoPaused)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
