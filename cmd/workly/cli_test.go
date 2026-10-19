package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/worklydev/workly/server"
)

// newTestServer serves the API without a dispatcher, so tasks stay put.
func newTestServer(t *testing.T, apiKey string) string {
	t.Helper()
	st, err := server.OpenStore(t.Context(), "sqlite://"+filepath.Join(t.TempDir(), "workly.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureWorkspace(t.Context(), server.DefaultWorkspace, "Default"); err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{
		Store:             st,
		WorkspaceResolver: server.StaticResolver(apiKey),
		Logger:            slog.New(slog.DiscardHandler),
		SigningSecret:     "test",
		DisableDispatcher: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Setenv("WORKLY_URL", ts.URL)
	t.Setenv("WORKLY_API_KEY", "")
	return ts.URL
}

type result struct {
	stdout, stderr string
	err            error
}

func cli(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := rootCmd()
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.ExecuteContext(t.Context())
	return result{stdout.String(), stderr.String(), err}
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	r := cli(t, "", args...)
	if r.err != nil {
		t.Fatalf("workly %s: %v", strings.Join(args, " "), r.err)
	}
	return r.stdout
}

func contains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q:\n%s", w, got)
		}
	}
}

func TestQueues(t *testing.T) {
	newTestServer(t, "")

	contains(t, mustRun(t, "queues", "ls"), "No queues yet")

	out := mustRun(t, "queues", "put", "emails", "--max-attempts", "8", "--rate", "50", "--timeout", "10s")
	contains(t, out, "Max attempts:     8", "Rate:             50/s", "Timeout:          10s", "Max concurrency:  3")

	// Declarative: settings left out go back to their defaults.
	contains(t, mustRun(t, "queues", "put", "emails"), "Rate:             no limit")

	contains(t, mustRun(t, "queues", "pause", "emails"), "Queue emails paused.")
	mustRun(t, "tasks", "enqueue", "emails", "https://example.com/hook")
	out = mustRun(t, "queues", "ls")
	contains(t, out, "NAME", "emails  paused  1")

	var q struct {
		Name   string `json:"name"`
		Paused bool   `json:"paused"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "queues", "get", "emails", "--json")), &q); err != nil {
		t.Fatal(err)
	}
	if q.Name != "emails" || !q.Paused {
		t.Fatalf("queues get --json = %+v", q)
	}
	contains(t, mustRun(t, "queues", "resume", "emails"), "Queue emails resumed.")

	if r := cli(t, "", "queues", "cancel-pending", "emails"); r.err == nil || !strings.Contains(r.err.Error(), "--yes") {
		t.Fatalf("cancel-pending without --yes: %v", r.err)
	}
	contains(t, mustRun(t, "queues", "cancel-pending", "emails", "--yes"), "Cancelled 1 pending task.")
	contains(t, mustRun(t, "queues", "failures", "emails"), "No failed tasks.")
	contains(t, mustRun(t, "queues", "retry-failed", "emails"), "Retrying 0 failed tasks.")
	contains(t, mustRun(t, "queues", "delete", "emails", "-y"), "Queue emails deleted.")

	r := cli(t, "", "queues", "get", "emails")
	if r.err == nil || r.err.Error() != `queue_not_found: queue "emails" does not exist` {
		t.Fatalf("get deleted queue: %v", r.err)
	}
}

func TestTasks(t *testing.T) {
	newTestServer(t, "")

	id := strings.TrimSpace(mustRun(t, "tasks", "enqueue", "emails", "https://example.com/hook",
		"-d", `{"user_id": 123}`, "-H", "X-Tenant: acme", "-k", "welcome-123", "--max-attempts", "3"))
	if !strings.HasPrefix(id, "task_") {
		t.Fatalf("enqueue printed %q, want a task ID", id)
	}

	// Same idempotency key: same task, and a note on stderr.
	r := cli(t, "", "tasks", "enqueue", "emails", "https://example.com/hook", "-k", "welcome-123")
	if r.err != nil || strings.TrimSpace(r.stdout) != id || !strings.Contains(r.stderr, "already exists") {
		t.Fatalf("repeat enqueue = %+v", r)
	}

	out := mustRun(t, "tasks", "get", id)
	contains(t, out, "ID:               "+id, "State:            pending", "POST https://example.com/hook",
		"Attempts:         0/3", "Idempotency key:  welcome-123", `"user_id": 123`)

	var task struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "tasks", "get", id, "--json")), &task); err != nil {
		t.Fatal(err)
	}
	if task.Headers["X-Tenant"] != "acme" {
		t.Fatalf("headers = %v", task.Headers)
	}

	scheduled := strings.TrimSpace(mustRun(t, "tasks", "enqueue", "reports", "https://example.com/build",
		"--delay", "1h", "--body", "-", "--content-type", "text/csv"))
	out = mustRun(t, "tasks", "ls", "--queue", "reports")
	contains(t, out, scheduled, "scheduled")
	if strings.Contains(out, id) {
		t.Fatalf("ls --queue reports lists a task from emails:\n%s", out)
	}

	r = cli(t, "", "tasks", "ls", "--limit", "1")
	contains(t, r.stdout, scheduled)
	contains(t, r.stderr, "--cursor "+scheduled)

	contains(t, mustRun(t, "tasks", "cancel", id), "Task "+id+" is cancelled.")
	contains(t, mustRun(t, "tasks", "retry", id), "Task "+id+" is pending.")
}

func TestEnqueueInput(t *testing.T) {
	newTestServer(t, "")
	file := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(file, []byte(`{"from": "file"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		stdin string
		args  []string
		want  string
	}{
		"payload from stdin": {`{"from": "stdin"}`, []string{"-d", "-"}, `"from": "stdin"`},
		"payload from file":  {"", []string{"-d", "@" + file}, `"from": "file"`},
		"raw body":           {"", []string{"--body", "a,b\n1,2"}, "a,b"},
	} {
		t.Run(name, func(t *testing.T) {
			r := cli(t, tc.stdin, append([]string{"tasks", "enqueue", "q", "https://example.com"}, tc.args...)...)
			if r.err != nil {
				t.Fatal(r.err)
			}
			contains(t, mustRun(t, "tasks", "get", strings.TrimSpace(r.stdout)), tc.want)
		})
	}

	for name, args := range map[string][]string{
		"invalid JSON":        {"-d", "nope"},
		"payload and body":    {"-d", "{}", "--body", "x"},
		"delay and at":        {"--delay", "1m", "--at", "2030-01-01T00:00:00Z"},
		"bad header":          {"-H", "no colon"},
		"bad time":            {"--at", "tomorrow"},
		"server-side invalid": {"--max-attempts", "5000"},
	} {
		t.Run(name, func(t *testing.T) {
			if r := cli(t, "", append([]string{"tasks", "enqueue", "q", "https://example.com"}, args...)...); r.err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestAuth(t *testing.T) {
	newTestServer(t, "secret")

	r := cli(t, "", "queues", "ls")
	if r.err == nil || !strings.Contains(r.err.Error(), "WORKLY_API_KEY") {
		t.Fatalf("without a key: %v", r.err)
	}
	mustRun(t, "queues", "ls", "--api-key", "secret")
	t.Setenv("WORKLY_API_KEY", "secret")
	mustRun(t, "queues", "ls")
}

func TestConnectionRefused(t *testing.T) {
	r := cli(t, "", "queues", "ls", "--url", "http://127.0.0.1:1")
	if r.err == nil || !strings.Contains(r.err.Error(), "workly dev") {
		t.Fatalf("err = %v, want a hint to start a server", r.err)
	}
}

func TestHealthcheck(t *testing.T) {
	url := newTestServer(t, "secret")
	// Readiness needs no API key.
	if r := cli(t, "", "healthcheck", "--url", url); r.err != nil {
		t.Fatalf("healthcheck: %v", r.err)
	}
	if r := cli(t, "", "healthcheck", "--url", "http://127.0.0.1:1"); r.err == nil {
		t.Fatal("healthcheck against a closed port succeeded")
	}
}

func TestSchedules(t *testing.T) {
	newTestServer(t, "")

	contains(t, mustRun(t, "schedules", "ls"), "No schedules yet")
	if r := cli(t, "", "schedules", "put", "nightly", "https://example.com/report"); r.err == nil || !strings.Contains(r.err.Error(), "cron") {
		t.Fatalf("put without --cron: %v", r.err)
	}
	out := mustRun(t, "schedules", "put", "nightly", "https://example.com/report", "--cron", "0 3 * * *",
		"--timezone", "Europe/Stockholm", "-d", `{"kind": "daily"}`, "--max-attempts", "5", "--overlap", "allow")
	contains(t, out, "0 3 * * * (Europe/Stockholm)", "POST https://example.com/report", "Max attempts:", "allow", "Next runs:", `"kind": "daily"`)

	r := cli(t, "", "schedules", "put", "bad", "https://example.com", "--cron", "every day")
	if r.err == nil || !strings.HasPrefix(r.err.Error(), "invalid_request: cron:") {
		t.Fatalf("put with a bad cron: %v", r.err)
	}

	contains(t, mustRun(t, "schedules", "pause", "nightly"), "Schedule nightly paused.")
	contains(t, mustRun(t, "schedules", "ls"), "nightly", "paused")
	contains(t, mustRun(t, "schedules", "resume", "nightly"), "Schedule nightly resumed. Next run in")

	id := strings.TrimSpace(mustRun(t, "schedules", "trigger", "nightly"))
	if !strings.HasPrefix(id, "run_") {
		t.Fatalf("trigger printed %q, want a run ID", id)
	}
	contains(t, mustRun(t, "schedules", "runs", "nightly"), id, "pending", "manual")
	contains(t, mustRun(t, "schedules", "get", "nightly"), "Recent runs:", id)
	contains(t, mustRun(t, "runs", "get", id), "Schedule:", "nightly", "Trigger:", "manual")
	contains(t, mustRun(t, "runs", "cancel", id), "Run "+id+" is cancelled.")
	contains(t, mustRun(t, "runs", "retry", id), "Run "+id+" is pending.")
	contains(t, mustRun(t, "schedules", "runs", "nightly", "--state", "failed"), "No runs.")

	var s struct {
		Name     string   `json:"name"`
		NextRuns []string `json:"next_runs"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "schedules", "get", "nightly", "--json")), &s); err != nil {
		t.Fatal(err)
	}
	if s.Name != "nightly" || len(s.NextRuns) != 5 {
		t.Fatalf("schedules get --json = %+v", s)
	}

	contains(t, mustRun(t, "schedules", "delete", "nightly", "-y"), "Schedule nightly deleted.")
	if r := cli(t, "", "runs", "get", id); r.err == nil || !strings.HasPrefix(r.err.Error(), "run_not_found") {
		t.Fatalf("get a deleted schedule's run: %v", r.err)
	}
}

func TestGroups(t *testing.T) {
	newTestServer(t, "")

	group := strings.TrimSpace(mustRun(t, "groups", "create", "--name", "daily-prices",
		"--callback-queue", "callbacks", "--callback-url", "https://example.com/refresh", "-H", "X-Job: prices"))
	if !strings.HasPrefix(group, "grp_") {
		t.Fatalf("create printed %q, want a group ID", group)
	}
	task := strings.TrimSpace(mustRun(t, "tasks", "enqueue", "prices", "https://example.com/price", "--group", group))
	contains(t, mustRun(t, "tasks", "get", task), "Group:     "+group)
	contains(t, mustRun(t, "tasks", "ls", "--group", group), task)

	contains(t, mustRun(t, "groups", "get", group), "Name:      daily-prices", "Status:    open", "Progress:  0/1",
		"1 remaining", "callbacks → https://example.com/refresh")
	contains(t, mustRun(t, "groups", "close", group), "Group "+group+" is running.")
	contains(t, mustRun(t, "groups", "ls", "--status", "running"), group, "daily-prices", "0/1")

	if r := cli(t, "", "tasks", "enqueue", "prices", "https://example.com/price", "--group", group); r.err == nil || !strings.Contains(r.err.Error(), "group_closed") {
		t.Fatalf("enqueue into closed group = %+v", r)
	}
	// Without --callback-queue, the callback goes to _callbacks.
	group = strings.TrimSpace(mustRun(t, "groups", "create", "--callback-url", "https://example.com/refresh"))
	contains(t, mustRun(t, "groups", "get", group), "_callbacks → https://example.com/refresh")
	if r := cli(t, "", "groups", "create", "--callback-queue", "callbacks"); r.err == nil {
		t.Fatalf("create with a callback queue but no URL succeeded")
	}
}

func TestDeploy(t *testing.T) {
	newTestServer(t, "")
	t.Setenv("API_URL", "https://api.example.com")
	t.Setenv("JOBS_TOKEN", "s3cret")
	path := filepath.Join(t.TempDir(), "workly.yaml")
	write := func(src string) {
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`version: 1
queues:
  stock-prices:
    max_concurrency: 10
    rate_per_second: 2
schedules:
  nightly:
    cron: "0 22 * * 1-5"
    timezone: Europe/Stockholm
    url: ${API_URL}/jobs/nightly
    headers: {Authorization: "Bearer ${JOBS_TOKEN}"}
    payload: {full: true}
`)
	contains(t, mustRun(t, "validate", "-f", path), "is valid: 1 queue, 1 schedule.")
	mustRun(t, "schedules", "put", "legacy", "https://example.com/legacy", "--cron", "@daily")

	out := mustRun(t, "plan", "-f", path)
	contains(t, out, "+ create queue stock-prices", "+ create schedule nightly", "2 to create",
		"Not in "+path+", left as they are: schedule legacy.")
	if r := cli(t, "", "deploy", "-f", path); r.err == nil || !strings.Contains(r.err.Error(), "--yes") {
		t.Fatalf("deploy without --yes: %v", r.err)
	}
	contains(t, mustRun(t, "deploy", "-f", path, "--yes"), "Created queue stock-prices", "Created schedule nightly",
		"Deployed "+path+": 2 created, 0 updated.")
	contains(t, mustRun(t, "queues", "get", "stock-prices"), "Max concurrency:  10")

	// Deploying again changes nothing.
	contains(t, mustRun(t, "deploy", "-f", path, "--yes"), "No changes: ", "(2 unchanged)")

	// A changed setting and secret: the plan names the header but not its value.
	mustRun(t, "schedules", "pause", "nightly")
	t.Setenv("JOBS_TOKEN", "rotated")
	out = mustRun(t, "deploy", "-f", path, "--yes")
	contains(t, out, "~ update schedule nightly (paused, stays paused)", "headers.Authorization: changed", "Updated schedule nightly")
	if strings.Contains(out, "rotated") || strings.Contains(out, "s3cret") {
		t.Errorf("deploy printed a header value:\n%s", out)
	}
	contains(t, mustRun(t, "schedules", "get", "nightly"), "paused")

	write("version: 1\nqueues:\n  stock-prices: {max_concurency: 3}\n")
	r := cli(t, "", "plan", "-f", path)
	if r.err == nil || !strings.Contains(r.err.Error(), path+":3: queues.stock-prices.max_concurency: unknown field: did you mean max_concurrency?") {
		t.Fatalf("plan with an invalid file: %v", r.err)
	}
	if r := cli(t, "", "validate", "-f", filepath.Join(t.TempDir(), "missing.yaml")); r.err == nil || !strings.Contains(r.err.Error(), "not found") {
		t.Fatalf("validate a missing file: %v", r.err)
	}
}
