package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/api"
	"github.com/worklydev/workly/internal/store"
)

type Action int

const (
	Unchanged Action = iota
	Create
	Update
	// Delete is planned only with prune: true, for what the file leaves out.
	Delete
)

// Change is what deploying does to one declared queue, workflow or
// schedule.
type Change struct {
	Kind   string // "queue", "workflow" or "schedule"
	Name   string
	Action Action
	// Diffs lists the settings an update changes.
	Diffs []Diff
	// Paused is set for an existing queue or schedule that is paused.
	// Deploying leaves it paused.
	Paused bool
	// Body is the PUT request body: an api.QueueConfig, api.WorkflowConfig
	// or api.ScheduleConfig. Deletes have none.
	Body any
	// Failed counts the failed tasks a queue's delete takes with it.
	Failed int
}

// Diff is one changed setting. Change reads "old → new", or for header
// values and bodies, which may hold secrets, just "changed", "added" or
// "removed".
type Diff struct {
	Field  string
	Change string
}

type Plan struct {
	// Changes has every declared queue, then every declared workflow, then
	// every declared schedule, in file order, unchanged ones included. That
	// is the order they are applied in, so a schedule's workflow exists by
	// the time the schedule is put. With prune, the deletes follow, in the
	// reverse order (schedules, workflows, queues), so nothing is deleted
	// while something left on the server still points at it.
	Changes []Change
	// Undeclared lists the schedules on the server the file doesn't
	// declare, if it has a schedules section and doesn't prune. Deploying
	// leaves them alone. Queues aren't listed: enqueueing creates them, so
	// most aren't in a file.
	Undeclared []string
	// UndeclaredWorkflows does the same for workflows.
	UndeclaredWorkflows []string
}

func (p Plan) Count(a Action) int {
	n := 0
	for _, c := range p.Changes {
		if c.Action == a {
			n++
		}
	}
	return n
}

// Plan compares the file with the queues, workflows and schedules on the
// server. With prune, it fails if a queue it would delete still has tasks
// to deliver: those tasks would be lost, and the next enqueue would bring
// the queue back anyway.
func (f *File) Plan(queues []api.Queue, workflows []api.Workflow, schedules []api.Schedule) (Plan, error) {
	var plan Plan
	current := map[string]api.Queue{}
	for _, q := range queues {
		current[q.Name] = q
	}
	for _, q := range f.Queues {
		c := Change{Kind: "queue", Name: q.Name, Action: Create, Body: q.Config}
		if cur, ok := current[q.Name]; ok {
			settings, err := api.QueueSettings(queueConfig(cur))
			if err != nil {
				return plan, fmt.Errorf("queue %s on the server: %w", q.Name, err)
			}
			c.Diffs = queueDiffs(settings, q.settings)
			c.Action, c.Paused = action(c.Diffs), cur.Paused
			delete(current, q.Name)
		} else if api.Reserved(q.Name) && !api.SystemQueue(q.Name) {
			return plan, fmt.Errorf("queue %s: names starting with '_' are reserved for Workly's own queues", q.Name)
		}
		plan.Changes = append(plan.Changes, c)
	}

	workflowsByName := map[string]api.Workflow{}
	for _, w := range workflows {
		workflowsByName[w.Name] = w
	}
	for _, w := range f.Workflows {
		c := Change{Kind: "workflow", Name: w.Name, Action: Create, Body: w.Config}
		if cur, ok := workflowsByName[w.Name]; ok {
			settings, err := api.WorkflowSettings(workflowConfig(cur), 1<<62)
			if err != nil {
				return plan, fmt.Errorf("workflow %s on the server: %w", w.Name, err)
			}
			c.Diffs = workflowDiffs(settings, w.settings)
			c.Action = action(c.Diffs)
			delete(workflowsByName, w.Name)
		}
		plan.Changes = append(plan.Changes, c)
	}
	if f.HasWorkflows && !f.Prune {
		plan.UndeclaredWorkflows = slices.Sorted(maps.Keys(workflowsByName))
	}

	byName := map[string]api.Schedule{}
	for _, s := range schedules {
		byName[s.Name] = s
	}
	for _, s := range f.Schedules {
		c := Change{Kind: "schedule", Name: s.Name, Action: Create, Body: s.Config}
		if cur, ok := byName[s.Name]; ok {
			// The server accepted the body, whatever its own limit was.
			settings, err := api.ScheduleSettings(scheduleConfig(cur), 1<<62)
			if err != nil {
				return plan, fmt.Errorf("schedule %s on the server: %w", s.Name, err)
			}
			c.Diffs = scheduleDiffs(settings, s.settings)
			c.Action, c.Paused = action(c.Diffs), cur.Paused
			delete(byName, s.Name)
		}
		plan.Changes = append(plan.Changes, c)
	}
	if f.HasSchedules && !f.Prune {
		plan.Undeclared = slices.Sorted(maps.Keys(byName))
	}
	if !f.Prune {
		return plan, nil
	}

	if f.HasSchedules {
		for _, name := range slices.Sorted(maps.Keys(byName)) {
			plan.Changes = append(plan.Changes, Change{Kind: "schedule", Name: name, Action: Delete})
		}
	}
	if f.HasWorkflows {
		for _, name := range slices.Sorted(maps.Keys(workflowsByName)) {
			plan.Changes = append(plan.Changes, Change{Kind: "workflow", Name: name, Action: Delete})
		}
	}
	if f.HasQueues {
		var busy []string
		for _, name := range slices.Sorted(maps.Keys(current)) {
			if api.Reserved(name) {
				continue
			}
			var stats api.QueueStats
			if s := current[name].Stats; s != nil {
				stats = *s
			}
			if n := stats.Due + stats.Scheduled + stats.Running; n > 0 {
				busy = append(busy, fmt.Sprintf("%s (%s)", name, plural(n, "unfinished task")))
				continue
			}
			plan.Changes = append(plan.Changes, Change{Kind: "queue", Name: name, Action: Delete, Failed: stats.Failed})
		}
		if len(busy) > 0 {
			return plan, fmt.Errorf("prune would delete queues that still have tasks to deliver: %s; add them under queues, or let their tasks finish first", strings.Join(busy, ", "))
		}
	}
	return plan, nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func action(diffs []Diff) Action {
	if len(diffs) == 0 {
		return Unchanged
	}
	return Update
}

func queueConfig(q api.Queue) api.QueueConfig {
	return api.QueueConfig{
		MaxAttempts:       &q.MaxAttempts,
		MinBackoffSeconds: &q.MinBackoffSeconds,
		MaxBackoffSeconds: &q.MaxBackoffSeconds,
		BackoffMultiplier: &q.BackoffMultiplier,
		TimeoutSeconds:    &q.TimeoutSeconds,
		RetryOn4xx:        &q.RetryOn4xx,
		RatePerSecond:     q.RatePerSecond,
		MaxConcurrency:    &q.MaxConcurrency,
	}
}

// scheduleConfig turns a schedule back into the request that sets it. Its
// headers include the Content-Type that goes with its body.
func scheduleConfig(s api.Schedule) api.ScheduleConfig {
	cfg := api.ScheduleConfig{
		Cron:              s.Cron,
		Timezone:          &s.Timezone,
		Method:            &s.Method,
		Url:               &s.Url,
		Headers:           &s.Headers,
		Payload:           s.Payload,
		Body:              s.Body,
		TimeoutSeconds:    &s.TimeoutSeconds,
		MaxAttempts:       &s.MaxAttempts,
		MinBackoffSeconds: &s.MinBackoffSeconds,
		MaxBackoffSeconds: &s.MaxBackoffSeconds,
		BackoffMultiplier: &s.BackoffMultiplier,
		RetryOn4xx:        &s.RetryOn4xx,
		Overlap:           &s.Overlap,
		Missed:            &s.Missed,
		OnComplete:        s.OnComplete,
	}
	if s.Workflow != nil {
		// It has no request; its payload is the workflow run's input.
		cfg.Workflow, cfg.Url, cfg.Method, cfg.Headers, cfg.Body = s.Workflow, nil, nil, nil, nil
	}
	return cfg
}

type field[T any] struct {
	name string
	get  func(T) string
}

func diffs[T any](fields []field[T], from, to T) []Diff {
	var out []Diff
	for _, f := range fields {
		if a, b := f.get(from), f.get(to); a != b {
			out = append(out, Diff{f.name, a + " → " + b})
		}
	}
	return out
}

func num(f float64) string        { return strconv.FormatFloat(f, 'f', -1, 64) }
func secs(d time.Duration) string { return num(d.Seconds()) }

var queueFields = []field[store.QueueConfig]{
	{"max_attempts", func(c store.QueueConfig) string { return strconv.Itoa(c.MaxAttempts) }},
	{"min_backoff_seconds", func(c store.QueueConfig) string { return secs(c.MinBackoff) }},
	{"max_backoff_seconds", func(c store.QueueConfig) string { return secs(c.MaxBackoff) }},
	{"backoff_multiplier", func(c store.QueueConfig) string { return num(c.BackoffMultiplier) }},
	{"timeout_seconds", func(c store.QueueConfig) string { return secs(c.Timeout) }},
	{"retry_on_4xx", func(c store.QueueConfig) string { return strconv.FormatBool(c.RetryOn4xx) }},
	{"rate_per_second", func(c store.QueueConfig) string {
		if c.RatePerSecond == 0 {
			return "no limit"
		}
		return num(c.RatePerSecond)
	}},
	{"max_concurrency", func(c store.QueueConfig) string { return strconv.Itoa(c.MaxConcurrency) }},
}

func queueDiffs(from, to store.QueueConfig) []Diff {
	return unlisted(diffs(queueFields, from, to), reflect.DeepEqual(from, to))
}

// unlisted makes sure a difference in a setting missing from the field
// lists still counts as a change.
func unlisted(diffs []Diff, same bool) []Diff {
	if len(diffs) == 0 && !same {
		return []Diff{{"other settings", "changed"}}
	}
	return diffs
}

var scheduleFields = []field[store.ScheduleConfig]{
	{"cron", func(c store.ScheduleConfig) string { return c.Cron }},
	{"timezone", func(c store.ScheduleConfig) string { return c.Timezone }},
	{"workflow", func(c store.ScheduleConfig) string {
		if c.Workflow == "" {
			return "none"
		}
		return c.Workflow
	}},
	{"method", func(c store.ScheduleConfig) string { return c.Method }},
	{"url", func(c store.ScheduleConfig) string { return c.URL }},
	{"timeout_seconds", func(c store.ScheduleConfig) string { return secs(c.Timeout) }},
	{"max_attempts", func(c store.ScheduleConfig) string { return strconv.Itoa(c.MaxAttempts) }},
	{"min_backoff_seconds", func(c store.ScheduleConfig) string { return secs(c.MinBackoff) }},
	{"max_backoff_seconds", func(c store.ScheduleConfig) string { return secs(c.MaxBackoff) }},
	{"backoff_multiplier", func(c store.ScheduleConfig) string { return num(c.BackoffMultiplier) }},
	{"retry_on_4xx", func(c store.ScheduleConfig) string { return strconv.FormatBool(c.RetryOn4xx) }},
	{"overlap", func(c store.ScheduleConfig) string { return string(c.Overlap) }},
	{"missed", func(c store.ScheduleConfig) string { return string(c.Missed) }},
	{"on_complete.url", func(c store.ScheduleConfig) string {
		if c.OnComplete == nil {
			return "none"
		}
		return c.OnComplete.URL
	}},
}

func scheduleDiffs(from, to store.ScheduleConfig) []Diff {
	out := diffs(scheduleFields, from, to)
	for _, name := range slices.Sorted(maps.Keys(from.Headers)) {
		switch v, ok := to.Headers[name]; {
		case !ok:
			out = append(out, Diff{"headers." + name, "removed"})
		case v != from.Headers[name]:
			out = append(out, Diff{"headers." + name, "changed"})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(to.Headers)) {
		if _, ok := from.Headers[name]; !ok {
			out = append(out, Diff{"headers." + name, "added"})
		}
	}
	var fromCb, toCb map[string]string
	if from.OnComplete != nil {
		fromCb = from.OnComplete.Headers
	}
	if to.OnComplete != nil {
		toCb = to.OnComplete.Headers
	}
	if !maps.Equal(fromCb, toCb) {
		out = append(out, Diff{"on_complete.headers", "changed"})
	}
	if !sameBody(from.Body, to.Body) {
		switch {
		case len(to.Body) == 0:
			out = append(out, Diff{"body", "removed"})
		case len(from.Body) == 0:
			out = append(out, Diff{"body", "added"})
		default:
			out = append(out, Diff{"body", "changed"})
		}
	}
	from.Body, to.Body = nil, nil
	return unlisted(out, reflect.DeepEqual(from, to))
}

// sameBody compares JSON bodies by value, since key order and spacing can
// change on the way through the API.
func sameBody(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// workflowConfig turns a workflow back into the request that sets it.
func workflowConfig(w api.Workflow) api.WorkflowConfig {
	return api.WorkflowConfig{Steps: w.Steps, MaxDurationSeconds: &w.MaxDurationSeconds}
}

// workflowDiffs lists what changes in a workflow: its max_duration, and its
// steps, by name.
func workflowDiffs(from, to store.WorkflowConfig) []Diff {
	var out []Diff
	if from.MaxDuration != to.MaxDuration {
		out = append(out, Diff{"max_duration_seconds", secs(from.MaxDuration) + " → " + secs(to.MaxDuration)})
	}
	old := map[string]store.WorkflowStepConfig{}
	var oldOrder, newOrder []string
	for _, sc := range from.Steps {
		old[sc.Name] = sc
		oldOrder = append(oldOrder, sc.Name)
	}
	for _, sc := range to.Steps {
		newOrder = append(newOrder, sc.Name)
		prev, ok := old[sc.Name]
		if !ok {
			out = append(out, Diff{"steps." + sc.Name, "added"})
			continue
		}
		delete(old, sc.Name)
		out = append(out, stepDiffs("steps."+sc.Name+".", prev, sc)...)
	}
	for _, name := range oldOrder {
		if _, ok := old[name]; ok {
			out = append(out, Diff{"steps." + name, "removed"})
		}
	}
	if len(out) == 0 && !slices.Equal(oldOrder, newOrder) {
		out = append(out, Diff{"steps", "reordered"})
	}
	return out
}

var stepFields = []field[store.WorkflowStepConfig]{
	{"depends_on", func(c store.WorkflowStepConfig) string { return "[" + strings.Join(c.DependsOn, ", ") + "]" }},
	{"method", func(c store.WorkflowStepConfig) string { return c.Method }},
	{"url", func(c store.WorkflowStepConfig) string { return c.URL }},
	{"queue", func(c store.WorkflowStepConfig) string { return c.Queue }},
	{"timeout_seconds", func(c store.WorkflowStepConfig) string {
		if c.Timeout == nil {
			return "the queue's"
		}
		return secs(*c.Timeout)
	}},
	{"max_attempts", func(c store.WorkflowStepConfig) string {
		if c.MaxAttempts == nil {
			return "the queue's"
		}
		return strconv.Itoa(*c.MaxAttempts)
	}},
}

func stepDiffs(prefix string, from, to store.WorkflowStepConfig) []Diff {
	var out []Diff
	for _, d := range diffs(stepFields, from, to) {
		out = append(out, Diff{prefix + d.Field, d.Change})
	}
	if !maps.Equal(from.Headers, to.Headers) {
		out = append(out, Diff{prefix + "headers", "changed"})
	}
	switch {
	case from.Body == nil && to.Body != nil:
		out = append(out, Diff{prefix + "body", "added"})
	case from.Body != nil && to.Body == nil:
		out = append(out, Diff{prefix + "body", "removed (the run's context is sent instead)"})
	case !sameBody(from.Body, to.Body):
		out = append(out, Diff{prefix + "body", "changed"})
	}
	return out
}
