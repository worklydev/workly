package dispatch

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/metrics"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/sqlite"
)

func TestMetrics(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		var flaky atomic.Int64
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/fail" || flaky.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer endpoint.Close()

		putQueue(t, s, "q", nil)
		one := 1
		ok := enqueue(t, s, store.NewTask{URL: endpoint.URL + "/flaky"})
		failed := enqueue(t, s, store.NewTask{URL: endpoint.URL + "/fail", MaxAttempts: &one})
		enqueue(t, s, store.NewTask{Queue: "later", URL: endpoint.URL, RunAt: time.Now().Add(time.Hour)})

		m := metrics.New()
		start(t, s, func(o *Options) { o.Metrics = m })
		waitForState(t, s, ok.ID, store.TaskSucceeded)
		waitForState(t, s, failed.ID, store.TaskFailed)

		want := []string{
			`workly_dispatcher_is_leader 1`,
			`workly_attempts_total{workspace="default",queue="q",outcome="http_error"} 2`,
			`workly_attempts_total{workspace="default",queue="q",outcome="success"} 1`,
			`workly_tasks_completed_total{workspace="default",queue="q",state="failed"} 1`,
			`workly_tasks_completed_total{workspace="default",queue="q",state="succeeded"} 1`,
			`workly_attempt_duration_seconds_count{workspace="default",queue="q"} 3`,
			`workly_dispatch_lag_seconds_count{workspace="default",queue="q"} 3`,
			`workly_queue_due{workspace="default",queue="q"} 0`,
			`workly_queue_scheduled{workspace="default",queue="later"} 1`,
			`workly_queue_oldest_due_age_seconds{workspace="default",queue="later"} 0`,
		}
		// Metrics are recorded just after the result is stored, so poll.
		deadline := time.Now().Add(5 * time.Second)
		for {
			got := scrape(m)
			missing := ""
			for _, line := range want {
				if !strings.Contains(got, line+"\n") {
					missing = line
					break
				}
			}
			if missing == "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("missing %q in:\n%s", missing, got)
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

func scrape(m *metrics.Workly) string {
	ctx := context.Background()
	var b strings.Builder
	w := bufio.NewWriter(&b)
	m.Write(ctx, w)
	w.Flush()
	return b.String()
}

// A replica that isn't leading reports no queue series, so only the leader's
// count.
func TestMetricsStandby(t *testing.T) {
	path := t.TempDir() + "/workly.db"
	s := setup(t, func() store.Store {
		st, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	})
	enqueue(t, s, store.NewTask{URL: "http://example.test"})

	m := metrics.New()
	New(Options{Store: s, Metrics: m}) // never run, so never leader
	got := scrape(m)
	if !strings.Contains(got, "workly_dispatcher_is_leader 0\n") || strings.Contains(got, "workly_queue_due{") {
		t.Fatalf("standby metrics:\n%s", got)
	}
}
