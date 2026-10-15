package storetest

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

func testQueueStats(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	start := now()
	oldest := start.Add(-time.Minute)
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: oldest})
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: start.Add(-time.Second)})
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: start.Add(time.Hour)})
	done := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing"})
	attempt(t, s, done, store.TaskSucceeded)
	claim(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing"}))
	attempt(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "broken"}), store.TaskFailed)
	if _, _, err := s.PutQueue(ctx, workspace.Default, "idle", store.DefaultQueueConfig()); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails", RunAt: start.Add(time.Hour)})

	stats, err := s.QueueStats(ctx, workspace.Default, "", start)
	if err != nil {
		t.Fatalf("QueueStats: %v", err)
	}
	want := map[string]store.QueueStats{
		"emails":  {Due: 2, Scheduled: 1, OldestDueAt: &oldest},
		"billing": {Running: 1},
		"broken":  {Failed: 1},
		"idle":    {},
	}
	if !reflect.DeepEqual(stats, want) {
		t.Fatalf("QueueStats = %+v, want %+v", stats, want)
	}

	one, err := s.QueueStats(ctx, workspace.Default, "billing", start)
	if err != nil || !reflect.DeepEqual(one, map[string]store.QueueStats{"billing": {Running: 1}}) {
		t.Fatalf("QueueStats(billing) = %+v, %v", one, err)
	}
	if missing, err := s.QueueStats(ctx, workspace.Default, "missing", start); err != nil || len(missing) != 0 {
		t.Fatalf("QueueStats(missing) = %+v, %v", missing, err)
	}

	if _, err := s.SetQueuePaused(ctx, workspace.Default, "idle", true); err != nil {
		t.Fatal(err)
	}
	all, err := s.AllQueueStats(ctx, start)
	if err != nil {
		t.Fatalf("AllQueueStats: %v", err)
	}
	wantAll := []store.QueueStatus{
		{WorkspaceID: workspace.Default, Queue: "billing", QueueStats: store.QueueStats{Running: 1}},
		{WorkspaceID: workspace.Default, Queue: "broken", QueueStats: store.QueueStats{Failed: 1}},
		{WorkspaceID: workspace.Default, Queue: "emails", QueueStats: want["emails"]},
		{WorkspaceID: workspace.Default, Queue: "idle", Paused: true},
		{WorkspaceID: otherWorkspace, Queue: "emails", QueueStats: store.QueueStats{Scheduled: 1}},
	}
	if !reflect.DeepEqual(all, wantAll) {
		t.Fatalf("AllQueueStats = %+v, want %+v", all, wantAll)
	}
}

func testQueueStatsCap(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	nts := make([]store.NewTask, store.StatsCountLimit+5)
	for i := range nts {
		nts[i] = store.NewTask{Queue: "big", Method: "POST", URL: "http://example.test", RunAt: now()}
	}
	for chunk := range slices.Chunk(nts, 1000) {
		if _, _, err := s.CreateTasks(t.Context(), workspace.Default, chunk, true); err != nil {
			t.Fatalf("CreateTasks: %v", err)
		}
	}
	stats, err := s.QueueStats(t.Context(), workspace.Default, "big", now().Add(time.Second))
	if err != nil || stats["big"].Due != store.StatsCountLimit {
		t.Fatalf("QueueStats = %+v, %v; want Due capped at %d", stats, err, store.StatsCountLimit)
	}
}

func testSearchTasks(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	early := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", IdempotencyKey: "welcome-42"})
	time.Sleep(5 * time.Millisecond)
	mid := now()
	billing := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing", IdempotencyKey: "welcome-42"})
	// IDs order by millisecond; keep the expected order deterministic.
	time.Sleep(2 * time.Millisecond)
	failed := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "webhooks"})
	attempt(t, s, failed, store.TaskFailed)
	mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails", IdempotencyKey: "welcome-42"})

	search := func(f store.TaskFilter) []string {
		t.Helper()
		f.Limit = 10
		tasks, _, err := s.ListTasks(ctx, workspace.Default, f)
		if err != nil {
			t.Fatalf("ListTasks(%+v): %v", f, err)
		}
		return taskIDs(tasks)
	}
	tests := []struct {
		name string
		f    store.TaskFilter
		want []string
	}{
		{"all queues", store.TaskFilter{}, []string{failed.ID, billing.ID, early.ID}},
		{"idempotency key", store.TaskFilter{IdempotencyKey: "welcome-42"}, []string{billing.ID, early.ID}},
		{"key and queue", store.TaskFilter{IdempotencyKey: "welcome-42", Queue: "emails"}, []string{early.ID}},
		{"state", store.TaskFilter{State: store.TaskFailed}, []string{failed.ID}},
		{"created after", store.TaskFilter{CreatedAfter: mid}, []string{failed.ID, billing.ID}},
		{"created before", store.TaskFilter{CreatedBefore: mid}, []string{early.ID}},
		{"no match", store.TaskFilter{IdempotencyKey: "nope"}, []string{}},
	}
	for _, tt := range tests {
		if got := search(tt.f); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func testQueueActivity(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	start := now().Add(-time.Second)

	for range 2 {
		attempt(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"}), store.TaskSucceeded)
	}
	attempt(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"}), store.TaskFailed)
	cancelled := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: now().Add(time.Hour)})
	if _, err := s.CancelTask(ctx, workspace.Default, cancelled.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	// Unfinished, in another queue or in another workspace: not counted.
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails", RunAt: now().Add(time.Hour)})
	attempt(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing"}), store.TaskSucceeded)
	attempt(t, s, mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails"}), store.TaskSucceeded)

	got, err := s.QueueActivity(ctx, workspace.Default, "emails", start)
	if want := (store.QueueActivity{Succeeded: 2, Failed: 1, Cancelled: 1}); err != nil || got != want {
		t.Fatalf("QueueActivity = %+v, %v; want %+v", got, err, want)
	}
	if got, err := s.QueueActivity(ctx, workspace.Default, "emails", now().Add(time.Minute)); err != nil || got != (store.QueueActivity{}) {
		t.Fatalf("QueueActivity since the future = %+v, %v; want zero", got, err)
	}
}

func testTaskActivity(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	start := now().Add(-time.Second)

	for range 3 {
		mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	}
	mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing"})
	// Another workspace's tasks aren't counted.
	mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails"})

	// One bucket spanning far into the past and future holds them all.
	day := 24 * time.Hour
	got, err := s.TaskActivity(ctx, workspace.Default, start, day)
	if err != nil {
		t.Fatalf("TaskActivity: %v", err)
	}
	bucket := now().Truncate(day)
	want := []store.ActivityCount{{Queue: "billing", Start: bucket, Count: 1}, {Queue: "emails", Start: bucket, Count: 3}}
	// A test running across midnight UTC may split them; that's rare enough to ignore.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TaskActivity = %+v, want %+v", got, want)
	}
	if got, err := s.TaskActivity(ctx, workspace.Default, now().Add(time.Minute), day); err != nil || len(got) != 0 {
		t.Fatalf("TaskActivity since the future = %+v, %v; want none", got, err)
	}
}

func testQueueFailures(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	fail := func(outcome store.Outcome, status int, errText, body string) store.Task {
		task := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
		failWith(t, s, task, outcome, status, errText, body)
		time.Sleep(2 * time.Millisecond) // distinct finish times, so "latest" is well defined
		return task
	}
	fail(store.OutcomeHTTPError, 503, "HTTP 503 Service Unavailable", "down")
	fail(store.OutcomeHTTPError, 503, "HTTP 503 Service Unavailable", "down")
	latest503 := fail(store.OutcomeHTTPError, 503, "HTTP 503 Service Unavailable", strings.Repeat("é", 400))
	timeout := fail(store.OutcomeTimeout, 0, "no response within 30s", "")
	// Not failed, in another queue or in another workspace: not grouped.
	attempt(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"}), store.TaskSucceeded)
	failWith(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing"}),
		store.OutcomeTimeout, 0, "no response within 30s", "")
	failWith(t, s, mustCreate(t, s, otherWorkspace, store.NewTask{Queue: "emails"}),
		store.OutcomeTimeout, 0, "no response within 30s", "")

	if got := get(t, s, timeout); got.LastOutcome != store.OutcomeTimeout {
		t.Fatalf("LastOutcome = %q, want timeout", got.LastOutcome)
	}

	groups, err := s.QueueFailures(ctx, workspace.Default, "emails", 10)
	if err != nil || len(groups) != 2 {
		t.Fatalf("QueueFailures = %+v, %v; want 2 groups", groups, err)
	}
	g := groups[0]
	if g.Error != "HTTP 503 Service Unavailable" || g.Count != 3 || g.Outcome != store.OutcomeHTTPError ||
		g.StatusCode == nil || *g.StatusCode != 503 || g.LatestTaskID != latest503.ID || g.LastFailed.IsZero() {
		t.Fatalf("most common group = %+v", g)
	}
	if len(g.ResponseBody) > 500 || !utf8.ValidString(g.ResponseBody) || !strings.HasPrefix(g.ResponseBody, "é") {
		t.Fatalf("response body = %d bytes, valid UTF-8 %v; want at most 500, cut on a character",
			len(g.ResponseBody), utf8.ValidString(g.ResponseBody))
	}
	g = groups[1]
	if g.Error != "no response within 30s" || g.Count != 1 || g.Outcome != store.OutcomeTimeout ||
		g.StatusCode != nil || g.LatestTaskID != timeout.ID || g.ResponseBody != "" {
		t.Fatalf("second group = %+v", g)
	}

	if groups, err := s.QueueFailures(ctx, workspace.Default, "emails", 1); err != nil || len(groups) != 1 {
		t.Fatalf("QueueFailures(limit 1) = %d groups, %v", len(groups), err)
	}
	if groups, err := s.QueueFailures(ctx, workspace.Default, "missing", 10); err != nil || len(groups) != 0 {
		t.Fatalf("QueueFailures(missing) = %+v, %v; want none", groups, err)
	}
}

func testRetryFailedByError(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	var down []store.Task
	for range 2 {
		task := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
		failWith(t, s, task, store.OutcomeHTTPError, 503, "HTTP 503 Service Unavailable", "")
		down = append(down, task)
	}
	bad := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	failWith(t, s, bad, store.OutcomeHTTPError, 422, "HTTP 422 Unprocessable Entity", "")

	listed, _, err := s.ListTasks(ctx, workspace.Default, store.TaskFilter{
		Queue: "emails", LastError: "HTTP 503 Service Unavailable", Limit: 10,
	})
	if err != nil || !reflect.DeepEqual(taskIDs(listed), []string{down[1].ID, down[0].ID}) {
		t.Fatalf("ListTasks(last error) = %v, %v; want %v", taskIDs(listed), err, taskIDs(down))
	}

	n, err := s.RetryFailedTasks(ctx, workspace.Default, "emails", "HTTP 503 Service Unavailable")
	if err != nil || n != 2 {
		t.Fatalf("RetryFailedTasks(last error) = %d, %v; want 2", n, err)
	}
	for _, task := range down {
		if got := get(t, s, task); got.State != store.TaskPending {
			t.Fatalf("retried task = %s, want pending", got.State)
		}
	}
	if got := get(t, s, bad); got.State != store.TaskFailed {
		t.Fatalf("task with another error = %s, want still failed", got.State)
	}
}
