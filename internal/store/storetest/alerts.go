package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

func testAlertChannels(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()

	email, err := s.CreateAlertChannel(ctx, workspace.Default, store.AlertEmail, "ops@example.com")
	if err != nil || email.ID == "" || email.Type != store.AlertEmail || email.Target != "ops@example.com" || email.CreatedAt.IsZero() {
		t.Fatalf("CreateAlertChannel = %+v, %v", email, err)
	}
	slack, err := s.CreateAlertChannel(ctx, workspace.Default, store.AlertSlack, "https://hooks.slack.com/services/a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAlertChannel(ctx, workspace.Default, store.AlertEmail, "ops@example.com"); !errors.Is(err, store.ErrExists) {
		t.Fatalf("duplicate CreateAlertChannel: %v, want ErrExists", err)
	}
	other, err := s.CreateAlertChannel(ctx, otherWorkspace, store.AlertEmail, "ops@example.com")
	if err != nil {
		t.Fatalf("same target in another workspace: %v", err)
	}

	list, err := s.ListAlertChannels(ctx, workspace.Default)
	if err != nil || len(list) != 2 || list[0] != email || list[1] != slack {
		t.Fatalf("ListAlertChannels = %+v, %v; want [email, slack]", list, err)
	}
	if got, err := s.GetAlertChannel(ctx, workspace.Default, slack.ID); err != nil || got != slack {
		t.Fatalf("GetAlertChannel = %+v, %v", got, err)
	}
	if _, err := s.GetAlertChannel(ctx, workspace.Default, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetAlertChannel(other workspace's) = %v, want ErrNotFound", err)
	}
	all, err := s.AllAlertChannels(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("AllAlertChannels = %+v, %v; want 3", all, err)
	}

	if err := s.DeleteAlertChannel(ctx, workspace.Default, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteAlertChannel(other workspace's) = %v, want ErrNotFound", err)
	}
	if err := s.DeleteAlertChannel(ctx, workspace.Default, email.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAlertChannel(ctx, workspace.Default, email.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetAlertChannel after delete = %v, want ErrNotFound", err)
	}
}

func testFailuresToAlert(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx := t.Context()
	since := now().Add(-time.Millisecond)
	failTask := func(ws workspace.ID, queue, errText string) {
		failWith(t, s, mustCreate(t, s, ws, store.NewTask{Queue: queue}), store.OutcomeHTTPError, 503, errText, "")
	}
	failTask(workspace.Default, "emails", "HTTP 503")
	failTask(workspace.Default, "emails", "no response within 30s")
	failTask(workspace.Default, "emails", "HTTP 503")
	failTask(otherWorkspace, "emails", "HTTP 503")
	attempt(t, s, mustCreate(t, s, workspace.Default, store.NewTask{Queue: "billing"}), store.TaskSucceeded)
	mustPutSchedule(t, s, workspace.Default, "nightly", now().Add(time.Hour))
	if _, err := s.TriggerSchedule(ctx, workspace.Default, "nightly", now()); err != nil {
		t.Fatal(err)
	}
	finishRun(t, s, claimRuns(t, s)[0].Run, store.RunFailed)

	w := store.AlertWindow{Now: now().Add(time.Millisecond), Since: since, Cooldown: time.Hour}
	got, err := s.FailuresToAlert(ctx, workspace.Default, w)
	if err != nil || len(got) != 2 {
		t.Fatalf("FailuresToAlert = %+v, %v; want emails and nightly", got, err)
	}
	q, sc := got[0], got[1]
	if q.Kind != store.AlertQueue || q.Name != "emails" || q.Total != 3 || !q.Since.Equal(since) || len(q.Groups) != 2 ||
		q.Groups[0] != (store.AlertErrorGroup{Error: "HTTP 503", Count: 2}) ||
		q.Groups[1] != (store.AlertErrorGroup{Error: "no response within 30s", Count: 1}) {
		t.Fatalf("queue failures = %+v", q)
	}
	if sc.Kind != store.AlertSchedule || sc.Name != "nightly" || sc.Total != 1 {
		t.Fatalf("schedule failures = %+v", sc)
	}

	// Once alerted, a subject waits out the cooldown, then gets only what
	// failed since.
	alertedAt := w.Now
	if err := s.MarkAlerted(ctx, workspace.Default, []store.AlertSubject{q.AlertSubject, sc.AlertSubject}, alertedAt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	failTask(workspace.Default, "emails", "HTTP 500")
	w.Now = now().Add(time.Millisecond)
	if got, err := s.FailuresToAlert(ctx, workspace.Default, w); err != nil || len(got) != 0 {
		t.Fatalf("FailuresToAlert in cooldown = %+v, %v; want none", got, err)
	}
	w.Cooldown = 0
	got, err = s.FailuresToAlert(ctx, workspace.Default, w)
	if err != nil || len(got) != 1 || got[0].Name != "emails" || got[0].Total != 1 || !got[0].Since.Equal(alertedAt) ||
		got[0].Groups[0].Error != "HTTP 500" {
		t.Fatalf("FailuresToAlert after cooldown = %+v, %v; want the one new failure", got, err)
	}
	// MarkAlerted moves an existing subject on.
	if err := s.MarkAlerted(ctx, workspace.Default, []store.AlertSubject{q.AlertSubject}, w.Now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.FailuresToAlert(ctx, workspace.Default, w); err != nil || len(got) != 0 {
		t.Fatalf("FailuresToAlert after second alert = %+v, %v; want none", got, err)
	}

	// Failures before Since don't count.
	w.Since = w.Now
	if got, err := s.FailuresToAlert(ctx, otherWorkspace, w); err != nil || len(got) != 0 {
		t.Fatalf("FailuresToAlert before Since = %+v, %v; want none", got, err)
	}
	w.Since = since
	if got, err := s.FailuresToAlert(ctx, otherWorkspace, w); err != nil || len(got) != 1 || got[0].Total != 1 {
		t.Fatalf("FailuresToAlert(other workspace) = %+v, %v", got, err)
	}
}
