package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/worklydev/workly/internal/api"
)

const workflowFile = `
version: 1
workflows:
  daily-refresh:
    max_duration_seconds: 7200
    steps:
      - name: prices
        url: ${API_URL}/jobs/prices
      - name: fx
        url: ${API_URL}/jobs/fx
        queue: fx
        max_attempts: 3
      - name: analytics
        url: ${API_URL}/jobs/analytics
        depends_on: [prices, fx]
        payload: {full: true}
schedules:
  daily-refresh:
    cron: "0 3 * * *"
    workflow: daily-refresh
    payload: {source: schedule}
`

func TestParseWorkflows(t *testing.T) {
	f := parse(t, workflowFile)
	if len(f.Workflows) != 1 || !f.HasWorkflows {
		t.Fatalf("workflows = %+v", f.Workflows)
	}
	w := f.Workflows[0]
	if w.Name != "daily-refresh" || len(w.settings.Steps) != 3 || w.settings.MaxDuration.Hours() != 2 {
		t.Fatalf("workflow = %+v", w.settings)
	}
	if s := w.settings.Steps[1]; s.Queue != "fx" || s.URL != "https://api.example.com/jobs/fx" || *s.MaxAttempts != 3 {
		t.Errorf("fx = %+v", s)
	}
	if s := w.settings.Steps[2]; strings.Join(s.DependsOn, ",") != "prices,fx" || string(s.Body) != `{"full":true}` {
		t.Errorf("analytics = %+v", s)
	}
	if s := f.Schedules[0].settings; s.Workflow != "daily-refresh" || string(s.Body) != `{"source":"schedule"}` {
		t.Errorf("schedule = %+v", s)
	}
}

func TestParseWorkflowProblems(t *testing.T) {
	for _, tt := range []struct{ src, want string }{
		{"version: 1\nworkflows:\n  a: {}", "workflows.a.steps: required"},
		{"version: 1\nworkflows:\n  a: {steps: x}", "workflows.a.steps: expected a list of steps"},
		{"version: 1\nworkflows:\n  a: {stepz: []}", "workflows.a.stepz: unknown field: did you mean steps?"},
		{"version: 1\nworkflows:\n  a:\n    steps:\n      - {name: x}", "workflows.a.steps[0].url: required"},
		{"version: 1\nworkflows:\n  a:\n    steps:\n      - {name: x, url: 'https://x.test', needs: [y]}", "workflows.a.steps[0].needs: unknown field: use depends_on"},
		{"version: 1\nworkflows:\n  a:\n    steps:\n      - {name: x, url: 'https://x.test'}\n      - {name: x, url: 'https://x.test'}",
			`workly.yaml:6: workflows.a.steps[1]: duplicate step name "x"`},
		{"version: 1\nworkflows:\n  a:\n    steps:\n      - {name: x, url: 'https://x.test', depends_on: [y]}\n      - {name: y, url: 'https://x.test', depends_on: [x]}",
			"steps form a cycle: x → y → x"},
		{"version: 1\nworkflows:\n  a:\n    steps:\n      - {name: x, url: 'ftp://x.test'}", "workflows.a.steps[0]: url must be an absolute http or https URL"},
		{"version: 1\nworkflows:\n  Bad: {steps: []}", `workflow name "Bad"`},
		{"version: 1\nschedules:\n  a: {cron: '@daily', workflow: w, url: 'https://x.test'}", "use either url or workflow"},
	} {
		_, err := Parse("workly.yaml", []byte(tt.src), getenv)
		var invalid *InvalidError
		if !errors.As(err, &invalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q:\n got %v\nwant %s", tt.src, err, tt.want)
		}
	}
}

// toAPIWorkflow is the workflow the server returns once w is deployed.
func toAPIWorkflow(w Workflow) api.Workflow {
	d := w.settings.EffectiveMaxDuration().Seconds()
	out := api.Workflow{Name: w.Name, MaxDurationSeconds: d, Version: 1}
	for _, sc := range w.settings.Steps {
		step := api.WorkflowStepConfig{Name: sc.Name, Url: sc.URL, Queue: &sc.Queue, MaxAttempts: sc.MaxAttempts}
		m := api.HTTPMethod(sc.Method)
		step.Method = &m
		if len(sc.DependsOn) > 0 {
			deps := sc.DependsOn
			step.DependsOn = &deps
		}
		if sc.Body != nil {
			raw := json.RawMessage(sc.Body)
			step.Payload = &raw
		}
		out.Steps = append(out.Steps, step)
	}
	return out
}

func TestPlanWorkflows(t *testing.T) {
	f := parse(t, workflowFile)
	current := []api.Workflow{toAPIWorkflow(f.Workflows[0])}

	// The deployed workflow is unchanged; the schedule is new, and comes
	// after it.
	plan, err := f.Plan(nil, current, nil)
	if err != nil || plan.Changes[0].Action != Unchanged || plan.Changes[1].Kind != "schedule" || plan.Changes[1].Action != Create {
		t.Fatalf("plan = %+v, %v", plan, err)
	}

	changed := parse(t, strings.NewReplacer(
		"max_duration_seconds: 7200", "max_duration_seconds: 3600",
		"        queue: fx\n", "",
		"depends_on: [prices, fx]", "depends_on: [prices]",
		"schedules:", "      - {name: notify, url: 'https://api.example.com/notify', depends_on: [analytics]}\nschedules:",
	).Replace(workflowFile))
	plan, err = changed.Plan(nil, current, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := plan.Changes[0]; c.Kind != "workflow" || c.Action != Update {
		t.Fatalf("change = %+v", c)
	}
	got := fmt.Sprint(plan.Changes[0].Diffs)
	for _, want := range []string{
		"{max_duration_seconds 7200 → 3600}",
		"{steps.fx.queue fx → _workflows}",
		"{steps.analytics.depends_on [prices, fx] → [prices]}",
		"{steps.notify added}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diffs %s, want %s", got, want)
		}
	}

	// Workflows on the server that the file doesn't declare are listed.
	plan, _ = parse(t, "version: 1\nworkflows: {}").Plan(nil, current, nil)
	if fmt.Sprint(plan.UndeclaredWorkflows) != "[daily-refresh]" {
		t.Errorf("undeclared = %v", plan.UndeclaredWorkflows)
	}
}
