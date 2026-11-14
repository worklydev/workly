// Package storetest is the conformance suite every store.Store implementation
// must pass. It is what keeps SQLite (`workly dev`) and Postgres (production)
// behaving identically.
package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// Factory creates a fresh, empty database for one test and returns a function
// that opens a new Store handle on it. Handles are closed by the factory via
// t.Cleanup.
type Factory func(t *testing.T) (open func() store.Store)

// Run executes the full conformance suite.
func Run(t *testing.T, factory Factory) {
	tests := []struct {
		name string
		fn   func(t *testing.T, open func() store.Store)
	}{
		{"Ping", testPing},
		{"MigrateIsIdempotent", testMigrateIsIdempotent},
		{"ConcurrentMigrate", testConcurrentMigrate},
		{"Workspaces", testWorkspaces},
		{"DeleteWorkspace", testDeleteWorkspace},
		{"Queues", testQueues},
		{"CreateTask", testCreateTask},
		{"Idempotency", testIdempotency},
		{"ListTasks", testListTasks},
		{"ListTasksSorted", testListTasksSorted},
		{"ClaimAndFinish", testClaimAndFinish},
		{"FinishAttempts", testFinishAttempts},
		{"LeaderWake", testLeaderWake},
		{"CreateTasks", testCreateTasks},
		{"ConcurrentEnqueues", testConcurrentEnqueues},
		{"PauseResume", testPauseResume},
		{"CancelPendingTasks", testCancelPendingTasks},
		{"CancelTask", testCancelTask},
		{"RetryTask", testRetryTask},
		{"ManualRetryHistory", testManualRetryHistory},
		{"RetryFailedTasks", testRetryFailedTasks},
		{"DeleteFinishedTasks", testDeleteFinishedTasks},
		{"SweepWorkspaces", testSweepWorkspaces},
		{"QueueStats", testQueueStats},
		{"QueueStatsCap", testQueueStatsCap},
		{"QueueActivity", testQueueActivity},
		{"TaskActivity", testTaskActivity},
		{"FinishedActivity", testFinishedActivity},
		{"QueueFailures", testQueueFailures},
		{"RetryFailedByError", testRetryFailedByError},
		{"SearchTasks", testSearchTasks},
		{"Schedules", testSchedules},
		{"FireSchedule", testFireSchedule},
		{"RunLifecycle", testRunLifecycle},
		{"RunLeases", testRunLeases},
		{"TriggerSchedule", testTriggerSchedule},
		{"NextScheduleWork", testNextScheduleWork},
		{"DeleteFinishedRuns", testDeleteFinishedRuns},
		{"ScheduleStatus", testScheduleStatus},
		{"ScheduleWake", testScheduleWake},
		{"TaskTimings", testTaskTimings},
		{"RunTimings", testRunTimings},
		{"ListRunsAllSchedules", testListRunsAllSchedules},
		{"SchedulePausedAt", testSchedulePausedAt},
		{"AlertChannels", testAlertChannels},
		{"FailuresToAlert", testFailuresToAlert},
		{"Changes", testChanges},
		{"Groups", testGroups},
		{"GroupLifecycle", testGroupLifecycle},
		{"GroupControls", testGroupControls},
		{"GroupDeleteQueue", testGroupDeleteQueue},
		{"GroupConcurrency", testGroupConcurrency},
		{"DeleteFinishedGroups", testDeleteFinishedGroups},
		{"TaskCallbacks", testTaskCallbacks},
		{"RunCallbacks", testRunCallbacks},
		{"Workflows", testWorkflows},
		{"WorkflowOrder", testWorkflowOrder},
		{"WorkflowFailure", testWorkflowFailure},
		{"RetryWorkflowRun", testRetryWorkflowRun},
		{"RetryWorkflowRunWaitFor", testRetryWorkflowRunWaitFor},
		{"WorkflowQueues", testWorkflowQueues},
		{"WorkflowWaitFor", testWorkflowWaitFor},
		{"WorkflowWaitRace", testWorkflowWaitRace},
		{"WorkflowSnapshot", testWorkflowSnapshot},
		{"ScheduledWorkflowOverlap", testScheduledWorkflowOverlap},
		{"ExpireWorkflowRuns", testExpireWorkflowRuns},
		{"DeleteFinishedWorkflowRuns", testDeleteFinishedWorkflowRuns},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.fn(t, factory(t))
		})
	}
}

func migrated(t *testing.T, open func() store.Store) store.Store {
	t.Helper()
	s := open()
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func testPing(t *testing.T, open func() store.Store) {
	if err := open().Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func testMigrateIsIdempotent(t *testing.T, open func() store.Store) {
	s := migrated(t, open)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	pending, err := s.MigrationsPending(t.Context())
	if err != nil {
		t.Fatalf("MigrationsPending: %v", err)
	}
	if pending {
		t.Fatal("MigrationsPending = true after Migrate")
	}
}

// Several replicas starting at once must not race each other's migrations.
func testConcurrentMigrate(t *testing.T, open func() store.Store) {
	const n = 4
	stores := make([]store.Store, n)
	for i := range stores {
		stores[i] = open()
	}
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i, s := range stores {
		wg.Go(func() { errs[i] = s.Migrate(context.Background()) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Migrate[%d]: %v", i, err)
		}
	}
}

func testWorkspaces(t *testing.T, open func() store.Store) {
	s := migrated(t, open)
	ctx := t.Context()

	if _, err := s.GetWorkspace(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetWorkspace(missing) err = %v, want ErrNotFound", err)
	}

	before := time.Now().Add(-time.Second)
	w, err := s.EnsureWorkspace(ctx, workspace.Default, "Default")
	if err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	if w.ID != workspace.Default || w.Name != "Default" {
		t.Fatalf("EnsureWorkspace = %+v", w)
	}
	if w.CreatedAt.Before(before) || w.CreatedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("CreatedAt = %v, want ~now", w.CreatedAt)
	}
	if w.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt location = %v, want UTC", w.CreatedAt.Location())
	}

	// A second call returns the existing workspace unchanged.
	again, err := s.EnsureWorkspace(ctx, workspace.Default, "Renamed")
	if err != nil {
		t.Fatalf("second EnsureWorkspace: %v", err)
	}
	if again.Name != "Default" || !again.CreatedAt.Equal(w.CreatedAt) {
		t.Fatalf("second EnsureWorkspace = %+v, want %+v", again, w)
	}

	got, err := s.GetWorkspace(ctx, workspace.Default)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got != again {
		t.Fatalf("GetWorkspace = %+v, want %+v", got, again)
	}
}

func testDeleteWorkspace(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	for _, ws := range []workspace.ID{workspace.Default, otherWorkspace} {
		task := mustCreate(t, s, ws, store.NewTask{Queue: "emails"})
		attempt(t, s, task, store.TaskFailed)
		mustCreate(t, s, ws, store.NewTask{Queue: "emails"})
		mustCreateGroup(t, s, ws, store.NewGroup{})
		mustPutSchedule(t, s, ws, "nightly", now().Add(time.Hour))
	}
	running := claim(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "reports"}))

	if err := s.DeleteWorkspace(ctx, workspace.Default); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if _, err := s.GetWorkspace(ctx, workspace.Default); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetWorkspace after delete: err = %v, want ErrNotFound", err)
	}
	if qs, err := s.ListQueues(ctx, workspace.Default); err != nil || len(qs) != 0 {
		t.Fatalf("ListQueues after delete = %v, %v", qs, err)
	}
	if _, err := s.GetTask(ctx, workspace.Default, running.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTask after delete: err = %v, want ErrNotFound", err)
	}
	if scs, err := s.ListSchedules(ctx, workspace.Default); err != nil || len(scs) != 0 {
		t.Fatalf("ListSchedules after delete = %v, %v", scs, err)
	}
	// A delivery that was in flight finds its task gone.
	_, err := s.FinishAttempt(ctx, workspace.Default, store.AttemptResult{
		Attempt:   store.Attempt{TaskID: running.ID, Number: running.AttemptCount, StartedAt: now(), FinishedAt: now(), Outcome: store.OutcomeSuccess},
		NextState: store.TaskSucceeded,
	})
	if !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("FinishAttempt after delete: err = %v, want ErrLeaseLost", err)
	}
	if err := s.DeleteWorkspace(ctx, workspace.Default); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second DeleteWorkspace: err = %v, want ErrNotFound", err)
	}

	// The other workspace is untouched.
	if qs, err := s.ListQueues(ctx, otherWorkspace); err != nil || len(qs) != 1 {
		t.Fatalf("other workspace's queues = %v, %v", qs, err)
	}
	if tasks, _, err := s.ListTasks(ctx, otherWorkspace, store.TaskFilter{Queue: "emails", Limit: 10}); err != nil || len(tasks) != 2 {
		t.Fatalf("other workspace's tasks = %d, %v", len(tasks), err)
	}
	if scs, err := s.ListSchedules(ctx, otherWorkspace); err != nil || len(scs) != 1 {
		t.Fatalf("other workspace's schedules = %v, %v", scs, err)
	}
}
