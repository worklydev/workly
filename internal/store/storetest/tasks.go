package storetest

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

const otherWorkspace workspace.ID = "other"

// seeded returns a migrated store with the default and "other" workspaces.
func seeded(t *testing.T, open func() store.Store) store.Store {
	t.Helper()
	s := migrated(t, open)
	for _, ws := range []workspace.ID{workspace.Default, otherWorkspace} {
		if _, err := s.EnsureWorkspace(t.Context(), ws, string(ws)); err != nil {
			t.Fatalf("EnsureWorkspace: %v", err)
		}
	}
	return s
}

// now is truncated to milliseconds, the precision both stores keep.
func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

func mustCreate(t *testing.T, s store.Store, ws workspace.ID, nt store.NewTask) store.Task {
	t.Helper()
	if nt.Method == "" {
		nt.Method = "POST"
	}
	if nt.URL == "" {
		nt.URL = "http://example.test/hook"
	}
	if nt.RunAt.IsZero() {
		nt.RunAt = now()
	}
	task, created, err := s.CreateTask(t.Context(), ws, nt, true)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !created {
		t.Fatalf("CreateTask: created = false")
	}
	return task
}

func testQueues(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	if _, err := s.GetQueue(ctx, workspace.Default, "emails"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetQueue(missing) err = %v, want ErrNotFound", err)
	}

	cfg := store.QueueConfig{
		MaxAttempts:       3,
		MinBackoff:        250 * time.Millisecond,
		MaxBackoff:        time.Minute,
		BackoffMultiplier: 1.5,
		Timeout:           10 * time.Second,
		RetryOn4xx:        true,
		RatePerSecond:     2.5,
		MaxConcurrency:    4,
	}
	q, created, err := s.PutQueue(ctx, workspace.Default, "emails", cfg)
	if err != nil {
		t.Fatalf("PutQueue: %v", err)
	}
	if !created || q.Name != "emails" || q.WorkspaceID != workspace.Default || q.QueueConfig != cfg || q.Paused {
		t.Fatalf("PutQueue = %+v, created %v", q, created)
	}

	cfg.MaxAttempts = 5
	cfg.RetryOn4xx = false
	cfg.RatePerSecond, cfg.MaxConcurrency = 0, 0 // unlimited
	updated, created, err := s.PutQueue(ctx, workspace.Default, "emails", cfg)
	if err != nil {
		t.Fatalf("PutQueue update: %v", err)
	}
	if created || updated.QueueConfig != cfg || !updated.CreatedAt.Equal(q.CreatedAt) {
		t.Fatalf("PutQueue update = %+v, created %v", updated, created)
	}

	if _, _, err := s.PutQueue(ctx, workspace.Default, "billing", store.DefaultQueueConfig()); err != nil {
		t.Fatalf("PutQueue: %v", err)
	}
	if _, _, err := s.PutQueue(ctx, otherWorkspace, "secret", store.DefaultQueueConfig()); err != nil {
		t.Fatalf("PutQueue: %v", err)
	}
	queues, err := s.ListQueues(ctx, workspace.Default)
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}
	if names := queueNames(queues); !reflect.DeepEqual(names, []string{"billing", "emails"}) {
		t.Fatalf("ListQueues = %v", names)
	}

	// Deleting a queue deletes its tasks.
	task := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	if err := s.DeleteQueue(ctx, workspace.Default, "emails"); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	if _, err := s.GetTask(ctx, workspace.Default, task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTask after DeleteQueue err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteQueue(ctx, workspace.Default, "emails"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second DeleteQueue err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteQueue(ctx, workspace.Default, "secret"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteQueue of another workspace's queue err = %v, want ErrNotFound", err)
	}
}

func queueNames(qs []store.Queue) []string {
	names := []string{}
	for _, q := range qs {
		names = append(names, q.Name)
	}
	return names
}

func testCreateTask(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	maxAttempts, timeout := 4, 90*time.Second
	runAt := now().Add(time.Hour)
	nt := store.NewTask{
		Queue:          "emails",
		Method:         "PUT",
		URL:            "https://api.example.test/send",
		Headers:        map[string]string{"Content-Type": "application/json", "X-Tenant": "acme"},
		Body:           []byte(`{"user_id":"123"}`),
		RunAt:          runAt,
		MaxAttempts:    &maxAttempts,
		Timeout:        &timeout,
		TraceContext:   "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		IdempotencyKey: "welcome:123",
	}
	before := time.Now().Add(-time.Second)
	task, created, err := s.CreateTask(ctx, workspace.Default, nt, true)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !created {
		t.Fatal("created = false")
	}
	want := store.Task{
		ID: task.ID, WorkspaceID: workspace.Default, Queue: "emails", State: store.TaskPending,
		Method: "PUT", URL: nt.URL, Headers: nt.Headers, Body: nt.Body, RunAt: runAt,
		MaxAttempts: &maxAttempts, Timeout: &timeout, IdempotencyKey: "welcome:123",
		TraceContext: nt.TraceContext, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
	if !reflect.DeepEqual(task, want) {
		t.Fatalf("CreateTask =\n %+v\nwant\n %+v", task, want)
	}
	if task.CreatedAt.Before(before) || task.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt = %v", task.CreatedAt)
	}

	// The queue was auto-created with defaults.
	q, err := s.GetQueue(ctx, workspace.Default, "emails")
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	if q.QueueConfig != store.DefaultQueueConfig() {
		t.Fatalf("auto-created queue config = %+v", q.QueueConfig)
	}

	got, err := s.GetTask(ctx, workspace.Default, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !reflect.DeepEqual(got, task) {
		t.Fatalf("GetTask =\n %+v\nwant\n %+v", got, task)
	}
	if _, err := s.GetTask(ctx, otherWorkspace, task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTask from another workspace err = %v, want ErrNotFound", err)
	}

	// Without auto-create, a missing queue is an error.
	if _, _, err := s.CreateTask(ctx, workspace.Default, store.NewTask{Queue: "nope", Method: "POST", URL: nt.URL, RunAt: runAt}, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateTask without queue err = %v, want ErrNotFound", err)
	}

	// Minimal task: no headers, body or overrides.
	minimal := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	if minimal.Headers == nil || len(minimal.Headers) != 0 || minimal.Body == nil || len(minimal.Body) != 0 {
		t.Fatalf("minimal task headers %v body %v, want empty non-nil", minimal.Headers, minimal.Body)
	}
	if minimal.MaxAttempts != nil || minimal.Timeout != nil || minimal.IdempotencyKey != "" {
		t.Fatalf("minimal task = %+v", minimal)
	}
}

func testIdempotency(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	first := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", IdempotencyKey: "k1"})
	dup, created, err := s.CreateTask(ctx, workspace.Default,
		store.NewTask{Queue: "emails", Method: "POST", URL: "http://other.test", RunAt: now(), IdempotencyKey: "k1"}, true)
	if err != nil {
		t.Fatalf("CreateTask duplicate: %v", err)
	}
	if created || dup.ID != first.ID || dup.URL != first.URL {
		t.Fatalf("duplicate = %+v, created %v; want existing %s", dup, created, first.ID)
	}

	// Keys are scoped to the queue and the workspace.
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing", IdempotencyKey: "k1"})
	mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails", IdempotencyKey: "k1"})
}

func testListTasks(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	var ids []string
	for range 5 {
		ids = append(ids, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"}).ID)
	}
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing"})
	mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails"})

	var got []string
	cursor := ""
	for page := 0; ; page++ {
		tasks, next, err := s.ListTasks(ctx, workspace.Default, store.TaskFilter{Queue: "emails", Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		for _, task := range tasks {
			got = append(got, task.ID)
		}
		if next == "" {
			break
		}
		if page > 5 {
			t.Fatal("ListTasks did not terminate")
		}
		cursor = next
	}
	// Newest first. IDs created in the same millisecond have random order,
	// so compare as sets plus check the order is descending.
	if len(got) != len(ids) {
		t.Fatalf("ListTasks returned %d tasks, want %d", len(got), len(ids))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] <= got[i] {
			t.Fatalf("ListTasks not newest first: %v", got)
		}
	}

	tasks, _, err := s.ListTasks(ctx, workspace.Default, store.TaskFilter{Queue: "emails", State: store.TaskFailed, Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks(failed): %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("ListTasks(failed) = %d tasks, want 0", len(tasks))
	}
}

func testClaimAndFinish(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	start := now()

	later := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: start.Add(-time.Second)})
	earlier := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: start.Add(-2 * time.Second)})
	timeout := 5 * time.Second
	third := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: start, Timeout: &timeout})
	future := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "scheduled", RunAt: start.Add(time.Hour)})
	mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails", RunAt: start})

	due, err := s.DueQueues(ctx, start)
	if err != nil {
		t.Fatalf("DueQueues: %v", err)
	}
	if len(due) != 2 || due[0].WorkspaceID != workspace.Default || due[0].Name != "emails" || due[1].WorkspaceID != otherWorkspace {
		t.Fatalf("DueQueues = %+v", due)
	}
	next, ok, err := s.NextRunAt(ctx, start.Add(-time.Hour))
	if err != nil || !ok || !next.Equal(earlier.RunAt) {
		t.Fatalf("NextRunAt = %v, %v, %v; want %v", next, ok, err, earlier.RunAt)
	}
	// Only run times after the given time count: due tasks are the
	// dispatcher's business, not its timer's.
	next, ok, err = s.NextRunAt(ctx, start)
	if err != nil || !ok || !next.Equal(future.RunAt) {
		t.Fatalf("NextRunAt(now) = %v, %v, %v; want %v", next, ok, err, future.RunAt)
	}
	if _, ok, err := s.NextRunAt(ctx, future.RunAt); ok || err != nil {
		t.Fatalf("NextRunAt(last) ok = %v, err = %v; want none", ok, err)
	}

	q := due[0]
	grace := 10 * time.Second
	claimed, err := s.ClaimTasks(ctx, q, 2, start, grace)
	if err != nil {
		t.Fatalf("ClaimTasks: %v", err)
	}
	if len(claimed) != 2 || claimed[0].ID != earlier.ID || claimed[1].ID != later.ID {
		t.Fatalf("ClaimTasks = %v, want [%s %s] (run_at order)", taskIDs(claimed), earlier.ID, later.ID)
	}
	for _, c := range claimed {
		if c.State != store.TaskRunning || c.AttemptCount != 1 || c.LeaseExpiresAt == nil ||
			!c.LeaseExpiresAt.Equal(start.Add(q.Timeout+grace)) {
			t.Fatalf("claimed task = %+v", c)
		}
		stored, err := s.GetTask(ctx, workspace.Default, c.ID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if !reflect.DeepEqual(stored, c) {
			t.Fatalf("stored claimed task =\n %+v\nwant\n %+v", stored, c)
		}
	}

	rest, err := s.ClaimTasks(ctx, q, 10, start, grace)
	if err != nil {
		t.Fatalf("ClaimTasks: %v", err)
	}
	if len(rest) != 1 || rest[0].ID != third.ID || !rest[0].LeaseExpiresAt.Equal(start.Add(timeout+grace)) {
		t.Fatalf("second ClaimTasks = %+v, want %s with its own timeout", rest, third.ID)
	}
	if more, _ := s.ClaimTasks(ctx, q, 10, start, grace); len(more) != 0 {
		t.Fatalf("third ClaimTasks = %v, want none", taskIDs(more))
	}

	// Success.
	status := 200
	finished := start.Add(150 * time.Millisecond)
	state, err := s.FinishAttempt(ctx, workspace.Default, store.AttemptResult{
		Attempt: store.Attempt{
			TaskID: earlier.ID, Number: 1, StartedAt: start, FinishedAt: finished,
			StatusCode: &status, Outcome: store.OutcomeSuccess, ResponseBody: "ok",
			ResponseHeaders: map[string]string{"Content-Type": "text/plain"},
		},
		NextState: store.TaskSucceeded,
	})
	if err != nil || state != store.TaskSucceeded {
		t.Fatalf("FinishAttempt(success) = %q, %v", state, err)
	}
	done, _ := s.GetTask(ctx, workspace.Default, earlier.ID)
	if done.State != store.TaskSucceeded || done.FinishedAt == nil || !done.FinishedAt.Equal(finished) ||
		done.LeaseExpiresAt != nil || done.LastStatus == nil || *done.LastStatus != 200 ||
		done.LastAttemptAt == nil || !done.LastAttemptAt.Equal(finished) {
		t.Fatalf("succeeded task = %+v", done)
	}
	attempts, err := s.ListAttempts(ctx, workspace.Default, earlier.ID)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("ListAttempts = %d attempts, want 1", len(attempts))
	}
	a := attempts[0]
	if a.Number != 1 || a.Outcome != store.OutcomeSuccess || *a.StatusCode != 200 || a.ResponseBody != "ok" ||
		a.ResponseHeaders["Content-Type"] != "text/plain" || a.Duration() != 150*time.Millisecond || a.ID == "" {
		t.Fatalf("attempt = %+v", a)
	}
	if other, _ := s.ListAttempts(ctx, otherWorkspace, earlier.ID); len(other) != 0 {
		t.Fatalf("ListAttempts from another workspace = %d attempts", len(other))
	}

	// Retryable failure goes back to pending at the retry time. Response
	// text that Postgres can't store as-is (NUL, invalid UTF-8) is cleaned.
	retryAt := start.Add(30 * time.Second)
	state, err = s.FinishAttempt(ctx, workspace.Default, store.AttemptResult{
		Attempt: store.Attempt{
			TaskID: later.ID, Number: 1, StartedAt: start, FinishedAt: finished,
			Outcome: store.OutcomeTimeout, Error: "timed out", ResponseBody: "bad\x00\xffbytes",
		},
		NextState: store.TaskPending,
		RunAt:     retryAt,
	})
	if err != nil || state != store.TaskPending {
		t.Fatalf("FinishAttempt(retry) = %q, %v", state, err)
	}
	retried, _ := s.GetTask(ctx, workspace.Default, later.ID)
	if retried.State != store.TaskPending || !retried.RunAt.Equal(retryAt) || retried.LastError != "timed out" ||
		retried.LastStatus != nil || retried.FinishedAt != nil || retried.AttemptCount != 1 ||
		retried.LastAttemptAt == nil || !retried.LastAttemptAt.Equal(finished) {
		t.Fatalf("retried task = %+v", retried)
	}
	attempts, _ = s.ListAttempts(ctx, workspace.Default, later.ID)
	if len(attempts) != 1 || attempts[0].StatusCode != nil || attempts[0].ResponseBody != "bad�bytes" {
		t.Fatalf("retry attempts = %+v", attempts)
	}

	// A stale result (wrong attempt number, or task not running) is rejected
	// and writes nothing.
	for _, r := range []store.AttemptResult{
		{Attempt: store.Attempt{TaskID: third.ID, Number: 2}, NextState: store.TaskSucceeded},
		{Attempt: store.Attempt{TaskID: earlier.ID, Number: 1}, NextState: store.TaskFailed},
		{Attempt: store.Attempt{TaskID: future.ID, Number: 0}, NextState: store.TaskFailed},
	} {
		r.StartedAt, r.FinishedAt, r.Outcome = start, finished, store.OutcomeSuccess
		if _, err := s.FinishAttempt(ctx, workspace.Default, r); !errors.Is(err, store.ErrLeaseLost) {
			t.Fatalf("FinishAttempt(%s #%d) err = %v, want ErrLeaseLost", r.TaskID, r.Number, err)
		}
	}
	if attempts, _ := s.ListAttempts(ctx, workspace.Default, third.ID); len(attempts) != 0 {
		t.Fatalf("stale FinishAttempt wrote %d attempts", len(attempts))
	}
	// Another workspace can't finish our task.
	_, err = s.FinishAttempt(ctx, otherWorkspace, store.AttemptResult{
		Attempt:   store.Attempt{TaskID: third.ID, Number: 1, StartedAt: start, FinishedAt: finished, Outcome: store.OutcomeSuccess},
		NextState: store.TaskSucceeded,
	})
	if !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("FinishAttempt from another workspace err = %v, want ErrLeaseLost", err)
	}

	// third is still running; its lease expires at start+timeout+grace.
	expired, err := s.ExpiredLeases(ctx, start.Add(timeout+grace-time.Millisecond), 10)
	if err != nil || len(expired) != 0 {
		t.Fatalf("ExpiredLeases before expiry = %v, %v", taskIDs(expired), err)
	}
	expired, err = s.ExpiredLeases(ctx, start.Add(timeout+grace+time.Millisecond), 10)
	if err != nil || len(expired) != 1 || expired[0].ID != third.ID {
		t.Fatalf("ExpiredLeases = %v, %v; want [%s]", taskIDs(expired), err, third.ID)
	}
}

// testFinishAttempts records many results in one call: more than one
// statement holds, across workspaces, with stale and cancelled tasks mixed
// in.
func testFinishAttempts(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	const bulk = 150
	for range bulk {
		mustCreate(t, s, workspace.Default, store.NewTask{Queue: "bulk"})
	}
	q, err := s.GetQueue(ctx, workspace.Default, "bulk")
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	claimed, err := s.ClaimTasks(ctx, q, bulk, time.Now(), time.Minute)
	if err != nil || len(claimed) != bulk {
		t.Fatalf("ClaimTasks = %d tasks, %v", len(claimed), err)
	}
	other := claim(t, s, mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "bulk"}))
	cancelled := claim(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "cancelled"}))
	if _, err := s.CancelTask(ctx, workspace.Default, cancelled.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	status := 200
	result := func(ws workspace.ID, task store.Task, number int, next store.TaskState) store.FinishedAttempt {
		return store.FinishedAttempt{Workspace: ws, AttemptResult: store.AttemptResult{
			Attempt: store.Attempt{
				TaskID: task.ID, Number: number, StartedAt: now(), FinishedAt: now(),
				StatusCode: &status, Outcome: store.OutcomeSuccess, ResponseBody: "ok",
			},
			NextState: next,
			RunAt:     now().Add(time.Minute),
		}}
	}
	var rs []store.FinishedAttempt
	for i, c := range claimed {
		next := store.TaskSucceeded
		if i%2 == 1 {
			next = store.TaskPending
		}
		rs = append(rs, result(workspace.Default, c, 1, next))
	}
	stale := result(workspace.Default, claimed[0], 2, store.TaskFailed)
	stale.TaskID = mustCreate(t, s, workspace.Default, store.NewTask{Queue: "idle"}).ID
	rs = append(rs,
		result(otherWorkspace, other, 1, store.TaskSucceeded),
		result(workspace.Default, cancelled, 1, store.TaskPending),
		stale, // not running
	)

	out, err := s.FinishAttempts(ctx, rs)
	if err != nil || len(out) != len(rs) {
		t.Fatalf("FinishAttempts = %d outcomes, %v", len(out), err)
	}
	for i, o := range out[:bulk] {
		if o.Err != nil || o.State != rs[i].NextState {
			t.Fatalf("outcome %d = %+v, want %q", i, o, rs[i].NextState)
		}
		got := get(t, s, claimed[i])
		attempts, _ := s.ListAttempts(ctx, workspace.Default, claimed[i].ID)
		if got.State != rs[i].NextState || got.LeaseExpiresAt != nil || len(attempts) != 1 || attempts[0].ResponseBody != "ok" {
			t.Fatalf("task %d = %+v with %d attempts", i, got, len(attempts))
		}
	}
	if o := out[bulk]; o.Err != nil || o.State != store.TaskSucceeded || get(t, s, other).State != store.TaskSucceeded {
		t.Fatalf("other workspace's outcome = %+v", o)
	}
	if o := out[bulk+1]; o.Err != nil || o.State != store.TaskCancelled {
		t.Fatalf("cancelled task's outcome = %+v, want cancelled", o)
	}
	if o := out[bulk+2]; !errors.Is(o.Err, store.ErrLeaseLost) {
		t.Fatalf("stale outcome = %+v, want ErrLeaseLost", o)
	}
	if attempts, _ := s.ListAttempts(ctx, workspace.Default, stale.TaskID); len(attempts) != 0 {
		t.Fatalf("stale result wrote %d attempts", len(attempts))
	}

	// Results are written all or nothing.
	running := claim(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "atomic"}))
	bad := result(workspace.Default, running, 1, store.TaskSucceeded)
	bad.NextState = store.TaskRunning
	if _, err := s.FinishAttempts(ctx, []store.FinishedAttempt{result(workspace.Default, running, 1, store.TaskSucceeded), bad}); err == nil {
		t.Fatal("FinishAttempts with an invalid next state succeeded")
	}
	if got := get(t, s, running); got.State != store.TaskRunning {
		t.Fatalf("task after a failed FinishAttempts = %q, want running", got.State)
	}
}

func taskIDs(tasks []store.Task) []string {
	ids := []string{}
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func testLeaderWake(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	leader, err := s.TryLead(t.Context())
	if err != nil {
		t.Fatalf("TryLead: %v", err)
	}
	defer leader.Release()

	// Both due and scheduled tasks wake the dispatcher: a scheduled one may
	// be due sooner than the dispatcher's current timer.
	for _, runAt := range []time.Time{now(), now().Add(time.Hour)} {
		mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: runAt})
		select {
		case <-leader.Wake():
		case <-time.After(5 * time.Second):
			t.Fatalf("no wake-up after enqueueing a task for %v", runAt)
		}
	}

	// A duplicate (idempotent) enqueue creates nothing and needn't wake it.
	nt := store.NewTask{Queue: "emails", Method: "POST", URL: "http://example.test", RunAt: now(), IdempotencyKey: "k"}
	if _, _, err := s.CreateTask(t.Context(), workspace.Default, nt, true); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	<-leader.Wake()
	if _, _, err := s.CreateTask(t.Context(), workspace.Default, nt, true); err != nil {
		t.Fatalf("CreateTask duplicate: %v", err)
	}
	select {
	case <-leader.Wake():
		t.Fatal("woken by a duplicate enqueue")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-leader.Lost():
		t.Fatal("leadership lost")
	default:
	}
}
