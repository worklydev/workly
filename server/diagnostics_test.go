package server_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

func TestQueueStats(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	doBody(t, h, "POST", "/v1/queues/emails/tasks", "", `{"url": "http://x.test"}`)
	doBody(t, h, "POST", "/v1/queues/emails/tasks", "", `{"url": "http://x.test", "delay_seconds": 3600}`)
	doBody(t, h, "PUT", "/v1/queues/idle", "", `{}`)

	res := do(t, h, "GET", "/v1/queues/emails", "")
	stats, _ := res.json["stats"].(map[string]any)
	if res.status != 200 || stats["due"] != 1.0 || stats["scheduled"] != 1.0 || stats["running"] != 0.0 {
		t.Fatalf("get queue = %d %s", res.status, res.body)
	}
	if _, err := time.Parse(time.RFC3339, stats["oldest_due_at"].(string)); err != nil {
		t.Fatalf("oldest_due_at: %v", err)
	}

	res = do(t, h, "GET", "/v1/queues", "")
	queues := res.json["queues"].([]any)
	idle := queues[1].(map[string]any)
	stats, _ = idle["stats"].(map[string]any)
	if len(queues) != 2 || idle["name"] != "idle" || stats["due"] != 0.0 || stats["oldest_due_at"] != nil {
		t.Fatalf("list queues = %d %s", res.status, res.body)
	}

	// Only reads include stats.
	res = doBody(t, h, "PUT", "/v1/queues/idle", "", `{}`)
	if _, ok := res.json["stats"]; ok {
		t.Fatalf("put queue includes stats: %s", res.body)
	}
}

func TestSearchTasks(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	before := time.Now().UTC()
	emails := doBody(t, h, "POST", "/v1/queues/emails/tasks", "", `{"url": "http://x.test", "idempotency_key": "order-7"}`)
	time.Sleep(2 * time.Millisecond)
	billing := doBody(t, h, "POST", "/v1/queues/billing/tasks", "", `{"url": "http://x.test", "idempotency_key": "order-7"}`)
	time.Sleep(2 * time.Millisecond)
	doBody(t, h, "POST", "/v1/queues/billing/tasks", "", `{"url": "http://x.test"}`)

	ids := func(query string) []string {
		t.Helper()
		res := do(t, h, "GET", "/v1/tasks?"+query, "")
		if res.status != 200 {
			t.Fatalf("search %q = %d %s", query, res.status, res.body)
		}
		var out []string
		for _, task := range res.json["tasks"].([]any) {
			out = append(out, task.(map[string]any)["id"].(string))
		}
		return out
	}
	if got := ids(""); len(got) != 3 {
		t.Fatalf("search all = %v", got)
	}
	if got := ids("idempotency_key=order-7"); len(got) != 2 || got[0] != billing.json["id"] || got[1] != emails.json["id"] {
		t.Fatalf("search by key = %v", got)
	}
	if got := ids("idempotency_key=order-7&queue=emails"); len(got) != 1 || got[0] != emails.json["id"] {
		t.Fatalf("search by key and queue = %v", got)
	}
	if got := ids("created_before=" + url.QueryEscape(before.Format(time.RFC3339Nano))); len(got) != 0 {
		t.Fatalf("search created before = %v", got)
	}
	if got := ids("created_after=" + url.QueryEscape(before.Format(time.RFC3339Nano)) + "&state=pending&limit=1"); len(got) != 1 {
		t.Fatalf("search created after = %v", got)
	}

	for _, query := range []string{"state=done", "limit=0", "created_after=yesterday"} {
		if res := do(t, h, "GET", "/v1/tasks?"+query, ""); errorCode(res) != "invalid_request" {
			t.Errorf("search %q = %d %s, want invalid_request", query, res.status, res.body)
		}
	}
}
