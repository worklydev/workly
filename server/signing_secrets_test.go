package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/signing"
	"github.com/worklydev/workly/server"
)

// byHeader resolves the workspace named in X-Workspace.
type byHeader struct{}

func (byHeader) Resolve(r *http.Request) (server.WorkspaceID, error) {
	return server.WorkspaceID(r.Header.Get("X-Workspace")), nil
}

func TestWorkspaceSigningSecrets(t *testing.T) {
	st := newStore(t, true)
	for _, ws := range []server.WorkspaceID{"ws_a", "ws_b"} {
		if _, err := st.EnsureWorkspace(t.Context(), ws, string(ws)); err != nil {
			t.Fatal(err)
		}
	}
	secrets := map[server.WorkspaceID]string{"ws_a": "secret-a", "ws_b": "secret-b"}
	h := newServer(t, server.Options{
		Store: st, WorkspaceResolver: byHeader{}, DisableDispatcher: true, SigningSecret: "server-wide",
		SigningSecrets: func(_ context.Context, ws server.WorkspaceID) ([]byte, []byte, error) {
			return []byte(secrets[ws]), nil, nil
		},
	}).Handler()

	delivery := func(ws string) (status int, sig, ts, body string) {
		req := func(method, path, body string) response {
			var r io.Reader
			if body != "" {
				r = strings.NewReader(body)
			}
			hr := httptest.NewRequest(method, path, r)
			hr.Header.Set("Content-Type", "application/json")
			hr.Header.Set("X-Workspace", ws)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, hr)
			res := response{status: rec.Code, body: rec.Body.String()}
			_ = json.Unmarshal(rec.Body.Bytes(), &res.json)
			return res
		}
		res := req("POST", "/v1/queues/q/tasks", `{"url": "https://example.com/hook", "payload": {"n": 1}}`)
		if res.status != 201 {
			t.Fatalf("enqueue in %s = %d %s", ws, res.status, res.body)
		}
		res = req("GET", "/v1/tasks/"+res.json["id"].(string)+"/delivery", "")
		if res.status != 200 {
			return res.status, "", "", ""
		}
		headers := res.json["headers"].(map[string]any)
		return res.status, headers["Workly-Signature"].(string), headers["Workly-Timestamp"].(string), res.json["body"].(string)
	}

	for ws, own := range secrets {
		_, sig, ts, body := delivery(string(ws))
		verify := func(secret string) error {
			return signing.Verify([]byte(secret), sig, ts, []byte(body), time.Minute, time.Now())
		}
		if err := verify(own); err != nil {
			t.Errorf("%s: own secret doesn't verify: %v", ws, err)
		}
		for _, other := range []string{"secret-a", "secret-b", "server-wide"} {
			if other != own && verify(other) == nil {
				t.Errorf("%s: %q verifies too", ws, other)
			}
		}
	}
}
