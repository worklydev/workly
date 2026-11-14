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
	if *s.Url != "https://api.example.com/jobs/nightly" || (*s.Headers)["Authorization"] != "Bearer s3cret" ||
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
		{"version: 1\ndestructive: true", "workly.yaml:2: destructive: unknown field: use prune: true"},
		{"version: 1\nprune: yes please", "workly.yaml:2: prune: must be true or false"},
		{"version: 1\nprune: 'true'", "workly.yaml:2: prune: must be true or false"},
		{"version: 1\nprnue: true", "workly.yaml:2: prnue: unknown field: did you mean prune?"},
		{"version: 1\nqueues: [a]", "workly.yaml:2: queues: expected a mapping"},
		{"version: 1\nqueues:\n  a: {max_attempts: '${MAX:-}'}", "workly.yaml:3: queues.a.max_attempts: must be a whole number"},
		{"version: 1\nqueues:\n  a: {max_attempts: '${1X:-3}'}", "${1X:-3} isn't a valid environment variable reference"},
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
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: 'https://x.test', on_complete: 'https://x.test/done'}", "schedules.a.on_complete: must be a mapping with url"},
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: 'https://x.test', on_complete: {url: 'https://x.test/done', queue: q}}", "schedules.a.on_complete: must be a mapping with url"},
		{"version: 1\nschedules:\n  a: {cron: '@daily', url: 'https://x.test', on_complete: {url: 'ftp://x.test'}}", "schedules.a.on_complete: url must be an absolute http or https URL"},
	} {
		_, err := Parse("workly.yaml", []byte(tt.src), getenv)
		var invalid *InvalidError
		if !errors.As(err, &invalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q:\n got %v\nwant %s", tt.src, err, tt.want)
		}
	}
}

// Anchors and aliases share settings, directly or through merge keys, and
// x- keys at the top hold anchors without declaring anything.
func TestParseAliases(t *testing.T) {
	f := parse(t, `
version: 1
x-yahoo: &yahoo
  max_concurrency: 2
  rate_per_second: 1
x-auth: &auth
  Authorization: "Bearer ${TOKEN}"
queues:
  stock-prices: *yahoo
  earnings-dates:
    <<: *yahoo
    max_concurrency: 5
  dividends:
    <<: [*yahoo, {max_attempts: 3, rate_per_second: 9}]
schedules:
  nightly: &nightly
    cron: "@daily"
    url: ${API_URL}/nightly
    headers: *auth
  weekly:
    <<: *nightly
    cron: "@weekly"
`)
	if len(f.Queues) != 3 || len(f.Schedules) != 2 {
		t.Fatalf("file = %+v", f)
	}
	for _, tt := range []struct {
		q           Queue
		concurrency int
		rate        float64
		attempts    *int
	}{
		{f.Queues[0], 2, 1, nil},
		{f.Queues[1], 5, 1, nil},
		{f.Queues[2], 2, 1, ptr(3)},
	} {
		c := tt.q.Config
		if *c.MaxConcurrency != tt.concurrency || *c.RatePerSecond != tt.rate || fmt.Sprint(deref(c.MaxAttempts)) != fmt.Sprint(deref(tt.attempts)) {
			t.Errorf("%s = %+v", tt.q.Name, c)
		}
	}
	for _, s := range f.Schedules {
		if *s.Config.Url != "https://api.example.com/nightly" || (*s.Config.Headers)["Authorization"] != "Bearer s3cret" {
			t.Errorf("%s = %+v", s.Name, s.Config)
		}
	}
	if f.Schedules[1].Config.Cron != "@weekly" {
		t.Errorf("weekly cron = %q", f.Schedules[1].Config.Cron)
	}

	// A problem in an alias's copy is reported at the alias, under its own name.
	_, err := Parse("workly.yaml", []byte("version: 1\nx-bad: &bad {max_attempts: 0}\nqueues:\n  a: {}\n  b: *bad\n"), getenv)
	if err == nil || err.Error() != "workly.yaml:5: queues.b.max_attempts: must be between 1 and 1000" {
		t.Errorf("got %v", err)
	}
	// Aliases of aliases can't expand without bound.
	src := "version: 1\nx-0: &a0 [x, x, x, x, x, x, x, x, x, x]\n"
	for i := 1; i < 8; i++ {
		src += fmt.Sprintf("x-%d: &a%d [*a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d]\n", i, i, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1)
	}
	if _, err := Parse("workly.yaml", []byte(src), getenv); err == nil || !strings.Contains(err.Error(), "too large once its aliases are expanded") {
		t.Errorf("got %v", err)
	}
	if _, err := Parse("workly.yaml", []byte("version: 1\nqueues:\n  a:\n    <<: 3\n"), getenv); err == nil || !strings.Contains(err.Error(), "workly.yaml:4: << merges a mapping") {
		t.Errorf("got %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// ${NAME:-default} takes the default when NAME is unset or empty, and the
// file lists each default it took.
func TestParseDefaults(t *testing.T) {
	env["EMPTY"] = ""
	t.Cleanup(func() { delete(env, "EMPTY") })
	f := parse(t, `
version: 1
queues:
  a:
    max_concurrency: ${CONCURRENCY:-1}
    rate_per_second: ${RATE:-2.5}
schedules:
  nightly:
    cron: "@daily"
    url: ${APP_URL:-http://localhost:8000}/nightly
    headers: {Authorization: "Bearer ${EMPTY:-dev}"}
`)
	if *f.Queues[0].Config.MaxConcurrency != 7 || *f.Queues[0].Config.RatePerSecond != 2.5 {
		t.Errorf("queue = %+v", f.Queues[0].Config)
	}
	s := f.Schedules[0].Config
	if *s.Url != "http://localhost:8000/nightly" || (*s.Headers)["Authorization"] != "Bearer dev" {
		t.Errorf("schedule = %+v", s)
	}
	want := []Default{
		{6, "queues.a.rate_per_second", "RATE", "2.5"},
		{10, "schedules.nightly.url", "APP_URL", "http://localhost:8000"},
		{11, "schedules.nightly.headers.Authorization", "EMPTY", "dev"},
	}
	if fmt.Sprint(f.Defaults) != fmt.Sprint(want) {
		t.Errorf("defaults = %+v\nwant %+v", f.Defaults, want)
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
    on_complete: {url: "${API_URL}/done"}
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
	plan, err := f.Plan(queues, nil, schedules)
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
		"schedule changed 2 false [{cron @hourly → 0 6 * * *} {on_complete.url none → https://api.example.com/done} {headers.Authorization changed} {headers.X-New added} {body changed}]",
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

	// Workly's own queues can be configured, but no new reserved ones created.
	if _, err := parse(t, "version: 1\nqueues:\n  _callbacks: {max_attempts: 10}").Plan(nil, nil, nil); err != nil {
		t.Errorf("_callbacks: %v", err)
	}
	if _, err := parse(t, "version: 1\nqueues:\n  _mine: {}").Plan(nil, nil, nil); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("_mine: %v", err)
	}

	// Without a schedules section, the server's schedules aren't the file's business.
	plan, _ = parse(t, "version: 1\nqueues: {}").Plan(queues, nil, schedules)
	if len(plan.Changes) != 0 || plan.Undeclared != nil {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPlanPrune(t *testing.T) {
	queues := []api.Queue{
		{Name: "old", Stats: &api.QueueStats{Failed: 3}},
		{Name: "idle"},
		{Name: "_workflows"},
		{Name: "_callbacks"},
	}
	workflows := []api.Workflow{{Name: "old-flow"}}
	schedules := []api.Schedule{{Name: "old-job"}, {Name: "another-job"}}

	plan, err := parse(t, "version: 1\nprune: true\nqueues:\n  keep: {}\nworkflows: {}\nschedules: {}").Plan(queues, workflows, schedules)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range plan.Changes {
		got = append(got, fmt.Sprintf("%s %s %d %d", c.Kind, c.Name, c.Action, c.Failed))
	}
	// Deletes come last, schedules first, so nothing left points at what's gone.
	want := []string{
		"queue keep 1 0",
		"schedule another-job 3 0",
		"schedule old-job 3 0",
		"workflow old-flow 3 0",
		"queue idle 3 0",
		"queue old 3 3",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if plan.Undeclared != nil || plan.UndeclaredWorkflows != nil {
		t.Errorf("undeclared = %v %v", plan.Undeclared, plan.UndeclaredWorkflows)
	}

	// A section left out isn't managed, prune or not.
	plan, err = parse(t, "version: 1\nprune: true\nschedules: {}").Plan(nil, nil, schedules)
	if err != nil || plan.Count(Delete) != 2 {
		t.Errorf("schedules only: %d deletes, %v", plan.Count(Delete), err)
	}
	plan, _ = parse(t, "version: 1\nprune: true\nqueues:\n  keep: {}").Plan(queues, workflows, schedules)
	if plan.Count(Delete) != 2 {
		t.Errorf("queues only: %+v", plan.Changes)
	}

	// Without prune, nothing is deleted.
	plan, _ = parse(t, "version: 1\nqueues: {}\nschedules: {}").Plan(queues, nil, schedules)
	if plan.Count(Delete) != 0 || fmt.Sprint(plan.Undeclared) != "[another-job old-job]" {
		t.Errorf("without prune: %+v", plan)
	}

	// Queues with tasks still to deliver stop the plan, all of them named.
	busy := append(queues, api.Queue{Name: "emails", Stats: &api.QueueStats{Due: 2, Scheduled: 9, Running: 1}},
		api.Queue{Name: "sms", Stats: &api.QueueStats{Scheduled: 1}})
	_, err = parse(t, "version: 1\nprune: true\nqueues: {}").Plan(busy, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "emails (12 unfinished tasks), sms (1 unfinished task)") {
		t.Errorf("busy queues: %v", err)
	}
}
