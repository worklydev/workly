//go:build e2e

package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store/storetest"
)

// A queue that auto-paused stays paused, with one probe at a time, while the
// leader is SIGKILLed twice: while it waits for its first probe, and in the
// middle of a later one. The pause and the probe slot live in the database,
// so a new leader must neither deliver the backlog nor send a probe early.
// Once the endpoint is back, the next probe resumes the queue and every task
// succeeds, without the wait having used attempts.
func TestAutoPauseLeaderFailover(t *testing.T) {
	if os.Getenv("WORKLY_TEST_POSTGRES_URL") == "" {
		t.Fatal("WORKLY_TEST_POSTGRES_URL not set; run `just test-e2e`")
	}
	dbURL := storetest.PostgresURL(t)
	bin := build(t)
	// Failures take 3s, so a probe is in flight long enough to kill the
	// leader during it.
	ep := newFlakyEndpoint(t, 3*time.Second)

	c := &cluster{t: t, bin: bin, dbURL: dbURL}
	c.start()
	c.start()
	c.waitLeader()

	// One delivery at a time, so failures come in a row and deliveries never
	// overlap unless a probe is sent twice. Short backoff, so the queue pauses
	// within seconds; a short timeout, so a lost probe's lease (5s + 10s
	// grace) runs out quickly.
	const queue = "e2e-autopause"
	c.do("PUT", "/v1/queues/"+queue, map[string]any{
		"max_concurrency":     1,
		"max_attempts":        50,
		"min_backoff_seconds": 1,
		"max_backoff_seconds": 1,
		"timeout_seconds":     5,
		"auto_pause":          map[string]any{"after_failures": 3},
	})
	var ids []string
	for range 5 {
		task := c.do("POST", "/v1/queues/"+queue+"/tasks", map[string]any{"url": ep.url + "/hook", "method": "POST"})
		ids = append(ids, task["id"].(string))
	}

	// 1. Paused, waiting for its first probe.
	ap := c.waitAutoPaused(queue, func(ap map[string]any) bool { return true }, 60*time.Second)
	since, firstProbe := ap["since"].(string), parseTime(t, ap["next_probe_at"])
	pausedAt := ep.count()
	t.Logf("auto-paused after %d deliveries: %s; first probe at %s", pausedAt, ap["error"], firstProbe.Local().Format(time.TimeOnly))
	if time.Until(firstProbe) < 10*time.Second {
		t.Fatalf("first probe in %s, too soon to kill the leader before it", time.Until(firstProbe))
	}
	c.killLeader("while auto-paused, before the first probe")
	c.start()
	if ap := c.queue(queue)["auto_paused"]; ap == nil || ap.(map[string]any)["since"] != since {
		t.Fatalf("after failover auto_paused = %v, want the pause since %s", ap, since)
	}

	// 2. The first probe fails; kill the leader in the middle of the second.
	ap = c.waitAutoPaused(queue, func(ap map[string]any) bool { return ap["probes"].(float64) == 1 }, 90*time.Second)
	secondProbe := parseTime(t, ap["next_probe_at"])
	t.Logf("first probe failed; second at %s", secondProbe.Local().Format(time.TimeOnly))
	d := ep.waitArrival(t, pausedAt+2, time.Until(secondProbe)+30*time.Second)
	time.Sleep(time.Second)
	c.killLeader("mid-probe, attempt " + d.attempt + " of " + d.taskID)
	ep.up.Store(true)
	c.start()
	if c.queue(queue)["auto_paused"] == nil {
		t.Fatal("queue resumed after the leader died mid-probe, before any probe succeeded")
	}

	// 3. The lost probe's lease runs out, the next probe succeeds and resumes
	// the queue, and the backlog drains.
	for _, id := range ids {
		c.waitTask(id, "succeeded", 90*time.Second)
	}
	if ap := c.queue(queue)["auto_paused"]; ap != nil {
		t.Errorf("queue still auto-paused after its tasks succeeded: %v", ap)
	}

	all := ep.all()
	probes := all[pausedAt:]
	for i, d := range all {
		t.Logf("delivery %d: task %s attempt %s at %s for %s, up %t",
			i, d.taskID, d.attempt, d.start.Format("15:04:05.000"), d.end.Sub(d.start).Round(time.Millisecond), d.up)
	}
	// While paused, exactly three probes went: the failed one, the one cut
	// off by the kill, and the one that resumed the queue. Each went no
	// earlier than its slot, and none overlapped another.
	if len(probes) < 3 || probes[0].up || probes[1].up || !probes[2].up {
		t.Fatalf("%d deliveries after the pause, want two probes while down, then the first while up", len(probes))
	}
	const slack = time.Second
	if probes[0].start.Before(firstProbe.Add(-slack)) {
		t.Errorf("first probe at %s, before its slot %s", probes[0].start.Format(time.TimeOnly), firstProbe.Local().Format(time.TimeOnly))
	}
	if probes[1].start.Before(secondProbe.Add(-slack)) {
		t.Errorf("second probe at %s, before its slot %s", probes[1].start.Format(time.TimeOnly), secondProbe.Local().Format(time.TimeOnly))
	}
	// The cut-off probe holds the slot until its lease (5s timeout + 10s
	// grace) runs out.
	if lease := probes[1].start.Add(15 * time.Second); probes[2].start.Before(lease.Add(-slack)) {
		t.Errorf("third probe at %s, before the second's lease ran out at %s", probes[2].start.Format(time.TimeOnly), lease.Local().Format(time.TimeOnly))
	}
	for i := 1; i < 3; i++ {
		if probes[i].start.Before(probes[i-1].end) {
			t.Errorf("probes %d and %d overlapped", i-1, i)
		}
	}

	// Waiting used no attempts: every attempt is a delivery the endpoint
	// saw (the cut-off probe recorded as lost), and only the probes' tasks
	// have more than the attempts before the pause and the final success.
	attempts := 0
	lost := false
	for _, id := range ids {
		outcomes := c.taskAttemptOutcomes(id)
		attempts += len(outcomes)
		lost = lost || slices.Contains(outcomes, "lost")
		if outcomes[len(outcomes)-1] != "success" {
			t.Errorf("task %s attempts = %v, want the last to succeed", id, outcomes)
		}
	}
	if attempts != len(all) {
		t.Errorf("tasks recorded %d attempts, the endpoint saw %d deliveries", attempts, len(all))
	}
	if !lost {
		t.Error("no task recorded the probe cut off by the kill as lost")
	}
}

// waitAutoPaused waits until the queue is auto-paused and ok accepts its
// auto_paused object, and returns it.
func (c *cluster) waitAutoPaused(name string, ok func(map[string]any) bool, timeout time.Duration) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		q := c.queue(name)
		if ap, _ := q["auto_paused"].(map[string]any); ap != nil && ok(ap) {
			return ap
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("queue %s not in the expected auto-pause state after %s: %v", name, timeout, q["auto_paused"])
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (c *cluster) queue(name string) map[string]any {
	c.t.Helper()
	return c.do("GET", "/v1/queues/"+name, nil)
}

func (c *cluster) waitTask(id, state string, timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		task := c.do("GET", "/v1/tasks/"+id, nil)
		if task["state"] == state {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("task %s is %s after %s, want %s (last error %v)", id, task["state"], timeout, state, task["last_error"])
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (c *cluster) taskAttemptOutcomes(id string) []string {
	c.t.Helper()
	var res struct {
		Attempts []struct {
			Number  int    `json:"number"`
			Outcome string `json:"outcome"`
		} `json:"attempts"`
	}
	c.doInto("GET", "/v1/tasks/"+id+"/attempts", nil, &res)
	slices.SortFunc(res.Attempts, func(a, b struct {
		Number  int    `json:"number"`
		Outcome string `json:"outcome"`
	}) int {
		return a.Number - b.Number
	})
	var out []string
	for _, a := range res.Attempts {
		out = append(out, a.Outcome)
	}
	return out
}

func parseTime(t *testing.T, v any) time.Time {
	t.Helper()
	s, _ := v.(string)
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("time %v: %v", v, err)
	}
	return at
}

// flakyEndpoint answers 503 after delay while down, and 200 at once once up.
type flakyEndpoint struct {
	url string
	up  atomic.Bool

	mu         sync.Mutex
	deliveries []flakyDelivery
	arrived    chan struct{}
}

type flakyDelivery struct {
	taskID, attempt string
	start, end      time.Time
	up              bool
}

func newFlakyEndpoint(t *testing.T, delay time.Duration) *flakyEndpoint {
	e := &flakyEndpoint{arrived: make(chan struct{}, 1000)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d := flakyDelivery{
			taskID:  r.Header.Get("Workly-Task-Id"),
			attempt: r.Header.Get("Workly-Attempt"),
			start:   time.Now(),
			up:      e.up.Load(),
		}
		e.mu.Lock()
		i := len(e.deliveries)
		e.deliveries = append(e.deliveries, d)
		e.mu.Unlock()
		e.arrived <- struct{}{}
		if !d.up {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		e.mu.Lock()
		e.deliveries[i].end = time.Now()
		e.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

func (e *flakyEndpoint) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.deliveries)
}

// waitArrival waits until n deliveries have arrived and returns the nth.
func (e *flakyEndpoint) waitArrival(t *testing.T, n int, timeout time.Duration) flakyDelivery {
	t.Helper()
	deadline := time.After(timeout)
	for {
		e.mu.Lock()
		if len(e.deliveries) >= n {
			d := e.deliveries[n-1]
			e.mu.Unlock()
			return d
		}
		e.mu.Unlock()
		select {
		case <-e.arrived:
		case <-deadline:
			t.Fatalf("%d deliveries after %s, want %d", e.count(), timeout, n)
		}
	}
}

func (e *flakyEndpoint) all() []flakyDelivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.deliveries)
}
