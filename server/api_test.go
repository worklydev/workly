package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

func TestQueues(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()

	res := doBody(t, h, "PUT", "/v1/queues/emails", "", `{"max_attempts": 8, "min_backoff_seconds": 0.5, "retry_on_4xx": true}`)
	if res.status != 201 {
		t.Fatalf("PUT new queue = %d %s", res.status, res.body)
	}
	want := map[string]any{
		"name": "emails", "paused": false, "max_attempts": 8.0, "min_backoff_seconds": 0.5,
		"max_backoff_seconds": 3600.0, "backoff_multiplier": 2.0, "timeout_seconds": 30.0, "retry_on_4xx": true,
	}
	for k, v := range want {
		if res.json[k] != v {
			t.Errorf("%s = %v, want %v", k, res.json[k], v)
		}
	}

	// PUT is declarative: omitted fields go back to defaults.
	res = doBody(t, h, "PUT", "/v1/queues/emails", "", `{"timeout_seconds": 60}`)
	if res.status != 200 || res.json["max_attempts"] != 5.0 || res.json["min_backoff_seconds"] != 2.0 || res.json["timeout_seconds"] != 60.0 || res.json["retry_on_4xx"] != false {
		t.Fatalf("PUT existing queue = %d %s", res.status, res.body)
	}

	res = do(t, h, "GET", "/v1/queues/emails", "")
	if res.status != 200 || res.json["timeout_seconds"] != 60.0 {
		t.Fatalf("GET queue = %d %s", res.status, res.body)
	}
	doBody(t, h, "PUT", "/v1/queues/billing", "", `{}`)
	res = do(t, h, "GET", "/v1/queues", "")
	if queues, _ := res.json["queues"].([]any); res.status != 200 || len(queues) != 2 {
		t.Fatalf("GET queues = %d %s", res.status, res.body)
	}

	if res = do(t, h, "DELETE", "/v1/queues/emails", ""); res.status != 204 {
		t.Fatalf("DELETE queue = %d %s", res.status, res.body)
	}
	for _, method := range []string{"GET", "DELETE"} {
		if res = do(t, h, method, "/v1/queues/emails", ""); res.status != 404 || errorCode(res) != "queue_not_found" {
			t.Fatalf("%s deleted queue = %d %s", method, res.status, res.body)
		}
	}

	for name, body := range map[string]string{
		"max_attempts 0":        `{"max_attempts": 0}`,
		"negative backoff":      `{"min_backoff_seconds": -1}`,
		"max below min backoff": `{"min_backoff_seconds": 10, "max_backoff_seconds": 5}`,
		"multiplier below 1":    `{"backoff_multiplier": 0.5}`,
		"timeout too long":      `{"timeout_seconds": 3600}`,
		"wrong type":            `{"max_attempts": "five"}`,
		"malformed JSON":        `{`,
	} {
		t.Run(name, func(t *testing.T) {
			res := doBody(t, h, "PUT", "/v1/queues/q", "", body)
			if res.status != 400 || errorCode(res) != "invalid_request" {
				t.Fatalf("PUT %s = %d %s", body, res.status, res.body)
			}
		})
	}
	if res = doBody(t, h, "PUT", "/v1/queues/Bad%20Name", "", `{}`); res.status != 400 {
		t.Fatalf("PUT invalid name = %d %s", res.status, res.body)
	}
}

func TestEnqueue(t *testing.T) {
	store := newStore(t, true)
	h := newServer(t, server.Options{Store: store, DisableDispatcher: true}).Handler()

	res := doBody(t, h, "POST", "/v1/queues/emails/tasks", "", `{
		"url": "https://api.example.test/send",
		"headers": {"X-Tenant": "acme"},
		"payload": {"user_id": "123", "tags": ["a", "b"]},
		"delay_seconds": 60,
		"max_attempts": 3,
		"timeout_seconds": 5
	}`)
	if res.status != 201 {
		t.Fatalf("enqueue = %d %s", res.status, res.body)
	}
	task := res.json
	id, _ := task["id"].(string)
	if !strings.HasPrefix(id, "task_") || task["state"] != "pending" || task["method"] != "POST" ||
		task["max_attempts"] != 3.0 || task["timeout_seconds"] != 5.0 || task["attempt_count"] != 0.0 {
		t.Fatalf("task = %s", res.body)
	}
	headers, _ := task["headers"].(map[string]any)
	if headers["Content-Type"] != "application/json" || headers["X-Tenant"] != "acme" {
		t.Fatalf("headers = %v", headers)
	}
	if payload, _ := json.Marshal(task["payload"]); string(payload) != `{"tags":["a","b"],"user_id":"123"}` {
		t.Fatalf("payload = %s", payload)
	}
	runAt, _ := time.Parse(time.RFC3339Nano, task["run_at"].(string))
	if d := time.Until(runAt); d < 55*time.Second || d > 60*time.Second {
		t.Fatalf("run_at is %v from now, want ~60s", d)
	}

	// The queue was auto-created.
	if res := do(t, h, "GET", "/v1/queues/emails", ""); res.status != 200 {
		t.Fatalf("auto-created queue = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/tasks/"+id, ""); res.status != 200 || res.json["id"] != id {
		t.Fatalf("GET task = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/tasks/"+id+"/attempts", ""); res.status != 200 || res.body != "{\"attempts\":[]}\n" {
		t.Fatalf("GET attempts = %d %s", res.status, res.body)
	}

	// A text body keeps its content type and is returned as body.
	res = doBody(t, h, "POST", "/v1/queues/emails/tasks", "",
		`{"url": "http://localhost:3000/hook", "method": "PUT", "body": "a,b,c", "content_type": "text/csv", "run_at": "2030-01-01T00:00:00Z"}`)
	if res.status != 201 || res.json["body"] != "a,b,c" || res.json["payload"] != nil || res.json["method"] != "PUT" ||
		res.json["run_at"] != "2030-01-01T00:00:00Z" || res.json["headers"].(map[string]any)["Content-Type"] != "text/csv" {
		t.Fatalf("text enqueue = %d %s", res.status, res.body)
	}

	// A minimal enqueue needs only a URL; the W3C trace context is captured
	// from the enqueue request.
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req := httptest.NewRequest("POST", "/v1/queues/emails/tasks", strings.NewReader(`{"url": "http://localhost:3000/hook"}`))
	req.Header.Set("traceparent", traceparent)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("minimal enqueue = %d %s", rec.Code, rec.Body)
	}
	var minimal struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &minimal)
	stored, err := store.GetTask(t.Context(), server.DefaultWorkspace, minimal.ID)
	if err != nil || stored.TraceContext != traceparent {
		t.Fatalf("stored trace context = %q, %v", stored.TraceContext, err)
	}
}

func TestEnqueueIdempotency(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	body := `{"url": "http://localhost:3000/hook", "idempotency_key": "welcome:123"}`

	first := doBody(t, h, "POST", "/v1/queues/emails/tasks", "", body)
	second := doBody(t, h, "POST", "/v1/queues/emails/tasks", "", body)
	if first.status != 201 || second.status != 200 || first.json["id"] != second.json["id"] {
		t.Fatalf("enqueues = %d %v, %d %v", first.status, first.json["id"], second.status, second.json["id"])
	}
}

func TestEnqueueValidation(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true, MaxPayloadBytes: 1024}).Handler()

	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"missing url", `{}`, 400, "invalid_request"},
		{"relative url", `{"url": "/hook"}`, 400, "invalid_request"},
		{"ftp url", `{"url": "ftp://example.test/"}`, 400, "invalid_request"},
		{"bad method", `{"url": "http://x.test", "method": "TRACE"}`, 400, "invalid_request"},
		{"payload and body", `{"url": "http://x.test", "payload": {}, "body": "x"}`, 400, "invalid_request"},
		{"content_type without body", `{"url": "http://x.test", "content_type": "text/plain"}`, 400, "invalid_request"},
		{"run_at and delay", `{"url": "http://x.test", "delay_seconds": 1, "run_at": "2030-01-01T00:00:00Z"}`, 400, "invalid_request"},
		{"negative delay", `{"url": "http://x.test", "delay_seconds": -1}`, 400, "invalid_request"},
		{"max_attempts 0", `{"url": "http://x.test", "max_attempts": 0}`, 400, "invalid_request"},
		{"timeout too short", `{"url": "http://x.test", "timeout_seconds": 0.5}`, 400, "invalid_request"},
		{"reserved Workly header", `{"url": "http://x.test", "headers": {"workly-task-id": "x"}}`, 400, "invalid_request"},
		{"reserved Host header", `{"url": "http://x.test", "headers": {"Host": "evil.test"}}`, 400, "invalid_request"},
		{"header injection", `{"url": "http://x.test", "headers": {"X-A": "a\r\nX-B: b"}}`, 400, "invalid_request"},
		{"bad header name", `{"url": "http://x.test", "headers": {"X A": "a"}}`, 400, "invalid_request"},
		{"payload too large", `{"url": "http://x.test", "body": "` + strings.Repeat("x", 1025) + `"}`, 413, "payload_too_large"},
		{"request body too large", `{"url": "http://x.test", "body": "` + strings.Repeat("x", 300<<10) + `"}`, 413, "payload_too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := doBody(t, h, "POST", "/v1/queues/q/tasks", "", tt.body)
			if res.status != tt.status || errorCode(res) != tt.code {
				t.Fatalf("status %d %s, want %d %s", res.status, res.body, tt.status, tt.code)
			}
		})
	}

	strict := newServer(t, server.Options{DisableDispatcher: true, DisableQueueAutoCreate: true}).Handler()
	res := doBody(t, strict, "POST", "/v1/queues/missing/tasks", "", `{"url": "http://x.test"}`)
	if res.status != 404 || errorCode(res) != "queue_not_found" {
		t.Fatalf("enqueue without auto-create = %d %s", res.status, res.body)
	}
}

func TestListTasks(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	for range 5 {
		doBody(t, h, "POST", "/v1/queues/emails/tasks", "", `{"url": "http://x.test"}`)
	}

	seen := map[string]bool{}
	path := "/v1/queues/emails/tasks?limit=2"
	for page := 0; page < 5; page++ {
		res := do(t, h, "GET", path, "")
		if res.status != 200 {
			t.Fatalf("list = %d %s", res.status, res.body)
		}
		for _, task := range res.json["tasks"].([]any) {
			seen[task.(map[string]any)["id"].(string)] = true
		}
		cursor, ok := res.json["next_cursor"].(string)
		if !ok {
			break
		}
		path = "/v1/queues/emails/tasks?limit=2&cursor=" + cursor
	}
	if len(seen) != 5 {
		t.Fatalf("paged through %d tasks, want 5", len(seen))
	}

	res := do(t, h, "GET", "/v1/queues/emails/tasks?state=failed", "")
	if res.status != 200 || len(res.json["tasks"].([]any)) != 0 {
		t.Fatalf("list failed = %d %s", res.status, res.body)
	}
	for path, code := range map[string]string{
		"/v1/queues/emails/tasks?state=done": "invalid_request",
		"/v1/queues/emails/tasks?limit=1000": "invalid_request",
		"/v1/queues/missing/tasks":           "queue_not_found",
		"/v1/tasks/task_missing":             "task_not_found",
		"/v1/tasks/task_missing/attempts":    "task_not_found",
	} {
		if res := do(t, h, "GET", path, ""); errorCode(res) != code {
			t.Errorf("GET %s = %d %s, want %s", path, res.status, res.body, code)
		}
	}
}

// Tasks sort by run_at soonest first by default, so a queue's pending tasks
// list in the order they run; other sorts default to newest first.
func TestListTasksSorted(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	delays := map[string]int{}
	for _, d := range []int{30, 10, 20} {
		res := doBody(t, h, "POST", "/v1/queues/emails/tasks", "", fmt.Sprintf(`{"url": "http://x.test", "delay_seconds": %d}`, d))
		delays[res.json["id"].(string)] = d
	}
	order := func(path string) []int {
		t.Helper()
		var got []int
		for {
			res := do(t, h, "GET", path, "")
			if res.status != 200 {
				t.Fatalf("GET %s = %d %s", path, res.status, res.body)
			}
			for _, task := range res.json["tasks"].([]any) {
				got = append(got, delays[task.(map[string]any)["id"].(string)])
			}
			cursor, ok := res.json["next_cursor"].(string)
			if !ok {
				return got
			}
			path = strings.Split(path, "&cursor=")[0] + "&cursor=" + cursor
		}
	}
	for path, want := range map[string][]int{
		"/v1/queues/emails/tasks?state=pending&sort=run_at&limit=1": {10, 20, 30},
		"/v1/queues/emails/tasks?sort=run_at&order=desc&limit=1":    {30, 20, 10},
		"/v1/tasks?sort=run_at&limit=2":                             {10, 20, 30},
		// Tasks created in the same millisecond may be in either order.
		"/v1/queues/emails/tasks?sort=created_at&order=asc&limit=2":                  nil,
		"/v1/queues/emails/tasks?limit=2":                                            nil,
		"/v1/queues/emails/tasks?sort=attempt_count&order=asc&limit=1&state=pending": nil,
	} {
		got := order(path)
		if want == nil {
			if len(got) != 3 {
				t.Errorf("GET %s listed %d tasks, want 3", path, len(got))
			}
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("GET %s = %v, want %v", path, got, want)
		}
	}

	next := do(t, h, "GET", "/v1/queues/emails/tasks?sort=run_at&limit=1", "").json["next_cursor"].(string)
	for _, path := range []string{
		"/v1/queues/emails/tasks?sort=soonest",
		"/v1/queues/emails/tasks?order=up",
		"/v1/queues/emails/tasks?sort=attempt_count&cursor=" + next,
		"/v1/tasks?cursor=nonsense",
	} {
		if res := do(t, h, "GET", path, ""); errorCode(res) != "invalid_request" {
			t.Errorf("GET %s = %d %s, want invalid_request", path, res.status, res.body)
		}
	}
}

// Phase 1's exit criterion: enqueue a task to a failing endpoint, watch it
// get signed deliveries and retries with backoff, and read the full history
// from the API.
func TestEndToEnd(t *testing.T) {
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

	call("PUT", "/v1/queues/demo", `{"max_attempts": 3, "min_backoff_seconds": 0.05, "max_backoff_seconds": 0.2}`)
	task := call("POST", "/v1/queues/demo/tasks",
		fmt.Sprintf(`{"url": "%s/_dev/echo?status=500", "payload": {"hello": "world"}}`, base))
	id := task["id"].(string)

	deadline := time.Now().Add(10 * time.Second)
	for task["state"] != "failed" {
		if time.Now().After(deadline) {
			t.Fatalf("task state = %v after 10s, want failed", task["state"])
		}
		time.Sleep(20 * time.Millisecond)
		task = call("GET", "/v1/tasks/"+id, "")
	}
	if task["attempt_count"] != 3.0 || task["last_status"] != 500.0 || task["finished_at"] == nil {
		t.Fatalf("task = %v", task)
	}

	attempts := call("GET", "/v1/tasks/"+id+"/attempts", "")["attempts"].([]any)
	if len(attempts) != 3 {
		t.Fatalf("got %d attempts, want 3", len(attempts))
	}
	for i, a := range attempts {
		a := a.(map[string]any)
		if a["number"] != float64(i+1) || a["status_code"] != 500.0 || a["outcome"] != "http_error" {
			t.Fatalf("attempt %d = %v", i+1, a)
		}
		// The echo target verified each delivery's signature.
		var echo map[string]any
		if err := json.Unmarshal([]byte(a["response_body"].(string)), &echo); err != nil {
			t.Fatalf("attempt %d response_body: %v", i+1, err)
		}
		if echo["signature"] != "valid" || echo["body"] != `{"hello": "world"}` {
			t.Fatalf("echo = %v", echo)
		}
	}
}
