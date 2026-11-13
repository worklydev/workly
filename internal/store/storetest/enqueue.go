package storetest

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// testConcurrentEnqueues enqueues from many goroutines at once, so stores
// that share statements between enqueues do: each enqueue must still get
// its own outcome, in its own workspace.
func testConcurrentEnqueues(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	for _, ws := range []workspace.ID{workspace.Default, otherWorkspace} {
		mustCreate(t, s, ws, store.NewTask{Queue: "emails"})
	}

	type outcome struct {
		task    store.Task
		created bool
		err     error
	}
	const n = 200
	out := make([]outcome, n)
	// Every fifth enqueue is to a missing queue; every seventh shares the
	// key "dup" in its workspace; the rest are ordinary.
	enqueue := func(i int) (workspace.ID, store.NewTask) {
		ws := workspace.Default
		if i%2 == 1 {
			ws = otherWorkspace
		}
		nt := store.NewTask{Queue: "emails", Method: "POST", URL: "http://example.test", RunAt: now(),
			Body: fmt.Appendf(nil, "%d", i)}
		switch {
		case i%5 == 0:
			nt.Queue = "missing"
		case i%7 == 0:
			nt.IdempotencyKey = "dup"
		}
		return ws, nt
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			ws, nt := enqueue(i)
			o := &out[i]
			o.task, o.created, o.err = s.CreateTask(ctx, ws, nt, false)
		})
	}
	wg.Wait()

	dup := map[workspace.ID]string{}
	dupCreated := map[workspace.ID]int{}
	for i, o := range out {
		ws, nt := enqueue(i)
		switch {
		case nt.Queue == "missing":
			if !errors.Is(o.err, store.ErrNotFound) {
				t.Fatalf("enqueue %d to a missing queue err = %v, want ErrNotFound", i, o.err)
			}
		case o.err != nil:
			t.Fatalf("enqueue %d: %v", i, o.err)
		case o.task.WorkspaceID != ws:
			t.Fatalf("enqueue %d: workspace %q, want %q", i, o.task.WorkspaceID, ws)
		case nt.IdempotencyKey != "":
			if dup[ws] == "" {
				dup[ws] = o.task.ID
			} else if o.task.ID != dup[ws] {
				t.Fatalf("enqueue %d: key dup in %s is task %s and %s", i, ws, o.task.ID, dup[ws])
			}
			if o.created {
				dupCreated[ws]++
			}
		default:
			if !o.created || string(o.task.Body) != string(nt.Body) {
				t.Fatalf("enqueue %d: created %v, body %q", i, o.created, o.task.Body)
			}
		}
	}
	for _, ws := range []workspace.ID{workspace.Default, otherWorkspace} {
		if dupCreated[ws] != 1 {
			t.Fatalf("key dup in %s created %d times, want 1", ws, dupCreated[ws])
		}
	}
}
