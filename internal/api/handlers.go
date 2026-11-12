package api

import (
	"context"
	"time"

	"github.com/worklydev/workly/internal/alert"
	"github.com/worklydev/workly/internal/dispatch"
	"github.com/worklydev/workly/internal/metrics"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// Handlers implements StrictServerInterface, generated from api/openapi.yaml.
type Handlers struct {
	Store            store.Store
	AutoCreateQueues bool
	MaxPayloadBytes  int64
	Metrics          *metrics.Workly
	Signer           dispatch.Signer
	Retention        time.Duration
	// WorkspaceRetention, if set, gives some workspaces their own retention.
	WorkspaceRetention func(ctx context.Context) (map[workspace.ID]time.Duration, error)
	// Admission, if set, may refuse new tasks and schedules.
	Admission Admission
	// Alerts sends test alerts and says whether email can be sent.
	Alerts *alert.Sender
}

// retentionSeconds is how long the request's workspace keeps finished
// tasks, nil for forever. If its own retention can't be looked up, the
// default is reported: it's only shown, never acted on.
func (h Handlers) retentionSeconds(ctx context.Context) *float64 {
	r := h.Retention
	if h.WorkspaceRetention != nil {
		if own, err := h.WorkspaceRetention(ctx); err == nil {
			if d, ok := own[workspaceID(ctx)]; ok {
				r = d
			}
		}
	}
	if r <= 0 {
		return nil
	}
	s := seconds(r)
	return &s
}

// workspaceID returns the request's workspace, set by the resolveWorkspace
// middleware for every /v1 route.
func workspaceID(ctx context.Context) workspace.ID {
	id, ok := workspace.FromContext(ctx)
	if !ok {
		panic("api: no workspace in request context")
	}
	return id
}

var _ StrictServerInterface = Handlers{}

func (h Handlers) GetHealthz(ctx context.Context, _ GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return GetHealthz200JSONResponse{Status: "ok"}, nil
}

func (h Handlers) GetReadyz(ctx context.Context, _ GetReadyzRequestObject) (GetReadyzResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := h.Store.Ping(ctx); err != nil {
		return GetReadyz503JSONResponse(newError("database_unavailable", err.Error())), nil
	}
	pending, err := h.Store.MigrationsPending(ctx)
	if err != nil {
		return GetReadyz503JSONResponse(newError("migrations_unknown", err.Error())), nil
	}
	if pending {
		return GetReadyz503JSONResponse(newError("migrations_pending", "database migrations have not been applied; run `workly migrate`")), nil
	}
	return GetReadyz200JSONResponse{Status: "ok"}, nil
}
