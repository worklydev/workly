package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/sqlite"
	"github.com/worklydev/workly/internal/workspace"
)

// batchStore records the size of each FinishAttempts call. The first call
// waits for release; with fail set, every call fails.
type batchStore struct {
	store.Store
	release chan struct{}
	fail    bool

	mu      sync.Mutex
	batches []int
}

func (s *batchStore) FinishAttempts(ctx context.Context, rs []store.FinishedAttempt) ([]store.FinishOutcome, error) {
	s.mu.Lock()
	first := len(s.batches) == 0
	s.batches = append(s.batches, len(rs))
	s.mu.Unlock()
	if first {
		<-s.release
	}
	if s.fail {
		return nil, errors.New("rejected")
	}
	return s.Store.FinishAttempts(ctx, rs)
}

func TestResultWriter(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "batches"
		if fail {
			name = "falls back to one at a time"
		}
		t.Run(name, func(t *testing.T) {
			s, err := sqlite.Open(filepath.Join(t.TempDir(), "workly.db"))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			inner := setup(t, func() store.Store { return s })

			const n = 20
			putQueue(t, inner, "q", nil)
			for range n {
				enqueue(t, inner, store.NewTask{URL: "http://example.test"})
			}
			q, _ := inner.GetQueue(t.Context(), workspace.Default, "q")
			tasks, err := inner.ClaimTasks(t.Context(), q, n, time.Now(), time.Minute)
			if err != nil || len(tasks) != n {
				t.Fatalf("ClaimTasks = %d, %v", len(tasks), err)
			}

			bs := &batchStore{Store: inner, release: make(chan struct{}), fail: fail}
			w := startResultWriter(bs)
			defer w.stop()

			// The first result's write holds up the writer while the rest
			// queue, so they go into one batch.
			states := make([]store.TaskState, n)
			errs := make([]error, n)
			var wg sync.WaitGroup
			finish := func(i int) {
				states[i], errs[i] = w.finish(store.FinishedAttempt{
					Workspace: workspace.Default,
					AttemptResult: store.AttemptResult{
						Attempt:   store.Attempt{TaskID: tasks[i].ID, Number: 1, StartedAt: time.Now(), FinishedAt: time.Now(), Outcome: store.OutcomeSuccess},
						NextState: store.TaskSucceeded,
					},
				})
			}
			wg.Go(func() { finish(0) })
			for len(bs.batchSizes()) == 0 {
				time.Sleep(time.Millisecond)
			}
			for i := 1; i < n; i++ {
				wg.Go(func() { finish(i) })
			}
			for len(w.reqs) < n-1 {
				time.Sleep(time.Millisecond)
			}
			close(bs.release)
			wg.Wait()

			if got := bs.batchSizes(); len(got) != 2 || got[0] != 1 || got[1] != n-1 {
				t.Fatalf("batches = %v, want [1 %d]", got, n-1)
			}
			for i := range n {
				// Alone in its batch, the first result has nothing to fall
				// back to.
				if fail && i == 0 {
					if errs[i] == nil {
						t.Fatal("first result: no error from a failed write")
					}
					continue
				}
				if errs[i] != nil || states[i] != store.TaskSucceeded {
					t.Fatalf("result %d = %q, %v", i, states[i], errs[i])
				}
			}
		})
	}
}

func (s *batchStore) batchSizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.batches...)
}
