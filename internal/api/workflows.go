package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/store"
)

func workflowNotFound(name string) error {
	return &apiError{http.StatusNotFound, "workflow_not_found", fmt.Sprintf("workflow %q does not exist", name)}
}

func workflowRunNotFound(id string) error {
	return &apiError{http.StatusNotFound, "workflow_run_not_found", fmt.Sprintf("workflow run %q does not exist", id)}
}

// WorkflowQueue is the queue workflow steps go to unless they name one.
const WorkflowQueue = store.WorkflowQueue

// workflowConfig validates a workflow definition: every step's request,
// and that the steps form a DAG.
func workflowConfig(in WorkflowConfig, maxBody int64) (store.WorkflowConfig, error) {
	cfg := store.WorkflowConfig{MaxDuration: store.DefaultWorkflowMaxDuration}
	if in.MaxDurationSeconds != nil {
		if *in.MaxDurationSeconds < 1 {
			return cfg, invalid("max_duration_seconds must be at least 1")
		}
		cfg.MaxDuration = duration(*in.MaxDurationSeconds)
	}
	switch {
	case len(in.Steps) == 0:
		return cfg, invalid("a workflow needs at least one step")
	case len(in.Steps) > store.MaxWorkflowSteps:
		return cfg, invalid("a workflow has at most %d steps", store.MaxWorkflowSteps)
	}
	names := map[string]bool{}
	for i, in := range in.Steps {
		sc, err := workflowStep(in, maxBody)
		if err != nil {
			return cfg, inField(fmt.Sprintf("steps[%d]", i), err)
		}
		if names[sc.Name] {
			return cfg, invalid("steps[%d]: duplicate step name %q", i, sc.Name)
		}
		names[sc.Name] = true
		cfg.Steps = append(cfg.Steps, sc)
	}
	for i, sc := range cfg.Steps {
		for _, dep := range sc.DependsOn {
			switch {
			case dep == sc.Name:
				return cfg, invalid("steps[%d]: step %q depends on itself", i, sc.Name)
			case !names[dep]:
				return cfg, invalid("steps[%d]: step %q depends on %q, which is not a step", i, sc.Name, dep)
			}
		}
	}
	if cycle := findCycle(cfg.Steps); cycle != nil {
		return cfg, invalid("steps form a cycle: %s", strings.Join(cycle, " → "))
	}
	return cfg, nil
}

func workflowStep(in WorkflowStepConfig, maxBody int64) (store.WorkflowStepConfig, error) {
	sc := store.WorkflowStepConfig{Name: in.Name, Queue: WorkflowQueue}
	if err := validateName("step", in.Name); err != nil {
		return sc, err
	}
	r, err := validateRequest(request{in.Url, in.Method, in.Headers, in.Payload, in.Body, in.ContentType}, maxBody)
	if err != nil {
		return sc, err
	}
	sc.Method, sc.URL, sc.Headers = r.method, r.url, r.headers
	if hasBody(in) {
		sc.Body = r.body
		if sc.Body == nil {
			sc.Body = []byte{}
		}
	}
	if len(sc.Headers) == 0 {
		sc.Headers = nil
	}
	if in.DependsOn != nil {
		seen := map[string]bool{}
		for _, dep := range *in.DependsOn {
			if !seen[dep] {
				sc.DependsOn = append(sc.DependsOn, dep)
				seen[dep] = true
			}
		}
	}
	if in.Queue != nil && *in.Queue != WorkflowQueue {
		if err := validateQueueName(*in.Queue); err != nil {
			return sc, err
		}
		if Reserved(*in.Queue) {
			return sc, invalid("queue %q is reserved: steps go to %s or a queue of your own", *in.Queue, WorkflowQueue)
		}
		sc.Queue = *in.Queue
	}
	if in.MaxAttempts != nil {
		if *in.MaxAttempts < 1 || *in.MaxAttempts > maxAttemptsLimit {
			return sc, invalid("max_attempts must be between 1 and %d", maxAttemptsLimit)
		}
		sc.MaxAttempts = in.MaxAttempts
	}
	if in.TimeoutSeconds != nil {
		d := duration(*in.TimeoutSeconds)
		if d < minTimeout || d > maxTimeout {
			return sc, invalid("timeout_seconds must be between %d and %d", int(minTimeout.Seconds()), int(maxTimeout.Seconds()))
		}
		sc.Timeout = &d
	}
	return sc, nil
}

// hasBody reports whether a step sets its own body; one that doesn't is
// sent the run's context.
func hasBody(in WorkflowStepConfig) bool {
	return (in.Payload != nil && string(*in.Payload) != "null") || in.Body != nil
}

// findCycle returns the steps of a dependency cycle, first step repeated
// at the end, or nil if the steps form a DAG.
func findCycle(steps []store.WorkflowStepConfig) []string {
	deps := map[string][]string{}
	for _, sc := range steps {
		deps[sc.Name] = sc.DependsOn
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var path []string
	var visit func(string) []string
	visit = func(name string) []string {
		switch state[name] {
		case visiting:
			i := slices.Index(path, name)
			return append(slices.Clone(path[i:]), name)
		case done:
			return nil
		}
		state[name] = visiting
		path = append(path, name)
		for _, dep := range deps[name] {
			if c := visit(dep); c != nil {
				return c
			}
		}
		path = path[:len(path)-1]
		state[name] = done
		return nil
	}
	for _, sc := range steps {
		if c := visit(sc.Name); c != nil {
			// Found walking dependencies; read it in the order steps run.
			slices.Reverse(c)
			return c
		}
	}
	return nil
}

func toWorkflow(w store.Workflow) Workflow {
	out := Workflow{
		Name:               w.Name,
		Steps:              make([]WorkflowStepConfig, len(w.Steps)),
		MaxDurationSeconds: seconds(w.EffectiveMaxDuration()),
		Version:            w.Version,
		CreatedAt:          w.CreatedAt,
		UpdatedAt:          w.UpdatedAt,
	}
	for i, sc := range w.Steps {
		out.Steps[i] = toWorkflowStepConfig(sc)
	}
	if r := w.LastRun; r != nil {
		out.LastRun = &WorkflowRunSummary{
			Id: r.ID, Workflow: w.Name, Status: WorkflowRunStatus(r.Status), Error: optional(r.Error),
			CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt, DurationMs: runDuration(r.CreatedAt, r.FinishedAt),
		}
	}
	return out
}

func toWorkflowStepConfig(sc store.WorkflowStepConfig) WorkflowStepConfig {
	out := WorkflowStepConfig{
		Name: sc.Name, Url: sc.URL, Method: (*HTTPMethod)(&sc.Method), Queue: &sc.Queue,
		MaxAttempts: sc.MaxAttempts,
	}
	if len(sc.DependsOn) > 0 {
		deps := slices.Clone(sc.DependsOn)
		out.DependsOn = &deps
	}
	if len(sc.Headers) > 0 {
		h := sc.Headers
		out.Headers = &h
	}
	if sc.Body != nil {
		out.Payload, out.Body = bodyFields(sc.Headers, sc.Body)
		if out.Payload == nil && out.Body == nil {
			empty := ""
			out.Body = &empty
		}
		if out.Body != nil {
			// A body's content type is in its headers already, as
			// validateRequest set it.
			out.ContentType = nil
		}
	}
	if sc.Timeout != nil {
		s := seconds(*sc.Timeout)
		out.TimeoutSeconds = &s
	}
	return out
}

// runDuration is from start to finish, or to now while running.
func runDuration(start time.Time, finished *time.Time) *int {
	end := time.Now()
	if finished != nil {
		end = *finished
	}
	ms := int(end.Sub(start).Milliseconds())
	return &ms
}

func toWorkflowRunSummary(r store.WorkflowRun) WorkflowRunSummary {
	deadline := r.DeadlineAt
	return WorkflowRunSummary{
		Id: r.ID, Workflow: r.Workflow, Version: r.Version, Status: WorkflowRunStatus(r.Status),
		Error: optional(r.Error), IdempotencyKey: optional(r.IdempotencyKey), ScheduleRunId: optional(r.ScheduleRunID),
		CreatedAt: r.CreatedAt, DeadlineAt: &deadline, FinishedAt: r.FinishedAt,
		DurationMs: runDuration(r.CreatedAt, r.FinishedAt),
	}
}

func toWorkflowRun(r store.WorkflowRun) WorkflowRun {
	s := toWorkflowRunSummary(r)
	out := WorkflowRun{
		Id: s.Id, Workflow: s.Workflow, Version: s.Version, Status: s.Status, Error: s.Error,
		IdempotencyKey: s.IdempotencyKey, ScheduleRunId: s.ScheduleRunId, CreatedAt: s.CreatedAt,
		DeadlineAt: s.DeadlineAt, FinishedAt: s.FinishedAt, DurationMs: s.DurationMs,
		Steps: make([]WorkflowStep, len(r.Steps)),
	}
	if len(r.Input) > 0 {
		in := json.RawMessage(r.Input)
		out.Input = &in
	}
	succeeded := map[string]bool{}
	for _, st := range r.Steps {
		succeeded[st.Name] = st.Status == store.StepSucceeded
	}
	for i, st := range r.Steps {
		out.Steps[i] = toWorkflowStep(st, succeeded)
	}
	return out
}

func toWorkflowStep(st store.WorkflowStep, succeeded map[string]bool) WorkflowStep {
	out := WorkflowStep{
		Id: st.ID, Name: st.Name, Status: StepStatus(st.Status),
		DependsOn: nonNilStrings(st.DependsOn), BlockedBy: []string{},
		TaskId: optional(st.TaskID), Error: optional(st.Error), CreatedAt: &st.CreatedAt,
		ReadyAt: st.ReadyAt, HttpDoneAt: st.HTTPDoneAt, FinishedAt: st.FinishedAt,
	}
	if st.Status == store.StepBlocked {
		for _, dep := range st.DependsOn {
			if !succeeded[dep] {
				out.BlockedBy = append(out.BlockedBy, dep)
			}
		}
	}
	// Running is the task's state, not the step's: the store never has to
	// touch a step to claim its task.
	if t := st.Task; t != nil {
		if st.Status == store.StepPending && t.State == store.TaskRunning {
			out.Status = "running"
		}
		task := WorkflowStepTask{
			Id: t.ID, Queue: t.Queue, State: TaskState(t.State), AttemptCount: t.AttemptCount,
			LastStatus: t.LastStatus, LastError: optional(t.LastError),
		}
		if t.LastOutcome != "" {
			o := Outcome(t.LastOutcome)
			task.LastOutcome = &o
		}
		if t.State == store.TaskPending {
			runAt := t.RunAt
			task.RunAt = &runAt
		}
		out.Task = &task
	}
	if st.WaitID != "" {
		out.WaitFor = &WaitFor{Type: WaitForType(st.WaitType), Id: st.WaitID}
	}
	end := time.Now()
	if st.ReadyAt != nil {
		out.BlockedMs = msBetween(st.CreatedAt, *st.ReadyAt)
		if st.FinishedAt != nil {
			end = *st.FinishedAt
		}
		out.DurationMs = msBetween(*st.ReadyAt, end)
	} else if st.Status == store.StepBlocked {
		out.BlockedMs = msBetween(st.CreatedAt, end)
	}
	if st.HTTPDoneAt != nil && st.WaitID != "" {
		out.WaitingMs = msBetween(*st.HTTPDoneAt, end)
	}
	return out
}

func msBetween(a, b time.Time) *int {
	ms := int(b.Sub(a).Milliseconds())
	return &ms
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (h Handlers) ListWorkflows(ctx context.Context, _ ListWorkflowsRequestObject) (ListWorkflowsResponseObject, error) {
	ws, err := h.Store.ListWorkflows(ctx, workspaceID(ctx))
	if err != nil {
		return nil, err
	}
	out := ListWorkflows200JSONResponse{Workflows: make([]Workflow, len(ws))}
	for i, w := range ws {
		out.Workflows[i] = toWorkflow(w)
	}
	return out, nil
}

func (h Handlers) GetWorkflow(ctx context.Context, req GetWorkflowRequestObject) (GetWorkflowResponseObject, error) {
	w, err := h.Store.GetWorkflow(ctx, workspaceID(ctx), req.Workflow)
	if errors.Is(err, store.ErrNotFound) {
		return nil, workflowNotFound(req.Workflow)
	}
	if err != nil {
		return nil, err
	}
	return GetWorkflow200JSONResponse(toWorkflow(w)), nil
}

func (h Handlers) PutWorkflow(ctx context.Context, req PutWorkflowRequestObject) (PutWorkflowResponseObject, error) {
	if err := validateName("workflow", req.Workflow); err != nil {
		return nil, err
	}
	cfg, err := workflowConfig(*req.Body, h.MaxPayloadBytes)
	if err != nil {
		return nil, err
	}
	w, created, err := h.Store.PutWorkflow(ctx, workspaceID(ctx), req.Workflow, cfg)
	if err != nil {
		return nil, err
	}
	if created {
		return PutWorkflow201JSONResponse(toWorkflow(w)), nil
	}
	return PutWorkflow200JSONResponse(toWorkflow(w)), nil
}

func (h Handlers) DeleteWorkflow(ctx context.Context, req DeleteWorkflowRequestObject) (DeleteWorkflowResponseObject, error) {
	err := h.Store.DeleteWorkflow(ctx, workspaceID(ctx), req.Workflow)
	if errors.Is(err, store.ErrNotFound) {
		return nil, workflowNotFound(req.Workflow)
	}
	if err != nil {
		return nil, err
	}
	return DeleteWorkflow204Response{}, nil
}

func (h Handlers) StartWorkflowRun(ctx context.Context, req StartWorkflowRunRequestObject) (StartWorkflowRunResponseObject, error) {
	var nr store.NewWorkflowRun
	if in := req.Body; in != nil {
		if in.Input != nil && string(*in.Input) != "null" {
			if int64(len(*in.Input)) > h.MaxPayloadBytes {
				return nil, payloadTooLarge(h.MaxPayloadBytes)
			}
			nr.Input = *in.Input
		}
		nr.IdempotencyKey = deref(in.IdempotencyKey)
		if len(nr.IdempotencyKey) > maxIdempotencyKeyLength {
			return nil, invalid("idempotency_key must be at most %d bytes", maxIdempotencyKeyLength)
		}
	}
	r, created, err := h.startWorkflowRun(ctx, req.Workflow, nr)
	if err != nil {
		return nil, err
	}
	if created {
		return StartWorkflowRun201JSONResponse(toWorkflowRun(r)), nil
	}
	return StartWorkflowRun200JSONResponse(toWorkflowRun(r)), nil
}

// startWorkflowRun starts a run, as the API and schedules do. Each step is
// a task, so an Admission admits a run's steps as tasks.
func (h Handlers) startWorkflowRun(ctx context.Context, name string, nr store.NewWorkflowRun) (store.WorkflowRun, bool, error) {
	ws := workspaceID(ctx)
	w, err := h.Store.GetWorkflow(ctx, ws, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.WorkflowRun{}, false, workflowNotFound(name)
	}
	if err != nil {
		return store.WorkflowRun{}, false, err
	}
	if h.Admission != nil {
		if err := h.Admission.AdmitTasks(ctx, ws, len(w.Steps)); err != nil {
			return store.WorkflowRun{}, false, err
		}
	}
	r, created, err := h.Store.StartWorkflowRun(ctx, ws, name, nr, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return store.WorkflowRun{}, false, workflowNotFound(name)
	}
	if err != nil {
		return store.WorkflowRun{}, false, err
	}
	if created && h.Admission != nil {
		h.Admission.TasksCreated(ctx, ws, len(r.Config.Steps))
	}
	if r, err = h.Store.GetWorkflowRun(ctx, ws, r.ID); err != nil {
		return store.WorkflowRun{}, false, err
	}
	return r, created, nil
}

func (h Handlers) ListWorkflowRuns(ctx context.Context, req ListWorkflowRunsRequestObject) (ListWorkflowRunsResponseObject, error) {
	p := req.Params
	f := store.WorkflowRunFilter{Limit: 50, Workflow: deref(p.Workflow), Cursor: deref(p.Cursor)}
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > 200 {
			return nil, invalid("limit must be between 1 and 200")
		}
		f.Limit = *p.Limit
	}
	if p.Status != nil {
		switch s := store.WorkflowRunStatus(*p.Status); s {
		case store.WorkflowRunning, store.WorkflowSucceeded, store.WorkflowFailed, store.WorkflowCancelled:
			f.Status = s
		default:
			return nil, invalid("unknown status %q", s)
		}
	}
	runs, cursor, err := h.Store.ListWorkflowRuns(ctx, workspaceID(ctx), f)
	if err != nil {
		return nil, err
	}
	out := ListWorkflowRuns200JSONResponse{WorkflowRuns: make([]WorkflowRunSummary, len(runs)), NextCursor: optional(cursor)}
	for i, r := range runs {
		out.WorkflowRuns[i] = toWorkflowRunSummary(r)
	}
	return out, nil
}

func (h Handlers) GetWorkflowRun(ctx context.Context, req GetWorkflowRunRequestObject) (GetWorkflowRunResponseObject, error) {
	r, err := h.Store.GetWorkflowRun(ctx, workspaceID(ctx), req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, workflowRunNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	return GetWorkflowRun200JSONResponse(toWorkflowRun(r)), nil
}

func (h Handlers) CancelWorkflowRun(ctx context.Context, req CancelWorkflowRunRequestObject) (CancelWorkflowRunResponseObject, error) {
	r, err := h.Store.CancelWorkflowRun(ctx, workspaceID(ctx), req.Id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, workflowRunNotFound(req.Id)
	case errors.Is(err, store.ErrInvalidState):
		return nil, invalidState("workflow run %q already finished and can't be cancelled", req.Id)
	case err != nil:
		return nil, err
	}
	return CancelWorkflowRun200JSONResponse(toWorkflowRun(r)), nil
}
