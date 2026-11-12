package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// addWorkspace creates workspace ws with the named queues, each allowing
// maxConcurrency deliveries at once.
func addWorkspace(t *testing.T, s store.Store, ws workspace.ID, maxConcurrency int, queues ...string) {
	t.Helper()
	if _, err := s.EnsureWorkspace(t.Context(), ws, string(ws)); err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	for _, name := range queues {
		cfg := store.DefaultQueueConfig()
		cfg.MaxConcurrency = maxConcurrency
		if _, _, err := s.PutQueue(t.Context(), ws, name, cfg); err != nil {
			t.Fatalf("PutQueue: %v", err)
		}
	}
}

func enqueueIn(t *testing.T, s store.Store, ws workspace.ID, queue, url string) store.Task {
	t.Helper()
	task, _, err := s.CreateTask(t.Context(), ws, store.NewTask{Queue: queue, Method: "POST", URL: url, RunAt: time.Now()}, true)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

// A workspace whose endpoint hangs keeps at most its own share of the
// delivery slots, so other workspaces' tasks are still delivered.
func TestWorkspaceIsolation(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		var hanging atomic.Int64
		release := make(chan struct{})
		slow := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
			hanging.Add(1)
			<-release
		})
		fast := newRecorder(t, nil)

		// Many queues, so a share per queue would hand it most of the slots.
		addWorkspace(t, s, "noisy", 100, "a", "b", "c", "d")
		addWorkspace(t, s, "quiet", 100, "q")
		for _, q := range []string{"a", "b", "c", "d"} {
			for range 25 {
				enqueueIn(t, s, "noisy", q, slow.URL)
			}
		}
		start(t, s, func(o *Options) {
			o.MaxInFlight = 20
			o.WorkspaceMaxInFlight = func(context.Context, workspace.ID) (int, error) { return 10, nil }
		})
		t.Cleanup(func() { close(release) })

		deadline := time.Now().Add(5 * time.Second)
		for hanging.Load() < 10 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		// Give the dispatcher time to take more slots, if it would.
		time.Sleep(200 * time.Millisecond)
		if n := hanging.Load(); n != 10 {
			t.Fatalf("noisy workspace has %d deliveries in flight, want its cap of 10", n)
		}

		var tasks []store.Task
		for range 5 {
			tasks = append(tasks, enqueueIn(t, s, "quiet", "q", fast.URL))
		}
		for _, task := range tasks {
			deadline := time.Now().Add(3 * time.Second)
			for {
				got, err := s.GetTask(t.Context(), "quiet", task.ID)
				if err == nil && got.State == store.TaskSucceeded {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("quiet workspace's task %s is %s while the noisy one hangs, want succeeded", task.ID, got.State)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	})
}

// A workspace's cap is looked up once per limitTTL, and a failed lookup
// keeps the cap it had.
func TestWorkspaceLimitLookup(t *testing.T) {
	var calls int
	var fail bool
	d := New(Options{
		Logger: slog.New(slog.DiscardHandler),
		WorkspaceMaxInFlight: func(context.Context, workspace.ID) (int, error) {
			calls++
			if fail {
				return 0, errors.New("accounts are down")
			}
			return 7, nil
		},
	})
	queues := []store.Queue{{WorkspaceID: "acme", Name: "a"}, {WorkspaceID: "acme", Name: "b"}}
	now := time.Now()

	d.refreshLimits(t.Context(), queues, now)
	d.refreshLimits(t.Context(), queues, now.Add(limitTTL/2))
	if calls != 1 {
		t.Fatalf("looked up %d times within limitTTL, want 1", calls)
	}
	if room := d.workspaceRoom("acme"); room != 7 {
		t.Fatalf("room = %d, want the cap of 7", room)
	}

	fail = true
	d.refreshLimits(t.Context(), queues, now.Add(limitTTL))
	if calls != 2 {
		t.Fatalf("looked up %d times after limitTTL, want 2", calls)
	}
	if room := d.workspaceRoom("acme"); room != 7 {
		t.Fatalf("room after a failed lookup = %d, want the previous cap of 7", room)
	}
}

// When slots are scarce, workspaces take turns, however many queues each
// has: a share per queue would give a workspace with five queues five times
// as many deliveries.
func TestWorkspaceFairness(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		var (
			mu  sync.Mutex
			got []string
		)
		endpoint := func(ws string) string {
			return newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				got = append(got, ws)
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
			}).URL
		}
		bigURL, smallURL := endpoint("big"), endpoint("small")
		addWorkspace(t, s, "big", 3, "a", "b", "c", "d", "e")
		addWorkspace(t, s, "small", 3, "q")
		for _, q := range []string{"a", "b", "c", "d", "e"} {
			for range 8 {
				enqueueIn(t, s, "big", q, bigURL)
			}
		}
		for range 20 {
			enqueueIn(t, s, "small", "q", smallURL)
		}
		start(t, s, func(o *Options) { o.MaxInFlight = 2 })

		var first []string
		deadline := time.Now().Add(10 * time.Second)
		for len(first) < 20 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			first = slices.Clone(got[:min(len(got), 20)])
			mu.Unlock()
		}
		if n := count(first, "small"); n < 7 {
			t.Fatalf("small workspace got %d of the first 20 deliveries, want about half: %v", n, first)
		}
	})
}
