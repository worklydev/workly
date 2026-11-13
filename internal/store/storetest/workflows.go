package storetest

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// step is a step of a test workflow, sent to example.test.
func step(name string, deps ...string) store.WorkflowStepConfig {
	return store.WorkflowStepConfig{
		Name: name, DependsOn: deps, Queue: store.WorkflowQueue, Method: "POST",
		URL: "http://example.test/" + name,
	}
}

func putWorkflow(t *testing.T, s store.Store, ws workspace.ID, name string, steps ...store.WorkflowStepConfig) store.Workflow {
	t.Helper()
	w, _, err := s.PutWorkflow(t.Context(), ws, name, store.WorkflowConfig{Steps: steps})
	if err != nil {
		t.Fatalf("PutWorkflow: %v", err)
	}
	return w
}

func startRun(t *testing.T, s store.Store, ws workspace.ID, name string) store.WorkflowRun {
	t.Helper()
	r, created, err := s.StartWorkflowRun(t.Context(), ws, name, store.NewWorkflowRun{Input: json.RawMessage(`{"date":"2026-11-13"}`)}, time.Now())
	if err != nil || !created {
		t.Fatalf("StartWorkflowRun = %v, %v", created, err)
	}
	return r
}

func getWfRun(t *testing.T, s store.Store, ws workspace.ID, id string) store.WorkflowRun {
	t.Helper()
	r, err := s.GetWorkflowRun(t.Context(), ws, id)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	return r
}

// wantSteps checks a run's status and its steps', as "name=status".
func wantSteps(t *testing.T, r store.WorkflowRun, status store.WorkflowRunStatus, steps ...string) {
	t.Helper()
	var got []string
	for _, st := range r.Steps {
		got = append(got, st.Name+"="+string(st.Status))
	}
	if r.Status != status || !slices.Equal(got, steps) {
		t.Fatalf("run %s %v (error %q), want %s %v", r.Status, got, r.Error, status, steps)
	}
}

// claimSteps claims every due task in the queue, by the step each delivers.
func claimSteps(t *testing.T, s store.Store, ws workspace.ID, queue string) map[string]store.Task {
	t.Helper()
	out := map[string]store.Task{}
	for _, task := range claimAll(t, s, ws, queue) {
		out[task.Headers["Workly-Workflow-Step"]] = task
	}
	return out
}

// deliver finishes a claimed task's attempt, with the response headers.
func deliver(t *testing.T, s store.Store, task store.Task, next store.TaskState, headers map[string]string) {
	t.Helper()
	r := store.AttemptResult{
		Attempt: store.Attempt{
			TaskID: task.ID, Number: task.AttemptCount, StartedAt: now(), FinishedAt: now(),
			Outcome: store.OutcomeSuccess, ResponseHeaders: headers,
		},
		NextState: next, RunAt: now(),
	}
	if next != store.TaskSucceeded {
		code := 500
		r.Outcome, r.StatusCode, r.Error = store.OutcomeHTTPError, &code, "HTTP 500"
	}
	if _, err := s.FinishAttempt(t.Context(), task.WorkspaceID, r); err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
}

func testWorkflows(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx, ws := t.Context(), workspace.Default

	w := putWorkflow(t, s, ws, "refresh", step("prices"), step("fx"))
	if w.Version != 1 || len(w.Steps) != 2 || w.LastRun != nil {
		t.Fatalf("PutWorkflow = %+v", w)
	}
	// The same definition again isn't a new version; a changed one is.
	w, created, err := s.PutWorkflow(ctx, ws, "refresh", w.WorkflowConfig)
	if err != nil || created || w.Version != 1 {
		t.Fatalf("PutWorkflow again = %v, %v, version %d", created, err, w.Version)
	}
	w = putWorkflow(t, s, ws, "refresh", step("prices"), step("fx"), step("analytics", "prices", "fx"))
	if w.Version != 2 {
		t.Fatalf("version = %d, want 2", w.Version)
	}
	if _, err := s.GetWorkflow(ctx, otherWorkspace, "refresh"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetWorkflow in another workspace = %v, want ErrNotFound", err)
	}
	if _, _, err := s.StartWorkflowRun(ctx, otherWorkspace, "refresh", store.NewWorkflowRun{}, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("StartWorkflowRun in another workspace = %v, want ErrNotFound", err)
	}

	// Idempotent start.
	nr := store.NewWorkflowRun{IdempotencyKey: "2026-11-13"}
	r1, created, err := s.StartWorkflowRun(ctx, ws, "refresh", nr, time.Now())
	if err != nil || !created {
		t.Fatalf("StartWorkflowRun = %v, %v", created, err)
	}
	r2, created, err := s.StartWorkflowRun(ctx, ws, "refresh", nr, time.Now())
	if err != nil || created || r2.ID != r1.ID {
		t.Fatalf("StartWorkflowRun again = %s %v, %v; want %s", r2.ID, created, err, r1.ID)
	}
	if r1.Version != 2 || r1.Status != store.WorkflowRunning || !r1.DeadlineAt.After(r1.CreatedAt) {
		t.Fatalf("run = %+v", r1)
	}

	// The step tasks carry the run's context.
	tasks := claimSteps(t, s, ws, store.WorkflowQueue)
	if len(tasks) != 2 {
		t.Fatalf("claimed %d step tasks, want prices and fx", len(tasks))
	}
	run := getWfRun(t, s, ws, r1.ID)
	prices := tasks["prices"]
	if prices.Headers["Workly-Workflow-Run-Id"] != r1.ID || prices.Headers["Workly-Workflow"] != "refresh" ||
		prices.Headers["Workly-Workflow-Step-Id"] != run.Steps[0].ID || prices.WorkflowStepID != run.Steps[0].ID {
		t.Fatalf("step task headers = %v", prices.Headers)
	}
	var body store.WorkflowStepBody
	if err := json.Unmarshal(prices.Body, &body); err != nil || body.WorkflowRunID != r1.ID || body.Step != "prices" ||
		string(body.Input) != "null" {
		t.Fatalf("step task body = %s (%v)", prices.Body, err)
	}
	if run.Steps[0].Task == nil || run.Steps[0].Task.ID != prices.ID {
		t.Fatalf("step task = %+v", run.Steps[0].Task)
	}
	// A step task can't be retried alone.
	deliver(t, s, prices, store.TaskSucceeded, nil)
	if _, err := s.RetryTask(ctx, ws, prices.ID); !errors.Is(err, store.ErrWorkflowStepTask) {
		t.Fatalf("RetryTask of a step task = %v, want ErrWorkflowStepTask", err)
	}

	list, err := s.ListWorkflows(ctx, ws)
	if err != nil || len(list) != 1 || list[0].LastRun == nil || list[0].LastRun.ID != r1.ID {
		t.Fatalf("ListWorkflows = %+v, %v", list, err)
	}
	runs, cursor, err := s.ListWorkflowRuns(ctx, ws, store.WorkflowRunFilter{Workflow: "refresh", Limit: 10})
	if err != nil || len(runs) != 1 || cursor != "" {
		t.Fatalf("ListWorkflowRuns = %d, %q, %v", len(runs), cursor, err)
	}

	// Deleting the definition keeps its runs.
	if err := s.DeleteWorkflow(ctx, ws, "refresh"); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	if err := s.DeleteWorkflow(ctx, ws, "refresh"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteWorkflow again = %v", err)
	}
	if _, _, err := s.StartWorkflowRun(ctx, ws, "refresh", store.NewWorkflowRun{}, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("StartWorkflowRun of a deleted workflow = %v", err)
	}
	// The run still finishes, under the definition it started with.
	deliver(t, s, tasks["fx"], store.TaskSucceeded, nil)
	wantSteps(t, getWfRun(t, s, ws, r1.ID), store.WorkflowRunning, "prices=succeeded", "fx=succeeded", "analytics=pending")
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["analytics"], store.TaskSucceeded, nil)
	wantSteps(t, getWfRun(t, s, ws, r1.ID), store.WorkflowSucceeded, "prices=succeeded", "fx=succeeded", "analytics=succeeded")
}

func testWorkflowOrder(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ws := workspace.Default
	// a → (b, c) → d → e
	putWorkflow(t, s, ws, "dag", step("a"), step("b", "a"), step("c", "a"), step("d", "b", "c"), step("e", "d"))
	r := startRun(t, s, ws, "dag")
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowRunning,
		"a=pending", "b=blocked", "c=blocked", "d=blocked", "e=blocked")

	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["a"], store.TaskSucceeded, nil)
	tasks := claimSteps(t, s, ws, store.WorkflowQueue)
	if len(tasks) != 2 {
		t.Fatalf("after a, claimed %v; want b and c together", slices.Collect(mapKeys(tasks)))
	}
	// d waits for both; the first to finish doesn't start it.
	deliver(t, s, tasks["b"], store.TaskSucceeded, nil)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowRunning,
		"a=succeeded", "b=succeeded", "c=pending", "d=blocked", "e=blocked")
	// A retried attempt of c is not c finishing.
	deliver(t, s, tasks["c"], store.TaskPending, nil)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowRunning,
		"a=succeeded", "b=succeeded", "c=pending", "d=blocked", "e=blocked")
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["c"], store.TaskSucceeded, nil)

	tasks = claimSteps(t, s, ws, store.WorkflowQueue)
	if len(tasks) != 1 || tasks["d"].ID == "" {
		t.Fatalf("after b and c, claimed %v; want d once", slices.Collect(mapKeys(tasks)))
	}
	deliver(t, s, tasks["d"], store.TaskSucceeded, nil)
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["e"], store.TaskSucceeded, nil)
	run := getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowSucceeded,
		"a=succeeded", "b=succeeded", "c=succeeded", "d=succeeded", "e=succeeded")
	if run.FinishedAt == nil {
		t.Fatal("finished run has no finished_at")
	}
	for _, st := range run.Steps {
		if st.ReadyAt == nil || st.HTTPDoneAt == nil || st.FinishedAt == nil {
			t.Fatalf("step %s timings = %v %v %v", st.Name, st.ReadyAt, st.HTTPDoneAt, st.FinishedAt)
		}
	}
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func testWorkflowFailure(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx, ws := t.Context(), workspace.Default
	// a and b start together; c waits for b; d for c.
	putWorkflow(t, s, ws, "fail", step("a"), step("b"), step("c", "b"), step("d", "c"), step("e"))
	r := startRun(t, s, ws, "fail")
	tasks := claimSteps(t, s, ws, store.WorkflowQueue)
	if len(tasks) != 3 {
		t.Fatalf("claimed %d, want a, b and e", len(tasks))
	}
	// e is due again later (a retry), so it is pending but not running when
	// b fails.
	deliver(t, s, tasks["e"], store.TaskPending, nil)
	deliver(t, s, tasks["b"], store.TaskFailed, nil)
	run := getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowFailed, "a=pending", "b=failed", "c=skipped", "d=skipped", "e=skipped")
	if !strings.Contains(run.Error, `step "b" failed: HTTP 500`) || run.Steps[1].Error != "HTTP 500" {
		t.Fatalf("run error %q, step error %q", run.Error, run.Steps[1].Error)
	}
	if e := run.Steps[4].Task; e == nil || e.State != store.TaskCancelled {
		t.Fatalf("e's pending task = %+v, want cancelled", e)
	}
	// a was being delivered: it finishes and is recorded, but starts nothing.
	deliver(t, s, tasks["a"], store.TaskSucceeded, nil)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowFailed, "a=succeeded", "b=failed", "c=skipped", "d=skipped", "e=skipped")
	if got := claimSteps(t, s, ws, store.WorkflowQueue); len(got) != 0 {
		t.Fatalf("a failed run started %d more tasks", len(got))
	}
	if _, err := s.CancelWorkflowRun(ctx, ws, r.ID); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("CancelWorkflowRun of a failed run = %v, want ErrInvalidState", err)
	}

	// Cancelling a step's task fails it, and so the run.
	r = startRun(t, s, ws, "fail")
	run = getWfRun(t, s, ws, r.ID)
	if _, err := s.CancelTask(ctx, ws, run.Steps[0].TaskID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	run = getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowFailed, "a=failed", "b=skipped", "c=skipped", "d=skipped", "e=skipped")
	if run.Steps[0].Error != "its task was cancelled" {
		t.Fatalf("step error = %q", run.Steps[0].Error)
	}

	// Cancelling the run.
	r = startRun(t, s, ws, "fail")
	got, err := s.CancelWorkflowRun(ctx, ws, r.ID)
	if err != nil {
		t.Fatalf("CancelWorkflowRun: %v", err)
	}
	wantSteps(t, got, store.WorkflowCancelled, "a=skipped", "b=skipped", "c=skipped", "d=skipped", "e=skipped")
	if _, err := s.CancelWorkflowRun(ctx, ws, r.ID); err != nil {
		t.Fatalf("CancelWorkflowRun again: %v", err)
	}
	if _, err := s.CancelWorkflowRun(ctx, otherWorkspace, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CancelWorkflowRun in another workspace = %v", err)
	}
}

func testWorkflowQueues(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx, ws := t.Context(), workspace.Default
	if _, _, err := s.PutQueue(ctx, ws, "prices", store.DefaultQueueConfig()); err != nil {
		t.Fatalf("PutQueue: %v", err)
	}
	a := step("a")
	a.Queue = "prices"
	b := step("b", "a")
	b.Queue = "missing"
	b.Body = []byte("own body")
	putWorkflow(t, s, ws, "queues", a, b)
	r := startRun(t, s, ws, "queues")
	tasks := claimSteps(t, s, ws, "prices")
	deliver(t, s, tasks["a"], store.TaskSucceeded, nil)
	// b's queue doesn't exist, and only _workflows is created on demand.
	run := getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowFailed, "a=succeeded", "b=failed")
	if run.Steps[1].Error != `queue "missing" does not exist` {
		t.Fatalf("step error = %q", run.Steps[1].Error)
	}

	// Deleting a queue fails the steps whose tasks it held.
	b.Queue = "prices"
	putWorkflow(t, s, ws, "queues", a, b)
	r = startRun(t, s, ws, "queues")
	if err := s.DeleteQueue(ctx, ws, "prices"); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	run = getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowFailed, "a=failed", "b=skipped")
	if run.Steps[0].Task != nil {
		t.Fatalf("deleted task still returned: %+v", run.Steps[0].Task)
	}
}

func testWorkflowWaitFor(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx, ws := t.Context(), workspace.Default
	putWorkflow(t, s, ws, "wait", step("prices"), step("analytics", "prices"))
	waitFor := func(g store.Group) map[string]string {
		return map[string]string{store.WaitForHeader: "group/" + g.ID}
	}

	// An open group with tasks: the step waits until it is closed and done.
	r := startRun(t, s, ws, "wait")
	g := mustCreateGroup(t, s, ws, store.NewGroup{OnComplete: onComplete()})
	member := mustCreate(t, s, ws, store.NewTask{Queue: "members", GroupID: g.ID})
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["prices"], store.TaskSucceeded, waitFor(g))
	run := getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowRunning, "prices=waiting", "analytics=blocked")
	if st := run.Steps[0]; st.WaitType != "group" || st.WaitID != g.ID || st.HTTPDoneAt == nil || st.FinishedAt != nil {
		t.Fatalf("waiting step = %+v", st)
	}
	attempt(t, s, member, store.TaskSucceeded)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowRunning, "prices=waiting", "analytics=blocked")
	closeGroup(t, s, g)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowRunning, "prices=succeeded", "analytics=pending")
	// The group's callback is enqueued as well.
	if cbs := callbacks(t, s, ws); len(cbs) != 1 {
		t.Fatalf("%d callbacks, want the group's", len(cbs))
	}
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["analytics"], store.TaskSucceeded, nil)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowSucceeded, "prices=succeeded", "analytics=succeeded")

	// A closed group whose last task fails, after a retry: the step fails.
	r = startRun(t, s, ws, "wait")
	g = mustCreateGroup(t, s, ws, store.NewGroup{})
	member = mustCreate(t, s, ws, store.NewTask{Queue: "members", GroupID: g.ID})
	closeGroup(t, s, g)
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["prices"], store.TaskSucceeded, waitFor(g))
	attempt(t, s, member, store.TaskPending)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowRunning, "prices=waiting", "analytics=blocked")
	attempt(t, s, member, store.TaskFailed)
	run = getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowFailed, "prices=failed", "analytics=skipped")
	if want := "group " + g.ID + " failed: 0 of 1 tasks succeeded, 1 failed, 0 cancelled"; run.Steps[0].Error != want {
		t.Fatalf("step error = %q, want %q", run.Steps[0].Error, want)
	}

	// A group that had already completed: the step finishes at once.
	r = startRun(t, s, ws, "wait")
	g = closeGroup(t, s, mustCreateGroup(t, s, ws, store.NewGroup{})) // empty, so succeeded
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["prices"], store.TaskSucceeded, waitFor(g))
	run = getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowRunning, "prices=succeeded", "analytics=pending")
	if run.Steps[0].WaitID != g.ID {
		t.Fatalf("step that found its group complete has wait_id %q, want %s", run.Steps[0].WaitID, g.ID)
	}
	claimSteps(t, s, ws, store.WorkflowQueue)

	// Invalid headers fail the step, without retrying it.
	other := mustCreateGroup(t, s, otherWorkspace, store.NewGroup{})
	for _, tt := range []struct{ header, want string }{
		{"grp_123", `want "<type>/<id>"`},
		{"task/task_123", `unsupported resource type "task"`},
		{"group/" + g.ID + ", group/" + g.ID, "only one resource"},
		{"group/grp_missing", `group "grp_missing" does not exist`},
		{"group/" + other.ID, "does not exist"}, // another workspace's
	} {
		r := startRun(t, s, ws, "wait")
		task := claimSteps(t, s, ws, store.WorkflowQueue)["prices"]
		deliver(t, s, task, store.TaskSucceeded, map[string]string{"workly-wait-for": tt.header})
		run := getWfRun(t, s, ws, r.ID)
		wantSteps(t, run, store.WorkflowFailed, "prices=failed", "analytics=skipped")
		if !strings.Contains(run.Steps[0].Error, tt.want) {
			t.Errorf("header %q: step error %q, want %q", tt.header, run.Steps[0].Error, tt.want)
		}
		if run.Steps[0].Task.State != store.TaskSucceeded || run.Steps[0].Task.AttemptCount != 1 {
			t.Errorf("header %q: task %s after %d attempts, want succeeded once", tt.header, run.Steps[0].Task.State, run.Steps[0].Task.AttemptCount)
		}
	}

	// Cancelling a run with a waiting step skips it; the group goes on.
	r = startRun(t, s, ws, "wait")
	g = mustCreateGroup(t, s, ws, store.NewGroup{})
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["prices"], store.TaskSucceeded, waitFor(g))
	run, err := s.CancelWorkflowRun(ctx, ws, r.ID)
	if err != nil {
		t.Fatalf("CancelWorkflowRun: %v", err)
	}
	wantSteps(t, run, store.WorkflowCancelled, "prices=skipped", "analytics=skipped")
	closeGroup(t, s, g)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowCancelled, "prices=skipped", "analytics=skipped")

	// Another step failing skips a waiting one too, and its group finishing
	// later changes nothing.
	putWorkflow(t, s, ws, "wait-fail", step("prices"), step("fx"), step("analytics", "prices", "fx"))
	r = startRun(t, s, ws, "wait-fail")
	g = mustCreateGroup(t, s, ws, store.NewGroup{})
	tasks := claimSteps(t, s, ws, store.WorkflowQueue)
	deliver(t, s, tasks["prices"], store.TaskSucceeded, waitFor(g))
	deliver(t, s, tasks["fx"], store.TaskFailed, nil)
	run = getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowFailed, "prices=skipped", "fx=failed", "analytics=skipped")
	if st := run.Steps[0]; st.FinishedAt == nil || st.Error == "" {
		t.Fatalf("skipped waiting step = %+v", st)
	}
	closeGroup(t, s, g)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowFailed, "prices=skipped", "fx=failed", "analytics=skipped")
}

// testWorkflowWaitRace closes groups while the steps naming them are being
// recorded, in concurrent transactions: each step must end up finished,
// whichever commits first.
func testWorkflowWaitRace(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ws := workspace.Default
	putWorkflow(t, s, ws, "race", step("prices"))
	const n = 20
	var (
		runs   []store.WorkflowRun
		groups []store.Group
	)
	for range n {
		runs = append(runs, startRun(t, s, ws, "race"))
		groups = append(groups, mustCreateGroup(t, s, ws, store.NewGroup{}))
	}
	tasks := claimAll(t, s, ws, store.WorkflowQueue)
	byRun := map[string]store.Task{}
	for _, task := range tasks {
		byRun[task.Headers["Workly-Workflow-Run-Id"]] = task
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			deliver(t, s, byRun[runs[i].ID], store.TaskSucceeded, map[string]string{store.WaitForHeader: "group/" + groups[i].ID})
		})
		wg.Go(func() {
			if _, err := s.CloseGroup(t.Context(), ws, groups[i].ID); err != nil {
				t.Errorf("CloseGroup: %v", err)
			}
		})
	}
	wg.Wait()
	for _, r := range runs {
		wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowSucceeded, "prices=succeeded")
	}
}

func testWorkflowSnapshot(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ws := workspace.Default
	putWorkflow(t, s, ws, "v", step("a"), step("b", "a"))
	old := startRun(t, s, ws, "v")
	putWorkflow(t, s, ws, "v", step("a"), step("c", "a"))
	fresh := startRun(t, s, ws, "v")
	if old.Version != 1 || fresh.Version != 2 {
		t.Fatalf("versions = %d, %d", old.Version, fresh.Version)
	}
	for _, task := range claimAll(t, s, ws, store.WorkflowQueue) {
		deliver(t, s, task, store.TaskSucceeded, nil)
	}
	wantSteps(t, getWfRun(t, s, ws, old.ID), store.WorkflowRunning, "a=succeeded", "b=pending")
	wantSteps(t, getWfRun(t, s, ws, fresh.ID), store.WorkflowRunning, "a=succeeded", "c=pending")
}

func testExpireWorkflowRuns(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx, ws := t.Context(), workspace.Default
	putWorkflow(t, s, ws, "slow", step("prices"), step("analytics", "prices"))
	r := startRun(t, s, ws, "slow")
	g := mustCreateGroup(t, s, ws, store.NewGroup{})
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["prices"], store.TaskSucceeded,
		map[string]string{store.WaitForHeader: "group/" + g.ID})

	if n, err := s.ExpireWorkflowRuns(ctx, time.Now(), 10); err != nil || n != 0 {
		t.Fatalf("ExpireWorkflowRuns before the deadline = %d, %v", n, err)
	}
	n, err := s.ExpireWorkflowRuns(ctx, time.Now().Add(25*time.Hour), 10)
	if err != nil || n != 1 {
		t.Fatalf("ExpireWorkflowRuns = %d, %v", n, err)
	}
	run := getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowFailed, "prices=failed", "analytics=skipped")
	if !strings.Contains(run.Error, "max_duration of 24h0m0s") {
		t.Fatalf("run error = %q", run.Error)
	}
}

func testDeleteFinishedWorkflowRuns(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx, ws := t.Context(), workspace.Default
	putWorkflow(t, s, ws, "sweep", step("a"))
	var runs []store.WorkflowRun
	for range 3 {
		r := startRun(t, s, ws, "sweep")
		if _, err := s.CancelWorkflowRun(ctx, ws, r.ID); err != nil {
			t.Fatalf("CancelWorkflowRun: %v", err)
		}
		runs = append(runs, r)
	}
	running := startRun(t, s, ws, "sweep")
	n, err := s.DeleteFinishedWorkflowRuns(ctx, store.Sweep{Before: time.Now().Add(time.Hour)}, 10)
	if err != nil || n != 3 {
		// Only the latest run is kept, and it is running anyway.
		t.Fatalf("DeleteFinishedWorkflowRuns = %d, %v; want 3", n, err)
	}
	if _, err := s.GetWorkflowRun(ctx, ws, runs[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("swept run = %v", err)
	}
	getWfRun(t, s, ws, running.ID)
}

// testScheduledWorkflowOverlap: a schedule that starts a workflow overlaps
// its last slot while the workflow run that slot started is still going,
// though the schedule's own run finished as soon as it started it.
func testScheduledWorkflowOverlap(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx, ws := t.Context(), workspace.Default
	putWorkflow(t, s, ws, "nightly", step("only"))
	cfg := scheduleConfig()
	cfg.Workflow = "nightly"
	sc, _, err := s.PutSchedule(ctx, ws, "nightly-wf", cfg, now().Add(-time.Second))
	if err != nil {
		t.Fatalf("PutSchedule: %v", err)
	}

	// The slot's run starts the workflow run, and succeeds.
	r := fire(t, s, sc, "")
	if claims := claimRuns(t, s); len(claims) != 1 || claims[0].Run.ID != r.ID {
		t.Fatalf("claimed %+v, want run %s", claims, r.ID)
	}
	wr, _, err := s.StartWorkflowRun(ctx, ws, "nightly", store.NewWorkflowRun{IdempotencyKey: r.ID, ScheduleRunID: r.ID}, now())
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	finishRun(t, s, getRun(t, s, r), store.RunSucceeded)

	// The next slot comes up while the workflow run is going: skipped.
	if next := fire(t, s, sc, ""); next.State != store.RunSkipped || next.SkipReason != store.SkipOverlap {
		t.Fatalf("slot during the workflow run = %+v, want skipped for overlap", next)
	}
	// A workflow run started some other way doesn't count.
	startRun(t, s, ws, "nightly")
	if _, err := s.CancelWorkflowRun(ctx, ws, wr.ID); err != nil {
		t.Fatalf("CancelWorkflowRun: %v", err)
	}
	if next := fire(t, s, sc, ""); next.State != store.RunPending {
		t.Fatalf("slot after the workflow run = %+v, want pending", next)
	}
}

func retryRun(t *testing.T, s store.Store, ws workspace.ID, id string) store.WorkflowRun {
	t.Helper()
	r, err := s.RetryWorkflowRun(t.Context(), ws, id, now())
	if err != nil {
		t.Fatalf("RetryWorkflowRun: %v", err)
	}
	return r
}

// testRetryWorkflowRun: a failed or cancelled run reopens in place, under
// the definition it started with; succeeded steps stay, the rest run
// again on the tasks they had.
func testRetryWorkflowRun(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ctx, ws := t.Context(), workspace.Default
	putWorkflow(t, s, ws, "fail", step("a"), step("b"), step("c", "b"), step("d", "c"), step("e"))
	r := startRun(t, s, ws, "fail")
	tasks := claimSteps(t, s, ws, store.WorkflowQueue)
	deliver(t, s, tasks["e"], store.TaskPending, nil)
	deliver(t, s, tasks["b"], store.TaskFailed, nil)
	failed := getWfRun(t, s, ws, r.ID)
	wantSteps(t, failed, store.WorkflowFailed, "a=pending", "b=failed", "c=skipped", "d=skipped", "e=skipped")

	// Changing the workflow doesn't change what the retry runs.
	putWorkflow(t, s, ws, "fail", step("other"))
	if _, err := s.RetryWorkflowRun(ctx, otherWorkspace, r.ID, now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RetryWorkflowRun in another workspace = %v, want ErrNotFound", err)
	}
	run := retryRun(t, s, ws, r.ID)
	// a's request is still in flight from the first attempt: it stays pending.
	wantSteps(t, run, store.WorkflowRunning, "a=pending", "b=pending", "c=blocked", "d=blocked", "e=pending")
	if run.Attempt != 2 || run.RetriedAt == nil || run.Error != "" || run.FinishedAt != nil ||
		!run.DeadlineAt.After(failed.DeadlineAt.Add(-time.Second)) {
		t.Fatalf("retried run = %+v", run)
	}
	if _, err := s.RetryWorkflowRun(ctx, ws, r.ID, now()); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("RetryWorkflowRun of a running run = %v, want ErrInvalidState", err)
	}
	// b and e keep their tasks, pending again with a fresh set of attempts
	// and the run's new attempt.
	retried := claimSteps(t, s, ws, store.WorkflowQueue)
	if len(retried) != 2 || retried["b"].ID != tasks["b"].ID || retried["e"].ID != tasks["e"].ID {
		t.Fatalf("claimed %v after the retry, want b's and e's tasks again", slices.Collect(mapKeys(retried)))
	}
	b := retried["b"]
	var body store.WorkflowStepBody
	if err := json.Unmarshal(b.Body, &body); err != nil || body.RunAttempt != 2 ||
		b.Headers[store.HeaderWorkflowRunAttempt] != "2" || b.RetriedAt == nil || b.ReplayedAttempts != 1 {
		t.Fatalf("b's task after the retry = %+v, body %s", b, b.Body)
	}
	deliver(t, s, tasks["a"], store.TaskSucceeded, nil)
	deliver(t, s, b, store.TaskSucceeded, nil)
	deliver(t, s, retried["e"], store.TaskSucceeded, nil)
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["c"], store.TaskSucceeded, nil)
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["d"], store.TaskSucceeded, nil)
	run = getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowSucceeded, "a=succeeded", "b=succeeded", "c=succeeded", "d=succeeded", "e=succeeded")
	if _, err := s.RetryWorkflowRun(ctx, ws, r.ID, now()); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("RetryWorkflowRun of a succeeded run = %v, want ErrInvalidState", err)
	}

	// A cancelled run starts over from its first steps, whose cancelled
	// tasks come back.
	r = startRun(t, s, ws, "fail")
	if _, err := s.CancelWorkflowRun(ctx, ws, r.ID); err != nil {
		t.Fatalf("CancelWorkflowRun: %v", err)
	}
	run = retryRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowRunning, "other=pending")
}

// testRetryWorkflowRunWaitFor: a step that was waiting for a group when
// its run stopped follows the group when the run is retried.
func testRetryWorkflowRunWaitFor(t *testing.T, open func() store.Store) {
	s := seeded(t, open)
	ws := workspace.Default
	putWorkflow(t, s, ws, "wait", step("prices"), step("fx"), step("analytics", "prices", "fx"))
	waitFor := func(g store.Group) map[string]string {
		return map[string]string{store.WaitForHeader: "group/" + g.ID}
	}
	// failWhileWaiting runs prices up to waiting for g, then fails fx.
	failWhileWaiting := func(g store.Group) (store.WorkflowRun, map[string]store.Task) {
		r := startRun(t, s, ws, "wait")
		tasks := claimSteps(t, s, ws, store.WorkflowQueue)
		deliver(t, s, tasks["prices"], store.TaskSucceeded, waitFor(g))
		deliver(t, s, tasks["fx"], store.TaskFailed, nil)
		wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowFailed, "prices=skipped", "fx=failed", "analytics=skipped")
		return r, tasks
	}

	// The group is still going: prices waits for it again.
	g := mustCreateGroup(t, s, ws, store.NewGroup{})
	member := mustCreate(t, s, ws, store.NewTask{Queue: "members", GroupID: g.ID})
	r, tasks := failWhileWaiting(g)
	wantSteps(t, retryRun(t, s, ws, r.ID), store.WorkflowRunning, "prices=waiting", "fx=pending", "analytics=blocked")
	closeGroup(t, s, g)
	attempt(t, s, member, store.TaskSucceeded)
	deliver(t, s, claimSteps(t, s, ws, store.WorkflowQueue)["fx"], store.TaskSucceeded, nil)
	wantSteps(t, getWfRun(t, s, ws, r.ID), store.WorkflowRunning, "prices=succeeded", "fx=succeeded", "analytics=pending")
	if again := claimSteps(t, s, ws, store.WorkflowQueue); again["prices"].ID != "" || tasks["prices"].ID == "" {
		t.Fatalf("prices was delivered again: %v", slices.Collect(mapKeys(again)))
	}

	// The group succeeded since: prices is done, and only fx runs again.
	g = mustCreateGroup(t, s, ws, store.NewGroup{})
	r, _ = failWhileWaiting(g)
	closeGroup(t, s, g)
	wantSteps(t, retryRun(t, s, ws, r.ID), store.WorkflowRunning, "prices=succeeded", "fx=pending", "analytics=blocked")

	// The group failed: prices runs again, its request on its old task.
	g = mustCreateGroup(t, s, ws, store.NewGroup{})
	member = mustCreate(t, s, ws, store.NewTask{Queue: "members", GroupID: g.ID})
	r, tasks = failWhileWaiting(g)
	closeGroup(t, s, g)
	attempt(t, s, member, store.TaskFailed)
	wantSteps(t, retryRun(t, s, ws, r.ID), store.WorkflowRunning, "prices=pending", "fx=pending", "analytics=blocked")
	if again := claimSteps(t, s, ws, store.WorkflowQueue); again["prices"].ID != tasks["prices"].ID {
		t.Fatalf("prices's task after the retry = %q, want %q", again["prices"].ID, tasks["prices"].ID)
	}

	// A request in flight when the run failed hands over to a group: the
	// step is skipped, not left waiting, and a retry picks the group up.
	g = mustCreateGroup(t, s, ws, store.NewGroup{})
	mustCreate(t, s, ws, store.NewTask{Queue: "members", GroupID: g.ID})
	r = startRun(t, s, ws, "wait")
	tasks = claimSteps(t, s, ws, store.WorkflowQueue)
	deliver(t, s, tasks["fx"], store.TaskFailed, nil)
	deliver(t, s, tasks["prices"], store.TaskSucceeded, waitFor(g))
	run := getWfRun(t, s, ws, r.ID)
	wantSteps(t, run, store.WorkflowFailed, "prices=skipped", "fx=failed", "analytics=skipped")
	if run.Steps[0].WaitID != g.ID {
		t.Fatalf("skipped step's wait_id = %q, want %s", run.Steps[0].WaitID, g.ID)
	}
	wantSteps(t, retryRun(t, s, ws, r.ID), store.WorkflowRunning, "prices=waiting", "fx=pending", "analytics=blocked")
}
