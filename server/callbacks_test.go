package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

// A task that fails for good, and a schedule's run, are each called back
// with their summary.
func TestCallbacksEndToEnd(t *testing.T) {
	ready := make(chan net.Addr, 1)
	srv := newServer(t, server.Options{
		Addr:         "127.0.0.1:0",
		DevEndpoints: true,
		OnReady:      func(addr net.Addr) { ready <- addr },
	})
	ctx, cancel := context.WithCancel(context.Background())
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
	// delivered waits for the callback task path's callback_task_id names
	// to succeed, and returns the body the echo endpoint got.
	delivered := func(path string) (map[string]any, map[string]any) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			if time.Now().After(deadline) {
				t.Fatalf("no callback delivered for %s after 10s", path)
			}
			time.Sleep(20 * time.Millisecond)
			owner := call("GET", path, "")
			cbID, ok := owner["callback_task_id"].(string)
			if !ok {
				continue
			}
			cb := call("GET", "/v1/tasks/"+cbID, "")
			if cb["state"] != "succeeded" {
				continue
			}
			if cb["queue"] != "_callbacks" || cb["headers"].(map[string]any)["X-Job"] != "nightly" {
				t.Fatalf("callback task = %v", cb)
			}
			attempts := call("GET", "/v1/tasks/"+cbID+"/attempts", "")["attempts"].([]any)
			var echo map[string]any
			if err := json.Unmarshal([]byte(attempts[0].(map[string]any)["response_body"].(string)), &echo); err != nil {
				t.Fatal(err)
			}
			if echo["signature"] != "valid" {
				t.Fatalf("callback delivery = %v", echo)
			}
			var summary map[string]any
			if err := json.Unmarshal([]byte(echo["body"].(string)), &summary); err != nil {
				t.Fatalf("callback body %v: %v", echo["body"], err)
			}
			return owner, summary
		}
	}
	onComplete := fmt.Sprintf(`{"url": "%s/_dev/echo", "headers": {"X-Job": "nightly"}}`, base)

	task := call("POST", "/v1/queues/jobs/tasks", fmt.Sprintf(
		`{"url": "%s/_dev/echo?status=400", "idempotency_key": "k1", "max_attempts": 1, "on_complete": %s}`, base, onComplete))
	got, summary := delivered("/v1/tasks/" + task["id"].(string))
	want := map[string]any{
		"task_id": task["id"], "queue": "jobs", "state": "failed", "attempt_count": 1.0, "last_status": 400.0,
		"last_outcome": "http_error", "last_error": got["last_error"], "idempotency_key": "k1", "finished_at": got["finished_at"],
	}
	if fmt.Sprint(summary) != fmt.Sprint(want) {
		t.Fatalf("task callback = %v\nwant %v", summary, want)
	}

	call("PUT", "/v1/schedules/nightly", fmt.Sprintf(`{"cron": "@daily", "url": "%s/_dev/echo", "on_complete": %s}`, base, onComplete))
	run := call("POST", "/v1/schedules/nightly/trigger", "")
	got, summary = delivered("/v1/runs/" + run["id"].(string))
	if summary["run_id"] != run["id"] || summary["schedule"] != "nightly" || summary["state"] != "succeeded" ||
		summary["trigger"] != "manual" || summary["attempt_count"] != 1.0 || summary["last_status"] != 200.0 ||
		summary["finished_at"] != got["finished_at"] {
		t.Fatalf("run callback = %v", summary)
	}
}
