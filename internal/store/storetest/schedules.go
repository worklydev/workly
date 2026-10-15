package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

func scheduleConfig() store.ScheduleConfig {
	cfg := store.DefaultScheduleConfig()
	cfg.Cron = "0 3 * * *"
	cfg.Timezone = "Europe/Stockholm"
	cfg.URL = "http://example.test/nightly"
	cfg.Headers = map[string]string{"Content-Type": "application/json", "X-Tenant": "acme"}
	cfg.Body = []byte(`{"kind":"daily"}`)
	return cfg
}

func mustPutSchedule(t *testing.T, s store.Store, ws workspace.ID, name string, next time.Time) store.Schedule {
	t.Helper()
	sc, _, err := s.PutSchedule(t.Context(), ws, name, scheduleConfig(), next)
	if err != nil {
		t.Fatalf("PutSchedule: %v", err)
	}
	return sc
}

// fire fires the schedule's next slot and returns the run.
func fire(t *testing.T, s store.Store, sc store.Schedule, reason store.SkipReason) store.Run {
	t.Helper()
	sc, err := s.GetSchedule(t.Context(), sc.WorkspaceID, sc.Name)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	f := store.Fire{
		WorkspaceID: sc.WorkspaceID, Schedule: sc.Name, Due: *sc.NextRunAt, Slot: *sc.NextRunAt,
		SkipReason: reason, NextRunAt: sc.NextRunAt.Add(time.Hour),
	}
	r, fired, err := s.FireSchedule(t.Context(), f, now())
	if err != nil || !fired {
		t.Fatalf("FireSchedule = %v, %v", fired, err)
	}
	return r
}

func claimRuns(t *testing.T, s store.Store) []store.RunClaim {
	t.Helper()
	claims, err := s.ClaimRuns(t.Context(), 10, now().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("ClaimRuns: %v", err)
	}
	return claims
}

func finishRun(t *testing.T, s store.Store, r store.Run, next store.RunState) store.RunState {
	t.Helper()
	res := store.RunAttemptResult{
		Attempt: store.Attempt{
			RunID: r.ID, Number: r.AttemptCount, StartedAt: now(), FinishedAt: now(), Outcome: store.OutcomeHTTPError,
		},
		NextState: next,
		RunAt:     now(),
	}
	if next == store.RunSucceeded {
		res.Outcome = store.OutcomeSuccess
	}
	state, err := s.FinishRunAttempt(t.Context(), r.WorkspaceID, res)
	if err != nil {
		t.Fatalf("FinishRunAttempt: %v", err)
	}
	return state
}

func getRun(t *testing.T, s store.Store, r store.Run) store.Run {
	t.Helper()
	got, err := s.GetRun(t.Context(), r.WorkspaceID, r.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	return got
}

func testSchedules(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	next := now().Add(time.Hour)

	if _, err := s.GetSchedule(ctx, workspace.Default, "nightly"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetSchedule(missing) err = %v, want ErrNotFound", err)
	}
	cfg := scheduleConfig()
	sc, created, err := s.PutSchedule(ctx, workspace.Default, "nightly", cfg, next)
	if err != nil || !created {
		t.Fatalf("PutSchedule = %v, %v", created, err)
	}
	if sc.Cron != cfg.Cron || sc.Timezone != cfg.Timezone || sc.URL != cfg.URL || string(sc.Body) != string(cfg.Body) ||
		sc.Headers["X-Tenant"] != "acme" || sc.MaxAttempts != cfg.MaxAttempts || sc.MinBackoff != cfg.MinBackoff ||
		sc.MaxBackoff != cfg.MaxBackoff || sc.Timeout != cfg.Timeout || sc.Overlap != store.OverlapSkip ||
		sc.Missed != store.MissedSkip || sc.Paused || sc.NextRunAt == nil || !sc.NextRunAt.Equal(next) || sc.LastRun != nil {
		t.Fatalf("PutSchedule = %+v", sc)
	}

	// An update replaces the settings.
	cfg.Cron, cfg.Overlap, cfg.Missed, cfg.RetryOn4xx = "*/5 * * * *", store.OverlapAllow, store.MissedCatchUp, true
	sc, created, err = s.PutSchedule(ctx, workspace.Default, "nightly", cfg, next.Add(time.Minute))
	if err != nil || created {
		t.Fatalf("PutSchedule update = %v, %v", created, err)
	}
	if sc.Cron != "*/5 * * * *" || sc.Overlap != store.OverlapAllow || sc.Missed != store.MissedCatchUp ||
		!sc.RetryOn4xx || !sc.NextRunAt.Equal(next.Add(time.Minute)) {
		t.Fatalf("PutSchedule update = %+v", sc)
	}

	// Pausing clears the next slot, and an update keeps it paused.
	if sc, err = s.SetSchedulePaused(ctx, workspace.Default, "nightly", true, time.Time{}); err != nil || !sc.Paused || sc.NextRunAt != nil {
		t.Fatalf("pause = %+v, %v", sc, err)
	}
	if sc, _, err = s.PutSchedule(ctx, workspace.Default, "nightly", cfg, next); err != nil || !sc.Paused || sc.NextRunAt != nil {
		t.Fatalf("PutSchedule while paused = %+v, %v", sc, err)
	}
	if sc, err = s.SetSchedulePaused(ctx, workspace.Default, "nightly", false, next); err != nil || sc.Paused || !sc.NextRunAt.Equal(next) {
		t.Fatalf("resume = %+v, %v", sc, err)
	}
	// Resuming a running schedule doesn't move it.
	if sc, err = s.SetSchedulePaused(ctx, workspace.Default, "nightly", false, next.Add(time.Hour)); err != nil || !sc.NextRunAt.Equal(next) {
		t.Fatalf("second resume = %+v, %v", sc, err)
	}
	if _, err := s.SetSchedulePaused(ctx, workspace.Default, "missing", true, time.Time{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pause missing err = %v", err)
	}

	// Workspaces are separate.
	mustPutSchedule(t, s, otherWorkspace, "other", next)
	mustPutSchedule(t, s, workspace.Default, "a-first", next)
	list, err := s.ListSchedules(ctx, workspace.Default)
	if err != nil || len(list) != 2 || list[0].Name != "a-first" || list[1].Name != "nightly" {
		t.Fatalf("ListSchedules = %+v, %v", list, err)
	}
	if _, err := s.GetSchedule(ctx, workspace.Default, "other"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetSchedule from another workspace err = %v", err)
	}

	// Deleting a schedule deletes its runs.
	r, err := s.TriggerSchedule(ctx, workspace.Default, "nightly", now())
	if err != nil {
		t.Fatalf("TriggerSchedule: %v", err)
	}
	if err := s.DeleteSchedule(ctx, workspace.Default, "nightly"); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := s.GetRun(ctx, workspace.Default, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetRun after delete err = %v", err)
	}
	if err := s.DeleteSchedule(ctx, workspace.Default, "nightly"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second DeleteSchedule err = %v", err)
	}
}

func testFireSchedule(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	slot := now().Add(-time.Second)
	sc := mustPutSchedule(t, s, workspace.Default, "nightly", slot)
	mustPutSchedule(t, s, workspace.Default, "later", now().Add(time.Hour))

	due, err := s.DueSchedules(ctx, now())
	if err != nil || len(due) != 1 || due[0].Name != "nightly" {
		t.Fatalf("DueSchedules = %+v, %v", due, err)
	}

	r := fire(t, s, sc, "")
	if r.State != store.RunPending || r.Trigger != store.TriggerSchedule || !r.ScheduledFor.Equal(slot) ||
		r.URL != sc.URL || string(r.Body) != string(sc.Body) || r.Headers["X-Tenant"] != "acme" || r.Schedule != "nightly" {
		t.Fatalf("fired run = %+v", r)
	}
	sc, _ = s.GetSchedule(ctx, workspace.Default, "nightly")
	if !sc.NextRunAt.Equal(slot.Add(time.Hour)) || sc.LastRun == nil || sc.LastRun.ID != r.ID {
		t.Fatalf("schedule after fire = %+v", sc)
	}

	// The same slot can't fire twice: the guard on the old next slot fails,
	// and so would the unique slot.
	f := store.Fire{WorkspaceID: workspace.Default, Schedule: "nightly", Due: slot, Slot: slot, NextRunAt: slot.Add(time.Hour)}
	if _, fired, err := s.FireSchedule(ctx, f, now()); err != nil || fired {
		t.Fatalf("second FireSchedule = %v, %v", fired, err)
	}
	f.Due = *sc.NextRunAt
	if _, fired, err := s.FireSchedule(ctx, f, now()); err != nil || fired {
		t.Fatalf("FireSchedule of a slot with a run = %v, %v", fired, err)
	}

	// With the first run still pending, overlap skip records a skipped run.
	sc = mustPutSchedule(t, s, workspace.Default, "nightly", slot.Add(2*time.Hour))
	skipped := fire(t, s, sc, "")
	if skipped.State != store.RunSkipped || skipped.SkipReason != store.SkipOverlap || skipped.FinishedAt == nil {
		t.Fatalf("overlapping run = %+v", skipped)
	}
	// A missed slot is skipped whatever the overlap.
	missed := fire(t, s, sc, store.SkipMissed)
	if missed.State != store.RunSkipped || missed.SkipReason != store.SkipMissed {
		t.Fatalf("missed run = %+v", missed)
	}

	// Overlap allow runs alongside.
	cfg := scheduleConfig()
	cfg.Overlap = store.OverlapAllow
	if _, _, err := s.PutSchedule(ctx, workspace.Default, "nightly", cfg, slot.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if r := fire(t, s, sc, ""); r.State != store.RunPending {
		t.Fatalf("overlap allow run = %+v", r)
	}

	// A paused schedule isn't due and doesn't fire.
	sc, _ = s.SetSchedulePaused(ctx, workspace.Default, "later", true, time.Time{})
	if due, _ := s.DueSchedules(ctx, now().Add(7*time.Hour)); len(due) != 1 || due[0].Name != "nightly" {
		t.Fatalf("DueSchedules with a paused one = %+v", due)
	}
	f = store.Fire{WorkspaceID: workspace.Default, Schedule: "later", Due: now().Add(time.Hour), Slot: now(), NextRunAt: now()}
	if _, fired, err := s.FireSchedule(ctx, f, now()); err != nil || fired {
		t.Fatalf("FireSchedule paused = %v, %v", fired, err)
	}
}

func testRunLifecycle(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	sc := mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(-time.Second))
	r := fire(t, s, sc, "")

	claims := claimRuns(t, s)
	if len(claims) != 1 || claims[0].Run.ID != r.ID || claims[0].Run.State != store.RunRunning ||
		claims[0].Run.AttemptCount != 1 || claims[0].Schedule.Name != "nightly" || claims[0].Run.LeaseExpiresAt == nil {
		t.Fatalf("ClaimRuns = %+v", claims)
	}
	if again := claimRuns(t, s); len(again) != 0 {
		t.Fatalf("ClaimRuns while running = %+v", again)
	}
	// Retry, then succeed.
	if state := finishRun(t, s, claims[0].Run, store.RunPending); state != store.RunPending {
		t.Fatalf("state = %s", state)
	}
	claims = claimRuns(t, s)
	if len(claims) != 1 || claims[0].Run.AttemptCount != 2 {
		t.Fatalf("second ClaimRuns = %+v", claims)
	}
	if state := finishRun(t, s, claims[0].Run, store.RunSucceeded); state != store.RunSucceeded {
		t.Fatalf("state = %s", state)
	}
	// A late result for the same attempt is discarded.
	res := store.RunAttemptResult{Attempt: store.Attempt{RunID: r.ID, Number: 2, StartedAt: now(), FinishedAt: now(), Outcome: store.OutcomeSuccess}, NextState: store.RunSucceeded}
	if _, err := s.FinishRunAttempt(ctx, workspace.Default, res); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("late FinishRunAttempt err = %v, want ErrLeaseLost", err)
	}

	got := getRun(t, s, r)
	if got.State != store.RunSucceeded || got.FinishedAt == nil || got.LastOutcome != store.OutcomeSuccess || got.LastAttemptAt == nil {
		t.Fatalf("run = %+v", got)
	}
	attempts, err := s.ListRunAttempts(ctx, workspace.Default, r.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Number != 1 || attempts[1].Outcome != store.OutcomeSuccess || attempts[0].RunID != r.ID {
		t.Fatalf("ListRunAttempts = %+v, %v", attempts, err)
	}
	if a, err := s.ListRunAttempts(ctx, otherWorkspace, r.ID); err != nil || len(a) != 0 {
		t.Fatalf("ListRunAttempts from another workspace = %+v, %v", a, err)
	}
	if sc, _ := s.GetSchedule(ctx, workspace.Default, "nightly"); sc.LastRun == nil || sc.LastRun.State != store.RunSucceeded {
		t.Fatalf("LastRun = %+v", sc.LastRun)
	}

	// A succeeded run can be retried, with a fresh budget, and the retry is
	// recorded on the next attempt.
	if _, err := s.CancelRun(ctx, workspace.Default, r.ID); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("CancelRun succeeded err = %v", err)
	}
	// After the previous attempt's millisecond, so the retry is told apart from it.
	time.Sleep(2 * time.Millisecond)
	retried, err := s.RetryRun(ctx, workspace.Default, r.ID)
	if err != nil || retried.State != store.RunPending || retried.RetriedAt == nil || retried.AttemptsSinceReplay() != 0 {
		t.Fatalf("RetryRun = %+v, %v", retried, err)
	}
	claims = claimRuns(t, s)
	if len(claims) != 1 {
		t.Fatalf("ClaimRuns after retry = %+v", claims)
	}
	if _, err := s.RetryRun(ctx, workspace.Default, r.ID); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("RetryRun in flight err = %v", err)
	}
	// Cancelled mid-attempt: the attempt is recorded, the run stays cancelled.
	if c, err := s.CancelRun(ctx, workspace.Default, r.ID); err != nil || c.State != store.RunCancelled {
		t.Fatalf("CancelRun running = %+v, %v", c, err)
	}
	if state := finishRun(t, s, claims[0].Run, store.RunPending); state != store.RunCancelled {
		t.Fatalf("state after cancelled attempt = %s", state)
	}
	attempts, _ = s.ListRunAttempts(ctx, workspace.Default, r.ID)
	if len(attempts) != 3 || attempts[2].RetriedAt == nil || attempts[1].RetriedAt != nil {
		t.Fatalf("attempts after retry = %+v", attempts)
	}
	if c, err := s.CancelRun(ctx, workspace.Default, r.ID); err != nil || c.State != store.RunCancelled {
		t.Fatalf("second CancelRun = %+v, %v", c, err)
	}
	if _, err := s.CancelRun(ctx, otherWorkspace, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CancelRun from another workspace err = %v", err)
	}

	// A skipped run can be retried to deliver it.
	sc = mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(-time.Second))
	skipped := fire(t, s, sc, store.SkipMissed)
	if r, err := s.RetryRun(ctx, workspace.Default, skipped.ID); err != nil || r.State != store.RunPending || r.FinishedAt != nil {
		t.Fatalf("RetryRun skipped = %+v, %v", r, err)
	}
}

func testRunLeases(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	sc := mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(-time.Second))
	r := fire(t, s, sc, "")
	claimRuns(t, s)

	// The lease is the schedule's timeout plus grace.
	if expired, err := s.ExpiredRunLeases(ctx, now().Add(sc.Timeout), 10); err != nil || len(expired) != 0 {
		t.Fatalf("ExpiredRunLeases before expiry = %+v, %v", expired, err)
	}
	expired, err := s.ExpiredRunLeases(ctx, now().Add(sc.Timeout+2*time.Minute), 10)
	if err != nil || len(expired) != 1 || expired[0].ID != r.ID {
		t.Fatalf("ExpiredRunLeases = %+v, %v", expired, err)
	}
}

func testTriggerSchedule(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(time.Hour))
	if _, err := s.SetSchedulePaused(ctx, workspace.Default, "nightly", true, time.Time{}); err != nil {
		t.Fatal(err)
	}
	// Manual runs ignore pause and overlap, and don't move the schedule.
	at := now()
	a, err := s.TriggerSchedule(ctx, workspace.Default, "nightly", at)
	if err != nil || a.Trigger != store.TriggerManual || a.State != store.RunPending || !a.ScheduledFor.Equal(at) {
		t.Fatalf("TriggerSchedule = %+v, %v", a, err)
	}
	b, err := s.TriggerSchedule(ctx, workspace.Default, "nightly", at)
	if err != nil || b.ID == a.ID || b.State != store.RunPending {
		t.Fatalf("second TriggerSchedule = %+v, %v", b, err)
	}
	if _, err := s.TriggerSchedule(ctx, workspace.Default, "missing", at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("TriggerSchedule missing err = %v", err)
	}

	// Paging, newest first, and the state filter.
	page, cursor, err := s.ListRuns(ctx, workspace.Default, store.RunFilter{Schedule: "nightly", Limit: 1})
	if err != nil || len(page) != 1 || page[0].ID != b.ID || cursor == "" {
		t.Fatalf("ListRuns page 1 = %+v, %q, %v", page, cursor, err)
	}
	page, cursor, err = s.ListRuns(ctx, workspace.Default, store.RunFilter{Schedule: "nightly", Limit: 1, Cursor: cursor})
	if err != nil || len(page) != 1 || page[0].ID != a.ID || cursor != "" {
		t.Fatalf("ListRuns page 2 = %+v, %q, %v", page, cursor, err)
	}
	if _, err := s.CancelRun(ctx, workspace.Default, a.ID); err != nil {
		t.Fatal(err)
	}
	page, _, err = s.ListRuns(ctx, workspace.Default, store.RunFilter{Schedule: "nightly", State: store.RunCancelled, Limit: 10})
	if err != nil || len(page) != 1 || page[0].ID != a.ID {
		t.Fatalf("ListRuns cancelled = %+v, %v", page, err)
	}
	if page, _, _ := s.ListRuns(ctx, otherWorkspace, store.RunFilter{Schedule: "nightly", Limit: 10}); len(page) != 0 {
		t.Fatalf("ListRuns from another workspace = %+v", page)
	}
}

func testNextScheduleWork(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	if _, ok, err := s.NextScheduleWork(ctx, now()); err != nil || ok {
		t.Fatalf("NextScheduleWork empty = %v, %v", ok, err)
	}
	fireAt := now().Add(time.Hour)
	mustPutSchedule(t, s, workspace.Default, "nightly", fireAt)
	if next, ok, err := s.NextScheduleWork(ctx, now()); err != nil || !ok || !next.Equal(fireAt) {
		t.Fatalf("NextScheduleWork = %v, %v, %v; want %v", next, ok, err, fireAt)
	}
	// A run waiting to retry sooner comes first.
	if _, err := s.TriggerSchedule(ctx, workspace.Default, "nightly", now()); err != nil {
		t.Fatal(err)
	}
	claims := claimRuns(t, s)
	retryAt := now().Add(time.Minute)
	res := store.RunAttemptResult{
		Attempt:   store.Attempt{RunID: claims[0].Run.ID, Number: 1, StartedAt: now(), FinishedAt: now(), Outcome: store.OutcomeTimeout},
		NextState: store.RunPending, RunAt: retryAt,
	}
	if _, err := s.FinishRunAttempt(ctx, workspace.Default, res); err != nil {
		t.Fatal(err)
	}
	if next, ok, err := s.NextScheduleWork(ctx, now()); err != nil || !ok || !next.Equal(retryAt) {
		t.Fatalf("NextScheduleWork with a retry = %v, %v, %v; want %v", next, ok, err, retryAt)
	}
}

func testDeleteFinishedRuns(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(time.Hour))
	var runs []store.Run
	for range 3 {
		r, err := s.TriggerSchedule(ctx, workspace.Default, "nightly", now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CancelRun(ctx, workspace.Default, r.ID); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	pending, err := s.TriggerSchedule(ctx, workspace.Default, "nightly", now())
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteFinishedRuns(ctx, now().Add(-time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("DeleteFinishedRuns before retention = %d, %v", n, err)
	}
	if n, err := s.DeleteFinishedRuns(ctx, now().Add(time.Hour), 10); err != nil || n != 3 {
		t.Fatalf("DeleteFinishedRuns = %d, %v; want 3", n, err)
	}
	left, _, _ := s.ListRuns(ctx, workspace.Default, store.RunFilter{Schedule: "nightly", Limit: 10})
	if len(left) != 1 || left[0].ID != pending.ID {
		t.Fatalf("runs left = %+v", left)
	}

	// The latest run is kept even when it's finished.
	if _, err := s.CancelRun(ctx, workspace.Default, pending.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteFinishedRuns(ctx, now().Add(time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("DeleteFinishedRuns of the latest run = %d, %v", n, err)
	}
}

func testScheduleStatus(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(time.Hour))
	mustPutSchedule(t, s, otherWorkspace, "hourly", now().Add(time.Hour))
	if _, err := s.SetSchedulePaused(ctx, otherWorkspace, "hourly", true, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TriggerSchedule(ctx, workspace.Default, "nightly", now()); err != nil {
		t.Fatal(err)
	}
	claims := claimRuns(t, s)
	finishRun(t, s, claims[0].Run, store.RunSucceeded)

	status, err := s.AllScheduleStatus(ctx)
	if err != nil || len(status) != 2 {
		t.Fatalf("AllScheduleStatus = %+v, %v", status, err)
	}
	byName := map[string]store.ScheduleStatus{}
	for _, st := range status {
		byName[st.Schedule] = st
	}
	if st := byName["nightly"]; st.Paused || st.LastSuccessAt == nil || st.WorkspaceID != workspace.Default {
		t.Fatalf("nightly status = %+v", st)
	}
	if st := byName["hourly"]; !st.Paused || st.LastSuccessAt != nil || st.WorkspaceID != otherWorkspace {
		t.Fatalf("hourly status = %+v", st)
	}
}

func testScheduleWake(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	leader, err := s.TryLead(t.Context())
	if err != nil {
		t.Fatalf("TryLead: %v", err)
	}
	defer leader.Release()
	woken := func(what string) {
		t.Helper()
		select {
		case <-leader.Wake():
		case <-time.After(5 * time.Second):
			t.Fatalf("no wake-up after %s", what)
		}
	}
	mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(time.Hour))
	woken("PutSchedule")
	r, err := s.TriggerSchedule(t.Context(), workspace.Default, "nightly", now())
	if err != nil {
		t.Fatal(err)
	}
	woken("TriggerSchedule")
	if _, err := s.CancelRun(t.Context(), workspace.Default, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryRun(t.Context(), workspace.Default, r.ID); err != nil {
		t.Fatal(err)
	}
	woken("RetryRun")
	if _, err := s.SetSchedulePaused(t.Context(), workspace.Default, "nightly", true, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSchedulePaused(t.Context(), workspace.Default, "nightly", false, now()); err != nil {
		t.Fatal(err)
	}
	woken("resume")
}
