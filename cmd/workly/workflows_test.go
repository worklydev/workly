package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowsCLI(t *testing.T) {
	newTestServer(t, "")
	file := filepath.Join(t.TempDir(), "workly.yaml")
	if err := os.WriteFile(file, []byte(`version: 1
workflows:
  daily-refresh:
    steps:
      - {name: prices, url: "https://api.example.com/prices"}
      - {name: fx, url: "https://api.example.com/fx"}
      - {name: analytics, url: "https://api.example.com/analytics", depends_on: [prices, fx]}
schedules:
  nightly:
    cron: "0 3 * * *"
    workflow: daily-refresh
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := mustRun(t, "validate", "-f", file); !strings.Contains(out, "1 workflow, 1 schedule") {
		t.Fatalf("validate = %q", out)
	}
	out := mustRun(t, "deploy", "-f", file, "--yes")
	if !strings.Contains(out, "Created workflow daily-refresh") || !strings.Contains(out, "Created schedule nightly") ||
		strings.Index(out, "Created workflow") > strings.Index(out, "Created schedule") {
		t.Fatalf("deploy = %q", out)
	}
	if out := mustRun(t, "plan", "-f", file); !strings.Contains(out, "No changes") {
		t.Fatalf("plan after deploy = %q", out)
	}

	if out := mustRun(t, "workflows", "ls"); !strings.Contains(out, "daily-refresh") || !strings.Contains(out, "3") {
		t.Fatalf("ls = %q", out)
	}
	if out := mustRun(t, "workflows", "get", "daily-refresh"); !strings.Contains(out, "prices, fx") {
		t.Fatalf("get = %q", out)
	}
	id := strings.TrimSpace(mustRun(t, "workflows", "trigger", "daily-refresh", "--input", `{"date": "2026-11-13"}`, "-k", "d1"))
	if !strings.HasPrefix(id, "wfr_") {
		t.Fatalf("trigger = %q", id)
	}
	if again := cli(t, "", "workflows", "trigger", "daily-refresh", "-k", "d1"); strings.TrimSpace(again.stdout) != id ||
		!strings.Contains(again.stderr, "already exists") {
		t.Fatalf("trigger again = %+v", again)
	}
	out = mustRun(t, "workflows", "run", id)
	if !strings.Contains(out, "analytics") || !strings.Contains(out, "waiting for prices, fx") || !strings.Contains(out, "queued") {
		t.Fatalf("run = %q", out)
	}
	if out := mustRun(t, "workflows", "runs", "daily-refresh"); !strings.Contains(out, id) {
		t.Fatalf("runs = %q", out)
	}
	if out := mustRun(t, "workflows", "cancel", id); !strings.Contains(out, "cancelled") {
		t.Fatalf("cancel = %q", out)
	}
	if out := mustRun(t, "schedules", "get", "nightly"); !strings.Contains(out, "start workflow daily-refresh") {
		t.Fatalf("schedule = %q", out)
	}
	if out := mustRun(t, "workflows", "rm", "daily-refresh", "--yes"); !strings.Contains(out, "Deleted") {
		t.Fatalf("rm = %q", out)
	}
}
