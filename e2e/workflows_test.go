//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store/storetest"
)

// A workflow whose first step hands its completion to a group keeps going
// while the leader is SIGKILLed twice: while the step waits for its group,
// and just after the group completes (the transaction that also starts the
// next step). The run must succeed, with every step's request carried by
// one task and one group created however often the fan-out step is retried.
func TestWorkflowLeaderFailover(t *testing.T) {
	if os.Getenv("WORKLY_TEST_POSTGRES_URL") == "" {
		t.Fatal("WORKLY_TEST_POSTGRES_URL not set; run `just test-e2e`")
	}
	dbURL := storetest.PostgresURL(t)
	bin := build(t)

	c := &cluster{t: t, bin: bin, dbURL: dbURL}
	c.start()
	c.start()
	c.waitLeader()
	app := newWorkflowApp(t)
	app.api.Store(&c.live()[len(c.live())-1].url)
	restart := func(when string) {
		c.killLeader(when)
		r := c.start()
		app.api.Store(&r.url)
	}

	// Short timeouts keep the leases of deliveries cut off by a kill short.
	c.do("PUT", "/v1/queues/_workflows", map[string]any{"timeout_seconds": 5, "min_backoff_seconds": 1})
	c.do("PUT", "/v1/queues/e2e-members", map[string]any{"timeout_seconds": 5, "min_backoff_seconds": 1})
	c.do("PUT", "/v1/workflows/failover", map[string]any{
		"steps": []map[string]any{
			{"name": "fanout", "url": app.url + "/fanout"},
			{"name": "side", "url": app.url + "/step"},
			{"name": "after", "url": app.url + "/step", "depends_on": []string{"fanout"}},
			{"name": "last", "url": app.url + "/step", "depends_on": []string{"after", "side"}},
		},
	})
	run := c.do("POST", "/v1/workflows/failover/runs", map[string]any{"input": map[string]any{"n": 1}})
	runID := run["id"].(string)

	// 1. While fanout waits for its group, whose tasks are still to run.
	step := c.waitStep(runID, "fanout", "waiting", 30*time.Second)
	groupID := step["wait_for"].(map[string]any)["id"].(string)
	restart("while fanout waits for " + groupID)

	// 2. Just after the group completes, which succeeds fanout and starts after.
	c.waitGroup(groupID, "succeeded", 60*time.Second)
	restart("just after " + groupID + " completed")

	final := c.waitWorkflowRun(runID, 120*time.Second)
	if final["status"] != "succeeded" {
		t.Fatalf("run %s is %s (%v): %v", runID, final["status"], final["error"], final["steps"])
	}
	for _, s := range final["steps"].([]any) {
		s := s.(map[string]any)
		if s["status"] != "succeeded" {
			t.Errorf("step %s is %s: %v", s["name"], s["status"], s["error"])
		}
		// Every delivery of a step carried its one task: a retry reuses it,
		// and a step is never started twice.
		var attempts struct {
			Attempts []struct {
				Number  int    `json:"number"`
				Outcome string `json:"outcome"`
				Error   string `json:"error"`
			} `json:"attempts"`
		}
		c.doInto("GET", "/v1/tasks/"+s["task_id"].(string)+"/attempts", nil, &attempts)
		t.Logf("step %s: task %s, attempts %+v", s["name"], s["task_id"], attempts.Attempts)
		tasks := app.tasksOf(s["id"].(string))
		if len(tasks) != 1 || !tasks[s["task_id"].(string)] {
			t.Errorf("step %s was delivered by tasks %v, want only %v", s["name"], tasks, s["task_id"])
		}
	}
	if groups := app.groupIDs(); len(groups) != 1 {
		t.Errorf("fanout created groups %v, want one", groups)
	}
	t.Logf("run %s succeeded in %v ms; deliveries per step: %v", runID, final["duration_ms"], app.counts())
}

// workflowApp is the application a workflow calls: /fanout starts a group of
// member tasks through the API and hands the step to it, /step and /member
// just answer.
type workflowApp struct {
	t   *testing.T
	url string
	api atomic.Pointer[string]

	mu     sync.Mutex
	tasks  map[string]map[string]bool // step ID → task IDs that delivered it
	count  map[string]int             // step name → deliveries
	groups map[string]bool
}

func newWorkflowApp(t *testing.T) *workflowApp {
	a := &workflowApp{t: t, tasks: map[string]map[string]bool{}, count: map[string]int{}, groups: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /fanout", func(w http.ResponseWriter, r *http.Request) {
		stepID := a.record(r)
		var group struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		// Keyed on the step, so a retried request reuses its group.
		if err := a.call("POST", "/v1/groups", map[string]any{"idempotency_key": stepID}, &group); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		a.mu.Lock()
		a.groups[group.ID] = true
		a.mu.Unlock()
		if group.Status == "open" {
			for i := range 3 {
				err := a.call("POST", "/v1/queues/e2e-members/tasks", map[string]any{
					"url":             a.url + "/member",
					"group_id":        group.ID,
					"idempotency_key": fmt.Sprintf("%s-%d", stepID, i),
					// Late enough that the leader is killed while the step waits.
					"delay_seconds": 8 + 2*i,
				}, nil)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
			}
			if err := a.call("POST", "/v1/groups/"+group.ID+"/close", nil, nil); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		w.Header().Set("Workly-Wait-For", "group/"+group.ID)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /step", func(w http.ResponseWriter, r *http.Request) {
		a.record(r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /member", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a.url = srv.URL
	return a
}

func (a *workflowApp) record(r *http.Request) string {
	stepID := r.Header.Get("Workly-Workflow-Step-Id")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tasks[stepID] == nil {
		a.tasks[stepID] = map[string]bool{}
	}
	a.tasks[stepID][r.Header.Get("Workly-Task-Id")] = true
	a.count[r.Header.Get("Workly-Workflow-Step")]++
	return stepID
}

// call uses the API from the handler's goroutine, so it reports errors
// rather than failing the test; the step is retried.
func (a *workflowApp) call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, *a.api.Load()+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s = %d %s", method, path, res.StatusCode, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (a *workflowApp) tasksOf(stepID string) map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tasks[stepID]
}

func (a *workflowApp) groupIDs() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.groups
}

func (a *workflowApp) counts() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.count
}

func (c *cluster) waitStep(runID, name, status string, timeout time.Duration) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run := c.do("GET", "/v1/workflow-runs/"+runID, nil)
		for _, s := range run["steps"].([]any) {
			if s := s.(map[string]any); s["name"] == name && s["status"] == status {
				return s
			}
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("step %s not %s after %s: %v", name, status, timeout, run["steps"])
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (c *cluster) waitGroup(id, status string, timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		g := c.do("GET", "/v1/groups/"+id, nil)
		if g["status"] == status {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("group %s is %s after %s, want %s", id, g["status"], timeout, status)
		}
		// Polled closely, so the kill lands right after the group completes.
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *cluster) waitWorkflowRun(id string, timeout time.Duration) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run := c.do("GET", "/v1/workflow-runs/"+id, nil)
		if run["status"] != "running" {
			return run
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("run %s still running after %s: %v", id, timeout, run["steps"])
		}
		time.Sleep(200 * time.Millisecond)
	}
}
