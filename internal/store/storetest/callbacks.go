package storetest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// callbackFor returns the callback tasks whose body is about id, oldest
// first, decoded into T.
func callbackFor[T any](t *testing.T, s store.Store, ws workspace.ID, id string, key string) []T {
	t.Helper()
	var out []T
	tasks := callbacks(t, s, ws)
	for i := len(tasks) - 1; i >= 0; i-- { // newest first
		cb := tasks[i]
		var m map[string]any
		if err := json.Unmarshal(cb.Body, &m); err != nil {
			t.Fatalf("callback body %q: %v", cb.Body, err)
		}
		if m[key] != id {
			continue
		}
		var body T
		if err := json.Unmarshal(cb.Body, &body); err != nil {
			t.Fatalf("callback body %q: %v", cb.Body, err)
		}
		out = append(out, body)
		if cb.Method != "POST" || cb.URL != "http://example.test/done" || cb.Headers["X-Job"] != "prices" ||
			cb.Headers["Content-Type"] != "application/json" || cb.OnComplete != nil {
			t.Fatalf("callback task = %+v", cb)
		}
	}
	return out
}

func taskCallbacks(t *testing.T, s store.Store, task store.Task) []store.TaskCallbackBody {
	t.Helper()
	return callbackFor[store.TaskCallbackBody](t, s, task.WorkspaceID, task.ID, "task_id")
}

func testTaskCallbacks(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	ws := workspace.Default

	// Stored with the task.
	task := mustCreate(t, s, ws, store.NewTask{Queue: "jobs", IdempotencyKey: "k1", OnComplete: onComplete()})
	if cb := task.OnComplete; cb == nil || cb.URL != "http://example.test/done" || cb.Headers["X-Job"] != "prices" {
		t.Fatalf("OnComplete = %+v", task.OnComplete)
	}
	if got := get(t, s, task); got.OnComplete == nil || got.CallbackTaskID != "" {
		t.Fatalf("GetTask = %+v", got)
	}

	// A retryable failure doesn't call back; the last attempt does.
	attempt(t, s, task, store.TaskPending)
	if got := taskCallbacks(t, s, task); len(got) != 0 {
		t.Fatalf("callbacks after a retry = %+v", got)
	}
	failWith(t, s, task, store.OutcomeHTTPError, 500, "HTTP 500", "boom")
	got := taskCallbacks(t, s, task)
	if len(got) != 1 {
		t.Fatalf("callbacks = %+v, want 1", got)
	}
	failed := get(t, s, task)
	if b := got[0]; b.State != store.TaskFailed || b.Queue != "jobs" || b.AttemptCount != 2 ||
		b.LastStatus == nil || *b.LastStatus != 500 || b.LastError != "HTTP 500" || b.IdempotencyKey != "k1" ||
		!b.FinishedAt.Equal(*failed.FinishedAt) {
		t.Fatalf("callback = %+v", b)
	}
	if failed.CallbackTaskID == "" {
		t.Fatalf("CallbackTaskID not set")
	}
	if cb, err := s.GetTask(ctx, ws, failed.CallbackTaskID); err != nil || cb.Queue != store.CallbackQueue {
		t.Fatalf("callback task = %+v, %v", cb, err)
	}

	// Retried by hand, it calls back again when it finishes again.
	if _, err := s.RetryTask(ctx, ws, task.ID); err != nil {
		t.Fatalf("RetryTask: %v", err)
	}
	attempt(t, s, task, store.TaskSucceeded)
	got = taskCallbacks(t, s, task)
	if len(got) != 2 || got[1].State != store.TaskSucceeded || got[1].AttemptCount != 3 {
		t.Fatalf("callbacks after retry = %+v", got)
	}
	if again := get(t, s, task); again.CallbackTaskID == failed.CallbackTaskID {
		t.Fatalf("CallbackTaskID not updated")
	}

	// Cancelling calls back, and a cancelled task's in-flight attempt
	// finishing later doesn't call back again.
	running := mustCreate(t, s, ws, store.NewTask{Queue: "jobs", OnComplete: onComplete()})
	claimed := claim(t, s, running)
	if _, err := s.CancelTask(ctx, ws, running.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	finish(t, s, claimed, store.TaskSucceeded)
	if got := taskCallbacks(t, s, running); len(got) != 1 || got[0].State != store.TaskCancelled {
		t.Fatalf("callbacks for a cancelled task = %+v", got)
	}

	// Cancelling a queue's pending tasks calls back for each.
	a := mustCreate(t, s, ws, store.NewTask{Queue: "bulk", OnComplete: onComplete(), RunAt: now().Add(time.Hour)})
	b := mustCreate(t, s, ws, store.NewTask{Queue: "bulk", OnComplete: onComplete(), RunAt: now().Add(time.Hour)})
	plain := mustCreate(t, s, ws, store.NewTask{Queue: "bulk", RunAt: now().Add(time.Hour)})
	if n, err := s.CancelPendingTasks(ctx, ws, "bulk"); err != nil || n != 3 {
		t.Fatalf("CancelPendingTasks = %d, %v", n, err)
	}
	for _, task := range []store.Task{a, b} {
		if got := taskCallbacks(t, s, task); len(got) != 1 || got[0].State != store.TaskCancelled {
			t.Fatalf("callbacks for %s = %+v", task.ID, got)
		}
	}
	if got := taskCallbacks(t, s, plain); len(got) != 0 {
		t.Fatalf("callbacks for a task without on_complete = %+v", got)
	}

	// Deleting a queue deletes its tasks without calling back.
	gone := mustCreate(t, s, ws, store.NewTask{Queue: "gone", OnComplete: onComplete()})
	if err := s.DeleteQueue(ctx, ws, "gone"); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	if got := taskCallbacks(t, s, gone); len(got) != 0 {
		t.Fatalf("callbacks for a deleted task = %+v", got)
	}

	// Finishing many at once calls back for each, in the task's workspace.
	other := mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "jobs", OnComplete: onComplete()})
	attempt(t, s, other, store.TaskSucceeded)
	if got := taskCallbacks(t, s, other); len(got) != 1 {
		t.Fatalf("callbacks in the other workspace = %+v", got)
	}

	// The callback queue is recreated if it was deleted.
	if err := s.DeleteQueue(ctx, ws, store.CallbackQueue); err != nil {
		t.Fatalf("DeleteQueue(callbacks): %v", err)
	}
	last := mustCreate(t, s, ws, store.NewTask{Queue: "jobs", OnComplete: onComplete()})
	attempt(t, s, last, store.TaskSucceeded)
	if got := taskCallbacks(t, s, last); len(got) != 1 {
		t.Fatalf("callbacks after the callback queue was deleted = %+v", got)
	}
}

func runCallbacks(t *testing.T, s store.Store, r store.Run) []store.RunCallbackBody {
	t.Helper()
	return callbackFor[store.RunCallbackBody](t, s, r.WorkspaceID, r.ID, "run_id")
}

func testRunCallbacks(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	ws := workspace.Default
	cfg := scheduleConfig()
	cfg.OnComplete = onComplete()
	cfg.Overlap = store.OverlapAllow
	sc, _, err := s.PutSchedule(ctx, ws, "nightly", cfg, now())
	if err != nil {
		t.Fatalf("PutSchedule: %v", err)
	}
	if cb := sc.OnComplete; cb == nil || cb.URL != "http://example.test/done" || cb.Headers["X-Job"] != "prices" {
		t.Fatalf("OnComplete = %+v", sc.OnComplete)
	}

	// A retry doesn't call back; the run finishing does.
	r := fire(t, s, sc, "")
	claimed := claimRuns(t, s)[0].Run
	finishRun(t, s, claimed, store.RunPending)
	if got := runCallbacks(t, s, r); len(got) != 0 {
		t.Fatalf("callbacks after a retry = %+v", got)
	}
	claimed = claimRuns(t, s)[0].Run
	finishRun(t, s, claimed, store.RunSucceeded)
	got := runCallbacks(t, s, r)
	done := getRun(t, s, r)
	if len(got) != 1 || got[0].State != store.RunSucceeded || got[0].Schedule != "nightly" ||
		got[0].AttemptCount != 2 || got[0].Trigger != store.TriggerSchedule ||
		!got[0].ScheduledFor.Equal(r.ScheduledFor) || !got[0].FinishedAt.Equal(*done.FinishedAt) {
		t.Fatalf("callbacks = %+v", got)
	}
	if done.CallbackTaskID == "" {
		t.Fatalf("CallbackTaskID not set")
	}

	// Retried by hand, it calls back again.
	if _, err := s.RetryRun(ctx, ws, r.ID); err != nil {
		t.Fatalf("RetryRun: %v", err)
	}
	finishRun(t, s, claimRuns(t, s)[0].Run, store.RunFailed)
	if got := runCallbacks(t, s, r); len(got) != 2 || got[1].State != store.RunFailed {
		t.Fatalf("callbacks after retry = %+v", got)
	}

	// Cancelling calls back once, even when the attempt in flight finishes.
	r2 := fire(t, s, sc, "")
	claimed = claimRuns(t, s)[0].Run
	if _, err := s.CancelRun(ctx, ws, r2.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	finishRun(t, s, claimed, store.RunSucceeded)
	if got := runCallbacks(t, s, r2); len(got) != 1 || got[0].State != store.RunCancelled {
		t.Fatalf("callbacks for a cancelled run = %+v", got)
	}

	// Skipped runs don't call back.
	skipped := fire(t, s, sc, store.SkipOverlap)
	if got := runCallbacks(t, s, skipped); len(got) != 0 {
		t.Fatalf("callbacks for a skipped run = %+v", got)
	}

	// The schedule's current callback applies: without one, none.
	cfg.OnComplete = nil
	if sc, _, err = s.PutSchedule(ctx, ws, "nightly", cfg, now()); err != nil || sc.OnComplete != nil {
		t.Fatalf("PutSchedule = %+v, %v", sc, err)
	}
	r3 := fire(t, s, sc, "")
	finishRun(t, s, claimRuns(t, s)[0].Run, store.RunSucceeded)
	if got := runCallbacks(t, s, r3); len(got) != 0 {
		t.Fatalf("callbacks without on_complete = %+v", got)
	}
}
