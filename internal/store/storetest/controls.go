package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// attempt claims a due task and finishes its attempt with the given next
// state. It returns the task as claimed.
func attempt(t *testing.T, s store.Store, task store.Task, next store.TaskState) store.Task {
	t.Helper()
	claimed := claim(t, s, task)
	finish(t, s, claimed, next)
	return claimed
}

// claim claims a task, which must be the only due task in its queue.
func claim(t *testing.T, s store.Store, task store.Task) store.Task {
	t.Helper()
	q, err := s.GetQueue(t.Context(), task.WorkspaceID, task.Queue)
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	claimed, err := s.ClaimTasks(t.Context(), q, 10, time.Now(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimTasks: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != task.ID {
		t.Fatalf("ClaimTasks = %v, want [%s]", taskIDs(claimed), task.ID)
	}
	return claimed[0]
}

func finish(t *testing.T, s store.Store, claimed store.Task, next store.TaskState) store.TaskState {
	t.Helper()
	r := store.AttemptResult{
		Attempt: store.Attempt{
			TaskID: claimed.ID, Number: claimed.AttemptCount,
			StartedAt: now(), FinishedAt: now(), Outcome: store.OutcomeHTTPError,
		},
		NextState: next,
		RunAt:     now(),
	}
	if next == store.TaskSucceeded {
		r.Outcome = store.OutcomeSuccess
	}
	state, err := s.FinishAttempt(t.Context(), claimed.WorkspaceID, r)
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
	return state
}

// failWith claims a due task and fails it for good with the given result.
func failWith(t *testing.T, s store.Store, task store.Task, outcome store.Outcome, status int, errText, body string) {
	t.Helper()
	claimed := claim(t, s, task)
	r := store.AttemptResult{
		Attempt: store.Attempt{
			TaskID: claimed.ID, Number: claimed.AttemptCount, StartedAt: now(), FinishedAt: now(),
			Outcome: outcome, Error: errText, ResponseBody: body,
		},
		NextState: store.TaskFailed,
	}
	if status != 0 {
		r.StatusCode = &status
	}
	if _, err := s.FinishAttempt(t.Context(), claimed.WorkspaceID, r); err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
}

func get(t *testing.T, s store.Store, task store.Task) store.Task {
	t.Helper()
	got, err := s.GetTask(t.Context(), task.WorkspaceID, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	return got
}

func testCreateTasks(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	existing := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", IdempotencyKey: "a"})

	nt := func(key string) store.NewTask {
		return store.NewTask{Queue: "emails", Method: "POST", URL: "http://example.test", RunAt: now(), IdempotencyKey: key}
	}
	tasks, created, err := s.CreateTasks(ctx, workspace.Default, []store.NewTask{nt("a"), nt("b"), nt(""), nt("b")}, true)
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	if len(tasks) != 4 || created[0] || !created[1] || !created[2] || created[3] {
		t.Fatalf("CreateTasks created = %v", created)
	}
	// Duplicates, including within the batch, return the first task.
	if tasks[0].ID != existing.ID || tasks[3].ID != tasks[1].ID || tasks[2].ID == tasks[1].ID {
		t.Fatalf("CreateTasks = %v", taskIDs(tasks))
	}

	// A batch is atomic: one task for a missing queue fails all of it.
	batch := []store.NewTask{nt("c"), {Queue: "missing", Method: "POST", URL: "http://example.test", RunAt: now()}}
	if _, _, err := s.CreateTasks(ctx, workspace.Default, batch, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateTasks with a missing queue err = %v, want ErrNotFound", err)
	}
	all, _, err := s.ListTasks(ctx, workspace.Default, store.TaskFilter{Queue: "emails", Limit: 10})
	if err != nil || len(all) != 3 {
		t.Fatalf("ListTasks after failed batch = %d tasks, %v; want 3", len(all), err)
	}
}

func testPauseResume(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})

	if _, err := s.SetQueuePaused(ctx, workspace.Default, "missing", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetQueuePaused(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := s.SetQueuePaused(ctx, otherWorkspace, "emails", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetQueuePaused from another workspace err = %v, want ErrNotFound", err)
	}
	q, err := s.SetQueuePaused(ctx, workspace.Default, "emails", true)
	if err != nil || !q.Paused {
		t.Fatalf("SetQueuePaused(true) = %+v, %v", q, err)
	}
	if due, _ := s.DueQueues(ctx, time.Now()); len(due) != 0 {
		t.Fatalf("DueQueues with the queue paused = %v", queueNames(due))
	}
	// A dispatcher that listed the queue before the pause claims nothing.
	if claimed, err := s.ClaimTasks(ctx, q, 10, time.Now(), time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("ClaimTasks on a paused queue = %d tasks, %v", len(claimed), err)
	}
	// Configuration and pausing are separate: PUT keeps the queue paused.
	if q, _, err := s.PutQueue(ctx, workspace.Default, "emails", store.DefaultQueueConfig()); err != nil || !q.Paused {
		t.Fatalf("PutQueue on a paused queue = %+v, %v", q, err)
	}

	leader, err := s.TryLead(ctx)
	if err != nil {
		t.Fatalf("TryLead: %v", err)
	}
	defer leader.Release()
	drain(leader)
	q, err = s.SetQueuePaused(ctx, workspace.Default, "emails", false)
	if err != nil || q.Paused {
		t.Fatalf("SetQueuePaused(false) = %+v, %v", q, err)
	}
	select {
	case <-leader.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("no wake-up after resuming a queue")
	}
	if due, _ := s.DueQueues(ctx, time.Now()); len(due) != 1 {
		t.Fatalf("DueQueues after resume = %v", queueNames(due))
	}
}

// drain discards pending wake-ups.
func drain(l store.Leader) {
	for {
		select {
		case <-l.Wake():
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func testCancelPendingTasks(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	running := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	claim(t, s, running)
	done := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	attempt(t, s, done, store.TaskSucceeded)
	due := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	scheduled := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: now().Add(time.Hour)})
	other := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "other"})
	otherWS := mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails"})

	n, err := s.CancelPendingTasks(ctx, workspace.Default, "emails")
	if err != nil || n != 2 {
		t.Fatalf("CancelPendingTasks = %d, %v; want 2 (the due and the scheduled task)", n, err)
	}
	for _, task := range []store.Task{due, scheduled} {
		if got := get(t, s, task); got.State != store.TaskCancelled || got.FinishedAt == nil {
			t.Fatalf("pending task after CancelPendingTasks = %+v, want cancelled and finished", got)
		}
	}
	for _, tt := range []struct {
		task store.Task
		want store.TaskState
	}{
		{running, store.TaskRunning}, {done, store.TaskSucceeded}, {other, store.TaskPending}, {otherWS, store.TaskPending},
	} {
		if got := get(t, s, tt.task); got.State != tt.want {
			t.Fatalf("task %s = %s, want %s", tt.task.ID, got.State, tt.want)
		}
	}

	if _, err := s.CancelPendingTasks(ctx, workspace.Default, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CancelPendingTasks(missing) err = %v, want ErrNotFound", err)
	}
}

func testCancelTask(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	if _, err := s.CancelTask(ctx, workspace.Default, "task_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CancelTask(missing) err = %v, want ErrNotFound", err)
	}

	pending := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: now().Add(time.Hour)})
	if _, err := s.CancelTask(ctx, otherWorkspace, pending.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CancelTask from another workspace err = %v, want ErrNotFound", err)
	}
	cancelled, err := s.CancelTask(ctx, workspace.Default, pending.ID)
	if err != nil || cancelled.State != store.TaskCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("CancelTask(pending) = %+v, %v", cancelled, err)
	}
	// Cancelling again is a no-op.
	again, err := s.CancelTask(ctx, workspace.Default, pending.ID)
	if err != nil || !again.FinishedAt.Equal(*cancelled.FinishedAt) {
		t.Fatalf("second CancelTask = %+v, %v", again, err)
	}
	if next, ok, _ := s.NextRunAt(ctx, time.Now()); ok {
		t.Fatalf("NextRunAt = %v, want none after cancelling the only task", next)
	}

	done := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	attempt(t, s, done, store.TaskSucceeded)
	got, err := s.CancelTask(ctx, workspace.Default, done.ID)
	if !errors.Is(err, store.ErrInvalidState) || got.State != store.TaskSucceeded {
		t.Fatalf("CancelTask(succeeded) = %q, %v; want ErrInvalidState", got.State, err)
	}

	// A running task is cancelled at once. Its in-flight attempt is still
	// recorded, but doesn't make it pending again.
	running := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	claimed := claim(t, s, running)
	if got, err := s.CancelTask(ctx, workspace.Default, running.ID); err != nil || got.State != store.TaskCancelled {
		t.Fatalf("CancelTask(running) = %q, %v", got.State, err)
	}
	if expired, _ := s.ExpiredLeases(ctx, time.Now().Add(time.Hour), 10); len(expired) != 1 || expired[0].ID != running.ID {
		t.Fatalf("ExpiredLeases = %v; a cancelled task's attempt is still leased", taskIDs(expired))
	}
	if state := finish(t, s, claimed, store.TaskPending); state != store.TaskCancelled {
		t.Fatalf("FinishAttempt on a cancelled task = %q, want cancelled", state)
	}
	got = get(t, s, running)
	attempts, _ := s.ListAttempts(ctx, workspace.Default, running.ID)
	if got.State != store.TaskCancelled || got.LeaseExpiresAt != nil || got.FinishedAt == nil || len(attempts) != 1 {
		t.Fatalf("cancelled running task = %+v with %d attempts", got, len(attempts))
	}
}

func testRetryTask(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	if _, err := s.RetryTask(ctx, workspace.Default, "task_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RetryTask(missing) err = %v, want ErrNotFound", err)
	}

	// A failed task is replayed with a fresh attempt budget.
	failed := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	attempt(t, s, failed, store.TaskPending)
	attempt(t, s, failed, store.TaskFailed)
	if _, err := s.RetryTask(ctx, otherWorkspace, failed.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RetryTask from another workspace err = %v, want ErrNotFound", err)
	}
	before := now()
	replayed, err := s.RetryTask(ctx, workspace.Default, failed.ID)
	if err != nil || replayed.State != store.TaskPending || replayed.FinishedAt != nil ||
		replayed.AttemptCount != 2 || replayed.ReplayedAttempts != 2 || replayed.AttemptsSinceReplay() != 0 ||
		replayed.RunAt.Before(before) {
		t.Fatalf("RetryTask(failed) = %+v, %v", replayed, err)
	}
	// Attempt numbers keep counting.
	if c := claim(t, s, failed); c.AttemptCount != 3 || c.AttemptsSinceReplay() != 1 {
		t.Fatalf("claimed replayed task = %+v", c)
	}
	// While an attempt is in flight it can't be retried, even if cancelled.
	if _, err := s.RetryTask(ctx, workspace.Default, failed.ID); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("RetryTask(running) err = %v, want ErrInvalidState", err)
	}
	if _, err := s.CancelTask(ctx, workspace.Default, failed.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if _, err := s.RetryTask(ctx, workspace.Default, failed.ID); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("RetryTask(cancelled mid-attempt) err = %v, want ErrInvalidState", err)
	}

	// A scheduled task runs now, keeping its budget.
	scheduled := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "later", RunAt: now().Add(time.Hour)})
	leader, err := s.TryLead(ctx)
	if err != nil {
		t.Fatalf("TryLead: %v", err)
	}
	defer leader.Release()
	drain(leader)
	got, err := s.RetryTask(ctx, workspace.Default, scheduled.ID)
	if err != nil || got.State != store.TaskPending || got.RunAt.After(time.Now()) || got.ReplayedAttempts != 0 {
		t.Fatalf("RetryTask(scheduled) = %+v, %v", got, err)
	}
	select {
	case <-leader.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("no wake-up after retrying a task")
	}
}

func testRetryFailedTasks(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	if _, err := s.RetryFailedTasks(ctx, workspace.Default, "missing", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RetryFailedTasks(missing) err = %v, want ErrNotFound", err)
	}

	var failed []store.Task
	for range 2 {
		task := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
		attempt(t, s, task, store.TaskPending)
		attempt(t, s, task, store.TaskFailed)
		failed = append(failed, task)
	}
	succeeded := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	attempt(t, s, succeeded, store.TaskSucceeded)
	scheduled := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: now().Add(time.Hour)})
	otherQueue := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "other"})
	attempt(t, s, otherQueue, store.TaskFailed)
	otherWS := mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails"})
	attempt(t, s, otherWS, store.TaskFailed)

	leader, err := s.TryLead(ctx)
	if err != nil {
		t.Fatalf("TryLead: %v", err)
	}
	defer leader.Release()
	drain(leader)

	before := now()
	n, err := s.RetryFailedTasks(ctx, workspace.Default, "emails", "")
	if err != nil || n != 2 {
		t.Fatalf("RetryFailedTasks = %d, %v; want 2", n, err)
	}
	for _, task := range failed {
		got := get(t, s, task)
		if got.State != store.TaskPending || got.FinishedAt != nil || got.AttemptsSinceReplay() != 0 ||
			got.AttemptCount != 2 || got.RunAt.Before(before) {
			t.Fatalf("retried task = %+v", got)
		}
	}
	select {
	case <-leader.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("no wake-up after retrying failed tasks")
	}

	// Other states, queues and workspaces are untouched.
	if got := get(t, s, succeeded); got.State != store.TaskSucceeded {
		t.Fatalf("succeeded task is now %q", got.State)
	}
	if got := get(t, s, scheduled); !got.RunAt.After(time.Now()) {
		t.Fatalf("scheduled task run_at = %v, want unchanged", got.RunAt)
	}
	if got := get(t, s, otherQueue); got.State != store.TaskFailed {
		t.Fatalf("task in another queue is now %q", got.State)
	}
	if got := get(t, s, otherWS); got.State != store.TaskFailed {
		t.Fatalf("task in another workspace is now %q", got.State)
	}
	if n, err := s.RetryFailedTasks(ctx, workspace.Default, "emails", ""); err != nil || n != 0 {
		t.Fatalf("second RetryFailedTasks = %d, %v; want 0", n, err)
	}
}

func testDeleteFinishedTasks(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	var finished []store.Task
	for range 3 {
		task := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
		attempt(t, s, task, store.TaskSucceeded)
		finished = append(finished, task)
	}
	failed := mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails"})
	attempt(t, s, failed, store.TaskFailed)
	cancelled := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: now().Add(time.Hour)})
	if _, err := s.CancelTask(ctx, workspace.Default, cancelled.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	pending := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "later", RunAt: now().Add(time.Hour)})
	inFlight := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "slow"})
	claim(t, s, inFlight)
	if _, err := s.CancelTask(ctx, workspace.Default, inFlight.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	if n, err := s.DeleteFinishedTasks(ctx, time.Now().Add(-time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("DeleteFinishedTasks(an hour ago) = %d, %v; want 0", n, err)
	}
	cutoff := time.Now().Add(time.Second)
	if n, err := s.DeleteFinishedTasks(ctx, cutoff, 2); err != nil || n != 2 {
		t.Fatalf("DeleteFinishedTasks(limit 2) = %d, %v; want 2", n, err)
	}
	if n, err := s.DeleteFinishedTasks(ctx, cutoff, 10); err != nil || n != 3 {
		t.Fatalf("second DeleteFinishedTasks = %d, %v; want 3", n, err)
	}
	for _, task := range append(finished, failed, cancelled) {
		if _, err := s.GetTask(ctx, task.WorkspaceID, task.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("finished task %s err = %v, want ErrNotFound", task.ID, err)
		}
		if a, _ := s.ListAttempts(ctx, task.WorkspaceID, task.ID); len(a) != 0 {
			t.Fatalf("finished task %s kept %d attempts", task.ID, len(a))
		}
	}
	// Unfinished tasks, and a cancelled task whose attempt is in flight, stay.
	get(t, s, pending)
	get(t, s, inFlight)
}

func testManualRetryHistory(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	// Timestamps have millisecond precision; keep a retry from landing in
	// the same millisecond as the attempt before it.
	pause := func() { time.Sleep(2 * time.Millisecond) }

	task := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	attempt(t, s, task, store.TaskFailed)
	pause()
	retried, err := s.RetryTask(ctx, workspace.Default, task.ID)
	if err != nil || retried.RetriedAt == nil {
		t.Fatalf("RetryTask = %+v, %v; want RetriedAt set", retried, err)
	}
	attempt(t, s, task, store.TaskPending)
	attempt(t, s, task, store.TaskFailed)
	pause()
	if n, err := s.RetryFailedTasks(ctx, workspace.Default, "emails", ""); n != 1 || err != nil {
		t.Fatalf("RetryFailedTasks = %d, %v", n, err)
	}
	again := get(t, s, task)
	if again.RetriedAt == nil || !again.RetriedAt.After(*retried.RetriedAt) {
		t.Fatalf("RetriedAt after RetryFailedTasks = %v, want after %v", again.RetriedAt, retried.RetriedAt)
	}
	attempt(t, s, task, store.TaskSucceeded)

	attempts, err := s.ListAttempts(ctx, workspace.Default, task.ID)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	// Attempts 2 and 4 followed manual retries.
	want := []*time.Time{nil, retried.RetriedAt, nil, again.RetriedAt}
	if len(attempts) != len(want) {
		t.Fatalf("ListAttempts = %d attempts, want %d", len(attempts), len(want))
	}
	for i, a := range attempts {
		if (a.RetriedAt == nil) != (want[i] == nil) || (a.RetriedAt != nil && !a.RetriedAt.Equal(*want[i])) {
			t.Errorf("attempt %d RetriedAt = %v, want %v", a.Number, a.RetriedAt, want[i])
		}
	}
}
