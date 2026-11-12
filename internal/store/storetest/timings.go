package storetest

import (
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// finishTook finishes a claimed task's attempt as having taken d.
func finishTook(t *testing.T, s store.Store, claimed store.Task, next store.TaskState, d time.Duration) {
	t.Helper()
	start := now()
	_, err := s.FinishAttempt(t.Context(), claimed.WorkspaceID, store.AttemptResult{
		Attempt: store.Attempt{
			TaskID: claimed.ID, Number: claimed.AttemptCount,
			StartedAt: start, FinishedAt: start.Add(d), Outcome: store.OutcomeSuccess,
		},
		NextState: next,
		RunAt:     now(),
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
}

func testTaskTimings(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	task := mustCreate(t, s, workspace.Default, store.NewTask{Queue: "emails"})
	if task.StartedAt != nil || task.LastDuration != nil {
		t.Fatalf("new task StartedAt = %v, LastDuration = %v; want nil", task.StartedAt, task.LastDuration)
	}

	before := now()
	claimed := claim(t, s, task)
	// Set when the first attempt starts, so a running task has it.
	if claimed.StartedAt == nil || claimed.StartedAt.Before(before) || claimed.StartedAt.After(now().Add(time.Millisecond)) {
		t.Fatalf("claimed StartedAt = %v, want about %v", claimed.StartedAt, before)
	}
	started := *claimed.StartedAt
	finishTook(t, s, claimed, store.TaskPending, 250*time.Millisecond)
	got, err := s.GetTask(ctx, workspace.Default, task.ID)
	if err != nil || got.LastDuration == nil || *got.LastDuration != 250*time.Millisecond || !got.StartedAt.Equal(started) {
		t.Fatalf("after first attempt = %+v, %v", got, err)
	}

	// The second attempt, and a replay, keep the first attempt's start and
	// record the latest duration.
	finishTook(t, s, claim(t, s, task), store.TaskSucceeded, 40*time.Millisecond)
	if _, err := s.RetryTask(ctx, workspace.Default, task.ID); err != nil {
		t.Fatalf("RetryTask: %v", err)
	}
	finishTook(t, s, claim(t, s, task), store.TaskSucceeded, 7*time.Millisecond)
	got, err = s.GetTask(ctx, workspace.Default, task.ID)
	if err != nil || *got.LastDuration != 7*time.Millisecond || !got.StartedAt.Equal(started) {
		t.Fatalf("after replay StartedAt = %v, LastDuration = %v, %v; want %v, 7ms", got.StartedAt, got.LastDuration, err, started)
	}
	if list, _, err := s.ListTasks(ctx, workspace.Default, store.TaskFilter{Queue: "emails", Limit: 10}); err != nil ||
		len(list) != 1 || list[0].StartedAt == nil || list[0].LastDuration == nil {
		t.Fatalf("ListTasks = %+v, %v", list, err)
	}
}

func testRunTimings(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	sc := mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(-time.Second))
	r := fire(t, s, sc, "")
	if r.StartedAt != nil || r.LastDuration != nil {
		t.Fatalf("new run StartedAt = %v, LastDuration = %v; want nil", r.StartedAt, r.LastDuration)
	}

	claims := claimRuns(t, s)
	if len(claims) != 1 || claims[0].Run.StartedAt == nil {
		t.Fatalf("ClaimRuns = %+v", claims)
	}
	stored := getRun(t, s, r)
	if stored.StartedAt == nil || !stored.StartedAt.Equal(*claims[0].Run.StartedAt) {
		t.Fatalf("stored StartedAt = %v, claimed %v", stored.StartedAt, claims[0].Run.StartedAt)
	}
	started := *stored.StartedAt

	start := now()
	finish := func(run store.Run, next store.RunState, d time.Duration) {
		t.Helper()
		if _, err := s.FinishRunAttempt(ctx, workspace.Default, store.RunAttemptResult{
			Attempt:   store.Attempt{RunID: run.ID, Number: run.AttemptCount, StartedAt: start, FinishedAt: start.Add(d), Outcome: store.OutcomeSuccess},
			NextState: next, RunAt: now(),
		}); err != nil {
			t.Fatalf("FinishRunAttempt: %v", err)
		}
	}
	finish(claims[0].Run, store.RunPending, 1500*time.Millisecond)
	if got := getRun(t, s, r); got.LastDuration == nil || *got.LastDuration != 1500*time.Millisecond {
		t.Fatalf("LastDuration = %v, want 1.5s", got.LastDuration)
	}
	claims = claimRuns(t, s)
	if len(claims) != 1 || !claims[0].Run.StartedAt.Equal(started) {
		t.Fatalf("second ClaimRuns StartedAt = %+v, want %v", claims, started)
	}
	finish(claims[0].Run, store.RunSucceeded, 20*time.Millisecond)
	if got := getRun(t, s, r); *got.LastDuration != 20*time.Millisecond || !got.StartedAt.Equal(started) {
		t.Fatalf("run = %+v", got)
	}
}

func testListRunsAllSchedules(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	var ids []string // oldest first
	for _, name := range []string{"nightly", "hourly", "nightly"} {
		sc := mustPutSchedule(t, s, workspace.Default, name, now().Add(-time.Second))
		ids = append(ids, fire(t, s, sc, store.SkipMissed).ID)
		time.Sleep(2 * time.Millisecond) // IDs order by time
	}
	other := mustPutSchedule(t, s, otherWorkspace, "nightly", now().Add(-time.Second))
	fire(t, s, other, "")

	runs, cursor, err := s.ListRuns(ctx, workspace.Default, store.RunFilter{Limit: 2})
	if err != nil || len(runs) != 2 || runs[0].ID != ids[2] || runs[1].ID != ids[1] || cursor == "" {
		t.Fatalf("ListRuns page 1 = %v, %q, %v; want [%s %s]", runIDs(runs), cursor, err, ids[2], ids[1])
	}
	if runs[1].Schedule != "hourly" {
		t.Fatalf("run schedule = %q, want hourly", runs[1].Schedule)
	}
	runs, cursor, err = s.ListRuns(ctx, workspace.Default, store.RunFilter{Limit: 2, Cursor: cursor})
	if err != nil || len(runs) != 1 || runs[0].ID != ids[0] || cursor != "" {
		t.Fatalf("ListRuns page 2 = %v, %q, %v; want [%s]", runIDs(runs), cursor, err, ids[0])
	}
	if runs, _, err := s.ListRuns(ctx, workspace.Default, store.RunFilter{State: store.RunPending, Limit: 10}); err != nil || len(runs) != 0 {
		t.Fatalf("ListRuns pending = %v, %v; want none", runIDs(runs), err)
	}
	if runs, _, err := s.ListRuns(ctx, workspace.Default, store.RunFilter{State: store.RunSkipped, Limit: 10}); err != nil || len(runs) != 3 {
		t.Fatalf("ListRuns skipped = %v, %v; want 3", runIDs(runs), err)
	}
	if runs, _, err := s.ListRuns(ctx, otherWorkspace, store.RunFilter{Limit: 10}); err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns in the other workspace = %v, %v; want 1", runIDs(runs), err)
	}
}

func runIDs(runs []store.Run) []string {
	ids := make([]string, len(runs))
	for i, r := range runs {
		ids[i] = r.ID
	}
	return ids
}

func testSchedulePausedAt(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	sc := mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(time.Hour))
	if sc.PausedAt != nil {
		t.Fatalf("new schedule PausedAt = %v, want nil", sc.PausedAt)
	}
	before := now()
	paused, err := s.SetSchedulePaused(ctx, workspace.Default, "nightly", true, time.Time{})
	if err != nil || paused.PausedAt == nil || paused.PausedAt.Before(before) {
		t.Fatalf("paused = %+v, %v", paused, err)
	}
	pausedAt := *paused.PausedAt

	// Pausing again, or changing its settings, keeps when it was paused.
	time.Sleep(2 * time.Millisecond)
	if again, err := s.SetSchedulePaused(ctx, workspace.Default, "nightly", true, time.Time{}); err != nil || !again.PausedAt.Equal(pausedAt) {
		t.Fatalf("paused again PausedAt = %v, %v; want %v", again.PausedAt, err, pausedAt)
	}
	if put := mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(time.Hour)); put.PausedAt == nil || !put.PausedAt.Equal(pausedAt) {
		t.Fatalf("PutSchedule while paused PausedAt = %v, want %v", put.PausedAt, pausedAt)
	}
	if list, err := s.ListSchedules(ctx, workspace.Default); err != nil || len(list) != 1 || list[0].PausedAt == nil {
		t.Fatalf("ListSchedules = %+v, %v", list, err)
	}

	resumed, err := s.SetSchedulePaused(ctx, workspace.Default, "nightly", false, now().Add(time.Hour))
	if err != nil || resumed.PausedAt != nil || resumed.Paused {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}
}
