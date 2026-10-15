//go:build e2e

// End-to-end tests against real `workly serve` processes and Postgres. They
// take minutes of wall-clock time (cron's resolution is a minute), so they
// only build with the e2e tag: `just test-e2e`.
package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store/storetest"
)

// An every-minute schedule keeps one run per slot while the leader is
// SIGKILLed at three points: mid-delivery, just before a slot is due, and just
// after it fired. Each kill is followed by a replacement replica, so two are
// always running against the same database.
func TestScheduleLeaderFailover(t *testing.T) {
	if os.Getenv("WORKLY_TEST_POSTGRES_URL") == "" {
		t.Fatal("WORKLY_TEST_POSTGRES_URL not set; run `just test-e2e`")
	}
	dbURL := storetest.PostgresURL(t)
	bin := build(t)
	endpoint := newEndpoint(t, 5*time.Second)

	c := &cluster{t: t, bin: bin, dbURL: dbURL}
	first := c.start()
	c.start()
	leader := c.waitLeader()
	if leader != first {
		t.Fatalf("leader is %s, want the first replica", leader.name)
	}

	created := time.Now()
	// Defaults for overlap and missed (skip), so a late or doubled slot shows
	// up as a skipped run. The timeout keeps the lease short: 10s + 10s grace.
	res := c.do("PUT", "/v1/schedules/failover", map[string]any{
		"cron":            "* * * * *",
		"url":             endpoint.url + "/hook",
		"timeout_seconds": 10,
	})
	slot0, _ := time.Parse(time.RFC3339, res["next_run_at"].(string))
	if !slot0.After(created) || slot0.Sub(created) > time.Minute {
		t.Fatalf("next_run_at = %v, created at %v", slot0, created)
	}
	slot := func(i int) time.Time { return slot0.Add(time.Duration(i) * time.Minute) }
	t.Logf("first slot %s, in %s", slot0.Format(time.TimeOnly), time.Until(slot0).Round(time.Second))

	// 1. Mid-delivery: the run's lease expires, the new leader records a lost
	// attempt and delivers it again.
	d := endpoint.waitDelivery(t, slot(0))
	time.Sleep(2 * time.Second)
	c.killLeader("mid-delivery of " + d.scheduledFor)
	c.start()

	// 2. Just before a slot: the next leader has to fire it, late but within
	// the minute after which a slot counts as missed.
	sleepUntil(slot(1).Add(-time.Second))
	c.killLeader("1s before " + slot(1).Format(time.TimeOnly))
	c.start()

	// 3. Just after a slot: the run may already exist and be claimed; the
	// next leader must neither fire it again nor leave it undelivered.
	sleepUntil(slot(2).Add(300 * time.Millisecond))
	c.killLeader("300ms after " + slot(2).Format(time.TimeOnly))
	c.start()

	endpoint.waitDelivery(t, slot(3))
	c.do("POST", "/v1/schedules/failover/pause", nil)
	runs := c.waitRunsFinished("failover", 90*time.Second)

	// One run per slot, consecutive from the first, all delivered.
	if len(runs) < 4 {
		t.Fatalf("got %d runs, want at least 4: %v", len(runs), runs)
	}
	for i, r := range runs {
		if r.ScheduledFor != slot(i).UTC().Format(time.RFC3339) {
			t.Errorf("run %d is for %s, want %s (missing or duplicate slot)", i, r.ScheduledFor, slot(i).UTC().Format(time.RFC3339))
		}
		if r.Trigger != "schedule" || r.State != "succeeded" {
			t.Errorf("run %s for %s: trigger %s, state %s (skip reason %q, last error %q)",
				r.ID, r.ScheduledFor, r.Trigger, r.State, r.SkipReason, r.LastError)
		}
	}

	// The run killed mid-delivery was recovered as lost, then succeeded.
	outcomes := c.attemptOutcomes(runs[0].ID)
	if !slices.Contains(outcomes, "lost") || outcomes[len(outcomes)-1] != "success" {
		t.Errorf("slot 0 attempts = %v, want a lost attempt and then success", outcomes)
	}

	// Every delivery for a slot carried the same run, the one the API shows.
	byRun := map[string]string{}
	for _, r := range runs {
		byRun[r.ScheduledFor] = r.ID
	}
	for _, d := range endpoint.all() {
		if want, ok := byRun[d.scheduledFor]; ok && d.runID != want {
			t.Errorf("delivery for %s carried run %s, want %s", d.scheduledFor, d.runID, want)
		}
	}
	for i, r := range runs {
		t.Logf("slot %d %s: %s, attempts %v, created %s late",
			i, r.ScheduledFor, r.ID, c.attemptOutcomes(r.ID), r.lag().Round(time.Millisecond))
	}
}

func build(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("WORKLY_BIN"); bin != "" {
		return bin
	}
	bin := filepath.Join(t.TempDir(), "workly")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/workly")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func sleepUntil(at time.Time) { time.Sleep(time.Until(at)) }

// cluster runs `workly serve` replicas against one database.
type cluster struct {
	t        *testing.T
	bin      string
	dbURL    string
	replicas []*replica
	n        int
}

type replica struct {
	name string
	url  string
	cmd  *exec.Cmd
	log  string
	dead bool
}

func (c *cluster) start() *replica {
	c.t.Helper()
	c.n++
	port := freePort(c.t)
	r := &replica{
		name: fmt.Sprintf("replica-%d", c.n),
		url:  fmt.Sprintf("http://127.0.0.1:%d", port),
		log:  filepath.Join(c.t.TempDir(), fmt.Sprintf("replica-%d.log", c.n)),
	}
	logFile, err := os.Create(r.log)
	if err != nil {
		c.t.Fatal(err)
	}
	r.cmd = exec.Command(c.bin, "serve")
	r.cmd.Env = append(os.Environ(),
		"WORKLY_DATABASE_URL="+c.dbURL,
		"WORKLY_SIGNING_SECRET=e2e-secret",
		fmt.Sprintf("WORKLY_PORT=%d", port),
		"WORKLY_API_KEY=",
		"WORKLY_METRICS_PORT=",
		"WORKLY_LOG_FORMAT=text",
	)
	r.cmd.Stdout, r.cmd.Stderr = logFile, logFile
	if err := r.cmd.Start(); err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		if !r.dead {
			_ = r.cmd.Process.Signal(syscall.SIGTERM)
			_ = r.cmd.Wait()
		}
		logFile.Close()
		if c.t.Failed() {
			out, _ := os.ReadFile(r.log)
			c.t.Logf("%s log:\n%s", r.name, out)
		}
	})
	c.replicas = append(c.replicas, r)

	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := http.Get(r.url + "/readyz")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return r
			}
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("%s not ready after 30s: %v", r.name, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (c *cluster) live() []*replica {
	var live []*replica
	for _, r := range c.replicas {
		if !r.dead {
			live = append(live, r)
		}
	}
	return live
}

// waitLeader returns the one live replica that reports itself leader.
func (c *cluster) waitLeader() *replica {
	c.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var leaders []*replica
		for _, r := range c.live() {
			if r.isLeader() {
				leaders = append(leaders, r)
			}
		}
		if len(leaders) > 1 {
			c.t.Fatalf("%d replicas report themselves leader", len(leaders))
		}
		if len(leaders) == 1 {
			return leaders[0]
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.t.Fatal("no leader after 30s")
	return nil
}

func (r *replica) isLeader() bool {
	res, err := http.Get(r.url + "/metrics")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return strings.Contains(string(body), "\nworkly_dispatcher_is_leader 1\n")
}

// killLeader SIGKILLs the leader and waits for another replica to take over.
func (c *cluster) killLeader(when string) {
	c.t.Helper()
	old := c.waitLeader()
	if err := old.cmd.Process.Kill(); err != nil {
		c.t.Fatal(err)
	}
	_ = old.cmd.Wait()
	old.dead = true
	killed := time.Now()
	next := c.waitLeader()
	c.t.Logf("killed %s %s; %s took over in %s", old.name, when, next.name, time.Since(killed).Round(time.Millisecond))
}

func (c *cluster) do(method, path string, body any) map[string]any {
	c.t.Helper()
	var out map[string]any
	c.doInto(method, path, body, &out)
	return out
}

func (c *cluster) doInto(method, path string, body, out any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	live := c.live()
	req, _ := http.NewRequest(method, live[len(live)-1].url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		c.t.Fatalf("%s %s = %d %s", method, path, res.StatusCode, b)
	}
	if err := json.Unmarshal(b, out); err != nil {
		c.t.Fatalf("%s %s: %v: %s", method, path, err, b)
	}
}

type run struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	Trigger      string `json:"trigger"`
	ScheduledFor string `json:"scheduled_for"`
	CreatedAt    string `json:"created_at"`
	SkipReason   string `json:"skip_reason"`
	LastError    string `json:"last_error"`
}

func (r run) lag() time.Duration {
	s, _ := time.Parse(time.RFC3339Nano, r.ScheduledFor)
	c, _ := time.Parse(time.RFC3339Nano, r.CreatedAt)
	return c.Sub(s)
}

// waitRunsFinished returns the schedule's runs, oldest first, once none is
// pending or running.
func (c *cluster) waitRunsFinished(schedule string, timeout time.Duration) []run {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var runs []run
		cursor := ""
		for {
			var page struct {
				Runs       []run  `json:"runs"`
				NextCursor string `json:"next_cursor"`
			}
			c.doInto("GET", "/v1/schedules/"+schedule+"/runs?limit=100&cursor="+cursor, nil, &page)
			runs = append(runs, page.Runs...)
			if cursor = page.NextCursor; cursor == "" {
				break
			}
		}
		if !slices.ContainsFunc(runs, func(r run) bool { return r.State == "pending" || r.State == "running" }) {
			slices.SortFunc(runs, func(a, b run) int { return strings.Compare(a.ScheduledFor, b.ScheduledFor) })
			return runs
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("runs still unfinished after %s: %+v", timeout, runs)
		}
		time.Sleep(time.Second)
	}
}

func (c *cluster) attemptOutcomes(runID string) []string {
	c.t.Helper()
	type attempt struct {
		Number  int    `json:"number"`
		Outcome string `json:"outcome"`
	}
	var res struct {
		Attempts []attempt `json:"attempts"`
	}
	c.doInto("GET", "/v1/runs/"+runID+"/attempts", nil, &res)
	slices.SortFunc(res.Attempts, func(a, b attempt) int { return a.Number - b.Number })
	var out []string
	for _, a := range res.Attempts {
		out = append(out, a.Outcome)
	}
	return out
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// endpoint records every delivery and takes delay to answer, so a delivery
// is in flight long enough to kill the leader during it.
type endpoint struct {
	url string

	mu         sync.Mutex
	deliveries []delivery
	arrived    chan delivery
}

type delivery struct {
	runID, scheduledFor, attempt string
}

func newEndpoint(t *testing.T, delay time.Duration) *endpoint {
	e := &endpoint{arrived: make(chan delivery, 100)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d := delivery{
			runID:        r.Header.Get("Workly-Run-Id"),
			scheduledFor: r.Header.Get("Workly-Scheduled-For"),
			attempt:      r.Header.Get("Workly-Attempt"),
		}
		e.mu.Lock()
		e.deliveries = append(e.deliveries, d)
		e.mu.Unlock()
		e.arrived <- d
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

// waitDelivery waits for the first delivery of the run scheduled for at.
func (e *endpoint) waitDelivery(t *testing.T, at time.Time) delivery {
	t.Helper()
	want := at.UTC().Format(time.RFC3339)
	timeout := time.After(time.Until(at) + time.Minute)
	for {
		select {
		case d := <-e.arrived:
			if d.scheduledFor == want {
				return d
			}
		case <-timeout:
			t.Fatalf("no delivery for %s", want)
		}
	}
}

func (e *endpoint) all() []delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.deliveries)
}
