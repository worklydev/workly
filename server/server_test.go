package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/worklydev/workly/server"
)

func newStore(t *testing.T, migrate bool) server.Store {
	t.Helper()
	st, err := server.OpenStore(t.Context(), "sqlite://"+filepath.Join(t.TempDir(), "workly.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if migrate {
		if err := st.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := st.EnsureWorkspace(t.Context(), server.DefaultWorkspace, "Default"); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func newServer(t *testing.T, opts server.Options) *server.Server {
	t.Helper()
	if opts.Store == nil {
		opts.Store = newStore(t, true)
	}
	opts.Logger = slog.New(slog.DiscardHandler)
	if opts.SigningSecret == "" {
		opts.SigningSecret = "test-secret"
	}
	srv, err := server.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

type response struct {
	status int
	body   string
	json   map[string]any
}

func do(t *testing.T, h http.Handler, method, path, auth string) response {
	t.Helper()
	return doBody(t, h, method, path, auth, "")
}

func doBody(t *testing.T, h http.Handler, method, path, auth, body string) response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := response{status: rec.Code, body: rec.Body.String()}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &res.json); err != nil {
			t.Fatalf("invalid JSON body %q: %v", res.body, err)
		}
	}
	return res
}

func errorCode(r response) string {
	e, _ := r.json["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestHealthz(t *testing.T) {
	h := newServer(t, server.Options{}).Handler()
	res := do(t, h, "GET", "/healthz", "")
	if res.status != 200 || res.json["status"] != "ok" {
		t.Fatalf("GET /healthz = %d %s", res.status, res.body)
	}
}

func TestReadyz(t *testing.T) {
	t.Run("ready after migrate", func(t *testing.T) {
		h := newServer(t, server.Options{}).Handler()
		res := do(t, h, "GET", "/readyz", "")
		if res.status != 200 {
			t.Fatalf("GET /readyz = %d %s", res.status, res.body)
		}
	})
	t.Run("not ready before migrate", func(t *testing.T) {
		h := newServer(t, server.Options{Store: newStore(t, false)}).Handler()
		res := do(t, h, "GET", "/readyz", "")
		if res.status != 503 || errorCode(res) != "migrations_unknown" {
			t.Fatalf("GET /readyz = %d %s", res.status, res.body)
		}
	})
	t.Run("not ready when database closed", func(t *testing.T) {
		st := newStore(t, true)
		h := newServer(t, server.Options{Store: st}).Handler()
		st.Close()
		res := do(t, h, "GET", "/readyz", "")
		if res.status != 503 || errorCode(res) != "database_unavailable" {
			t.Fatalf("GET /readyz = %d %s", res.status, res.body)
		}
	})
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	h := newServer(t, server.Options{}).Handler()
	res := do(t, h, "GET", "/v1/nope", "")
	if res.status != 404 || errorCode(res) != "not_found" {
		t.Fatalf("GET /v1/nope = %d %s", res.status, res.body)
	}
}

func TestAPIKey(t *testing.T) {
	h := newServer(t, server.Options{WorkspaceResolver: server.StaticResolver("secret")}).Handler()

	if res := do(t, h, "GET", "/v1/nope", ""); res.status != 401 || errorCode(res) != "unauthorized" {
		t.Fatalf("no key: %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/nope", "Bearer wrong"); res.status != 401 {
		t.Fatalf("wrong key: %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/nope", "Bearer secret"); res.status != 404 {
		t.Fatalf("valid key: %d %s", res.status, res.body)
	}
	// Probes and the dashboard shell never require a key.
	if res := do(t, h, "GET", "/healthz", ""); res.status != 200 {
		t.Fatalf("healthz: %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/", ""); res.status != 200 {
		t.Fatalf("dashboard: %d %s", res.status, res.body)
	}
}

// The dashboard is served at / whether or not dist/ was built; without a
// build it's a placeholder page (see dashboard/embed_test.go).
func TestDashboard(t *testing.T) {
	h := newServer(t, server.Options{}).Handler()
	res := do(t, h, "GET", "/", "")
	if res.status != 200 || !strings.Contains(res.body, "<title>Workly</title>") {
		t.Fatalf("GET / = %d %s", res.status, res.body)
	}

	// Other sites can't frame the dashboard.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for k, want := range map[string]string{
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestRunAndGracefulShutdown(t *testing.T) {
	ready := make(chan net.Addr, 1)
	srv := newServer(t, server.Options{
		Addr:    "127.0.0.1:0",
		OnReady: func(addr net.Addr) { ready <- addr },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	addr := <-ready
	resp, err := http.Get("http://" + addr.String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /healthz = %d %s", resp.StatusCode, body)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v", err)
	}
}

func TestSQLitePath(t *testing.T) {
	for in, want := range map[string]string{
		"sqlite://.workly/dev.db": ".workly/dev.db",
		"sqlite:///var/lib/w.db":  "/var/lib/w.db",
		"sqlite:relative.db":      "relative.db",
	} {
		got, err := server.SQLitePath(in)
		if err != nil || got != want {
			t.Errorf("SQLitePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := server.SQLitePath("sqlite://"); err == nil {
		t.Error("SQLitePath(sqlite://) should fail")
	}
}

func TestOpenStoreRejectsUnknownScheme(t *testing.T) {
	if _, err := server.OpenStore(t.Context(), "mysql://localhost/db"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := server.OpenStore(t.Context(), ""); err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestCustomDashboard(t *testing.T) {
	custom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "cloud dashboard")
	})
	h := newServer(t, server.Options{Dashboard: custom}).Handler()
	if res := do(t, h, "GET", "/billing", ""); res.status != 200 || res.body != "cloud dashboard" {
		t.Fatalf("GET /billing = %d %s", res.status, res.body)
	}
	// The API is unaffected.
	if res := do(t, h, "GET", "/v1/queues", ""); res.status != 200 {
		t.Fatalf("GET /v1/queues = %d %s", res.status, res.body)
	}
}

func TestWrapHandler(t *testing.T) {
	h := newServer(t, server.Options{
		WrapHandler: func(workly http.Handler) http.Handler {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /cloud/v1/me", func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, "cloud")
			})
			mux.Handle("/", workly)
			return mux
		},
	}).Handler()
	if res := do(t, h, "GET", "/cloud/v1/me", ""); res.body != "cloud" {
		t.Fatalf("GET /cloud/v1/me = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/queues", ""); res.status != 200 {
		t.Fatalf("GET /v1/queues = %d %s", res.status, res.body)
	}
}
