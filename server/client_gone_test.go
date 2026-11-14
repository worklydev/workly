package server_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/worklydev/workly/server"
)

// resolverFunc resolves like a hosted resolver that looks sessions up in a
// database: with the request's context, so it fails once that's cancelled.
type resolverFunc func(*http.Request) (server.WorkspaceID, error)

func (f resolverFunc) Resolve(r *http.Request) (server.WorkspaceID, error) { return f(r) }

// A request the client cancelled, as a browser does when it navigates away,
// is logged as 499 rather than a 500 and an error: nobody gets the answer,
// and the server didn't fail.
func TestClientGone(t *testing.T) {
	failing := resolverFunc(func(r *http.Request) (server.WorkspaceID, error) {
		if err := r.Context().Err(); err != nil {
			return "", err
		}
		return server.DefaultWorkspace, nil
	})
	for _, tt := range []struct {
		name     string
		resolver server.WorkspaceResolver
		path     string
	}{
		{"resolving the workspace", failing, "/v1/events"},
		{"in a handler", nil, "/v1/queues"},
		{"in a handler, after resolving", failing, "/v1/runs"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			srv, err := server.New(server.Options{
				Store: newStore(t, true), DisableDispatcher: true, SigningSecret: "test-secret",
				WorkspaceResolver: tt.resolver,
				Logger:            slog.New(slog.NewTextHandler(&logs, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil).WithContext(ctx))
			if rec.Code != 499 {
				t.Errorf("status = %d, want 499; body %s", rec.Code, rec.Body)
			}
			if strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("logged an error:\n%s", logs.String())
			}
		})
	}
}
