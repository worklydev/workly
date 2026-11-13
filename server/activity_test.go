package server_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

func TestActivity(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	for _, q := range []string{"emails", "emails", "billing"} {
		if r := doBody(t, h, "POST", "/v1/queues/"+q+"/tasks", "", `{"url": "https://example.com/hook"}`); r.status != 201 {
			t.Fatalf("enqueue = %d %s", r.status, r.body)
		}
	}
	tests := []struct {
		rng     string
		buckets int
		size    float64
	}{
		{"", 24, 3600},
		{"?range=1h", 12, 300},
		{"?range=24h", 24, 3600},
		{"?range=7d", 28, 21600},
	}
	for _, tt := range tests {
		r := do(t, h, "GET", "/v1/activity"+tt.rng, "")
		buckets, _ := r.json["buckets"].([]any)
		if r.status != 200 || len(buckets) != tt.buckets || r.json["bucket_seconds"] != tt.size {
			t.Fatalf("activity%s = %d, %d buckets of %v", tt.rng, r.status, len(buckets), r.json["bucket_seconds"])
		}
		// The tasks were just created: all in the current, last bucket.
		counts := buckets[len(buckets)-1].(map[string]any)["counts"].(map[string]any)
		if counts["emails"] != float64(2) || counts["billing"] != float64(1) {
			t.Errorf("activity%s last bucket = %v", tt.rng, counts)
		}
		for _, b := range buckets[:len(buckets)-1] {
			if c := b.(map[string]any)["counts"].(map[string]any); len(c) != 0 {
				t.Errorf("activity%s: an earlier bucket has %v", tt.rng, c)
			}
		}
	}
	if r := do(t, h, "GET", "/v1/activity?range=1y", ""); r.status != 400 {
		t.Fatalf("range=1y = %d %s", r.status, r.body)
	}
}

// Finished tasks count in the bucket they finished in, by final state.
func TestActivityFinished(t *testing.T) {
	ready := make(chan net.Addr, 1)
	srv := newServer(t, server.Options{
		Addr:         "127.0.0.1:0",
		DevEndpoints: true,
		OnReady:      func(addr net.Addr) { ready <- addr },
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
	h := srv.Handler()

	for _, status := range []int{200, 200, 500} {
		body := fmt.Sprintf(`{"url": "%s/_dev/echo?status=%d", "max_attempts": 1}`, base, status)
		if r := doBody(t, h, "POST", "/v1/queues/emails/tasks", "", body); r.status != 201 {
			t.Fatalf("enqueue = %d %s", r.status, r.body)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		buckets, _ := do(t, h, "GET", "/v1/activity?range=1h", "").json["buckets"].([]any)
		last := buckets[len(buckets)-1].(map[string]any)
		if last["succeeded"] == float64(2) && last["failed"] == float64(1) {
			for _, b := range buckets[:len(buckets)-1] {
				if b := b.(map[string]any); b["succeeded"] != float64(0) || b["failed"] != float64(0) {
					t.Fatalf("an earlier bucket = %v", b)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("last bucket = %v, want 2 succeeded and 1 failed", last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPreviewCron(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	r := do(t, h, "GET", "/v1/cron/preview?cron=*/15+*+*+*+*&timezone=Europe/Stockholm", "")
	runs, _ := r.json["next_runs"].([]any)
	if r.status != 200 || len(runs) != 5 || r.json["interval_seconds"] != float64(900) {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	// A window lists its runs as spans at a fixed interval.
	week := "&from=2030-01-07T00:00:00Z&until=2030-01-14T00:00:00Z"
	for _, tt := range []struct {
		cron  string
		spans string
	}{
		// Every minute of the week is one span.
		{"*+*+*+*+*", `[map[count:10080 every_seconds:60 start:2030-01-07T00:00:00Z]]`},
		// 09:00 and 17:00: 8h then 16h apart, so a span per pair.
		{"0+9,17+*+*+1", `[map[count:2 every_seconds:28800 start:2030-01-07T09:00:00Z]]`},
		{"30+2+*+*+*", `[map[count:7 every_seconds:86400 start:2030-01-07T02:30:00Z]]`},
		{"0+0+1+1+*", `[]`},
	} {
		r := do(t, h, "GET", "/v1/cron/preview?cron="+tt.cron+week, "")
		if r.status != 200 || fmt.Sprint(r.json["spans"]) != tt.spans {
			t.Errorf("%s spans = %d %v, want %s", tt.cron, r.status, r.json["spans"], tt.spans)
		}
	}
	if r := do(t, h, "GET", "/v1/cron/preview?cron=*+*+*+*+*", ""); r.json["spans"] != nil {
		t.Errorf("spans without a window = %v", r.json["spans"])
	}
	for _, bad := range []string{
		"cron=nope",
		"cron=*+*+*+*+*&timezone=Mars/Olympus",
		"cron=*+*+*+*+*&from=2030-01-07T00:00:00Z",
		"cron=*+*+*+*+*&from=2030-01-07T00:00:00Z&until=2030-01-06T00:00:00Z",
		"cron=*+*+*+*+*&from=2030-01-01T00:00:00Z&until=2030-03-01T00:00:00Z",
	} {
		if r := do(t, h, "GET", "/v1/cron/preview?"+bad, ""); r.status != 400 {
			t.Errorf("%s = %d %s", bad, r.status, r.body)
		}
	}
}
