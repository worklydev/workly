package server_test

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

// Dashboards learn of changes from API calls and deliveries over
// GET /v1/events, and streams end when the server shuts down.
func TestEvents(t *testing.T) {
	ready := make(chan net.Addr, 1)
	srv := newServer(t, server.Options{
		Addr:              "127.0.0.1:0",
		WorkspaceResolver: server.StaticResolver("key"),
		DevEndpoints:      true,
		OnReady:           func(addr net.Addr) { ready <- addr },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer cancel()
	base := "http://" + (<-ready).String()

	call := func(method, path, body string) {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer key")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s = %d", method, path, resp.StatusCode)
		}
	}

	if r := do(t, srv.Handler(), "GET", "/v1/events", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("without the key: %d, want 401", r.status)
	}
	req, _ := http.NewRequest("GET", base+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	events := make(chan string, 100)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				events <- name
			}
		}
	}()
	next := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("event %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %q event", want)
		}
	}
	quiet := func() {
		t.Helper()
		select {
		case got := <-events:
			t.Fatalf("unexpected event %q", got)
		case <-time.After(500 * time.Millisecond):
		}
	}

	next("ready")
	call("GET", "/v1/queues", "")
	quiet()

	call("PUT", "/v1/queues/demo", `{}`)
	next("change")
	quiet()

	// The enqueue is one change; the delivery a second later is another.
	call("POST", "/v1/queues/demo/tasks", `{"url": "`+base+`/_dev/echo", "delay_seconds": 1}`)
	next("change")
	quiet()
	next("change")

	cancel()
	select {
	case _, open := <-events:
		for open {
			_, open = <-events
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open after shutdown started")
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
