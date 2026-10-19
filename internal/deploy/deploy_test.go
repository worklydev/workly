package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/worklydev/workly/internal/api"
)

var env = map[string]string{"API_URL": "https://api.example.com", "TOKEN": "s3cret", "CONCURRENCY": "7"}

func getenv(name string) (string, bool) {
	v, ok := env[name]
	return v, ok
}

func parse(t *testing.T, src string) *File {
	t.Helper()
	f, err := Parse("workly.yaml", []byte(src), getenv)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParse(t *testing.T) {
	f := parse(t, `
version: 1
queues:
  stock-prices:
    max_concurrency: ${CONCURRENCY}
    rate_per_second: 2.5
  defaults:
schedules:
  nightly:
    cron: "0 22 * * 1-5"
    timezone: Europe/Stockholm
    url: ${API_URL}/jobs/nightly
    headers: {Authorization: "Bearer ${TOKEN}"}
    payload: {full: true, note: "${TOKEN}"}
`)
	if !f.HasQueues || !f.HasSchedules || len(f.Queues) != 2 || len(f.Schedules) != 1 {
		t.Fatalf("file = %+v", f)
	}
	q := f.Queues[0]
	if q.Name != "stock-prices" || *q.Config.MaxConcurrency != 7 || *q.Config.RatePerSecond != 2.5 || q.Config.MaxAttempts != nil {
		t.Errorf("queue = %+v", q)
	}
	if f.Queues[1].settings.MaxConcurrency != 3 {
		t.Errorf("an empty queue gets defaults: %+v", f.Queues[1].settings)
	}
	s := f.Schedules[0].Config
	if s.Url != "https://api.example.com/jobs/nightly" || (*s.Headers)["Authorization"] != "Bearer s3cret" ||
		string(*s.Payload) != `{"full":true,"note":"s3cret"}` {
		t.Errorf("schedule = %+v", s)
	}

	// Left out, a section isn't managed; empty, it declares nothing.
	f = parse(t, "version: 1\nschedules: {}\n")
	if f.HasQueues || !f.HasSchedules || len(f.Schedules) != 0 {
		t.Errorf("file = %+v", f)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want string
	}{
		{``, "workly.yaml: the file is empty: start it with version: 1"},
		{`[1, 2`, "workly.yaml: line 1: did not find expected ',' or ']'"},
		{`queues: {}`, "workly.yaml:1: version: required: add version: 1"},
		{`version: 2`, "workly.yaml:1: version: 2 isn't supported: this version of workly reads version 1"},
		{"version: 1\nqueue: {}", "workly.yaml:2: queue: unknown field: did you mean queues?"},
		{"version: 1\ndestructive: true", "workly.yaml:2: destructive: unknown field: workly deploy never deletes anything"},
		{"version: 1\nqueues: [a]", "workly.yaml:2: queues: expected a mapping"},
		{"version: 1\nqueues:\n  Emails: {}", `workly.yaml:3: queues.Emails: queue name "Emails" must be`},
		{"version: 1\nqueues:\n  a: {}\n  a: {}", "workly.yaml:4: queues.a: defined more than once"},
		{"version: 1\nqueues:\n  a: {concurency: 2}", "workly.yaml:3: queues.a.concurency: unknown field: did you mean max_concurrency?"},
		{"version: 1\nqueues:\n  a: {retries: 2}", "workly.yaml:3: queues.a.retries: unknown field: use max_attempts"},
		{"version: 1\nqueues:\n  a: {colour: 2}", "workly.yaml:3: queues.a.colour: unknown field: expected one of backoff_multiplier,"},
		{"version: 1\nqueues:\n  a: {max_attempts: lots}", "workly.yaml:3: queues.a.max_attempts: must be a whole number"},
		{"version: 1\nqueues:\n  a: {retry_on_4xx: 1}", "workly.yaml:3: queues.a.retry_on_4xx: must be true or false"},
		{"version: 1\nqueues:\n  a:\n    timeout_seconds: 5000", "workly.yaml:4: queues.a.timeout_seconds: must be between 1 and 1800"},
		{"version: 1\nqueues:\n  a:\n    min_backoff_seconds: 10\n    max_backoff_seconds: 5", "queues.a.max_backoff_seconds: must be at least min_backoff_seconds"},
		{"version: 1\nschedules:\n  a: {url: 'https://x.test'}", "workly.yaml:3: schedules.a.cron: required"},
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: 'https://x.test', queue: q}", "schedules.a.queue: unknown field: schedules call their url directly"},
		{"version: 1\nschedules:\n  a:\n    cron: '0 22 * * 1-8'\n    url: https://x.test", `workly.yaml:4: schedules.a.cron: day of week field "1-8"`},
		{"version: 1\nschedules:\n  a:\n    cron: '@daily'\n    timezone: Europe/Stockolm\n    url: https://x.test", `workly.yaml:5: schedules.a.timezone: unknown timezone "Europe/Stockolm"`},
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: '${MISSING}/x'}", "workly.yaml:3: schedules.a.url: environment variable MISSING isn't set"},
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: 'https://x.test', payload: {k: '${NOPE}'}}", "schedules.a.payload.k: environment variable NOPE isn't set"},
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: 'ftp://x.test'}", "schedules.a.url: must be an absolute http or https URL"},
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: 'https://x.test', headers: {Workly-X: y}}", `schedules.a: header "Workly-X" is reserved`},
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: 'https://x.test', payload: {}, body: x}", "schedules.a: use either payload or body, not both"},
	} {
		_, err := Parse("workly.yaml", []byte(tt.src), getenv)
		var invalid *InvalidError
		if !errors.As(err, &invalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q:\n got %v\nwant %s", tt.src, err, tt.want)
		}
	}
}

// Every problem is reported, in line order.
func TestParseReportsAll(t *testing.T) {
	_, err := Parse("workly.yaml", []byte(`version: 1
schedules:
  b: {cron: '@daily', url: '${MISSING}'}
queues:
  a: {max_attempts: 0}
  c: {max_concurency: 1}
`), getenv)
	want := `workly.yaml:3: schedules.b.url: environment variable MISSING isn't set
workly.yaml:5: queues.a.max_attempts: must be between 1 and 1000
workly.yaml:6: queues.c.max_concurency: unknown field: did you mean max_concurrency?`
	if err == nil || err.Error() != want {
		t.Fatalf("got\n%v\nwant\n%s", err, want)
	}
}

func TestPlan(t *testing.T) {
	f := parse(t, `
version: 1
queues:
  same: {max_concurrency: 10}
  changed: {max_concurrency: 10, rate_per_second: 5}
  new: {}
schedules:
  same:
    cron: "@daily"
    url: https://api.example.com/same
    headers: {Authorization: "Bearer ${TOKEN}"}
    payload: {b: 2, a: 1}
  changed:
    cron: "0 6 * * *"
    url: https://api.example.com/changed
    headers: {Authorization: "Bearer ${TOKEN}", X-New: "1"}
    payload: {a: 2}
    max_attempts: 3
`)
	queues := []api.Queue{
		{Name: "same", MaxConcurrency: 10, MaxAttempts: 5, MinBackoffSeconds: 2, MaxBackoffSeconds: 3600, BackoffMultiplier: 2, TimeoutSeconds: 30},
		{Name: "changed", MaxConcurrency: 3, MaxAttempts: 5, MinBackoffSeconds: 2, MaxBackoffSeconds: 3600, BackoffMultiplier: 2, TimeoutSeconds: 30, Paused: true},
		{Name: "elsewhere", MaxConcurrency: 3, MaxAttempts: 5, MinBackoffSeconds: 2, MaxBackoffSeconds: 3600, BackoffMultiplier: 2, TimeoutSeconds: 30},
	}
	schedule := func(name, cron, auth string, payload string) api.Schedule {
		raw := json.RawMessage(payload)
		return api.Schedule{
			Name: name, Cron: cron, Timezone: "UTC", Method: "POST", Url: "https://api.example.com/" + name,
			Headers: map[string]string{"Authorization": auth, "Content-Type": "application/json"}, Payload: &raw,
			TimeoutSeconds: 30, MaxAttempts: 3, MinBackoffSeconds: 5, MaxBackoffSeconds: 300, BackoffMultiplier: 2,
			Overlap: "skip", Missed: "skip",
		}
	}
	schedules := []api.Schedule{
		// Key order and spacing don't make a body different.
		schedule("same", "@daily", "Bearer s3cret", `{"a": 1, "b": 2}`),
		schedule("changed", "@hourly", "Bearer old", `{"a": 1}`),
		schedule("legacy", "@daily", "", `{}`),
	}
	plan, err := f.Plan(queues, schedules)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range plan.Changes {
		got = append(got, fmt.Sprintf("%s %s %d %v %v", c.Kind, c.Name, c.Action, c.Paused, c.Diffs))
	}
	want := []string{
		"queue same 0 false []",
		"queue changed 2 true [{rate_per_second no limit → 5} {max_concurrency 3 → 10}]",
		"queue new 1 false []",
		"schedule same 0 false []",
		"schedule changed 2 false [{cron @hourly → 0 6 * * *} {headers.Authorization changed} {headers.X-New added} {body changed}]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if fmt.Sprint(plan.Undeclared) != "[legacy]" {
		t.Errorf("undeclared = %v", plan.Undeclared)
	}
	if plan.Count(Create) != 1 || plan.Count(Update) != 2 || plan.Count(Unchanged) != 2 {
		t.Errorf("counts = %d %d %d", plan.Count(Create), plan.Count(Update), plan.Count(Unchanged))
	}

	// Without a schedules section, the server's schedules aren't the file's business.
	plan, _ = parse(t, "version: 1\nqueues: {}").Plan(queues, schedules)
	if len(plan.Changes) != 0 || plan.Undeclared != nil {
		t.Errorf("plan = %+v", plan)
	}
}
