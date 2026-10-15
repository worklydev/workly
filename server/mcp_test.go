package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

// mcpClient talks JSON-RPC to /mcp, like an agent's MCP client.
type mcpClient struct {
	t    *testing.T
	h    http.Handler
	auth string
}

func (c mcpClient) post(body string, header ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c mcpClient) rpc(method string, params any) rpcResponse {
	c.t.Helper()
	p, _ := json.Marshal(params)
	rec := c.post(fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"method":%q,"params":%s}`, method, p))
	if rec.Code != 200 {
		c.t.Fatalf("%s: HTTP %d %s", method, rec.Code, rec.Body)
	}
	var res rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		c.t.Fatalf("%s: %v: %s", method, err, rec.Body)
	}
	if string(res.ID) != "7" {
		c.t.Fatalf("%s: id = %s, want 7", method, res.ID)
	}
	return res
}

type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// call runs a tool and returns its text, failing the test on any error.
func (c mcpClient) call(name string, args any) map[string]any {
	c.t.Helper()
	text, isError := c.callRaw(name, args)
	if isError {
		c.t.Fatalf("%s: tool error: %s", name, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.t.Fatalf("%s: result is not a JSON object: %s", name, text)
	}
	return out
}

func (c mcpClient) callRaw(name string, args any) (string, bool) {
	c.t.Helper()
	res := c.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	if res.Error != nil {
		c.t.Fatalf("%s: JSON-RPC error %d %s", name, res.Error.Code, res.Error.Message)
	}
	var r toolResult
	if err := json.Unmarshal(res.Result, &r); err != nil || len(r.Content) != 1 || r.Content[0].Type != "text" {
		c.t.Fatalf("%s: unexpected result %s", name, res.Result)
	}
	return r.Content[0].Text, r.IsError
}

func (c mcpClient) toolNames() []string {
	c.t.Helper()
	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
			Annotations map[string]any `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(c.rpc("tools/list", nil).Result, &list); err != nil {
		c.t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		if tool.Description == "" || tool.InputSchema["type"] != "object" || tool.Annotations["title"] == nil {
			c.t.Errorf("tool %s is incomplete: %+v", tool.Name, tool)
		}
		names = append(names, tool.Name)
	}
	return names
}

var readTools = []string{"list_queues", "get_queue", "list_tasks", "get_task", "get_task_delivery", "wait_for_task",
	"list_schedules", "get_schedule", "list_runs", "get_run", "get_run_delivery", "wait_for_run"}

func TestMCPProtocol(t *testing.T) {
	c := mcpClient{t: t, h: newServer(t, server.Options{DisableDispatcher: true, Version: "1.2.3"}).Handler()}

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
		ServerInfo   map[string]string `json:"serverInfo"`
		Instructions string            `json:"instructions"`
	}
	res := c.rpc("initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"},
	})
	if err := json.Unmarshal(res.Result, &init); err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != "2025-06-18" || init.Capabilities.Tools == nil ||
		init.ServerInfo["name"] != "workly" || init.ServerInfo["version"] != "1.2.3" ||
		!strings.Contains(init.Instructions, "untrusted") {
		t.Fatalf("initialize = %s", res.Result)
	}
	// An unknown version gets the newest one this server speaks.
	json.Unmarshal(c.rpc("initialize", map[string]any{"protocolVersion": "2099-01-01"}).Result, &init)
	if init.ProtocolVersion != "2025-11-25" {
		t.Errorf("negotiated %q for an unknown version", init.ProtocolVersion)
	}

	if rec := c.post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Errorf("notification = %d %q", rec.Code, rec.Body)
	}
	if res := c.rpc("ping", nil); string(res.Result) != "{}" {
		t.Errorf("ping = %s", res.Result)
	}
	if res := c.rpc("resources/list", nil); res.Error == nil || res.Error.Code != -32601 {
		t.Errorf("unknown method = %+v", res)
	}
	if res := c.rpc("tools/call", map[string]any{"name": "nope"}); res.Error == nil || res.Error.Code != -32602 {
		t.Errorf("unknown tool = %+v", res)
	}

	for name, tc := range map[string]struct {
		body   string
		header []string
		status int
	}{
		"parse error":      {`{"jsonrpc":`, nil, 400},
		"batch":            {`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, nil, 400},
		"wrong version":    {`{"jsonrpc":"1.0","id":1,"method":"ping"}`, nil, 400},
		"unknown protocol": {`{"jsonrpc":"2.0","id":1,"method":"ping"}`, []string{"MCP-Protocol-Version", "2000-01-01"}, 400},
		"known protocol":   {`{"jsonrpc":"2.0","id":1,"method":"ping"}`, []string{"MCP-Protocol-Version", "2025-06-18"}, 200},
		"other origin":     {`{"jsonrpc":"2.0","id":1,"method":"ping"}`, []string{"Origin", "https://evil.example"}, 403},
		"same origin":      {`{"jsonrpc":"2.0","id":1,"method":"ping"}`, []string{"Origin", "http://example.com"}, 200},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := c.post(tc.body, tc.header...); rec.Code != tc.status {
				t.Errorf("HTTP %d %s, want %d", rec.Code, rec.Body, tc.status)
			}
		})
	}

	// No server-initiated stream.
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, httptest.NewRequest("GET", "/mcp", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Errorf("GET /mcp = %d", rec.Code)
	}
}

func TestMCPModes(t *testing.T) {
	for _, tc := range []struct {
		mode  server.MCPMode
		tools []string
	}{
		{"", readTools}, // read-only by default
		{server.MCPReadOnly, readTools},
		{server.MCPFull, append(slices.Clone(readTools), "enqueue_task", "retry_task", "cancel_task",
			"retry_failed_tasks", "cancel_pending_tasks", "pause_queue", "resume_queue", "update_queue_policy",
			"update_schedule", "trigger_schedule", "pause_schedule", "resume_schedule", "retry_run", "cancel_run")},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			c := mcpClient{t: t, h: newServer(t, server.Options{DisableDispatcher: true, MCP: tc.mode}).Handler()}
			if got := c.toolNames(); !slices.Equal(got, tc.tools) {
				t.Errorf("tools = %v, want %v", got, tc.tools)
			}
		})
	}

	c := mcpClient{t: t, h: newServer(t, server.Options{DisableDispatcher: true}).Handler()}
	res := c.rpc("tools/call", map[string]any{"name": "enqueue_task", "arguments": map[string]any{"queue": "q", "url": "http://x.test"}})
	if res.Error == nil || !strings.Contains(res.Error.Message, "read-only") {
		t.Errorf("write tool on a read-only server = %+v", res)
	}

	h := newServer(t, server.Options{DisableDispatcher: true, MCP: server.MCPOff}).Handler()
	if res := doBody(t, h, "POST", "/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); res.status != 404 || errorCode(res) != "not_found" {
		t.Errorf("MCP off = %d %s", res.status, res.body)
	}
}

func TestMCPAPIKey(t *testing.T) {
	h := newServer(t, server.Options{
		DisableDispatcher: true, WorkspaceResolver: server.StaticResolver("secret"), MCP: server.MCPFull,
	}).Handler()
	if rec := (mcpClient{t: t, h: h}).post(`{"jsonrpc":"2.0","id":1,"method":"ping"}`); rec.Code != 401 {
		t.Fatalf("without key = %d", rec.Code)
	}
	c := mcpClient{t: t, h: h, auth: "Bearer secret"}
	c.call("enqueue_task", map[string]any{"queue": "q", "url": "http://x.test"})
	if res := do(t, h, "GET", "/v1/queues/q", "Bearer secret"); res.json["stats"].(map[string]any)["due"] != 1.0 {
		t.Errorf("task enqueued over MCP isn't in the workspace: %s", res.body)
	}
}

func TestMCPTools(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true, MCP: server.MCPFull}).Handler()
	c := mcpClient{t: t, h: h}

	task := c.call("enqueue_task", map[string]any{
		"queue": "emails", "url": "http://api.test/send", "payload": map[string]any{"user_id": 7},
		"headers": map[string]string{"X-Tenant": "acme"}, "idempotency_key": "k1",
	})
	id, _ := task["id"].(string)
	if !strings.HasPrefix(id, "task_") || task["state"] != "pending" || task["payload"].(map[string]any)["user_id"] != 7.0 {
		t.Fatalf("enqueue_task = %v", task)
	}

	// Errors the agent can fix come back as tool errors.
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"API validation":  {"enqueue_task", map[string]any{"queue": "emails", "url": "ftp://x"}, "invalid_request"},
		"unknown task":    {"get_task", map[string]any{"id": "task_nope"}, "task_not_found"},
		"unknown queue":   {"get_queue", map[string]any{"queue": "nope"}, "queue_not_found"},
		"unknown arg":     {"list_tasks", map[string]any{"queu": "emails"}, `unknown field "queu"`},
		"wrong arg type":  {"get_task", map[string]any{"id": 5}, "invalid arguments"},
		"unknown setting": {"update_queue_policy", map[string]any{"queue": "emails", "retries": 3}, `unknown setting "retries"`},
	} {
		t.Run(name, func(t *testing.T) {
			text, isError := c.callRaw(tc.tool, tc.args)
			if !isError || !strings.Contains(text, tc.want) {
				t.Errorf("%s = %v %q, want an error containing %q", tc.tool, isError, text, tc.want)
			}
		})
	}

	got := c.call("get_task", map[string]any{"id": id})
	if got["task"].(map[string]any)["id"] != id || len(got["attempts"].([]any)) != 0 {
		t.Errorf("get_task = %v", got)
	}

	list := c.call("list_tasks", map[string]any{"queue": "emails", "state": "pending"})
	tasks := list["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("list_tasks = %v", list)
	}
	if s := tasks[0].(map[string]any); s["id"] != id || s["payload"] != nil || s["headers"] != nil {
		t.Errorf("list_tasks returns summaries without payloads, got %v", s)
	}

	d := c.call("get_task_delivery", map[string]any{"id": id})
	curl, _ := d["curl"].(string)
	if d["url"] != "http://api.test/send" || !strings.Contains(curl, "-H 'X-Tenant: acme'") ||
		!strings.Contains(curl, `--data-raw '{"user_id":7}'`) || !strings.Contains(curl, "Workly-Signature: v1=") {
		t.Errorf("get_task_delivery = %v", d)
	}

	q := c.call("get_queue", map[string]any{"queue": "emails"})
	if q["queue"].(map[string]any)["stats"].(map[string]any)["due"] != 1.0 || q["activity"].(map[string]any)["succeeded"] != 0.0 ||
		len(q["failures"].([]any)) != 0 {
		t.Errorf("get_queue = %v", q)
	}
	if qs := c.call("list_queues", nil); len(qs["queues"].([]any)) != 1 {
		t.Errorf("list_queues = %v", qs)
	}

	if q := c.call("pause_queue", map[string]any{"queue": "emails"}); q["paused"] != true {
		t.Errorf("pause_queue = %v", q)
	}
	if q := c.call("resume_queue", map[string]any{"queue": "emails"}); q["paused"] != false {
		t.Errorf("resume_queue = %v", q)
	}

	if got := c.call("cancel_task", map[string]any{"id": id}); got["state"] != "cancelled" {
		t.Errorf("cancel_task = %v", got)
	}
	if got := c.call("cancel_task", map[string]any{"id": id}); got["state"] != "cancelled" {
		t.Errorf("cancelling twice = %v", got) // idempotent, like the API
	}
	if got := c.call("retry_task", map[string]any{"id": id}); got["state"] != "pending" || got["retried_at"] == nil {
		t.Errorf("retry_task = %v", got)
	}
	if got := c.call("retry_failed_tasks", map[string]any{"queue": "emails", "last_error": "HTTP 500"}); got["retried"] != 0.0 {
		t.Errorf("retry_failed_tasks = %v", got)
	}
	if got := c.call("list_tasks", map[string]any{"last_error": "HTTP 500"}); len(got["tasks"].([]any)) != 0 {
		t.Errorf("list_tasks by last error = %v", got)
	}
	if got := c.call("cancel_pending_tasks", map[string]any{"queue": "emails"}); got["cancelled"] != 1.0 {
		t.Errorf("cancel_pending_tasks = %v", got)
	}
}

func TestMCPUpdateQueuePolicy(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true, MCP: server.MCPFull}).Handler()
	c := mcpClient{t: t, h: h}

	// Creates a missing queue.
	q := c.call("update_queue_policy", map[string]any{"queue": "q", "max_attempts": 3, "rate_per_second": 50, "max_concurrency": 4})
	if q["max_attempts"] != 3.0 || q["rate_per_second"] != 50.0 || q["max_concurrency"] != 4.0 {
		t.Fatalf("create = %v", q)
	}
	// Only the settings given change.
	q = c.call("update_queue_policy", map[string]any{"queue": "q", "rate_per_second": 200})
	if q["max_attempts"] != 3.0 || q["rate_per_second"] != 200.0 || q["max_concurrency"] != 4.0 {
		t.Fatalf("update = %v", q)
	}
	// null removes the rate limit.
	q = c.call("update_queue_policy", map[string]any{"queue": "q", "rate_per_second": nil, "timeout_seconds": 5})
	if q["rate_per_second"] != nil || q["max_concurrency"] != 4.0 || q["timeout_seconds"] != 5.0 || q["max_attempts"] != 3.0 {
		t.Fatalf("null = %v", q)
	}
	if text, isError := c.callRaw("update_queue_policy", map[string]any{"queue": "q", "max_attempts": 0}); !isError || !strings.Contains(text, "invalid_request") {
		t.Errorf("invalid setting = %q", text)
	}
}

func TestMCPWaitForTask(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"database is down"}`)
	}))
	defer target.Close()

	// Run for the dispatcher; requests go straight to the handler.
	srv := newServer(t, server.Options{Addr: "127.0.0.1:0", MCP: server.MCPFull})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	c := mcpClient{t: t, h: srv.Handler()}
	c.call("update_queue_policy", map[string]any{"queue": "q", "max_attempts": 3, "min_backoff_seconds": 60})

	task := c.call("enqueue_task", map[string]any{"queue": "q", "url": target.URL})
	got := c.call("wait_for_task", map[string]any{"id": task["id"], "timeout_seconds": 10})
	attempt, _ := got["latest_attempt"].(map[string]any)
	if got["done"] != true || attempt["status_code"] != 503.0 || !strings.Contains(attempt["response_body"].(string), "database is down") {
		t.Fatalf("wait_for_task = %v", got)
	}
	// The first attempt already happened, so waiting again returns at once
	// rather than waiting a minute for the retry.
	start := time.Now()
	if got := c.call("wait_for_task", map[string]any{"id": task["id"], "timeout_seconds": 10}); got["done"] != true {
		t.Fatalf("second wait = %v", got)
	}
	if time.Since(start) > time.Second {
		t.Errorf("second wait took %v", time.Since(start))
	}

	// A task scheduled for later times out.
	later := c.call("enqueue_task", map[string]any{"queue": "q", "url": target.URL, "delay_seconds": 3600})
	if got := c.call("wait_for_task", map[string]any{"id": later["id"], "timeout_seconds": 1}); got["done"] != false {
		t.Errorf("wait for a scheduled task = %v", got)
	}
}

func TestMCPSchedules(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true, MCP: server.MCPFull}).Handler()
	c := mcpClient{t: t, h: h}

	// A new schedule needs cron and url; the result previews its slots.
	if text, isError := c.callRaw("update_schedule", map[string]any{"schedule": "nightly", "cron": "@daily"}); !isError || !strings.Contains(text, "invalid_request") {
		t.Errorf("create without url = %q", text)
	}
	sc := c.call("update_schedule", map[string]any{
		"schedule": "nightly", "cron": "0 3 * * *", "timezone": "Europe/Stockholm",
		"url": "http://api.test/report", "payload": map[string]any{"kind": "daily"}, "max_attempts": 5,
	})
	if sc["cron"] != "0 3 * * *" || len(sc["next_runs"].([]any)) != 5 || sc["max_attempts"] != 5.0 {
		t.Fatalf("update_schedule create = %v", sc)
	}
	// Only the settings given change.
	sc = c.call("update_schedule", map[string]any{"schedule": "nightly", "overlap": "allow"})
	if sc["overlap"] != "allow" || sc["max_attempts"] != 5.0 || sc["timezone"] != "Europe/Stockholm" || sc["payload"] == nil {
		t.Fatalf("update_schedule merge = %v", sc)
	}
	// Switching to a text body drops the JSON Content-Type.
	sc = c.call("update_schedule", map[string]any{"schedule": "nightly", "body": "a,b", "content_type": "text/csv"})
	if sc["body"] != "a,b" || sc["payload"] != nil || sc["headers"].(map[string]any)["Content-Type"] != "text/csv" {
		t.Fatalf("update_schedule body = %v", sc)
	}
	if text, isError := c.callRaw("update_schedule", map[string]any{"schedule": "nightly", "retries": 3}); !isError || !strings.Contains(text, `unknown setting "retries"`) {
		t.Errorf("unknown setting = %q", text)
	}

	if got := c.call("pause_schedule", map[string]any{"schedule": "nightly"}); got["paused"] != true {
		t.Errorf("pause_schedule = %v", got)
	}
	run := c.call("trigger_schedule", map[string]any{"schedule": "nightly"})
	id, _ := run["id"].(string)
	if !strings.HasPrefix(id, "run_") || run["trigger"] != "manual" || run["state"] != "pending" {
		t.Fatalf("trigger_schedule = %v", run)
	}
	if got := c.call("resume_schedule", map[string]any{"schedule": "nightly"}); got["paused"] != false {
		t.Errorf("resume_schedule = %v", got)
	}

	got := c.call("get_schedule", map[string]any{"schedule": "nightly"})
	recent := got["recent_runs"].([]any)
	if got["schedule"].(map[string]any)["name"] != "nightly" || len(recent) != 1 || recent[0].(map[string]any)["id"] != id {
		t.Errorf("get_schedule = %v", got)
	}
	if got := c.call("list_schedules", nil); len(got["schedules"].([]any)) != 1 {
		t.Errorf("list_schedules = %v", got)
	}
	if got := c.call("list_runs", map[string]any{"schedule": "nightly", "state": "pending"}); len(got["runs"].([]any)) != 1 {
		t.Errorf("list_runs = %v", got)
	}
	if got := c.call("get_run", map[string]any{"id": id}); got["run"].(map[string]any)["id"] != id || len(got["attempts"].([]any)) != 0 {
		t.Errorf("get_run = %v", got)
	}
	d := c.call("get_run_delivery", map[string]any{"id": id})
	if curl, _ := d["curl"].(string); !strings.Contains(curl, "Workly-Run-Id: "+id) || !strings.Contains(curl, "--data-raw 'a,b'") {
		t.Errorf("get_run_delivery = %v", d)
	}
	if got := c.call("cancel_run", map[string]any{"id": id}); got["state"] != "cancelled" {
		t.Errorf("cancel_run = %v", got)
	}
	if got := c.call("retry_run", map[string]any{"id": id}); got["state"] != "pending" {
		t.Errorf("retry_run = %v", got)
	}
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"unknown schedule": {"get_schedule", map[string]any{"schedule": "nope"}, "schedule_not_found"},
		"unknown run":      {"get_run", map[string]any{"id": "run_nope"}, "run_not_found"},
		"bad state":        {"list_runs", map[string]any{"schedule": "nightly", "state": "done"}, "invalid_request"},
	} {
		t.Run(name, func(t *testing.T) {
			text, isError := c.callRaw(tc.tool, tc.args)
			if !isError || !strings.Contains(text, tc.want) {
				t.Errorf("%s = %v %q, want an error containing %q", tc.tool, isError, text, tc.want)
			}
		})
	}
}

func TestMCPWaitForRun(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"rows":42}`)
	}))
	defer target.Close()
	srv := newServer(t, server.Options{Addr: "127.0.0.1:0", MCP: server.MCPFull})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	c := mcpClient{t: t, h: srv.Handler()}
	c.call("update_schedule", map[string]any{"schedule": "report", "cron": "@yearly", "url": target.URL})
	run := c.call("trigger_schedule", map[string]any{"schedule": "report"})
	got := c.call("wait_for_run", map[string]any{"id": run["id"], "timeout_seconds": 10})
	attempt, _ := got["latest_attempt"].(map[string]any)
	if got["done"] != true || got["run"].(map[string]any)["state"] != "succeeded" || !strings.Contains(attempt["response_body"].(string), "42") {
		t.Fatalf("wait_for_run = %v", got)
	}
}
