package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

func TestWorkflowsAPI(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()

	def := `{"steps": [
		{"name": "prices", "url": "https://example.test/prices"},
		{"name": "fx", "url": "https://example.test/fx", "payload": {"currency": "SEK"}},
		{"name": "analytics", "url": "https://example.test/analytics", "depends_on": ["prices", "fx"], "max_attempts": 2}
	], "max_duration_seconds": 3600}`
	res := doBody(t, h, "PUT", "/v1/workflows/refresh", "", def)
	if res.status != 201 || res.json["version"] != 1.0 || res.json["max_duration_seconds"] != 3600.0 {
		t.Fatalf("put = %d %s", res.status, res.body)
	}
	steps := res.json["steps"].([]any)
	if s := steps[0].(map[string]any); s["queue"] != "_workflows" || s["method"] != "POST" || s["payload"] != nil {
		t.Fatalf("step = %v", s)
	}
	if s := steps[1].(map[string]any); fmt.Sprint(s["payload"]) != "map[currency:SEK]" {
		t.Fatalf("step with payload = %v", s)
	}
	if res := doBody(t, h, "PUT", "/v1/workflows/refresh", "", def); res.status != 200 || res.json["version"] != 1.0 {
		t.Fatalf("put again = %d %s", res.status, res.body)
	}

	for _, tt := range []struct{ body, want string }{
		{`{"steps": []}`, "at least one step"},
		{`{"steps": [{"name": "a", "url": "https://e.test"}, {"name": "a", "url": "https://e.test"}]}`, `duplicate step name "a"`},
		{`{"steps": [{"name": "a", "url": "https://e.test", "depends_on": ["b"]}]}`, `depends on "b", which is not a step`},
		{`{"steps": [{"name": "a", "url": "https://e.test", "depends_on": ["a"]}]}`, "depends on itself"},
		{`{"steps": [{"name": "a", "url": "https://e.test", "depends_on": ["c"]}, {"name": "b", "url": "https://e.test", "depends_on": ["a"]},
			{"name": "c", "url": "https://e.test", "depends_on": ["b"]}]}`, "cycle"},
		{`{"steps": [{"name": "a", "url": "ftp://e.test"}]}`, "steps[0]: url"},
		{`{"steps": [{"name": "A", "url": "https://e.test"}]}`, "step name"},
		{`{"steps": [{"name": "a", "url": "https://e.test", "headers": {"Workly-Workflow": "x"}}]}`, "reserved"},
		{`{"steps": [{"name": "a", "url": "https://e.test", "queue": "_callbacks"}]}`, "reserved"},
		{`{"steps": [{"name": "a", "url": "https://e.test", "timeout_seconds": 0}]}`, "timeout_seconds"},
		{`{"steps": [{"name": "a", "url": "https://e.test"}], "max_duration_seconds": 0}`, "max_duration_seconds"},
	} {
		res := doBody(t, h, "PUT", "/v1/workflows/bad", "", tt.body)
		msg, _ := res.json["error"].(map[string]any)["message"].(string)
		if res.status != 400 || !strings.Contains(msg, tt.want) {
			t.Errorf("put %s = %d %s, want %q", tt.body, res.status, res.body, tt.want)
		}
	}
	var many []string
	for i := range 101 {
		many = append(many, fmt.Sprintf(`{"name": "s%d", "url": "https://e.test"}`, i))
	}
	if res := doBody(t, h, "PUT", "/v1/workflows/big", "", `{"steps": [`+strings.Join(many, ",")+`]}`); res.status != 400 {
		t.Errorf("put 101 steps = %d %s", res.status, res.body)
	}

	// Starting a run enqueues the steps without dependencies.
	res = doBody(t, h, "POST", "/v1/workflows/refresh/runs", "", `{"input": {"date": "2026-11-13"}, "idempotency_key": "d1"}`)
	if res.status != 201 || res.json["status"] != "running" || !strings.HasPrefix(res.json["id"].(string), "wfr_") {
		t.Fatalf("start = %d %s", res.status, res.body)
	}
	runID := res.json["id"].(string)
	steps = res.json["steps"].([]any)
	if s := steps[2].(map[string]any); s["status"] != "blocked" || fmt.Sprint(s["blocked_by"]) != "[prices fx]" {
		t.Fatalf("blocked step = %v", s)
	}
	prices := steps[0].(map[string]any)
	task := prices["task"].(map[string]any)
	if prices["status"] != "pending" || task["queue"] != "_workflows" || task["state"] != "pending" {
		t.Fatalf("first step = %v", prices)
	}
	if res := doBody(t, h, "POST", "/v1/workflows/refresh/runs", "", `{"idempotency_key": "d1"}`); res.status != 200 || res.json["id"] != runID {
		t.Fatalf("start again = %d %s", res.status, res.body)
	}
	if res := do(t, h, "POST", "/v1/workflows/missing/runs", ""); res.status != 404 || errorCode(res) != "workflow_not_found" {
		t.Fatalf("start missing = %d %s", res.status, res.body)
	}

	// The step's task carries the run's context, and can't be retried alone.
	res = do(t, h, "GET", "/v1/tasks/"+task["id"].(string), "")
	payload, _ := res.json["payload"].(map[string]any)
	if payload["workflow_run_id"] != runID || fmt.Sprint(payload["input"]) != "map[date:2026-11-13]" ||
		res.json["headers"].(map[string]any)["Workly-Workflow-Step"] != "prices" {
		t.Fatalf("step task = %s", res.body)
	}
	if step := res.json["workflow_step"].(map[string]any); step["id"] != prices["id"] || step["name"] != "prices" ||
		step["workflow"] != "refresh" || step["run_id"] != runID {
		t.Fatalf("step task's workflow_step = %v", step)
	}
	if res := do(t, h, "POST", "/v1/tasks/"+task["id"].(string)+"/cancel", ""); res.status != 200 {
		t.Fatalf("cancel step task = %d %s", res.status, res.body)
	}
	if res := do(t, h, "POST", "/v1/tasks/"+task["id"].(string)+"/retry", ""); res.status != 409 || !strings.Contains(res.body, "workflow step") {
		t.Fatalf("retry step task = %d %s", res.status, res.body)
	}
	res = do(t, h, "GET", "/v1/workflow-runs/"+runID, "")
	if res.json["status"] != "failed" || !strings.Contains(res.json["error"].(string), `step "prices" failed`) {
		t.Fatalf("run after cancelled step = %s", res.body)
	}
	if res := do(t, h, "POST", "/v1/workflow-runs/"+runID+"/cancel", ""); res.status != 409 || errorCode(res) != "invalid_state" {
		t.Fatalf("cancel failed run = %d %s", res.status, res.body)
	}
	// Retrying the run runs its failed step again, on the same task.
	res = do(t, h, "POST", "/v1/workflow-runs/"+runID+"/retry", "")
	if res.status != 200 || res.json["status"] != "running" || res.json["attempt"] != float64(2) || res.json["retried_at"] == nil {
		t.Fatalf("retry run = %d %s", res.status, res.body)
	}
	if st := res.json["steps"].([]any)[0].(map[string]any); st["status"] != "pending" ||
		st["task"].(map[string]any)["id"] != task["id"] || st["task"].(map[string]any)["state"] != "pending" {
		t.Fatalf("retried step = %v", st)
	}
	if res := do(t, h, "POST", "/v1/workflow-runs/"+runID+"/retry", ""); res.status != 409 || errorCode(res) != "invalid_state" {
		t.Fatalf("retry running run = %d %s", res.status, res.body)
	}
	if res := do(t, h, "POST", "/v1/workflow-runs/wfr_missing/retry", ""); res.status != 404 || errorCode(res) != "workflow_run_not_found" {
		t.Fatalf("retry missing run = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/tasks/"+task["id"].(string), ""); res.json["headers"].(map[string]any)["Workly-Workflow-Run-Attempt"] != "2" {
		t.Fatalf("retried step task = %s", res.body)
	}

	// _workflows is configurable, but only Workly enqueues into it.
	if res := doBody(t, h, "POST", "/v1/queues/_workflows/tasks", "", `{"url": "https://e.test"}`); res.status != 400 {
		t.Fatalf("enqueue into _workflows = %d %s", res.status, res.body)
	}
	if res := doBody(t, h, "PUT", "/v1/queues/_workflows", "", `{"max_concurrency": 20}`); res.status != 200 {
		t.Fatalf("configure _workflows = %d %s", res.status, res.body)
	}

	res = doBody(t, h, "POST", "/v1/workflows/refresh/runs", "", `{}`)
	second := res.json["id"].(string)
	if res := do(t, h, "POST", "/v1/workflow-runs/"+second+"/cancel", ""); res.status != 200 || res.json["status"] != "cancelled" {
		t.Fatalf("cancel = %d %s", res.status, res.body)
	}
	res = do(t, h, "GET", "/v1/workflow-runs?workflow=refresh&limit=1", "")
	if res.status != 200 || len(res.json["workflow_runs"].([]any)) != 1 || res.json["next_cursor"] == nil {
		t.Fatalf("list runs = %d %s", res.status, res.body)
	}
	res = do(t, h, "GET", "/v1/workflows", "")
	w := res.json["workflows"].([]any)[0].(map[string]any)
	if w["last_run"].(map[string]any)["id"] != second {
		t.Fatalf("list = %s", res.body)
	}
	if res := do(t, h, "DELETE", "/v1/workflows/refresh", ""); res.status != 204 {
		t.Fatalf("delete = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/workflow-runs/"+runID, ""); res.status != 200 {
		t.Fatalf("run of a deleted workflow = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/workflow-runs/wfr_missing", ""); res.status != 404 || errorCode(res) != "workflow_run_not_found" {
		t.Fatalf("missing run = %d %s", res.status, res.body)
	}
}

// TestWorkflowEndToEnd runs a workflow through the dispatcher: two steps
// in parallel, one of which fans out into a group and hands its completion
// to it, then a step after both.
func TestWorkflowEndToEnd(t *testing.T) {
	ready := make(chan net.Addr, 1)
	srv := newServer(t, server.Options{
		Addr: "127.0.0.1:0", DevEndpoints: true,
		OnReady: func(addr net.Addr) { ready <- addr },
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
			t.Error(err)
			return nil
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			t.Errorf("%s %s = %d %s", method, path, resp.StatusCode, b)
		}
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}

	// The fan-out endpoint creates a group keyed on the step ID, so a retry
	// of it reuses the group, and waits for it.
	var (
		mu    sync.Mutex
		order []string
	)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := r.Header.Get("Workly-Workflow-Step")
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
		if step != "prices" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		g := call("POST", "/v1/groups", fmt.Sprintf(`{"idempotency_key": %q}`, r.Header.Get("Workly-Workflow-Step-Id")))
		id := g["id"].(string)
		for range 3 {
			call("POST", "/v1/queues/prices/tasks", fmt.Sprintf(`{"url": "%s/_dev/echo", "group_id": %q}`, base, id))
		}
		call("POST", "/v1/groups/"+id+"/close", "")
		w.Header().Set("Workly-Wait-For", "group/"+id)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer app.Close()

	call("PUT", "/v1/workflows/refresh", fmt.Sprintf(`{"steps": [
		{"name": "prices", "url": "%[1]s/prices"},
		{"name": "fx", "url": "%[1]s/fx"},
		{"name": "analytics", "url": "%[1]s/analytics", "depends_on": ["prices", "fx"]}
	]}`, app.URL))
	id := call("POST", "/v1/workflows/refresh/runs", `{"input": {"date": "2026-11-13"}}`)["id"].(string)

	deadline := time.Now().Add(15 * time.Second)
	var run map[string]any
	for {
		if time.Now().After(deadline) {
			t.Fatalf("run after 15s = %v", run)
		}
		time.Sleep(20 * time.Millisecond)
		if run = call("GET", "/v1/workflow-runs/"+id, ""); run["status"] != "running" {
			break
		}
	}
	if run["status"] != "succeeded" {
		t.Fatalf("run = %v", run)
	}
	prices := run["steps"].([]any)[0].(map[string]any)
	wait, _ := prices["wait_for"].(map[string]any)
	if wait == nil {
		t.Fatalf("prices didn't wait: %v", prices)
	}
	if wait["type"] != "group" || prices["status"] != "succeeded" || prices["waiting_ms"] == nil {
		t.Fatalf("prices = %v", prices)
	}
	if g := call("GET", "/v1/groups/"+wait["id"].(string), ""); g["status"] != "succeeded" || g["total"] != 3.0 {
		t.Fatalf("group = %v", g)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[2] != "analytics" {
		t.Fatalf("calls = %v, want analytics last", order)
	}
}

// TestScheduledWorkflow triggers a schedule that starts a workflow, with
// its payload as the run's input.
func TestScheduledWorkflow(t *testing.T) {
	ready := make(chan net.Addr, 1)
	srv := newServer(t, server.Options{
		Addr: "127.0.0.1:0", DevEndpoints: true,
		OnReady: func(addr net.Addr) { ready <- addr },
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
	h := srv.Handler()
	base := "http://" + (<-ready).String()

	if res := doBody(t, h, "PUT", "/v1/schedules/nightly", "", `{"cron": "0 3 * * *", "workflow": "refresh"}`); res.status != 404 || errorCode(res) != "workflow_not_found" {
		t.Fatalf("schedule of a missing workflow = %d %s", res.status, res.body)
	}
	if res := doBody(t, h, "PUT", "/v1/workflows/refresh", "", fmt.Sprintf(`{"steps": [{"name": "a", "url": "%s/_dev/echo"}]}`, base)); res.status != 201 {
		t.Fatalf("put workflow = %d %s", res.status, res.body)
	}
	for _, body := range []string{
		`{"cron": "0 3 * * *"}`,
		`{"cron": "0 3 * * *", "workflow": "refresh", "url": "https://e.test"}`,
		`{"cron": "0 3 * * *", "workflow": "refresh", "body": "x"}`,
	} {
		if res := doBody(t, h, "PUT", "/v1/schedules/nightly", "", body); res.status != 400 {
			t.Errorf("put %s = %d %s", body, res.status, res.body)
		}
	}
	res := doBody(t, h, "PUT", "/v1/schedules/nightly", "", `{"cron": "0 3 * * *", "workflow": "refresh", "payload": {"full": true}}`)
	if res.status != 201 || res.json["workflow"] != "refresh" || res.json["url"] != "" {
		t.Fatalf("put schedule = %d %s", res.status, res.body)
	}
	runID := do(t, h, "POST", "/v1/schedules/nightly/trigger", "").json["id"].(string)

	deadline := time.Now().Add(10 * time.Second)
	var run map[string]any
	for {
		if time.Now().After(deadline) {
			t.Fatalf("schedule run after 10s = %v", run)
		}
		time.Sleep(20 * time.Millisecond)
		if run = do(t, h, "GET", "/v1/runs/"+runID, "").json; run["state"] == "succeeded" || run["state"] == "failed" {
			break
		}
	}
	wfRunID, _ := run["workflow_run_id"].(string)
	if run["state"] != "succeeded" || wfRunID == "" {
		t.Fatalf("schedule run = %v", run)
	}
	for {
		if time.Now().After(deadline) {
			t.Fatalf("workflow run after 10s = %v", run)
		}
		time.Sleep(20 * time.Millisecond)
		if run = do(t, h, "GET", "/v1/workflow-runs/"+wfRunID, "").json; run["status"] != "running" {
			break
		}
	}
	if run["status"] != "succeeded" || run["schedule_run_id"] != runID || fmt.Sprint(run["input"]) != "map[full:true]" {
		t.Fatalf("workflow run = %v", run)
	}
}
