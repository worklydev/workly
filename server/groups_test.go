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

func TestGroups(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()

	res := doBody(t, h, "POST", "/v1/groups", "", `{"name": "daily-prices", "idempotency_key": "2026-10-19",
		"on_complete": {"queue": "callbacks", "url": "https://example.test/done", "headers": {"X-Job": "prices"}}}`)
	if res.status != 201 || res.json["status"] != "open" || res.json["name"] != "daily-prices" ||
		res.json["total"] != 0.0 || res.json["remaining"] != 0.0 {
		t.Fatalf("create = %d %s", res.status, res.body)
	}
	cb := res.json["on_complete"].(map[string]any)
	if cb["queue"] != "callbacks" || cb["url"] != "https://example.test/done" || cb["headers"].(map[string]any)["X-Job"] != "prices" {
		t.Fatalf("on_complete = %v", cb)
	}
	id := res.json["id"].(string)
	if !strings.HasPrefix(id, "grp_") {
		t.Fatalf("id = %q", id)
	}
	if res := doBody(t, h, "POST", "/v1/groups", "", `{"idempotency_key": "2026-10-19"}`); res.status != 200 || res.json["id"] != id {
		t.Fatalf("create again = %d %s", res.status, res.body)
	}

	for _, body := range []string{
		`{"on_complete": {"queue": "Bad Name", "url": "https://example.test"}}`,
		`{"on_complete": {"queue": "q", "url": "ftp://example.test"}}`,
		`{"on_complete": {"queue": "q", "url": "https://example.test", "headers": {"Workly-Signature": "x"}}}`,
		`{"name": "` + strings.Repeat("x", 257) + `"}`,
	} {
		if res := doBody(t, h, "POST", "/v1/groups", "", body); res.status != 400 || errorCode(res) != "invalid_request" {
			t.Errorf("create %s = %d %s", body, res.status, res.body)
		}
	}

	// Tasks join one at a time or in batches.
	if res := doBody(t, h, "POST", "/v1/queues/prices/tasks", "", `{"url": "https://example.test/a", "group_id": "`+id+`"}`); res.status != 201 || res.json["group_id"] != id {
		t.Fatalf("enqueue = %d %s", res.status, res.body)
	}
	if res := doBody(t, h, "POST", "/v1/queues/prices/tasks:batch", "",
		`{"tasks": [{"url": "https://example.test/b", "group_id": "`+id+`"}, {"url": "https://example.test/c", "group_id": "`+id+`"}]}`); res.status != 200 {
		t.Fatalf("batch = %d %s", res.status, res.body)
	}
	if res := doBody(t, h, "POST", "/v1/queues/prices/tasks", "", `{"url": "https://example.test", "group_id": "grp_missing"}`); res.status != 404 || errorCode(res) != "group_not_found" {
		t.Fatalf("enqueue into missing group = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/groups/"+id, ""); res.status != 200 || res.json["total"] != 3.0 || res.json["remaining"] != 3.0 {
		t.Fatalf("get = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/tasks?group_id="+id, ""); res.status != 200 || len(res.json["tasks"].([]any)) != 3 {
		t.Fatalf("search by group = %d %s", res.status, res.body)
	}

	res = do(t, h, "POST", "/v1/groups/"+id+"/close", "")
	if res.status != 200 || res.json["status"] != "running" || res.json["closed_at"] == nil {
		t.Fatalf("close = %d %s", res.status, res.body)
	}
	if res := do(t, h, "POST", "/v1/groups/"+id+"/close", ""); res.status != 200 || res.json["status"] != "running" {
		t.Fatalf("close again = %d %s", res.status, res.body)
	}
	if res := doBody(t, h, "POST", "/v1/queues/prices/tasks", "", `{"url": "https://example.test", "group_id": "`+id+`"}`); res.status != 409 || errorCode(res) != "group_closed" {
		t.Fatalf("enqueue into closed group = %d %s", res.status, res.body)
	}
	if res := doBody(t, h, "POST", "/v1/queues/prices/tasks:batch", "",
		`{"tasks": [{"url": "https://example.test"}, {"url": "https://example.test", "group_id": "`+id+`"}]}`); res.status != 409 || !strings.Contains(res.body, id) {
		t.Fatalf("batch into closed group = %d %s", res.status, res.body)
	}

	empty := doBody(t, h, "POST", "/v1/groups", "", `{}`).json["id"].(string)
	if res := do(t, h, "POST", "/v1/groups/"+empty+"/close", ""); res.json["status"] != "succeeded" || res.json["finished_at"] == nil {
		t.Fatalf("close empty = %d %s", res.status, res.body)
	}

	res = do(t, h, "GET", "/v1/groups", "")
	if groups := res.json["groups"].([]any); res.status != 200 || len(groups) != 2 || groups[0].(map[string]any)["id"] != empty {
		t.Fatalf("list = %d %s", res.status, res.body)
	}
	res = do(t, h, "GET", "/v1/groups?status=running&limit=1", "")
	if groups := res.json["groups"].([]any); len(groups) != 1 || groups[0].(map[string]any)["id"] != id || res.json["next_cursor"] != nil {
		t.Fatalf("list running = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/groups?status=done", ""); res.status != 400 {
		t.Fatalf("list bad status = %d %s", res.status, res.body)
	}
	for _, path := range []string{"GET /v1/groups/grp_missing", "POST /v1/groups/grp_missing/close"} {
		method, p, _ := strings.Cut(path, " ")
		if res := do(t, h, method, p, ""); res.status != 404 || errorCode(res) != "group_not_found" {
			t.Errorf("%s = %d %s", path, res.status, res.body)
		}
	}
}

func TestGroupCallbackQueueMustExist(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true, DisableQueueAutoCreate: true}).Handler()
	res := doBody(t, h, "POST", "/v1/groups", "", `{"on_complete": {"queue": "callbacks", "url": "https://example.test"}}`)
	if res.status != 404 || errorCode(res) != "queue_not_found" {
		t.Fatalf("create = %d %s", res.status, res.body)
	}
}

// A group's tasks are delivered, one fails for good, and the callback is
// delivered with the group's summary.
func TestGroupEndToEnd(t *testing.T) {
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

	group := call("POST", "/v1/groups", fmt.Sprintf(`{"name": "prices", "on_complete": {"queue": "callbacks", "url": "%s/_dev/echo"}}`, base))
	id := group["id"].(string)
	for _, status := range []int{200, 200, 400} {
		call("POST", "/v1/queues/prices/tasks", fmt.Sprintf(`{"url": "%s/_dev/echo?status=%d", "group_id": "%s"}`, base, status, id))
	}
	call("POST", "/v1/groups/"+id+"/close", "")

	deadline := time.Now().Add(10 * time.Second)
	var cb map[string]any
	for {
		if time.Now().After(deadline) {
			t.Fatalf("group = %v, callback = %v after 10s", group, cb)
		}
		time.Sleep(20 * time.Millisecond)
		group = call("GET", "/v1/groups/"+id, "")
		if cbID, ok := group["callback_task_id"].(string); ok {
			if cb = call("GET", "/v1/tasks/"+cbID, ""); cb["state"] == "succeeded" {
				break
			}
		}
	}
	if group["status"] != "failed" || group["succeeded"] != 2.0 || group["failed"] != 1.0 || group["remaining"] != 0.0 {
		t.Fatalf("group = %v", group)
	}
	attempts := call("GET", "/v1/tasks/"+cb["id"].(string)+"/attempts", "")["attempts"].([]any)
	var echo map[string]any
	if err := json.Unmarshal([]byte(attempts[0].(map[string]any)["response_body"].(string)), &echo); err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(echo["body"].(string)), &summary); err != nil {
		t.Fatalf("callback body %v: %v", echo["body"], err)
	}
	want := map[string]any{"group_id": id, "name": "prices", "status": "failed", "total": 3.0, "succeeded": 2.0, "failed": 1.0, "cancelled": 0.0}
	if fmt.Sprint(summary) != fmt.Sprint(want) || echo["signature"] != "valid" {
		t.Fatalf("callback delivery = %v", echo)
	}
}
