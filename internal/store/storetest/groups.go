package storetest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

func mustCreateGroup(t *testing.T, s store.Store, ws workspace.ID, ng store.NewGroup) store.Group {
	t.Helper()
	g, created, err := s.CreateGroup(t.Context(), ws, ng)
	if err != nil || !created {
		t.Fatalf("CreateGroup = %v, %v", created, err)
	}
	return g
}

func getGroup(t *testing.T, s store.Store, g store.Group) store.Group {
	t.Helper()
	got, err := s.GetGroup(t.Context(), g.WorkspaceID, g.ID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	return got
}

func closeGroup(t *testing.T, s store.Store, g store.Group) store.Group {
	t.Helper()
	got, err := s.CloseGroup(t.Context(), g.WorkspaceID, g.ID)
	if err != nil {
		t.Fatalf("CloseGroup: %v", err)
	}
	return got
}

// wantCounts checks a group's status and counts.
func wantCounts(t *testing.T, g store.Group, status store.GroupStatus, total, succeeded, failed, cancelled int) {
	t.Helper()
	if g.Status != status || g.Total != total || g.Succeeded != succeeded || g.Failed != failed || g.Cancelled != cancelled {
		t.Fatalf("group = %s total %d succeeded %d failed %d cancelled %d; want %s %d %d %d %d",
			g.Status, g.Total, g.Succeeded, g.Failed, g.Cancelled, status, total, succeeded, failed, cancelled)
	}
}

// claimAll claims every due task in the queue.
func claimAll(t *testing.T, s store.Store, ws workspace.ID, queue string) []store.Task {
	t.Helper()
	q, err := s.GetQueue(t.Context(), ws, queue)
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	claimed, err := s.ClaimTasks(t.Context(), q, 1000, time.Now(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimTasks: %v", err)
	}
	return claimed
}

// callbacks returns the tasks in the callback queue.
func callbacks(t *testing.T, s store.Store, ws workspace.ID) []store.Task {
	t.Helper()
	tasks, _, err := s.ListTasks(t.Context(), ws, store.TaskFilter{Queue: "callbacks", Limit: 100})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	return tasks
}

func onComplete() *store.GroupCallback {
	return &store.GroupCallback{Queue: "callbacks", URL: "http://example.test/done", Headers: map[string]string{"X-Job": "prices"}}
}

func testGroups(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	if _, err := s.GetGroup(ctx, workspace.Default, "grp_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetGroup(missing) err = %v, want ErrNotFound", err)
	}
	g := mustCreateGroup(t, s, workspace.Default, store.NewGroup{Name: "prices", IdempotencyKey: "2026-10-19", OnComplete: onComplete()})
	if g.WorkspaceID != workspace.Default || g.Name != "prices" || g.Status != store.GroupOpen || g.Total != 0 ||
		g.ClosedAt != nil || g.FinishedAt != nil || g.CallbackTaskID != "" {
		t.Fatalf("CreateGroup = %+v", g)
	}
	if cb := g.OnComplete; cb == nil || cb.Queue != "callbacks" || cb.URL != "http://example.test/done" || cb.Headers["X-Job"] != "prices" {
		t.Fatalf("OnComplete = %+v", g.OnComplete)
	}
	if got := getGroup(t, s, g); !reflect.DeepEqual(got, g) {
		t.Fatalf("GetGroup =\n %+v\nwant\n %+v", got, g)
	}

	// The same idempotency key returns the group.
	again, created, err := s.CreateGroup(ctx, workspace.Default, store.NewGroup{Name: "other", IdempotencyKey: "2026-10-19"})
	if err != nil || created || again.ID != g.ID || again.Name != "prices" {
		t.Fatalf("CreateGroup(same key) = %+v, %v, %v", again, created, err)
	}
	// Keys are per workspace.
	theirs := mustCreateGroup(t, s, otherWorkspace, store.NewGroup{IdempotencyKey: "2026-10-19"})
	if _, err := s.GetGroup(ctx, workspace.Default, theirs.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetGroup(other workspace) err = %v, want ErrNotFound", err)
	}

	// An empty group without a callback succeeds when closed.
	empty := mustCreateGroup(t, s, workspace.Default, store.NewGroup{Name: "empty"})
	closed := closeGroup(t, s, empty)
	wantCounts(t, closed, store.GroupSucceeded, 0, 0, 0, 0)
	if closed.ClosedAt == nil || closed.FinishedAt == nil || closed.CallbackTaskID != "" {
		t.Fatalf("closed empty group = %+v", closed)
	}
	// Closing again changes nothing.
	if again := closeGroup(t, s, empty); !reflect.DeepEqual(again, closed) {
		t.Fatalf("second CloseGroup = %+v", again)
	}
	if _, err := s.CloseGroup(ctx, workspace.Default, theirs.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CloseGroup(other workspace) err = %v, want ErrNotFound", err)
	}

	// Newest first, filtered, paged.
	third := mustCreateGroup(t, s, workspace.Default, store.NewGroup{Name: "prices"})
	list := func(f store.GroupFilter) ([]string, string) {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 10
		}
		groups, cursor, err := s.ListGroups(ctx, workspace.Default, f)
		if err != nil {
			t.Fatalf("ListGroups: %v", err)
		}
		ids := make([]string, len(groups))
		for i, g := range groups {
			ids[i] = g.ID
		}
		return ids, cursor
	}
	if ids, cursor := list(store.GroupFilter{}); fmt.Sprint(ids) != fmt.Sprint([]string{third.ID, empty.ID, g.ID}) || cursor != "" {
		t.Fatalf("ListGroups = %v, %q", ids, cursor)
	}
	if ids, _ := list(store.GroupFilter{Name: "prices"}); fmt.Sprint(ids) != fmt.Sprint([]string{third.ID, g.ID}) {
		t.Fatalf("ListGroups(name) = %v", ids)
	}
	if ids, _ := list(store.GroupFilter{Status: store.GroupSucceeded}); fmt.Sprint(ids) != fmt.Sprint([]string{empty.ID}) {
		t.Fatalf("ListGroups(status) = %v", ids)
	}
	ids, cursor := list(store.GroupFilter{Limit: 2})
	if len(ids) != 2 || cursor == "" {
		t.Fatalf("ListGroups(limit 2) = %v, %q", ids, cursor)
	}
	if ids, cursor := list(store.GroupFilter{Limit: 2, Cursor: cursor}); fmt.Sprint(ids) != fmt.Sprint([]string{g.ID}) || cursor != "" {
		t.Fatalf("ListGroups(page 2) = %v, %q", ids, cursor)
	}
}

func testGroupLifecycle(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	g := mustCreateGroup(t, s, workspace.Default, store.NewGroup{Name: "prices", OnComplete: onComplete()})

	a := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "prices", GroupID: g.ID, IdempotencyKey: "a"})
	if a.GroupID != g.ID {
		t.Fatalf("task GroupID = %q, want %q", a.GroupID, g.ID)
	}
	tasks, created, err := s.CreateTasks(ctx, workspace.Default, []store.NewTask{
		{Queue: "prices", Method: "POST", URL: "http://example.test/b", RunAt: now(), GroupID: g.ID},
		{Queue: "prices", Method: "POST", URL: "http://example.test/c", RunAt: now(), GroupID: g.ID},
		// Already enqueued: not counted twice.
		{Queue: "prices", Method: "POST", URL: "http://example.test/a", RunAt: now(), GroupID: g.ID, IdempotencyKey: "a"},
		// Not in the group.
		{Queue: "prices", Method: "POST", URL: "http://example.test/d", RunAt: now()},
	}, true)
	if err != nil || !created[0] || !created[1] || created[2] || !created[3] {
		t.Fatalf("CreateTasks = %v, %v", created, err)
	}
	wantCounts(t, getGroup(t, s, g), store.GroupOpen, 3, 0, 0, 0)

	members, _, err := s.ListTasks(ctx, workspace.Default, store.TaskFilter{GroupID: g.ID, Limit: 10})
	if err != nil || len(members) != 3 {
		t.Fatalf("ListTasks(group) = %v, %v", taskIDs(members), err)
	}

	// Tasks run while the group is open; a retry doesn't finish one.
	claimed := map[string]store.Task{}
	for _, c := range claimAll(t, s, workspace.Default, "prices") {
		claimed[c.ID] = c
	}
	finish(t, s, claimed[a.ID], store.TaskSucceeded)
	finish(t, s, claimed[tasks[0].ID], store.TaskPending)
	finish(t, s, claimed[tasks[3].ID], store.TaskFailed)
	wantCounts(t, getGroup(t, s, g), store.GroupOpen, 3, 1, 0, 0)

	running := closeGroup(t, s, g)
	wantCounts(t, running, store.GroupRunning, 3, 1, 0, 0)
	if running.ClosedAt == nil || running.FinishedAt != nil {
		t.Fatalf("closed group = %+v", running)
	}
	// A closed group takes no more tasks; nor does a missing one.
	if _, _, err := s.CreateTask(ctx, workspace.Default, store.NewTask{Queue: "prices", Method: "POST",
		URL: "http://example.test/e", RunAt: now(), GroupID: g.ID}, true); !errors.Is(err, store.ErrGroupClosed) {
		t.Fatalf("CreateTask(closed group) err = %v, want ErrGroupClosed", err)
	}
	for _, missing := range []store.Group{{ID: "grp_missing"}, mustCreateGroup(t, s, otherWorkspace, store.NewGroup{})} {
		_, _, err := s.CreateTask(ctx, workspace.Default, store.NewTask{Queue: "prices", Method: "POST",
			URL: "http://example.test/e", RunAt: now(), GroupID: missing.ID}, true)
		if !errors.Is(err, store.ErrGroupNotFound) || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("CreateTask(missing group) err = %v, want ErrGroupNotFound", err)
		}
	}
	// A failed batch enqueues nothing.
	if _, _, err := s.CreateTasks(ctx, workspace.Default, []store.NewTask{
		{Queue: "prices", Method: "POST", URL: "http://example.test/f", RunAt: now()},
		{Queue: "prices", Method: "POST", URL: "http://example.test/g", RunAt: now(), GroupID: g.ID},
	}, true); !errors.Is(err, store.ErrGroupClosed) {
		t.Fatalf("CreateTasks(closed group) err = %v, want ErrGroupClosed", err)
	}
	if all, _, _ := s.ListTasks(ctx, workspace.Default, store.TaskFilter{Queue: "prices", Limit: 100}); len(all) != 4 {
		t.Fatalf("tasks after failed batch = %d, want 4", len(all))
	}

	attempt(t, s, tasks[0], store.TaskFailed)
	wantCounts(t, getGroup(t, s, g), store.GroupRunning, 3, 1, 1, 0)
	if cbs := callbacks(t, s, workspace.Default); len(cbs) != 0 {
		t.Fatalf("callbacks before the last task = %v", taskIDs(cbs))
	}

	finish(t, s, claimed[tasks[1].ID], store.TaskSucceeded)
	done := getGroup(t, s, g)
	wantCounts(t, done, store.GroupFailed, 3, 2, 1, 0)
	if done.FinishedAt == nil || done.CallbackTaskID == "" {
		t.Fatalf("completed group = %+v", done)
	}
	cbs := callbacks(t, s, workspace.Default)
	if len(cbs) != 1 || cbs[0].ID != done.CallbackTaskID {
		t.Fatalf("callbacks = %v, want [%s]", taskIDs(cbs), done.CallbackTaskID)
	}
	cb := cbs[0]
	if cb.State != store.TaskPending || cb.Method != "POST" || cb.URL != "http://example.test/done" || cb.GroupID != "" ||
		cb.Headers["X-Job"] != "prices" || cb.Headers["Content-Type"] != "application/json" {
		t.Fatalf("callback task = %+v", cb)
	}
	var summary store.GroupSummary
	if err := json.Unmarshal(cb.Body, &summary); err != nil {
		t.Fatalf("callback body %s: %v", cb.Body, err)
	}
	if summary != (store.GroupSummary{GroupID: g.ID, Name: "prices", Status: store.GroupFailed, Total: 3, Succeeded: 2, Failed: 1}) {
		t.Fatalf("callback body = %+v", summary)
	}

	// A completed group is frozen: retrying its failed task doesn't reopen
	// it or change its counts.
	if _, err := s.RetryTask(ctx, workspace.Default, tasks[0].ID); err != nil {
		t.Fatalf("RetryTask: %v", err)
	}
	attempt(t, s, tasks[0], store.TaskSucceeded)
	if got := getGroup(t, s, g); !reflect.DeepEqual(got, done) {
		t.Fatalf("group after retry =\n %+v\nwant\n %+v", got, done)
	}
	if again := closeGroup(t, s, g); again.Status != store.GroupFailed {
		t.Fatalf("CloseGroup(completed) = %+v", again)
	}
	if cbs := callbacks(t, s, workspace.Default); len(cbs) != 1 {
		t.Fatalf("callbacks = %v, want one", taskIDs(cbs))
	}
}

// Manual retries and cancellations while a group is open or running move
// its counts both ways.
func testGroupControls(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	g := mustCreateGroup(t, s, workspace.Default, store.NewGroup{OnComplete: onComplete()})
	newTask := func(queue string) store.Task {
		return mustCreate(t, s, workspace.Default, store.NewTask{Queue: queue, GroupID: g.ID})
	}
	a, b := newTask("a"), newTask("b")
	newTask("c")
	newTask("c")

	attempt(t, s, a, store.TaskFailed)
	wantCounts(t, getGroup(t, s, g), store.GroupOpen, 4, 0, 1, 0)
	if _, err := s.RetryTask(ctx, workspace.Default, a.ID); err != nil {
		t.Fatalf("RetryTask: %v", err)
	}
	wantCounts(t, getGroup(t, s, g), store.GroupOpen, 4, 0, 0, 0)
	attempt(t, s, a, store.TaskFailed)
	if n, err := s.RetryFailedTasks(ctx, workspace.Default, "a", ""); err != nil || n != 1 {
		t.Fatalf("RetryFailedTasks = %d, %v", n, err)
	}
	wantCounts(t, getGroup(t, s, g), store.GroupOpen, 4, 0, 0, 0)
	attempt(t, s, a, store.TaskSucceeded)
	// Retrying a succeeded task un-counts it too.
	if _, err := s.RetryTask(ctx, workspace.Default, a.ID); err != nil {
		t.Fatalf("RetryTask: %v", err)
	}
	wantCounts(t, getGroup(t, s, g), store.GroupOpen, 4, 0, 0, 0)
	attempt(t, s, a, store.TaskSucceeded)

	// Cancelled mid-attempt: counted once, when cancelled.
	running := claim(t, s, b)
	if _, err := s.CancelTask(ctx, workspace.Default, b.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if _, err := s.CancelTask(ctx, workspace.Default, b.ID); err != nil {
		t.Fatalf("second CancelTask: %v", err)
	}
	wantCounts(t, getGroup(t, s, g), store.GroupOpen, 4, 1, 0, 1)
	finish(t, s, running, store.TaskSucceeded)
	wantCounts(t, getGroup(t, s, g), store.GroupOpen, 4, 1, 0, 1)

	closeGroup(t, s, g)
	if n, err := s.CancelPendingTasks(ctx, workspace.Default, "c"); err != nil || n != 2 {
		t.Fatalf("CancelPendingTasks = %d, %v", n, err)
	}
	done := getGroup(t, s, g)
	wantCounts(t, done, store.GroupFailed, 4, 1, 0, 3)
	if done.CallbackTaskID == "" {
		t.Fatalf("no callback")
	}
}

// Deleting a queue takes its tasks out of their groups, which may complete
// without them.
func testGroupDeleteQueue(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	g := mustCreateGroup(t, s, workspace.Default, store.NewGroup{OnComplete: onComplete()})
	kept := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "kept", GroupID: g.ID})
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "gone", GroupID: g.ID})
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "gone", GroupID: g.ID})
	attempt(t, s, kept, store.TaskSucceeded)
	closeGroup(t, s, g)

	if err := s.DeleteQueue(ctx, workspace.Default, "gone"); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	done := getGroup(t, s, g)
	wantCounts(t, done, store.GroupSucceeded, 1, 1, 0, 0)
	if done.CallbackTaskID == "" {
		t.Fatalf("no callback")
	}

	// The callback queue is recreated if it was deleted.
	g2 := mustCreateGroup(t, s, workspace.Default, store.NewGroup{OnComplete: onComplete()})
	if err := s.DeleteQueue(ctx, workspace.Default, "callbacks"); err != nil {
		t.Fatalf("DeleteQueue(callbacks): %v", err)
	}
	if g2 = closeGroup(t, s, g2); g2.CallbackTaskID == "" {
		t.Fatalf("no callback after its queue was deleted")
	}
	if _, err := s.GetTask(ctx, workspace.Default, g2.CallbackTaskID); err != nil {
		t.Fatalf("GetTask(callback): %v", err)
	}
}

// Completions racing each other and the close, from separate handles (so
// separate connections on Postgres), complete the group once.
func testGroupConcurrency(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	const n = 40
	g := mustCreateGroup(t, s, workspace.Default, store.NewGroup{OnComplete: onComplete()})
	for range n {
		mustCreate(t, s, workspace.Default, store.NewTask{Queue: "prices", GroupID: g.ID})
	}
	claimed := claimAll(t, s, workspace.Default, "prices")
	if len(claimed) != n {
		t.Fatalf("claimed %d, want %d", len(claimed), n)
	}

	handles := []store.Store{s, open(), open(), open()}
	var wg sync.WaitGroup
	errs := make(chan error, n+1)
	for i, c := range claimed {
		h := handles[i%len(handles)]
		wg.Go(func() {
			_, err := h.FinishAttempt(ctx, workspace.Default, store.AttemptResult{
				Attempt:   store.Attempt{TaskID: c.ID, Number: c.AttemptCount, StartedAt: now(), FinishedAt: now(), Outcome: store.OutcomeSuccess},
				NextState: store.TaskSucceeded,
			})
			errs <- err
		})
		if i == n/2 {
			wg.Go(func() {
				_, err := handles[1].CloseGroup(ctx, workspace.Default, g.ID)
				errs <- err
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent finish or close: %v", err)
		}
	}
	wantCounts(t, getGroup(t, s, g), store.GroupSucceeded, n, n, 0, 0)
	if cbs := callbacks(t, s, workspace.Default); len(cbs) != 1 {
		t.Fatalf("callbacks = %v, want one", taskIDs(cbs))
	}

	// Enqueues racing the close: every task that got in is counted, and
	// none got in after.
	g2 := mustCreateGroup(t, s, workspace.Default, store.NewGroup{})
	var (
		mu     sync.Mutex
		joined int
		closed store.Group
	)
	for i := range n {
		h := handles[i%len(handles)]
		wg.Go(func() {
			_, _, err := h.CreateTask(ctx, workspace.Default, store.NewTask{Queue: "race", Method: "POST",
				URL: "http://example.test/race", RunAt: now(), GroupID: g2.ID}, true)
			switch {
			case err == nil:
				mu.Lock()
				joined++
				mu.Unlock()
			case !errors.Is(err, store.ErrGroupClosed):
				t.Errorf("CreateTask: %v", err)
			}
		})
		if i == n/2 {
			wg.Go(func() {
				var err error
				if closed, err = handles[2].CloseGroup(ctx, workspace.Default, g2.ID); err != nil {
					t.Errorf("CloseGroup: %v", err)
				}
			})
		}
	}
	wg.Wait()
	got := getGroup(t, s, g2)
	members, _, err := s.ListTasks(ctx, workspace.Default, store.TaskFilter{GroupID: g2.ID, Limit: 200})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if got.Total != joined || len(members) != joined || closed.Total != joined {
		t.Fatalf("total %d (at close %d), %d member tasks, %d enqueues succeeded", got.Total, closed.Total, len(members), joined)
	}
}

func testDeleteFinishedGroups(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	done := closeGroup(t, s, mustCreateGroup(t, s, workspace.Default, store.NewGroup{}))
	running := mustCreateGroup(t, s, workspace.Default, store.NewGroup{})
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "q", GroupID: running.ID})
	closeGroup(t, s, running)
	openGroup := mustCreateGroup(t, s, workspace.Default, store.NewGroup{})

	if n, err := s.DeleteFinishedGroups(ctx, done.FinishedAt.Add(-time.Second), 10); err != nil || n != 0 {
		t.Fatalf("DeleteFinishedGroups(before) = %d, %v", n, err)
	}
	if n, err := s.DeleteFinishedGroups(ctx, time.Now().Add(time.Second), 10); err != nil || n != 1 {
		t.Fatalf("DeleteFinishedGroups = %d, %v; want 1", n, err)
	}
	if _, err := s.GetGroup(ctx, workspace.Default, done.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetGroup(deleted) err = %v", err)
	}
	getGroup(t, s, running)
	getGroup(t, s, openGroup)
}
