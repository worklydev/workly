package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/worklydev/workly/server"
)

func TestMetricsEndpoint(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	doBody(t, h, "POST", "/v1/queues/emails/tasks", "", `{"url": "http://x.test", "idempotency_key": "k"}`)
	doBody(t, h, "POST", "/v1/queues/emails/tasks", "", `{"url": "http://x.test", "idempotency_key": "k"}`)
	doBody(t, h, "POST", "/v1/queues/emails/tasks:batch", "", `{"tasks": [{"url": "http://x.test"}, {"url": "http://x.test"}]}`)

	res := do(t, h, "GET", "/metrics", "")
	// The idempotent repeat isn't counted.
	if res.status != 200 || !strings.Contains(res.body, `workly_tasks_enqueued_total{workspace="default",queue="emails"} 3`+"\n") {
		t.Fatalf("GET /metrics = %d\n%s", res.status, res.body)
	}
}

func TestMetricsRequiresAPIKey(t *testing.T) {
	h := newServer(t, server.Options{
		DisableDispatcher: true,
		WorkspaceResolver: server.StaticResolver("secret"),
	}).Handler()
	if res := do(t, h, "GET", "/metrics", ""); res.status != 401 || errorCode(res) != "unauthorized" {
		t.Fatalf("without key = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/metrics", "Bearer secret"); res.status != 200 {
		t.Fatalf("with key = %d %s", res.status, res.body)
	}
}

func TestMetricsDisabled(t *testing.T) {
	srv := newServer(t, server.Options{DisableDispatcher: true, DisableMetrics: true})
	if res := do(t, srv.Handler(), "GET", "/metrics", ""); res.status != 404 || strings.Contains(res.body, "workly_") {
		t.Fatalf("GET /metrics while disabled = %d %s", res.status, res.body)
	}
	rec := httptest.NewRecorder()
	srv.MetricsHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "workly_tasks_enqueued_total") {
		t.Fatalf("MetricsHandler:\n%s", rec.Body.String())
	}
}

func TestMetricsAddr(t *testing.T) {
	// Reserve a free port for the metrics listener.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	metricsAddr := l.Addr().String()
	l.Close()

	ready := make(chan net.Addr, 1)
	srv := newServer(t, server.Options{
		Addr:              "127.0.0.1:0",
		MetricsAddr:       metricsAddr,
		WorkspaceResolver: server.StaticResolver("secret"),
		OnReady:           func(addr net.Addr) { ready <- addr },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned %v", err)
		}
	}()
	addr := <-ready

	get := func(url string) (int, string) {
		t.Helper()
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	// The metrics port needs no API key.
	if status, body := get("http://" + metricsAddr + "/metrics"); status != 200 || !strings.Contains(body, "workly_dispatcher_is_leader") {
		t.Fatalf("metrics port /metrics = %d\n%s", status, body)
	}
	// The API port no longer serves metrics.
	if status, body := get("http://" + addr.String() + "/metrics"); status != 404 || strings.Contains(body, "workly_") {
		t.Fatalf("API port /metrics = %d, still serves metrics", status)
	}
}
