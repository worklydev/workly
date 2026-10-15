package metrics

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

func scrape(t *testing.T, r *Registry) string {
	t.Helper()
	var b strings.Builder
	w := bufio.NewWriter(&b)
	r.Write(t.Context(), w)
	w.Flush()
	return b.String()
}

func TestExposition(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("jobs_total", "Jobs done.\nPer queue.", "queue", "outcome")
	g := r.Gauge("leader", "Leader or not.")
	h := r.Histogram("latency_seconds", "Latency.", []float64{0.1, 1}, "queue")

	c.Inc("b", "ok")
	c.Add(2, "a", `we"ird\`)
	g.Set(1)
	h.Observe(0.05, "a")
	h.Observe(0.1, "a")
	h.Observe(5, "a")

	want := `# HELP jobs_total Jobs done.\nPer queue.
# TYPE jobs_total counter
jobs_total{queue="a",outcome="we\"ird\\"} 2
jobs_total{queue="b",outcome="ok"} 1
# HELP leader Leader or not.
# TYPE leader gauge
leader 1
# HELP latency_seconds Latency.
# TYPE latency_seconds histogram
latency_seconds_bucket{queue="a",le="0.1"} 2
latency_seconds_bucket{queue="a",le="1"} 2
latency_seconds_bucket{queue="a",le="+Inf"} 3
latency_seconds_sum{queue="a"} 5.15
latency_seconds_count{queue="a"} 3
`
	if got := scrape(t, r); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCollectHookAndReset(t *testing.T) {
	r := NewRegistry()
	g := r.Gauge("queue_due", "Due.", "queue")
	calls := 0
	r.OnCollect(func(ctx context.Context) {
		calls++
		g.Reset()
		if calls == 1 {
			g.Set(3, "emails")
		}
	})
	if got := scrape(t, r); !strings.Contains(got, `queue_due{queue="emails"} 3`) {
		t.Fatalf("first scrape:\n%s", got)
	}
	// The queue is gone on the next scrape, and so is its series.
	if got := scrape(t, r); strings.Contains(got, "emails") {
		t.Fatalf("second scrape:\n%s", got)
	}
}

func TestWrongLabelCountPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	NewRegistry().Counter("c", "C.", "queue").Inc()
}
