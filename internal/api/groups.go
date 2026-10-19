package api

import (
	"context"
	"errors"

	"github.com/worklydev/workly/internal/store"
)

const maxGroupNameLength = 256

func (h Handlers) CreateGroup(ctx context.Context, req CreateGroupRequestObject) (CreateGroupResponseObject, error) {
	in := req.Body
	ng := store.NewGroup{Name: deref(in.Name), IdempotencyKey: deref(in.IdempotencyKey)}
	if len(ng.Name) > maxGroupNameLength {
		return nil, invalid("name must be at most %d bytes", maxGroupNameLength)
	}
	if len(ng.IdempotencyKey) > maxIdempotencyKeyLength {
		return nil, invalid("idempotency_key must be at most %d bytes", maxIdempotencyKeyLength)
	}
	ws := workspaceID(ctx)
	if cb := in.OnComplete; cb != nil {
		if err := validateName("on_complete queue", cb.Queue); err != nil {
			return nil, err
		}
		r, err := validateRequest(request{url: cb.Url, headers: cb.Headers}, h.MaxPayloadBytes)
		if err != nil {
			return nil, inField("on_complete", err)
		}
		// Checked now rather than when the group completes, when there is
		// no one to tell.
		if !h.AutoCreateQueues {
			if _, err := h.Store.GetQueue(ctx, ws, cb.Queue); errors.Is(err, store.ErrNotFound) {
				return nil, queueNotFound(cb.Queue)
			} else if err != nil {
				return nil, err
			}
		}
		ng.OnComplete = &store.GroupCallback{Queue: cb.Queue, URL: r.url, Headers: r.headers}
	}
	g, created, err := h.Store.CreateGroup(ctx, ws, ng)
	if err != nil {
		return nil, err
	}
	if created {
		return CreateGroup201JSONResponse(toGroup(g)), nil
	}
	return CreateGroup200JSONResponse(toGroup(g)), nil
}

func (h Handlers) ListGroups(ctx context.Context, req ListGroupsRequestObject) (ListGroupsResponseObject, error) {
	p := req.Params
	f := store.GroupFilter{Limit: 50, Name: deref(p.Name), Cursor: deref(p.Cursor)}
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > 200 {
			return nil, invalid("limit must be between 1 and 200")
		}
		f.Limit = *p.Limit
	}
	if p.Status != nil {
		switch s := store.GroupStatus(*p.Status); s {
		case store.GroupOpen, store.GroupRunning, store.GroupSucceeded, store.GroupFailed:
			f.Status = s
		default:
			return nil, invalid("unknown status %q", s)
		}
	}
	groups, cursor, err := h.Store.ListGroups(ctx, workspaceID(ctx), f)
	if err != nil {
		return nil, err
	}
	out := ListGroups200JSONResponse{Groups: make([]Group, len(groups)), NextCursor: optional(cursor)}
	for i, g := range groups {
		out.Groups[i] = toGroup(g)
	}
	return out, nil
}

func (h Handlers) GetGroup(ctx context.Context, req GetGroupRequestObject) (GetGroupResponseObject, error) {
	g, err := h.Store.GetGroup(ctx, workspaceID(ctx), req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, groupNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	return GetGroup200JSONResponse(toGroup(g)), nil
}

func (h Handlers) CloseGroup(ctx context.Context, req CloseGroupRequestObject) (CloseGroupResponseObject, error) {
	g, err := h.Store.CloseGroup(ctx, workspaceID(ctx), req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, groupNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	return CloseGroup200JSONResponse(toGroup(g)), nil
}

func toGroup(g store.Group) Group {
	out := Group{
		Id:             g.ID,
		Name:           optional(g.Name),
		Status:         GroupStatus(g.Status),
		IdempotencyKey: optional(g.IdempotencyKey),
		Total:          g.Total,
		Succeeded:      g.Succeeded,
		Failed:         g.Failed,
		Cancelled:      g.Cancelled,
		Remaining:      g.Remaining(),
		CallbackTaskId: optional(g.CallbackTaskID),
		CreatedAt:      g.CreatedAt,
		ClosedAt:       g.ClosedAt,
		FinishedAt:     g.FinishedAt,
	}
	if cb := g.OnComplete; cb != nil {
		out.OnComplete = &GroupCallback{Queue: cb.Queue, Url: cb.URL}
		if len(cb.Headers) > 0 {
			out.OnComplete.Headers = &cb.Headers
		}
	}
	return out
}
