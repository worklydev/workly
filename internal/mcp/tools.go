package mcp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations annotations    `json:"annotations"`

	run func(ctx context.Context, api apiClient, args json.RawMessage) (any, error)
}

// annotations tell clients how careful to be: most auto-approve read-only
// tools and ask before the others.
type annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	// OpenWorldHint: the tool reaches outside Workly, by delivering a task
	// to its endpoint.
	OpenWorldHint bool `json:"openWorldHint"`
}

var readOnly = annotations{ReadOnlyHint: true, IdempotentHint: true}

// newTool builds a tool whose arguments decode into A. Unknown arguments are
// rejected, so a misspelled filter isn't silently ignored.
func newTool[A any](name, title, description string, schema map[string]any, ann annotations,
	run func(ctx context.Context, api apiClient, args A) (any, error),
) *tool {
	ann.Title = title
	return &tool{
		Name: name, Title: title, Description: description, InputSchema: schema, Annotations: ann,
		run: func(ctx context.Context, api apiClient, raw json.RawMessage) (any, error) {
			var args A
			if len(raw) > 0 && string(raw) != "null" {
				dec := json.NewDecoder(bytes.NewReader(raw))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&args); err != nil {
					return nil, argumentError{err}
				}
			}
			return run(ctx, api, args)
		},
	}
}

type argumentError struct{ err error }

func (e argumentError) Error() string { return "invalid arguments: " + e.err.Error() }

// call runs a tool. Failures the agent can act on (bad arguments, API
// errors such as task_not_found) are tool results with isError, so the model
// sees them; only protocol problems are JSON-RPC errors.
func (s *server) call(r *http.Request, t *tool, args json.RawMessage) map[string]any {
	ctx := r.Context()
	start := time.Now()
	out, err := t.run(ctx, s.api, args)
	log := []any{"tool", t.Name, "duration_ms", time.Since(start).Milliseconds()}
	if err != nil {
		log = append(log, "error", err.Error())
	}
	s.opts.Logger.InfoContext(ctx, "mcp tool call", log...)

	if err == nil {
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			s.opts.Logger.ErrorContext(ctx, "mcp result", "tool", t.Name, "err", err)
			return toolError("internal error")
		}
		return map[string]any{"content": []any{textContent(string(b))}}
	}
	if e, ok := errors.AsType[argumentError](err); ok {
		return toolError(e.Error())
	}
	if e, ok := errors.AsType[*apiError](err); ok {
		return toolError(e.Error())
	}
	if errors.Is(err, context.Canceled) {
		return toolError("cancelled")
	}
	s.opts.Logger.ErrorContext(ctx, "mcp tool error", "tool", t.Name, "err", err)
	return toolError("internal error")
}

func toolError(message string) map[string]any {
	return map[string]any{"content": []any{textContent(message)}, "isError": true}
}

func textContent(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

// Schema helpers: JSON Schema, as MCP clients expect for inputSchema.

func object(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// nullable is a prop that also accepts null.
func nullable(typ, description string, extra ...any) map[string]any {
	p := prop(typ, description, extra...)
	p["type"] = []string{typ, "null"}
	return p
}

func prop(typ, description string, extra ...any) map[string]any {
	p := map[string]any{"type": typ, "description": description}
	for i := 0; i+1 < len(extra); i += 2 {
		p[extra[i].(string)] = extra[i+1]
	}
	return p
}

var (
	queueProp  = prop("string", "Queue name.")
	taskIDProp = prop("string", `Task ID, e.g. "task_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A".`)
	timeProp   = func(d string) map[string]any { return prop("string", d+" RFC 3339.", "format", "date-time") }
	taskStates = []string{"pending", "running", "succeeded", "failed", "cancelled"}
)

func allTools() []*tool {
	return []*tool{
		listQueues, getQueue, listTasks, getTask, getTaskDelivery, waitForTask,
		listSchedules, getSchedule, listRuns, getRun, getRunDelivery, waitForRun,
		enqueueTask, retryTask, cancelTask, retryFailedTasks, cancelPendingTasks, pauseQueue, resumeQueue,
		updateQueuePolicy,
		updateSchedule, triggerSchedule, pauseSchedule, resumeSchedule, retryRun, cancelRun,
	}
}

// Read tools.

var listQueues = newTool("list_queues", "List queues",
	"All queues with their delivery policy and current work: due (waiting now), scheduled (later, including retries), running and failed task counts, and oldest_due_at (how far behind the queue is).",
	object(map[string]any{}), readOnly,
	func(ctx context.Context, api apiClient, _ struct{}) (any, error) {
		var out map[string]any
		err := api.do(ctx, "GET", "/v1/queues", nil, nil, &out)
		return out, err
	})

var getQueue = newTool("get_queue", "Get queue",
	"A queue's delivery policy, current work (due, scheduled, running, failed), activity (how many tasks succeeded, failed and were cancelled since `since`; default the last 7 days, limited by the server's retention) and failures: its failed tasks grouped by error, most common first, each with a count, the latest task and the start of its response body. Start here to find out why a queue's tasks fail.",
	object(map[string]any{
		"queue": queueProp,
		"since": timeProp("Start of the activity period."),
	}, "queue"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		Queue string `json:"queue"`
		Since string `json:"since"`
	}) (any, error) {
		var queue, activity, failures map[string]any
		if err := api.do(ctx, "GET", apiPath("/v1/queues/%s", args.Queue), nil, nil, &queue); err != nil {
			return nil, err
		}
		q := url.Values{}
		if args.Since != "" {
			q.Set("since", args.Since)
		}
		if err := api.do(ctx, "GET", apiPath("/v1/queues/%s/activity", args.Queue), q, nil, &activity); err != nil {
			return nil, err
		}
		if err := api.do(ctx, "GET", apiPath("/v1/queues/%s/failures", args.Queue), nil, nil, &failures); err != nil {
			return nil, err
		}
		return map[string]any{"queue": queue, "activity": activity, "failures": failures["groups"]}, nil
	})

var listTasks = newTool("list_tasks", "List tasks",
	`Tasks, newest first, filtered by any of queue, state, idempotency key, last error and creation time. Returns summaries (state, URL, attempts, last status and error); use get_task for payloads and attempt details. Pass next_cursor as cursor for the next page. A "pending" task whose run_at is in the future is scheduled or waiting to retry.`,
	object(map[string]any{
		"queue":           queueProp,
		"state":           prop("string", "Only tasks in this state.", "enum", taskStates),
		"idempotency_key": prop("string", "Only the task with this idempotency key."),
		"last_error":      prop("string", "Only tasks whose last_error is exactly this, e.g. an error from get_queue's failures."),
		"created_after":   timeProp("Only tasks created at or after this time."),
		"created_before":  timeProp("Only tasks created before this time."),
		"limit":           prop("integer", "Tasks per page (default 20).", "minimum", 1, "maximum", 100),
		"cursor":          prop("string", "next_cursor from the previous page."),
	}), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		Queue          string `json:"queue"`
		State          string `json:"state"`
		IdempotencyKey string `json:"idempotency_key"`
		LastError      string `json:"last_error"`
		CreatedAfter   string `json:"created_after"`
		CreatedBefore  string `json:"created_before"`
		Limit          int    `json:"limit"`
		Cursor         string `json:"cursor"`
	}) (any, error) {
		q := url.Values{}
		for k, v := range map[string]string{
			"queue": args.Queue, "state": args.State, "idempotency_key": args.IdempotencyKey, "last_error": args.LastError,
			"created_after": args.CreatedAfter, "created_before": args.CreatedBefore, "cursor": args.Cursor,
		} {
			if v != "" {
				q.Set(k, v)
			}
		}
		q.Set("limit", strconv.Itoa(cmp.Or(min(args.Limit, 100), 20)))
		var page struct {
			Tasks      []map[string]any `json:"tasks"`
			NextCursor string           `json:"next_cursor,omitempty"`
		}
		if err := api.do(ctx, "GET", "/v1/tasks", q, nil, &page); err != nil {
			return nil, err
		}
		for i, t := range page.Tasks {
			page.Tasks[i] = summary(t)
		}
		return page, nil
	})

var getTask = newTool("get_task", "Get task",
	"A task with its delivery attempts, oldest first: each attempt's outcome, status code, error, response headers, the start of the response body and its duration. Attempts with retried_at followed a manual retry. Large payloads and response bodies are truncated.",
	object(map[string]any{"id": taskIDProp}, "id"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		var task map[string]any
		var attempts struct {
			Attempts []map[string]any `json:"attempts"`
		}
		if err := api.do(ctx, "GET", apiPath("/v1/tasks/%s", args.ID), nil, nil, &task); err != nil {
			return nil, err
		}
		if err := api.do(ctx, "GET", apiPath("/v1/tasks/%s/attempts", args.ID), nil, nil, &attempts); err != nil {
			return nil, err
		}
		truncateBody(task)
		out := map[string]any{"task": task}
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

var getTaskDelivery = newTool("get_task_delivery", "Get task delivery request",
	"The exact HTTP request Workly sends to deliver a task, freshly signed (Workly-Signature), plus a curl command that sends it. Nothing is sent. Use it to replay a delivery against a handler you are fixing, e.g. after changing the URL's host to a local one. The signature covers the body, so keep the body byte for byte.",
	object(map[string]any{"id": taskIDProp}, "id"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		var d struct {
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if err := api.do(ctx, "GET", apiPath("/v1/tasks/%s/delivery", args.ID), nil, nil, &d); err != nil {
			return nil, err
		}
		if len(d.Body) > maxDeliveryBody {
			return map[string]any{
				"method": d.Method, "url": d.URL, "headers": d.Headers,
				"body":         truncate(d.Body, maxDeliveryBody),
				"body_omitted": "the body is too large to show in full, so there is no curl command; get it from GET /v1/tasks/{id}/delivery",
			}, nil
		}
		return map[string]any{"method": d.Method, "url": d.URL, "headers": d.Headers, "body": d.Body, "curl": curl(d.Method, d.URL, d.Headers, d.Body)}, nil
	})

var waitForTask = newTool("wait_for_task", "Wait for task",
	"Waits for the first delivery attempt since the task was enqueued or last retried by hand, then returns the task and its latest attempt: how the endpoint answered. Returns at once if that attempt already happened or the task has finished. Use it after enqueue_task or retry_task. Times out after timeout_seconds with done=false; a task scheduled for later (run_at) isn't attempted before then.",
	object(map[string]any{
		"id":              taskIDProp,
		"timeout_seconds": prop("number", "How long to wait (default 30).", "minimum", 1, "maximum", 60),
	}, "id"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		ID             string  `json:"id"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}) (any, error) {
		timeout := time.Duration(min(cmp.Or(args.TimeoutSeconds, 30), 60) * float64(time.Second))
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var task map[string]any
		for {
			err := api.do(ctx, "GET", apiPath("/v1/tasks/%s", args.ID), nil, nil, &task)
			if err != nil && ctx.Err() == nil {
				return nil, err
			}
			if err == nil && attempted(task) {
				break
			}
			select {
			case <-ctx.Done():
				return map[string]any{
					"done":   false,
					"reason": fmt.Sprintf("no attempt completed within %v", timeout),
					"task":   summary(task),
				}, nil
			case <-time.After(pollInterval):
			}
		}

		out := map[string]any{"done": true, "task": summary(task)}
		var attempts struct {
			Attempts []map[string]any `json:"attempts"`
		}
		if err := api.do(context.WithoutCancel(ctx), "GET", apiPath("/v1/tasks/%s/attempts", args.ID), nil, nil, &attempts); err != nil {
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

// attempted reports whether a task has finished, or has had an attempt since
// it was enqueued or last retried by hand. Comparing times rather than
// counting attempts from when the wait began means a fast endpoint can't
// answer before the wait starts and be missed.
func attempted(task map[string]any) bool {
	if finished(task) {
		return true
	}
	last := timeField(task, "last_attempt_at")
	return !last.IsZero() && !last.Before(timeField(task, "created_at")) && !last.Before(timeField(task, "retried_at"))
}

func timeField(m map[string]any, key string) time.Time {
	s, _ := m[key].(string)
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// Write tools.

var enqueueTask = newTool("enqueue_task", "Enqueue task",
	"Enqueues a task: Workly will send an HTTP request to url and retry it with backoff until the endpoint answers 2xx or the queue's max_attempts run out. The queue is created with default settings if it doesn't exist. Returns the task; follow up with wait_for_task to see the first attempt.",
	object(map[string]any{
		"queue":           queueProp,
		"url":             prop("string", "The http(s) URL to deliver to."),
		"payload":         map[string]any{"description": "A JSON body (any JSON value). Use this or body."},
		"body":            prop("string", "A raw text body. Use this or payload."),
		"content_type":    prop("string", "Content type of body (default text/plain; charset=utf-8)."),
		"method":          prop("string", "HTTP method (default POST).", "enum", []string{"GET", "POST", "PUT", "PATCH", "DELETE"}),
		"headers":         prop("object", "Headers sent with every delivery. Workly-* headers are reserved.", "additionalProperties", map[string]any{"type": "string"}),
		"delay_seconds":   prop("number", "Deliver after this delay. Use this or run_at.", "minimum", 0),
		"run_at":          timeProp("Deliver at this time. Use this or delay_seconds."),
		"idempotency_key": prop("string", "If a task in the queue has this key, it is returned and nothing is enqueued."),
		"max_attempts":    prop("integer", "Overrides the queue's max_attempts.", "minimum", 1, "maximum", 1000),
		"timeout_seconds": prop("number", "Overrides the queue's response timeout.", "minimum", 1, "maximum", 1800),
	}, "queue", "url"),
	annotations{OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args enqueueArgs) (any, error) {
		var task map[string]any
		if err := api.do(ctx, "POST", apiPath("/v1/queues/%s/tasks", args.Queue), nil, args.enqueueRequest, &task); err != nil {
			return nil, err
		}
		truncateBody(task)
		return task, nil
	})

type enqueueArgs struct {
	Queue string `json:"queue"`
	enqueueRequest
}

// enqueueRequest is the body of POST /v1/queues/{queue}/tasks.
type enqueueRequest struct {
	URL            string            `json:"url"`
	Payload        json.RawMessage   `json:"payload,omitempty"`
	Body           *string           `json:"body,omitempty"`
	ContentType    string            `json:"content_type,omitempty"`
	Method         string            `json:"method,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	DelaySeconds   *float64          `json:"delay_seconds,omitempty"`
	RunAt          string            `json:"run_at,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	MaxAttempts    *int              `json:"max_attempts,omitempty"`
	TimeoutSeconds *float64          `json:"timeout_seconds,omitempty"`
}

var retryTask = newTool("retry_task", "Retry task",
	"Delivers a task again now. A pending task (e.g. waiting to retry) becomes due at once; a failed, succeeded or cancelled task is replayed with a fresh set of attempts. Fails with invalid_state for a running task.",
	object(map[string]any{"id": taskIDProp}, "id"),
	annotations{OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		return taskAction(ctx, api, args.ID, "retry")
	})

var cancelTask = newTool("cancel_task", "Cancel task",
	"Cancels a pending or running task, so it isn't delivered (again). A delivery already in flight finishes but isn't retried. Fails with invalid_state for a finished task.",
	object(map[string]any{"id": taskIDProp}, "id"),
	annotations{DestructiveHint: true, IdempotentHint: true},
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		return taskAction(ctx, api, args.ID, "cancel")
	})

var retryFailedTasks = newTool("retry_failed_tasks", "Retry failed tasks",
	"Retries the failed tasks in a queue now, each with a fresh set of attempts, e.g. after fixing the endpoint they failed on. With last_error, only the tasks that failed with exactly that error (a group from get_queue's failures) are retried. Returns how many were retried.",
	object(map[string]any{
		"queue":      queueProp,
		"last_error": prop("string", "Only tasks whose last_error is exactly this."),
	}, "queue"),
	annotations{OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Queue     string `json:"queue"`
		LastError string `json:"last_error"`
	}) (any, error) {
		q := url.Values{}
		if args.LastError != "" {
			q.Set("last_error", args.LastError)
		}
		var out map[string]any
		err := api.do(ctx, "POST", apiPath("/v1/queues/%s/retry-failed", args.Queue), q, nil, &out)
		return out, err
	})

var cancelPendingTasks = newTool("cancel_pending_tasks", "Cancel pending tasks",
	"Cancels every pending task in a queue, including scheduled ones and ones waiting to retry, so they aren't delivered. Running tasks finish. Cancelled tasks can be retried later. Returns how many were cancelled.",
	object(map[string]any{"queue": queueProp}, "queue"),
	annotations{DestructiveHint: true, IdempotentHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Queue string `json:"queue"`
	}) (any, error) {
		return queueAction(ctx, api, args.Queue, "cancel-pending")
	})

var pauseQueue = newTool("pause_queue", "Pause queue",
	"Stops delivering a queue's tasks. Deliveries in flight finish, and enqueueing still works; tasks wait until the queue is resumed.",
	object(map[string]any{"queue": queueProp}, "queue"),
	annotations{IdempotentHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Queue string `json:"queue"`
	}) (any, error) {
		return queueAction(ctx, api, args.Queue, "pause")
	})

var resumeQueue = newTool("resume_queue", "Resume queue",
	"Resumes delivering a paused queue's tasks.",
	object(map[string]any{"queue": queueProp}, "queue"),
	annotations{IdempotentHint: true, OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Queue string `json:"queue"`
	}) (any, error) {
		return queueAction(ctx, api, args.Queue, "resume")
	})

var updateQueuePolicy = newTool("update_queue_policy", "Update queue policy",
	"Changes a queue's delivery policy. Only the settings given change; the others keep their current values. Pass null for rate_per_second to remove the rate limit. Creates the queue if it doesn't exist. Returns the queue.",
	object(map[string]any{
		"queue":               queueProp,
		"max_attempts":        prop("integer", "Total delivery attempts before a task fails.", "minimum", 1, "maximum", 1000),
		"min_backoff_seconds": prop("number", "Shortest wait before a retry.", "minimum", 0),
		"max_backoff_seconds": prop("number", "Longest wait before a retry.", "minimum", 0),
		"backoff_multiplier":  prop("number", "Retry n waits up to min_backoff_seconds * backoff_multiplier^(n-1), capped at max_backoff_seconds.", "minimum", 1),
		"timeout_seconds":     prop("number", "How long to wait for a response.", "minimum", 1, "maximum", 1800),
		"retry_on_4xx":        prop("boolean", "Retry 4xx responses. By default only 408 and 429 are retried."),
		"rate_per_second":     nullable("number", "Most deliveries started per second; null for no limit.", "exclusiveMinimum", 0, "maximum", 10000),
		"max_concurrency":     prop("integer", "Most deliveries in flight at once.", "minimum", 1, "maximum", 10000),
	}, "queue"),
	annotations{IdempotentHint: true},
	func(ctx context.Context, api apiClient, args map[string]json.RawMessage) (any, error) {
		var queue string
		if err := json.Unmarshal(args["queue"], &queue); err != nil || queue == "" {
			return nil, argumentError{errors.New("queue is required")}
		}
		delete(args, "queue")
		for k := range args {
			if !slices.Contains(policyFields, k) {
				return nil, argumentError{fmt.Errorf("unknown setting %q", k)}
			}
		}

		var current map[string]any
		err := api.do(ctx, "GET", apiPath("/v1/queues/%s", queue), nil, nil, &current)
		if ae, ok := errors.AsType[*apiError](err); ok && ae.Code == "queue_not_found" {
			current = map[string]any{}
		} else if err != nil {
			return nil, err
		}
		// PUT is declarative, so start from the current policy.
		config := map[string]any{}
		for _, k := range policyFields {
			if v, ok := current[k]; ok {
				config[k] = v
			}
		}
		for k, v := range args {
			if string(v) == "null" {
				delete(config, k)
			} else {
				config[k] = v
			}
		}
		var queueOut map[string]any
		if err := api.do(ctx, "PUT", apiPath("/v1/queues/%s", queue), nil, config, &queueOut); err != nil {
			return nil, err
		}
		return queueOut, nil
	})

var policyFields = []string{
	"max_attempts", "min_backoff_seconds", "max_backoff_seconds", "backoff_multiplier", "timeout_seconds",
	"retry_on_4xx", "rate_per_second", "max_concurrency",
}

func taskAction(ctx context.Context, api apiClient, id, action string) (any, error) {
	var task map[string]any
	if err := api.do(ctx, "POST", apiPath("/v1/tasks/%s/", id)+action, nil, nil, &task); err != nil {
		return nil, err
	}
	return summary(task), nil
}

func queueAction(ctx context.Context, api apiClient, queue, action string) (any, error) {
	var out map[string]any
	err := api.do(ctx, "POST", apiPath("/v1/queues/%s/", queue)+action, nil, nil, &out)
	return out, err
}

// Limits on what goes back to the model, which pays for every byte.
const (
	maxAttempts     = 20
	maxResponseBody = 2000
	maxTaskBody     = 8000
	maxDeliveryBody = 32000
	maxErrorLength  = 500
	pollInterval    = 250 * time.Millisecond
)

// summary is a task without its payload, body and headers: what list views
// need.
func summary(t map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{
		"id", "queue", "state", "method", "url", "run_at", "attempt_count", "max_attempts", "last_status",
		"last_outcome", "last_error", "idempotency_key", "created_at", "finished_at", "last_attempt_at", "retried_at",
	} {
		if v, ok := t[k]; ok {
			out[k] = v
		}
	}
	if e, ok := out["last_error"].(string); ok {
		out["last_error"] = truncate(e, maxErrorLength)
	}
	return out
}

func finished(t map[string]any) bool {
	switch t["state"] {
	case "succeeded", "failed", "cancelled":
		return true
	}
	return false
}

// truncateBody shortens a task's payload or body in place.
func truncateBody(t map[string]any) {
	if body, ok := t["body"].(string); ok {
		t["body"] = truncate(body, maxTaskBody)
	}
	if p, ok := t["payload"]; ok {
		b, _ := json.Marshal(p)
		if len(b) > maxTaskBody {
			delete(t, "payload")
			t["payload_truncated"] = truncate(string(b), maxTaskBody)
		}
	}
}

// truncate cuts s to at most n bytes, on a rune boundary, and says so.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("… [truncated: %d of %d bytes shown]", cut, len(s))
}

// curl builds a POSIX shell command that sends the request exactly, like the
// dashboard's "Copy as curl".
func curl(method, target string, headers map[string]string, body string) string {
	lines := []string{"curl -X " + method + " " + shellQuote(target)}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		lines = append(lines, "-H "+shellQuote(k+": "+headers[k]))
	}
	if body != "" {
		lines = append(lines, "--data-raw "+shellQuote(body))
	}
	return strings.Join(lines, " \\\n  ")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
