package server_test

import (
	"testing"

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

func TestPreviewCron(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	r := do(t, h, "GET", "/v1/cron/preview?cron=*/15+*+*+*+*&timezone=Europe/Stockholm", "")
	runs, _ := r.json["next_runs"].([]any)
	if r.status != 200 || len(runs) != 5 || r.json["interval_seconds"] != float64(900) {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	for _, bad := range []string{"cron=nope", "cron=*+*+*+*+*&timezone=Mars/Olympus"} {
		if r := do(t, h, "GET", "/v1/cron/preview?"+bad, ""); r.status != 400 {
			t.Errorf("%s = %d %s", bad, r.status, r.body)
		}
	}
}
