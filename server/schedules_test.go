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

func TestSchedules(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()

	res := doBody(t, h, "PUT", "/v1/schedules/nightly", "", `{
		"cron": "0 3 * * *",
		"timezone": "Europe/Stockholm",
		"url": "https://api.example.test/reports",
		"payload": {"kind": "daily"},
		"max_attempts": 5,
		"overlap": "allow"
	}`)
	if res.status != 201 {
		t.Fatalf("PUT new schedule = %d %s", res.status, res.body)
	}
	want := map[string]any{
		"name": "nightly", "cron": "0 3 * * *", "timezone": "Europe/Stockholm", "method": "POST",
		"url": "https://api.example.test/reports", "max_attempts": 5.0, "timeout_seconds": 30.0,
		"min_backoff_seconds": 5.0, "max_backoff_seconds": 300.0, "backoff_multiplier": 2.0,
		"retry_on_4xx": false, "overlap": "allow", "missed": "skip", "paused": false,
	}
	for k, v := range want {
		if res.json[k] != v {
			t.Errorf("%s = %v, want %v", k, res.json[k], v)
		}
	}
	if payload, _ := json.Marshal(res.json["payload"]); string(payload) != `{"kind":"daily"}` {
		t.Errorf("payload = %s", payload)
	}
	// The next five slots, at 03:00 Stockholm.
	stockholm, _ := time.LoadLocation("Europe/Stockholm")
	nextRuns, _ := res.json["next_runs"].([]any)
	if len(nextRuns) != 5 || res.json["next_run_at"] != nextRuns[0] {
		t.Fatalf("next_runs = %v, next_run_at = %v", nextRuns, res.json["next_run_at"])
	}
	for _, v := range nextRuns {
		at, _ := time.Parse(time.RFC3339, v.(string))
		if l := at.In(stockholm); l.Hour() != 3 || l.Minute() != 0 || !at.After(time.Now()) {
			t.Fatalf("next run %v is not a future 03:00 in Stockholm", v)
		}
	}

	// PUT is declarative: omitted fields go back to defaults.
	res = doBody(t, h, "PUT", "/v1/schedules/nightly", "", `{"cron": "@hourly", "url": "https://api.example.test/reports"}`)
	if res.status != 200 || res.json["timezone"] != "UTC" || res.json["overlap"] != "skip" || res.json["max_attempts"] != 3.0 || res.json["payload"] != nil {
		t.Fatalf("PUT existing schedule = %d %s", res.status, res.body)
	}

	// Paused: no next_run_at, but next_runs still previews.
	res = do(t, h, "POST", "/v1/schedules/nightly/pause", "")
	if nr, _ := res.json["next_runs"].([]any); res.status != 200 || res.json["paused"] != true || res.json["next_run_at"] != nil || len(nr) != 5 {
		t.Fatalf("pause = %d %s", res.status, res.body)
	}
	res = do(t, h, "POST", "/v1/schedules/nightly/resume", "")
	if res.status != 200 || res.json["paused"] != false || res.json["next_run_at"] == nil {
		t.Fatalf("resume = %d %s", res.status, res.body)
	}

	doBody(t, h, "PUT", "/v1/schedules/a-first", "", `{"cron": "* * * * *", "url": "https://example.test"}`)
	res = do(t, h, "GET", "/v1/schedules", "")
	if s, _ := res.json["schedules"].([]any); res.status != 200 || len(s) != 2 || s[0].(map[string]any)["name"] != "a-first" {
		t.Fatalf("GET schedules = %d %s", res.status, res.body)
	}

	// Schedules and queues are separate: a schedule is not a queue.
	if res = do(t, h, "GET", "/v1/queues/nightly", ""); res.status != 404 {
		t.Fatalf("GET queue with a schedule's name = %d", res.status)
	}

	for name, body := range map[string]string{
		"missing cron":        `{"url": "https://example.test"}`,
		"bad cron":            `{"cron": "every day", "url": "https://example.test"}`,
		"never matches":       `{"cron": "0 0 30 2 *", "url": "https://example.test"}`,
		"seconds field":       `{"cron": "0 0 3 * * *", "url": "https://example.test"}`,
		"bad timezone":        `{"cron": "@daily", "timezone": "Mars/Olympus", "url": "https://example.test"}`,
		"local timezone":      `{"cron": "@daily", "timezone": "Local", "url": "https://example.test"}`,
		"missing url":         `{"cron": "@daily"}`,
		"relative url":        `{"cron": "@daily", "url": "/reports"}`,
		"reserved header":     `{"cron": "@daily", "url": "https://example.test", "headers": {"Workly-Run-Id": "x"}}`,
		"payload and body":    `{"cron": "@daily", "url": "https://example.test", "payload": {}, "body": "x"}`,
		"bad overlap":         `{"cron": "@daily", "url": "https://example.test", "overlap": "queue"}`,
		"bad missed":          `{"cron": "@daily", "url": "https://example.test", "missed": "all"}`,
		"max_attempts 0":      `{"cron": "@daily", "url": "https://example.test", "max_attempts": 0}`,
		"timeout too long":    `{"cron": "@daily", "url": "https://example.test", "timeout_seconds": 3600}`,
		"max below min":       `{"cron": "@daily", "url": "https://example.test", "min_backoff_seconds": 10, "max_backoff_seconds": 1}`,
		"multiplier below 1":  `{"cron": "@daily", "url": "https://example.test", "backoff_multiplier": 0.5}`,
		"invalid method":      `{"cron": "@daily", "url": "https://example.test", "method": "HEAD"}`,
		"content_type alone":  `{"cron": "@daily", "url": "https://example.test", "content_type": "text/csv"}`,
		"unknown macro":       `{"cron": "@every 5m", "url": "https://example.test"}`,
		"out of range minute": `{"cron": "60 * * * *", "url": "https://example.test"}`,
	} {
		t.Run(name, func(t *testing.T) {
			res := doBody(t, h, "PUT", "/v1/schedules/s", "", body)
			if res.status != 400 || errorCode(res) != "invalid_request" {
				t.Fatalf("PUT %s = %d %s", body, res.status, res.body)
			}
		})
	}
	if res = doBody(t, h, "PUT", "/v1/schedules/Bad%20Name", "", `{"cron": "@daily", "url": "https://example.test"}`); res.status != 400 {
		t.Fatalf("PUT invalid name = %d %s", res.status, res.body)
	}

	if res = do(t, h, "DELETE", "/v1/schedules/nightly", ""); res.status != 204 {
		t.Fatalf("DELETE = %d %s", res.status, res.body)
	}
	for _, r := range []struct{ method, path string }{
		{"GET", "/v1/schedules/nightly"},
		{"DELETE", "/v1/schedules/nightly"},
		{"POST", "/v1/schedules/nightly/pause"},
		{"POST", "/v1/schedules/nightly/resume"},
		{"POST", "/v1/schedules/nightly/trigger"},
		{"GET", "/v1/schedules/nightly/runs"},
	} {
		if res = do(t, h, r.method, r.path, ""); res.status != 404 || errorCode(res) != "schedule_not_found" {
			t.Fatalf("%s %s = %d %s", r.method, r.path, res.status, res.body)
		}
	}
	for _, r := range []struct{ method, path string }{
		{"GET", "/v1/runs/run_missing"},
		{"GET", "/v1/runs/run_missing/attempts"},
		{"GET", "/v1/runs/run_missing/delivery"},
		{"POST", "/v1/runs/run_missing/cancel"},
		{"POST", "/v1/runs/run_missing/retry"},
	} {
		if res = do(t, h, r.method, r.path, ""); res.status != 404 || errorCode(res) != "run_not_found" {
			t.Fatalf("%s %s = %d %s", r.method, r.path, res.status, res.body)
		}
	}
}

func TestRunsAPI(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	doBody(t, h, "PUT", "/v1/schedules/nightly", "", `{"cron": "@daily", "url": "https://api.example.test/reports", "body": "go", "content_type": "text/csv"}`)

	res := do(t, h, "POST", "/v1/schedules/nightly/trigger", "")
	if res.status != 201 || res.json["trigger"] != "manual" || res.json["state"] != "pending" || res.json["body"] != "go" {
		t.Fatalf("trigger = %d %s", res.status, res.body)
	}
	id := res.json["id"].(string)
	if !strings.HasPrefix(id, "run_") {
		t.Fatalf("run id = %q", id)
	}

	res = do(t, h, "GET", "/v1/runs/"+id+"/delivery", "")
	headers, _ := res.json["headers"].(map[string]any)
	if res.status != 200 || headers["Workly-Run-Id"] != id || headers["Workly-Schedule"] != "nightly" ||
		headers["Workly-Attempt"] != "1" || headers["Workly-Signature"] == nil || headers["Content-Type"] != "text/csv" ||
		headers["Workly-Scheduled-For"] == nil || res.json["body"] != "go" {
		t.Fatalf("delivery = %d %s", res.status, res.body)
	}

	if res = do(t, h, "POST", "/v1/runs/"+id+"/cancel", ""); res.status != 200 || res.json["state"] != "cancelled" {
		t.Fatalf("cancel = %d %s", res.status, res.body)
	}
	if res = do(t, h, "POST", "/v1/runs/"+id+"/retry", ""); res.status != 200 || res.json["state"] != "pending" || res.json["retried_at"] == nil {
		t.Fatalf("retry = %d %s", res.status, res.body)
	}

	do(t, h, "POST", "/v1/schedules/nightly/trigger", "")
	res = do(t, h, "GET", "/v1/schedules/nightly/runs?limit=1", "")
	runs, _ := res.json["runs"].([]any)
	if res.status != 200 || len(runs) != 1 || res.json["next_cursor"] == nil {
		t.Fatalf("runs page 1 = %d %s", res.status, res.body)
	}
	res = do(t, h, "GET", "/v1/schedules/nightly/runs?state=skipped", "")
	if runs, _ := res.json["runs"].([]any); res.status != 200 || len(runs) != 0 {
		t.Fatalf("skipped runs = %d %s", res.status, res.body)
	}
	if res = do(t, h, "GET", "/v1/schedules/nightly/runs?state=bogus", ""); res.status != 400 {
		t.Fatalf("bad state = %d %s", res.status, res.body)
	}
	res = do(t, h, "GET", "/v1/schedules/nightly", "")
	if last, _ := res.json["last_run"].(map[string]any); last["trigger"] != "manual" || last["state"] != "pending" {
		t.Fatalf("last_run = %v", res.json["last_run"])
	}
	if res = do(t, h, "GET", "/v1/runs/"+id+"/attempts", ""); res.status != 200 || len(res.json["attempts"].([]any)) != 0 {
		t.Fatalf("attempts = %d %s", res.status, res.body)
	}
}

// A schedule runs against the dev echo target end to end, with retries.
func TestScheduleEndToEnd(t *testing.T) {
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

	call("PUT", "/v1/schedules/report", fmt.Sprintf(`{"cron": "@yearly", "url": "%s/_dev/echo?status=500",
		"payload": {"hello": "world"}, "max_attempts": 2, "min_backoff_seconds": 0.05, "max_backoff_seconds": 0.1}`, base))
	run := call("POST", "/v1/schedules/report/trigger", "")
	id := run["id"].(string)
	deadline := time.Now().Add(10 * time.Second)
	for run["state"] != "failed" {
		if time.Now().After(deadline) {
			t.Fatalf("run state = %v after 10s, want failed", run["state"])
		}
		time.Sleep(20 * time.Millisecond)
		run = call("GET", "/v1/runs/"+id, "")
	}
	attempts := call("GET", "/v1/runs/"+id+"/attempts", "")["attempts"].([]any)
	if len(attempts) != 2 || run["last_status"] != 500.0 {
		t.Fatalf("run = %v, %d attempts", run, len(attempts))
	}
	var echo map[string]any
	if err := json.Unmarshal([]byte(attempts[0].(map[string]any)["response_body"].(string)), &echo); err != nil {
		t.Fatal(err)
	}
	if echo["signature"] != "valid" || echo["body"] != `{"hello": "world"}` {
		t.Fatalf("echo = %v", echo)
	}
}
