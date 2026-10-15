package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/store"
)

func (h Handlers) CreateTask(ctx context.Context, req CreateTaskRequestObject) (CreateTaskResponseObject, error) {
	if err := validateQueueName(req.Queue); err != nil {
		return nil, err
	}
	var traceparent string
	if req.Params.Traceparent != nil {
		traceparent = *req.Params.Traceparent
	}
	nt, err := newTask(req.Queue, *req.Body, traceparent, h.MaxPayloadBytes, time.Now())
	if err != nil {
		return nil, err
	}
	ws := workspaceID(ctx)
	if h.Admission != nil {
		if err := h.Admission.AdmitTasks(ctx, ws, 1); err != nil {
			return nil, err
		}
	}
	task, created, err := h.Store.CreateTask(ctx, ws, nt, h.AutoCreateQueues)
	if errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	}
	if err != nil {
		return nil, err
	}
	if created && h.Admission != nil {
		h.Admission.TasksCreated(ctx, ws, 1)
	}
	if created {
		h.Metrics.TasksEnqueued.Inc(string(task.WorkspaceID), task.Queue)
		return CreateTask201JSONResponse(toTask(task)), nil
	}
	return CreateTask200JSONResponse(toTask(task)), nil
}

// maxBatch is the most tasks one batch enqueue accepts.
const maxBatch = 100

func (h Handlers) CreateTasks(ctx context.Context, req CreateTasksRequestObject) (CreateTasksResponseObject, error) {
	if err := validateQueueName(req.Queue); err != nil {
		return nil, err
	}
	if n := len(req.Body.Tasks); n < 1 || n > maxBatch {
		return nil, invalid("a batch must have between 1 and %d tasks, got %d", maxBatch, n)
	}
	var traceparent string
	if req.Params.Traceparent != nil {
		traceparent = *req.Params.Traceparent
	}
	now := time.Now()
	nts := make([]store.NewTask, len(req.Body.Tasks))
	for i, in := range req.Body.Tasks {
		nt, err := newTask(req.Queue, in, traceparent, h.MaxPayloadBytes, now)
		if err != nil {
			return nil, inTask(i, err)
		}
		nts[i] = nt
	}
	ws := workspaceID(ctx)
	if h.Admission != nil {
		if err := h.Admission.AdmitTasks(ctx, ws, len(nts)); err != nil {
			return nil, err
		}
	}
	tasks, created, err := h.Store.CreateTasks(ctx, ws, nts, h.AutoCreateQueues)
	if errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	}
	if err != nil {
		return nil, err
	}
	out := CreateTasks200JSONResponse{Results: make([]BatchEnqueueResult, len(tasks))}
	n := 0
	for i, t := range tasks {
		out.Results[i] = BatchEnqueueResult{Created: created[i], Task: toTask(t)}
		if created[i] {
			h.Metrics.TasksEnqueued.Inc(string(t.WorkspaceID), t.Queue)
			n++
		}
	}
	if n > 0 && h.Admission != nil {
		h.Admission.TasksCreated(ctx, ws, n)
	}
	return out, nil
}

func (h Handlers) ListTasks(ctx context.Context, req ListTasksRequestObject) (ListTasksResponseObject, error) {
	ws := workspaceID(ctx)
	f := store.TaskFilter{Queue: req.Queue, LastError: deref(req.Params.LastError)}
	if err := page(&f, req.Params.State, req.Params.Cursor, req.Params.Limit); err != nil {
		return nil, err
	}
	// Distinguish an empty queue from a missing one.
	if _, err := h.Store.GetQueue(ctx, ws, req.Queue); errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	} else if err != nil {
		return nil, err
	}
	tasks, err := h.listTasks(ctx, f)
	if err != nil {
		return nil, err
	}
	return ListTasks200JSONResponse(tasks), nil
}

func (h Handlers) SearchTasks(ctx context.Context, req SearchTasksRequestObject) (SearchTasksResponseObject, error) {
	p := req.Params
	var f store.TaskFilter
	if err := page(&f, p.State, p.Cursor, p.Limit); err != nil {
		return nil, err
	}
	if p.Queue != nil {
		f.Queue = *p.Queue
	}
	if p.IdempotencyKey != nil {
		f.IdempotencyKey = *p.IdempotencyKey
	}
	f.LastError = deref(p.LastError)
	if p.CreatedAfter != nil {
		f.CreatedAfter = *p.CreatedAfter
	}
	if p.CreatedBefore != nil {
		f.CreatedBefore = *p.CreatedBefore
	}
	tasks, err := h.listTasks(ctx, f)
	if err != nil {
		return nil, err
	}
	return SearchTasks200JSONResponse(tasks), nil
}

// page applies the state filter and paging parameters shared by the task
// listings.
func page(f *store.TaskFilter, state *TaskState, cursor *string, limit *int) error {
	f.Limit = 50
	if limit != nil {
		if *limit < 1 || *limit > 200 {
			return invalid("limit must be between 1 and 200")
		}
		f.Limit = *limit
	}
	if state != nil {
		switch store.TaskState(*state) {
		case store.TaskPending, store.TaskRunning, store.TaskSucceeded, store.TaskFailed, store.TaskCancelled:
			f.State = store.TaskState(*state)
		default:
			return invalid("unknown state %q", *state)
		}
	}
	if cursor != nil {
		f.Cursor = *cursor
	}
	return nil
}

func (h Handlers) listTasks(ctx context.Context, f store.TaskFilter) (TaskList, error) {
	tasks, cursor, err := h.Store.ListTasks(ctx, workspaceID(ctx), f)
	if err != nil {
		return TaskList{}, err
	}
	out := TaskList{Tasks: make([]Task, len(tasks)), NextCursor: optional(cursor)}
	for i, t := range tasks {
		out.Tasks[i] = toTask(t)
	}
	return out, nil
}

func (h Handlers) GetTask(ctx context.Context, req GetTaskRequestObject) (GetTaskResponseObject, error) {
	task, err := h.Store.GetTask(ctx, workspaceID(ctx), req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, taskNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	return GetTask200JSONResponse(toTask(task)), nil
}

func (h Handlers) GetTaskDelivery(ctx context.Context, req GetTaskDeliveryRequestObject) (GetTaskDeliveryResponseObject, error) {
	task, err := h.Store.GetTask(ctx, workspaceID(ctx), req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, taskNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	// A pending task's next attempt; otherwise the latest (in flight, or
	// the one that finished the task).
	attempt := max(task.AttemptCount, 1)
	if task.State == store.TaskPending {
		attempt = task.AttemptCount + 1
	}
	out := GetTaskDelivery200JSONResponse{
		Method:  HTTPMethod(task.Method),
		Url:     task.URL,
		Headers: map[string]string{},
		Body:    string(task.Body),
	}
	header, err := h.Signer.Header(ctx, task, attempt, time.Now())
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		out.Headers[k] = strings.Join(v, ", ")
	}
	return out, nil
}

func (h Handlers) ListAttempts(ctx context.Context, req ListAttemptsRequestObject) (ListAttemptsResponseObject, error) {
	ws := workspaceID(ctx)
	if _, err := h.Store.GetTask(ctx, ws, req.Id); errors.Is(err, store.ErrNotFound) {
		return nil, taskNotFound(req.Id)
	} else if err != nil {
		return nil, err
	}
	attempts, err := h.Store.ListAttempts(ctx, ws, req.Id)
	if err != nil {
		return nil, err
	}
	out := ListAttempts200JSONResponse{Attempts: make([]Attempt, len(attempts))}
	for i, a := range attempts {
		out.Attempts[i] = toAttempt(a)
	}
	return out, nil
}

func (h Handlers) CancelTask(ctx context.Context, req CancelTaskRequestObject) (CancelTaskResponseObject, error) {
	task, err := h.Store.CancelTask(ctx, workspaceID(ctx), req.Id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, taskNotFound(req.Id)
	case errors.Is(err, store.ErrInvalidState):
		return nil, invalidState("task %q already %s and can't be cancelled", req.Id, task.State)
	case err != nil:
		return nil, err
	}
	return CancelTask200JSONResponse(toTask(task)), nil
}

func (h Handlers) RetryTask(ctx context.Context, req RetryTaskRequestObject) (RetryTaskResponseObject, error) {
	task, err := h.Store.RetryTask(ctx, workspaceID(ctx), req.Id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, taskNotFound(req.Id)
	case errors.Is(err, store.ErrInvalidState):
		return nil, invalidState("task %q has an attempt in flight; retry it once the attempt finishes", req.Id)
	case err != nil:
		return nil, err
	}
	return RetryTask200JSONResponse(toTask(task)), nil
}
