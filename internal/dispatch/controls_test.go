package dispatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// recorder is an endpoint that records when each delivery arrived and from
// which queue.
type recorder struct {
	*httptest.Server
	mu       sync.Mutex
	arrivals []time.Time
	queues   []string
}

func newRecorder(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *recorder {
	rec := &recorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.arrivals = append(rec.arrivals, time.Now())
		rec.queues = append(rec.queues, r.Header.Get("Workly-Queue"))
		rec.mu.Unlock()
		if handle != nil {
			handle(w, r)
		}
	}))
	t.Cleanup(rec.Close)
	return rec
}

func (r *recorder) snapshot() ([]time.Time, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.arrivals), slices.Clone(r.queues)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.arrivals)
}

func waitAll(t *testing.T, s store.Store, tasks []store.Task, want store.TaskState) {
	t.Helper()
	for _, task := range tasks {
		waitForState(t, s, task.ID, want)
	}
}

func TestRateLimit(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		putQueue(t, s, "q", func(c *store.QueueConfig) { c.RatePerSecond = 10 })
		rec := newRecorder(t, nil)
		var tasks []store.Task
		for range 11 {
			tasks = append(tasks, enqueue(t, s, store.NewTask{URL: rec.URL}))
		}
		start(t, s)
		waitAll(t, s, tasks, store.TaskSucceeded)

		// 11 deliveries at 10/s (a burst of 1) take a second: one at
		// once, then one every 100ms. The upper bound is loose for slow CI
		// runners under -race; waiting out the 1s poll between deliveries
		// instead of the next token would take ~10s.
		arrivals, _ := rec.snapshot()
		span := arrivals[10].Sub(arrivals[0])
		if span < 950*time.Millisecond || span > 5*time.Second {
			t.Fatalf("11 deliveries at 10/s took %v, want ~1s", span)
		}
		for i := 1; i < len(arrivals); i++ {
			if gap := arrivals[i].Sub(arrivals[i-1]); gap < 50*time.Millisecond {
				t.Fatalf("deliveries %d and %d were %v apart, want ~100ms", i-1, i, gap)
			}
		}
	})
}

func TestConcurrencyLimit(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		putQueue(t, s, "q", func(c *store.QueueConfig) { c.MaxConcurrency = 2 })
		var current, peak atomic.Int64
		rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
			n := current.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			current.Add(-1)
		})
		var tasks []store.Task
		for range 10 {
			tasks = append(tasks, enqueue(t, s, store.NewTask{URL: rec.URL}))
		}
		start(t, s)
		waitAll(t, s, tasks, store.TaskSucceeded)
		if p := peak.Load(); p != 2 {
			t.Fatalf("peak concurrency = %d, want 2", p)
		}
	})
}

// When slots are scarce, queues take turns: a big backlog in one queue
// doesn't hold up another.
func TestFairness(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(10 * time.Millisecond)
		})
		var tasks []store.Task
		for _, q := range []string{"a", "b"} {
			for range 20 {
				tasks = append(tasks, enqueue(t, s, store.NewTask{Queue: q, URL: rec.URL}))
			}
		}
		start(t, s, func(o *Options) { o.MaxInFlight = 2 })
		waitAll(t, s, tasks, store.TaskSucceeded)

		_, queues := rec.snapshot()
		first := queues[:10]
		if a, b := count(first, "a"), count(first, "b"); a < 3 || b < 3 {
			t.Fatalf("first 10 deliveries = %v, want both queues served", first)
		}
	})
}

func count(s []string, v string) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}

func TestPauseResume(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		ctx := context.Background()
		putQueue(t, s, "q", func(c *store.QueueConfig) { c.RatePerSecond = 20 })
		rec := newRecorder(t, nil)
		var tasks []store.Task
		for range 30 {
			tasks = append(tasks, enqueue(t, s, store.NewTask{URL: rec.URL}))
		}
		start(t, s)
		for rec.count() < 5 {
			time.Sleep(time.Millisecond)
		}

		// Dispatch stops within a loop tick: at most a claim already under
		// way still goes out, which at 20/s is up to a burst of 2.
		if _, err := s.SetQueuePaused(ctx, workspace.Default, "q", true); err != nil {
			t.Fatalf("SetQueuePaused: %v", err)
		}
		paused := rec.count()
		time.Sleep(300 * time.Millisecond)
		if n := rec.count(); n > paused+2 {
			t.Fatalf("%d deliveries after pausing, want at most 2", n-paused)
		}

		resumed := time.Now()
		if _, err := s.SetQueuePaused(ctx, workspace.Default, "q", false); err != nil {
			t.Fatalf("SetQueuePaused: %v", err)
		}
		waitAll(t, s, tasks, store.TaskSucceeded)
		// The rest go out at 20/s once resumed.
		if d := time.Since(resumed); d > 3*time.Second {
			t.Fatalf("remaining deliveries took %v after resuming", d)
		}
	})
}

// A task cancelled mid-delivery has that attempt recorded, but isn't retried.
func TestCancelDuringDelivery(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		arrived, release := make(chan struct{}), make(chan struct{})
		rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
			close(arrived)
			<-release
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		task := enqueue(t, s, store.NewTask{URL: rec.URL})
		start(t, s)
		<-arrived

		if _, err := s.CancelTask(context.Background(), workspace.Default, task.ID); err != nil {
			t.Fatalf("CancelTask: %v", err)
		}
		close(release)
		deadline := time.Now().Add(10 * time.Second)
		for len(outcomes(t, s, task.ID)) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("attempt never recorded")
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond) // a retry would be immediate (10ms backoff)
		got := waitForState(t, s, task.ID, store.TaskCancelled)
		if got.LeaseExpiresAt != nil || rec.count() != 1 || !equal(outcomes(t, s, task.ID), []store.Outcome{store.OutcomeHTTPError}) {
			t.Fatalf("cancelled task = %+v, %d deliveries", got, rec.count())
		}
	})
}

// A replayed task is delivered again with a fresh attempt budget.
func TestReplay(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		putQueue(t, s, "q", func(c *store.QueueConfig) { c.MaxAttempts = 1 })
		var fail atomic.Bool
		fail.Store(true)
		var attempts []string
		var mu sync.Mutex
		rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			attempts = append(attempts, r.Header.Get("Workly-Attempt"))
			mu.Unlock()
			if fail.Load() {
				w.WriteHeader(http.StatusInternalServerError)
			}
		})
		task := enqueue(t, s, store.NewTask{URL: rec.URL})
		start(t, s)
		waitForState(t, s, task.ID, store.TaskFailed)

		fail.Store(false)
		if _, err := s.RetryTask(context.Background(), workspace.Default, task.ID); err != nil {
			t.Fatalf("RetryTask: %v", err)
		}
		waitForState(t, s, task.ID, store.TaskSucceeded)
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(attempts, []string{"1", "2"}) {
			t.Fatalf("Workly-Attempt headers = %v, want [1 2]", attempts)
		}
	})
}

func TestRetention(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		rec := newRecorder(t, nil)
		done := enqueue(t, s, store.NewTask{URL: rec.URL})
		scheduled := enqueue(t, s, store.NewTask{URL: rec.URL, RunAt: time.Now().Add(time.Hour)})
		start(t, s, func(o *Options) {
			o.Retention = 100 * time.Millisecond
			o.SweepInterval = 20 * time.Millisecond
		})
		waitForState(t, s, done.ID, store.TaskSucceeded)

		deadline := time.Now().Add(10 * time.Second)
		for {
			_, err := s.GetTask(context.Background(), workspace.Default, done.ID)
			if err == store.ErrNotFound {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("finished task not deleted after 10s (err %v)", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		waitForState(t, s, scheduled.ID, store.TaskPending)
	})
}
