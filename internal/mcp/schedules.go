package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Schedules are separate from queues: a schedule calls its URL on a cron
// schedule, and each call is a run with its own attempts.

var (
	scheduleProp = prop("string", "Schedule name.")
	runIDProp    = prop("string", `Run ID, e.g. "run_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A".`)
	runStates    = []string{"pending", "running", "succeeded", "failed", "cancelled", "skipped"}
)

// recentRuns is how many runs get_schedule shows.
const recentRuns = 10

var listSchedules = newTool("list_schedules", "List schedules",
	"All schedules: cron expression and timezone, the URL each calls, whether it is paused, when it next runs (next_run_at) and how its latest run went (last_run). Schedules are separate from queues; their calls are runs, not tasks.",
	object(map[string]any{}), readOnly,
	func(ctx context.Context, api apiClient, _ struct{}) (any, error) {
		var out struct {
			Schedules []map[string]any `json:"schedules"`
		}
		if err := api.do(ctx, "GET", "/v1/schedules", nil, nil, &out); err != nil {
			return nil, err
		}
		for _, s := range out.Schedules {
			truncateBody(s)
		}
		return out, nil
	})

var getSchedule = newTool("get_schedule", "Get schedule",
	`A schedule's settings, its next five slots (next_runs) and its most recent runs, newest first. A "skipped" run was not delivered: skip_reason "overlap" means the previous run was still going, "missed" that the server was down at that time. Start here to find out why a scheduled job didn't run or failed.`,
	object(map[string]any{"schedule": scheduleProp}, "schedule"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		Schedule string `json:"schedule"`
	}) (any, error) {
		var schedule map[string]any
		if err := api.do(ctx, "GET", apiPath("/v1/schedules/%s", args.Schedule), nil, nil, &schedule); err != nil {
			return nil, err
		}
		truncateBody(schedule)
		var runs struct {
			Runs []map[string]any `json:"runs"`
		}
		q := url.Values{"limit": {strconv.Itoa(recentRuns)}}
		if err := api.do(ctx, "GET", apiPath("/v1/schedules/%s/runs", args.Schedule), q, nil, &runs); err != nil {
			return nil, err
		}
		for i, r := range runs.Runs {
			runs.Runs[i] = runSummary(r)
		}
		return map[string]any{"schedule": schedule, "recent_runs": runs.Runs}, nil
	})

var listRuns = newTool("list_runs", "List runs",
	"A schedule's runs, newest first, optionally only those in one state. Returns summaries; use get_run for attempts. Pass next_cursor as cursor for the next page.",
	object(map[string]any{
		"schedule": scheduleProp,
		"state":    prop("string", "Only runs in this state.", "enum", runStates),
		"limit":    prop("integer", "Runs per page (default 20).", "minimum", 1, "maximum", 100),
		"cursor":   prop("string", "next_cursor from the previous page."),
	}, "schedule"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		Schedule string `json:"schedule"`
		State    string `json:"state"`
		Limit    int    `json:"limit"`
		Cursor   string `json:"cursor"`
	}) (any, error) {
		q := url.Values{"limit": {strconv.Itoa(cmp.Or(min(args.Limit, 100), 20))}}
		if args.State != "" {
			q.Set("state", args.State)
		}
		if args.Cursor != "" {
			q.Set("cursor", args.Cursor)
		}
		var page struct {
			Runs       []map[string]any `json:"runs"`
			NextCursor string           `json:"next_cursor,omitempty"`
		}
		if err := api.do(ctx, "GET", apiPath("/v1/schedules/%s/runs", args.Schedule), q, nil, &page); err != nil {
			return nil, err
		}
		for i, r := range page.Runs {
			page.Runs[i] = runSummary(r)
		}
		return page, nil
	})

var getRun = newTool("get_run", "Get run",
	"A schedule run with its delivery attempts, oldest first: each attempt's outcome, status code, error, response headers, the start of the response body and its duration. Large bodies are truncated.",
	object(map[string]any{"id": runIDProp}, "id"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		var run map[string]any
		var attempts struct {
			Attempts []map[string]any `json:"attempts"`
		}
		if err := api.do(ctx, "GET", apiPath("/v1/runs/%s", args.ID), nil, nil, &run); err != nil {
			return nil, err
		}
		if err := api.do(ctx, "GET", apiPath("/v1/runs/%s/attempts", args.ID), nil, nil, &attempts); err != nil {
			return nil, err
		}
		truncateBody(run)
		out := map[string]any{"run": run}
		list := attempts.Attempts
		if omitted := len(list) - maxAttempts; omitted > 0 {
			list = list[omitted:]
			out["attempts_omitted"] = fmt.Sprintf("%d earlier attempts not shown", omitted)
		}
		for _, a := range list {
			if body, ok := a["response_body"].(string); ok {
				a["response_body"] = truncate(body, maxResponseBody)
			}
		}
		out["attempts"] = list
		return out, nil
	})

var getRunDelivery = newTool("get_run_delivery", "Get run delivery request",
	"The exact HTTP request Workly sends for a schedule run, freshly signed, plus a curl command that sends it. Nothing is sent. Use it to replay a run against a handler you are fixing.",
	object(map[string]any{"id": runIDProp}, "id"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		var d struct {
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if err := api.do(ctx, "GET", apiPath("/v1/runs/%s/delivery", args.ID), nil, nil, &d); err != nil {
			return nil, err
		}
		if len(d.Body) > maxDeliveryBody {
			return map[string]any{
				"method": d.Method, "url": d.URL, "headers": d.Headers,
				"body":         truncate(d.Body, maxDeliveryBody),
				"body_omitted": "the body is too large to show in full, so there is no curl command; get it from GET /v1/runs/{id}/delivery",
			}, nil
		}
		return map[string]any{"method": d.Method, "url": d.URL, "headers": d.Headers, "body": d.Body, "curl": curl(d.Method, d.URL, d.Headers, d.Body)}, nil
	})

var waitForRun = newTool("wait_for_run", "Wait for run",
	"Waits for the first delivery attempt since the run was created or last retried by hand, then returns the run and its latest attempt. Returns at once if that attempt already happened or the run has finished. Use it after trigger_schedule or retry_run. Times out after timeout_seconds with done=false.",
	object(map[string]any{
		"id":              runIDProp,
		"timeout_seconds": prop("number", "How long to wait (default 30).", "minimum", 1, "maximum", 60),
	}, "id"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		ID             string  `json:"id"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}) (any, error) {
		timeout := time.Duration(min(cmp.Or(args.TimeoutSeconds, 30), 60) * float64(time.Second))
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var run map[string]any
		for {
			err := api.do(ctx, "GET", apiPath("/v1/runs/%s", args.ID), nil, nil, &run)
			if err != nil && ctx.Err() == nil {
				return nil, err
			}
			if err == nil && (run["state"] == "skipped" || attempted(run)) {
				break
			}
			select {
			case <-ctx.Done():
				return map[string]any{
					"done":   false,
					"reason": fmt.Sprintf("no attempt completed within %v", timeout),
					"run":    runSummary(run),
				}, nil
			case <-time.After(pollInterval):
			}
		}
		out := map[string]any{"done": true, "run": runSummary(run)}
		var attempts struct {
			Attempts []map[string]any `json:"attempts"`
		}
		if err := api.do(context.WithoutCancel(ctx), "GET", apiPath("/v1/runs/%s/attempts", args.ID), nil, nil, &attempts); err != nil {
			return nil, err
		}
		if n := len(attempts.Attempts); n > 0 {
			latest := attempts.Attempts[n-1]
			if body, ok := latest["response_body"].(string); ok {
				latest["response_body"] = truncate(body, maxResponseBody)
			}
			out["latest_attempt"] = latest
		}
		return out, nil
	})

// Write tools.

var triggerSchedule = newTool("trigger_schedule", "Run schedule now",
	"Runs a schedule now, outside its timetable: creates a run that is delivered at once, even if the schedule is paused or a run is in flight. It doesn't change when the schedule next runs. Returns the run; follow up with wait_for_run.",
	object(map[string]any{"schedule": scheduleProp}, "schedule"),
	annotations{OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Schedule string `json:"schedule"`
	}) (any, error) {
		var run map[string]any
		if err := api.do(ctx, "POST", apiPath("/v1/schedules/%s/trigger", args.Schedule), nil, nil, &run); err != nil {
			return nil, err
		}
		return runSummary(run), nil
	})

var pauseSchedule = newTool("pause_schedule", "Pause schedule",
	"Stops a schedule from running on its timetable. Runs already pending or in flight finish, and trigger_schedule still works.",
	object(map[string]any{"schedule": scheduleProp}, "schedule"),
	annotations{IdempotentHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Schedule string `json:"schedule"`
	}) (any, error) {
		return scheduleAction(ctx, api, args.Schedule, "pause")
	})

var resumeSchedule = newTool("resume_schedule", "Resume schedule",
	"Resumes a paused schedule from its next slot. Slots that passed while it was paused are not run.",
	object(map[string]any{"schedule": scheduleProp}, "schedule"),
	annotations{IdempotentHint: true, OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Schedule string `json:"schedule"`
	}) (any, error) {
		return scheduleAction(ctx, api, args.Schedule, "resume")
	})

var retryRun = newTool("retry_run", "Retry run",
	"Delivers a schedule run again now. A failed, succeeded, cancelled or skipped run is delivered with a fresh set of attempts; a pending one becomes due at once. Fails with invalid_state for a running run.",
	object(map[string]any{"id": runIDProp}, "id"),
	annotations{OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		return runAction(ctx, api, args.ID, "retry")
	})

var cancelRun = newTool("cancel_run", "Cancel run",
	"Cancels a pending or running schedule run, so it isn't delivered (again). A delivery in flight finishes but isn't retried.",
	object(map[string]any{"id": runIDProp}, "id"),
	annotations{DestructiveHint: true, IdempotentHint: true},
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		return runAction(ctx, api, args.ID, "cancel")
	})

var updateSchedule = newTool("update_schedule", "Create or update schedule",
	"Creates a schedule, or changes an existing one's settings. For an existing schedule only the settings given change. A new one needs cron and url. Returns the schedule with its next five slots (next_runs), to check the cron expression.",
	object(map[string]any{
		"schedule":            scheduleProp,
		"cron":                prop("string", `Five fields (minute hour day-of-month month day-of-week), e.g. "0 3 * * *" or "*/15 * * * mon-fri", or @hourly, @daily, @weekly, @monthly, @yearly.`),
		"timezone":            prop("string", "IANA timezone the cron expression is read in (default UTC), e.g. Europe/Stockholm."),
		"url":                 prop("string", "The http(s) URL to call."),
		"method":              prop("string", "HTTP method (default POST).", "enum", []string{"GET", "POST", "PUT", "PATCH", "DELETE"}),
		"headers":             prop("object", "Headers sent with every run. Workly-* headers are reserved.", "additionalProperties", map[string]any{"type": "string"}),
		"payload":             map[string]any{"description": "A JSON body (any JSON value). Use this or body."},
		"body":                prop("string", "A raw text body. Use this or payload."),
		"content_type":        prop("string", "Content type of body (default text/plain; charset=utf-8)."),
		"timeout_seconds":     prop("number", "How long to wait for a response (default 30).", "minimum", 1, "maximum", 1800),
		"max_attempts":        prop("integer", "Total delivery attempts before a run fails (default 3).", "minimum", 1, "maximum", 1000),
		"min_backoff_seconds": prop("number", "Shortest wait before a retry (default 5).", "minimum", 0),
		"max_backoff_seconds": prop("number", "Longest wait before a retry (default 300).", "minimum", 0),
		"backoff_multiplier":  prop("number", "Retry n waits up to min_backoff_seconds * backoff_multiplier^(n-1).", "minimum", 1),
		"retry_on_4xx":        prop("boolean", "Retry 4xx responses. By default only 408 and 429 are retried."),
		"overlap":             prop("string", "When a slot comes while the previous run is still going: skip it (default) or allow it.", "enum", []string{"skip", "allow"}),
		"missed":              prop("string", "Slots missed while the server was down: skip them (default) or catch_up (run the latest once).", "enum", []string{"skip", "catch_up"}),
	}, "schedule"),
	annotations{IdempotentHint: true},
	func(ctx context.Context, api apiClient, args map[string]json.RawMessage) (any, error) {
		var name string
		if err := json.Unmarshal(args["schedule"], &name); err != nil || name == "" {
			return nil, argumentError{errors.New("schedule is required")}
		}
		delete(args, "schedule")
		for k := range args {
			if !slices.Contains(scheduleFields, k) {
				return nil, argumentError{fmt.Errorf("unknown setting %q", k)}
			}
		}

		var current map[string]any
		err := api.do(ctx, "GET", apiPath("/v1/schedules/%s", name), nil, nil, &current)
		if ae, ok := errors.AsType[*apiError](err); ok && ae.Code == "schedule_not_found" {
			current = map[string]any{}
		} else if err != nil {
			return nil, err
		}
		// PUT is declarative, so start from the current settings.
		config := map[string]any{}
		for _, k := range scheduleFields {
			if v, ok := current[k]; ok {
				config[k] = v
			}
		}
		_, newPayload := args["payload"]
		_, newBody := args["body"]
		_, newContentType := args["content_type"]
		_, newHeaders := args["headers"]
		if newPayload || newBody || newContentType {
			// The stored body's Content-Type would win over the new one.
			delete(config, "payload")
			delete(config, "body")
			if h, ok := config["headers"].(map[string]any); ok && !newHeaders {
				for k := range h {
					if strings.EqualFold(k, "Content-Type") {
						delete(h, k)
					}
				}
			}
		}
		for k, v := range args {
			config[k] = v
		}
		var out map[string]any
		if err := api.do(ctx, "PUT", apiPath("/v1/schedules/%s", name), nil, config, &out); err != nil {
			return nil, err
		}
		truncateBody(out)
		return out, nil
	})

var scheduleFields = []string{
	"cron", "timezone", "url", "method", "headers", "payload", "body", "content_type",
	"timeout_seconds", "max_attempts", "min_backoff_seconds", "max_backoff_seconds", "backoff_multiplier",
	"retry_on_4xx", "overlap", "missed",
}

func scheduleAction(ctx context.Context, api apiClient, schedule, action string) (any, error) {
	var out map[string]any
	if err := api.do(ctx, "POST", apiPath("/v1/schedules/%s/", schedule)+action, nil, nil, &out); err != nil {
		return nil, err
	}
	truncateBody(out)
	return out, nil
}

func runAction(ctx context.Context, api apiClient, id, action string) (any, error) {
	var run map[string]any
	if err := api.do(ctx, "POST", apiPath("/v1/runs/%s/", id)+action, nil, nil, &run); err != nil {
		return nil, err
	}
	return runSummary(run), nil
}

// runSummary is a run without its request: what list views need.
func runSummary(r map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{
		"id", "schedule", "state", "trigger", "scheduled_for", "skip_reason", "run_at", "attempt_count",
		"last_status", "last_outcome", "last_error", "created_at", "finished_at", "last_attempt_at", "retried_at",
	} {
		if v, ok := r[k]; ok {
			out[k] = v
		}
	}
	if e, ok := out["last_error"].(string); ok {
		out["last_error"] = truncate(e, maxErrorLength)
	}
	return out
}
