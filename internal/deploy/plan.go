package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/worklydev/workly/internal/api"
	"github.com/worklydev/workly/internal/store"
)

type Action int

const (
	Unchanged Action = iota
	Create
	Update
)

// Change is what deploying does to one declared queue or schedule.
type Change struct {
	Kind   string // "queue" or "schedule"
	Name   string
	Action Action
	// Diffs lists the settings an update changes.
	Diffs []Diff
	// Paused is set for an existing queue or schedule that is paused.
	// Deploying leaves it paused.
	Paused bool
	// Body is the PUT request body: an api.QueueConfig or api.ScheduleConfig.
	Body any
}

// Diff is one changed setting. Change reads "old → new", or for header
// values and bodies, which may hold secrets, just "changed", "added" or
// "removed".
type Diff struct {
	Field  string
	Change string
}

type Plan struct {
	// Changes has every declared queue and then every declared schedule, in
	// file order, unchanged ones included.
	Changes []Change
	// Undeclared lists the schedules on the server the file doesn't
	// declare, if it has a schedules section. Deploying leaves them alone.
	// Queues aren't listed: enqueueing creates them, so most aren't in a
	// file.
	Undeclared []string
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

// Plan compares the file with the queues and schedules on the server.
func (f *File) Plan(queues []api.Queue, schedules []api.Schedule) (Plan, error) {
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
		}
		plan.Changes = append(plan.Changes, c)
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
	if f.HasSchedules {
		plan.Undeclared = slices.Sorted(maps.Keys(byName))
	}
	return plan, nil
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
	return api.ScheduleConfig{
		Cron:              s.Cron,
		Timezone:          &s.Timezone,
		Method:            &s.Method,
		Url:               s.Url,
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
	}
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
