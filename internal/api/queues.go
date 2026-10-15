package api

import (
	"context"
	"errors"
	"time"

	"github.com/worklydev/workly/internal/store"
)

func (h Handlers) ListQueues(ctx context.Context, _ ListQueuesRequestObject) (ListQueuesResponseObject, error) {
	ws := workspaceID(ctx)
	queues, err := h.Store.ListQueues(ctx, ws)
	if err != nil {
		return nil, err
	}
	stats, err := h.Store.QueueStats(ctx, ws, "", time.Now())
	if err != nil {
		return nil, err
	}
	out := ListQueues200JSONResponse{Queues: make([]Queue, len(queues))}
	for i, q := range queues {
		out.Queues[i] = withStats(toQueue(q), stats)
	}
	return out, nil
}

func (h Handlers) GetQueue(ctx context.Context, req GetQueueRequestObject) (GetQueueResponseObject, error) {
	ws := workspaceID(ctx)
	q, err := h.Store.GetQueue(ctx, ws, req.Queue)
	if errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	}
	if err != nil {
		return nil, err
	}
	stats, err := h.Store.QueueStats(ctx, ws, req.Queue, time.Now())
	if err != nil {
		return nil, err
	}
	return GetQueue200JSONResponse(withStats(toQueue(q), stats)), nil
}

// withStats adds the queue's stats. A queue created after the stats were
// read has none, so it gets zero counts.
func withStats(q Queue, stats map[string]store.QueueStats) Queue {
	st := stats[q.Name]
	q.Stats = &QueueStats{Due: st.Due, Scheduled: st.Scheduled, Running: st.Running, Failed: st.Failed, OldestDueAt: st.OldestDueAt}
	return q
}

func (h Handlers) PutQueue(ctx context.Context, req PutQueueRequestObject) (PutQueueResponseObject, error) {
	if err := validateQueueName(req.Queue); err != nil {
		return nil, err
	}
	cfg, err := queueConfig(*req.Body)
	if err != nil {
		return nil, err
	}
	q, created, err := h.Store.PutQueue(ctx, workspaceID(ctx), req.Queue, cfg)
	if err != nil {
		return nil, err
	}
	if created {
		return PutQueue201JSONResponse(toQueue(q)), nil
	}
	return PutQueue200JSONResponse(toQueue(q)), nil
}

func (h Handlers) PauseQueue(ctx context.Context, req PauseQueueRequestObject) (PauseQueueResponseObject, error) {
	q, err := h.setPaused(ctx, req.Queue, true)
	if err != nil {
		return nil, err
	}
	return PauseQueue200JSONResponse(toQueue(q)), nil
}

func (h Handlers) ResumeQueue(ctx context.Context, req ResumeQueueRequestObject) (ResumeQueueResponseObject, error) {
	q, err := h.setPaused(ctx, req.Queue, false)
	if err != nil {
		return nil, err
	}
	return ResumeQueue200JSONResponse(toQueue(q)), nil
}

func (h Handlers) setPaused(ctx context.Context, name string, paused bool) (store.Queue, error) {
	q, err := h.Store.SetQueuePaused(ctx, workspaceID(ctx), name, paused)
	if errors.Is(err, store.ErrNotFound) {
		return q, queueNotFound(name)
	}
	return q, err
}

func (h Handlers) CancelPendingTasks(ctx context.Context, req CancelPendingTasksRequestObject) (CancelPendingTasksResponseObject, error) {
	n, err := h.Store.CancelPendingTasks(ctx, workspaceID(ctx), req.Queue)
	if errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	}
	if err != nil {
		return nil, err
	}
	return CancelPendingTasks200JSONResponse{Cancelled: n}, nil
}

func (h Handlers) RetryFailedTasks(ctx context.Context, req RetryFailedTasksRequestObject) (RetryFailedTasksResponseObject, error) {
	n, err := h.Store.RetryFailedTasks(ctx, workspaceID(ctx), req.Queue, deref(req.Params.LastError))
	if errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	}
	if err != nil {
		return nil, err
	}
	return RetryFailedTasks200JSONResponse{Retried: n}, nil
}

func (h Handlers) GetQueueFailures(ctx context.Context, req GetQueueFailuresRequestObject) (GetQueueFailuresResponseObject, error) {
	ws := workspaceID(ctx)
	if _, err := h.Store.GetQueue(ctx, ws, req.Queue); errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	} else if err != nil {
		return nil, err
	}
	limit := 10
	if l := req.Params.Limit; l != nil {
		if *l < 1 || *l > 50 {
			return nil, invalid("limit must be between 1 and 50")
		}
		limit = *l
	}
	groups, err := h.Store.QueueFailures(ctx, ws, req.Queue, limit)
	if err != nil {
		return nil, err
	}
	out := GetQueueFailures200JSONResponse{Groups: make([]FailureGroup, len(groups))}
	for i, g := range groups {
		out.Groups[i] = FailureGroup{
			Error:        g.Error,
			Count:        g.Count,
			LastFailedAt: g.LastFailed,
			LatestTaskId: g.LatestTaskID,
			StatusCode:   g.StatusCode,
			ResponseBody: optional(g.ResponseBody),
		}
		if g.Outcome != "" {
			o := Outcome(g.Outcome)
			out.Groups[i].Outcome = &o
		}
	}
	return out, nil
}

func (h Handlers) GetQueueActivity(ctx context.Context, req GetQueueActivityRequestObject) (GetQueueActivityResponseObject, error) {
	ws := workspaceID(ctx)
	if _, err := h.Store.GetQueue(ctx, ws, req.Queue); errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	} else if err != nil {
		return nil, err
	}
	since := time.Now().Add(-7 * 24 * time.Hour)
	if req.Params.Since != nil {
		since = *req.Params.Since
	}
	a, err := h.Store.QueueActivity(ctx, ws, req.Queue, since)
	if err != nil {
		return nil, err
	}
	out := GetQueueActivity200JSONResponse{
		Since: since.UTC(), Succeeded: a.Succeeded, Failed: a.Failed, Cancelled: a.Cancelled,
	}
	if h.Retention > 0 {
		r := seconds(h.Retention)
		out.RetentionSeconds = &r
	}
	return out, nil
}

func (h Handlers) DeleteQueue(ctx context.Context, req DeleteQueueRequestObject) (DeleteQueueResponseObject, error) {
	err := h.Store.DeleteQueue(ctx, workspaceID(ctx), req.Queue)
	if errors.Is(err, store.ErrNotFound) {
		return nil, queueNotFound(req.Queue)
	}
	if err != nil {
		return nil, err
	}
	return DeleteQueue204Response{}, nil
}
