package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

// meter counts executions by kind, as a hosted service's usage would.
type meter struct {
	mu     sync.Mutex
	counts map[server.ExecutionKind]int
}

func (m *meter) executed(_ context.Context, _ server.WorkspaceID, kind server.ExecutionKind) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[kind]++
}

func (m *meter) snapshot() map[server.ExecutionKind]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.counts)
}

func TestExecuted(t *testing.T) {
	m := &meter{counts: map[server.ExecutionKind]int{}}
	ready := make(chan net.Addr, 1)
	srv := newServer(t, server.Options{
		Addr:         "127.0.0.1:0",
		DevEndpoints: true,
		OnReady:      func(addr net.Addr) { ready <- addr },
		Executed:     m.executed,
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	base := "http://" + (<-ready).String()

	call := func(method, path, body string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s = %d %s", method, path, resp.StatusCode, b)
		}
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}
	wait := func(path, state string) {
		t.Helper()
		field := "state"
		if strings.HasPrefix(path, "/v1/workflow-runs/") {
			field = "status"
		}
		deadline := time.Now().Add(10 * time.Second)
		for call("GET", path, "")[field] != state {
			if time.Now().After(deadline) {
				t.Fatalf("%s not %s after 10s", path, state)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// expect waits for the counts, since a callback is executed after
	// the task that enqueued it is recorded as finished.
	expect := func(want map[server.ExecutionKind]int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			got := m.snapshot()
			if maps.Equal(got, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("executions = %v, want %v", got, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Two attempts, retried with backoff, are one execution; its callback
	// is another.
	call("PUT", "/v1/queues/jobs", `{"min_backoff_seconds": 0, "max_backoff_seconds": 0}`)
	task := call("POST", "/v1/queues/jobs/tasks", fmt.Sprintf(
		`{"url": "%s/_dev/echo?status=500", "max_attempts": 2, "on_complete": {"url": "%s/_dev/echo"}}`, base, base))
	path := "/v1/tasks/" + task["id"].(string)
	wait(path, "failed")
	expect(map[server.ExecutionKind]int{server.ExecutedTask: 1, server.ExecutedCallback: 1})

	// A retry by hand is a new execution, and calls the callback again.
	call("POST", path+"/retry", "")
	wait(path, "failed")
	expect(map[server.ExecutionKind]int{server.ExecutedTask: 2, server.ExecutedCallback: 2})

	call("PUT", "/v1/schedules/nightly", fmt.Sprintf(`{"cron": "@daily", "url": "%s/_dev/echo"}`, base))
	run := call("POST", "/v1/schedules/nightly/trigger", "")
	wait("/v1/runs/"+run["id"].(string), "succeeded")
	expect(map[server.ExecutionKind]int{server.ExecutedTask: 2, server.ExecutedCallback: 2, server.ExecutedScheduleRun: 1})

	// A schedule that starts a workflow executes its steps, not itself.
	call("PUT", "/v1/workflows/refresh", fmt.Sprintf(`{"steps": [
		{"name": "a", "url": "%s/_dev/echo"},
		{"name": "b", "url": "%s/_dev/echo", "depends_on": ["a"]}
	]}`, base, base))
	call("PUT", "/v1/schedules/refresh", `{"cron": "@daily", "workflow": "refresh"}`)
	run = call("POST", "/v1/schedules/refresh/trigger", "")
	wait("/v1/runs/"+run["id"].(string), "succeeded")
	wfr := call("GET", "/v1/runs/"+run["id"].(string), "")["workflow_run_id"].(string)
	wait("/v1/workflow-runs/"+wfr, "succeeded")
	expect(map[server.ExecutionKind]int{
		server.ExecutedTask: 2, server.ExecutedCallback: 2, server.ExecutedScheduleRun: 1, server.ExecutedWorkflowStep: 2,
	})
}
