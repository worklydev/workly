package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// Workflows run a DAG of HTTP steps, each delivered as a task. A step's
// endpoint can hand its completion to a group with Workly-Wait-For.

var (
	workflowProp      = prop("string", "Workflow name.")
	workflowRunIDProp = prop("string", `Workflow run ID, e.g. "wfr_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A".`)
	workflowStatuses  = []string{"running", "succeeded", "failed", "cancelled"}
)

var listWorkflows = newTool("list_workflows", "List workflows",
	"All workflows, in summary: their steps (name and depends_on), version (counts changes to the definition) and how the latest run went (last_run, null before the first). Use get_workflow for the steps' requests and recent runs.",
	object(map[string]any{}), readOnly,
	func(ctx context.Context, api apiClient, _ struct{}) (any, error) {
		var out struct {
			Workflows []map[string]any `json:"workflows"`
		}
		if err := api.do(ctx, "GET", "/v1/workflows", nil, nil, &out); err != nil {
			return nil, err
		}
		for i, w := range out.Workflows {
			out.Workflows[i] = map[string]any{
				"name": w["name"], "version": w["version"], "steps": stepOutline(w["steps"]), "last_run": w["last_run"],
			}
		}
		return out, nil
	})

// stepOutline is a definition's steps as name and depends_on alone.
func stepOutline(steps any) []map[string]any {
	list, _ := steps.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, s := range list {
		s, _ := s.(map[string]any)
		deps := s["depends_on"]
		if deps == nil {
			deps = []any{}
		}
		out = append(out, map[string]any{"name": s["name"], "depends_on": deps})
	}
	return out
}

var getWorkflow = newTool("get_workflow", "Get workflow",
	"A workflow's definition (each step's request, queue, depends_on; max_duration_seconds) and its most recent runs, newest first. Use get_workflow_run to see where a run is.",
	object(map[string]any{"workflow": workflowProp}, "workflow"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		Workflow string `json:"workflow"`
	}) (any, error) {
		var w map[string]any
		if err := api.do(ctx, "GET", apiPath("/v1/workflows/%s", args.Workflow), nil, nil, &w); err != nil {
			return nil, err
		}
		withNulls(w, "last_run")
		steps, _ := w["steps"].([]any)
		for _, s := range steps {
			if s, ok := s.(map[string]any); ok {
				if s["depends_on"] == nil {
					s["depends_on"] = []any{}
				}
				withNulls(s, stepConfigKeys...)
			}
		}
		var runs struct {
			Runs []map[string]any `json:"workflow_runs"`
		}
		q := url.Values{"workflow": {args.Workflow}, "limit": {strconv.Itoa(recentRuns)}}
		if err := api.do(ctx, "GET", "/v1/workflow-runs", q, nil, &runs); err != nil {
			return nil, err
		}
		for i, r := range runs.Runs {
			runs.Runs[i] = workflowRunSummary(r)
		}
		return map[string]any{"workflow": w, "recent_runs": runs.Runs}, nil
	})

// stepConfigKeys are the optional fields of a step's definition, which the
// API leaves out when they aren't set.
var stepConfigKeys = []string{"method", "headers", "payload", "body", "content_type", "queue", "timeout_seconds", "max_attempts"}

var workflowRunKeys = []string{"id", "workflow", "version", "status", "error", "idempotency_key",
	"schedule_run_id", "attempt", "retried_at", "created_at", "deadline_at", "finished_at", "duration_ms"}

func workflowRunSummary(r map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range workflowRunKeys {
		out[k] = r[k]
	}
	if e, ok := out["error"].(string); ok {
		out["error"] = truncate(e, maxErrorLength)
	}
	return out
}

var listWorkflowRuns = newTool("list_workflow_runs", "List workflow runs",
	`Workflow runs, newest first: one workflow's, or every workflow's if workflow is left out (e.g. status "failed"), optionally only those in one status. Returns summaries; use get_workflow_run for the steps. Pass next_cursor as cursor for the next page.`,
	object(map[string]any{
		"workflow": prop("string", "Only this workflow's runs. Leave out for every workflow's."),
		"status":   prop("string", "Only runs in this status.", "enum", workflowStatuses),
		"limit":    prop("integer", "Runs per page (default 20).", "minimum", 1, "maximum", 100),
		"cursor":   prop("string", "next_cursor from the previous page."),
	}), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		Workflow string `json:"workflow"`
		Status   string `json:"status"`
		Limit    int    `json:"limit"`
		Cursor   string `json:"cursor"`
	}) (any, error) {
		q := url.Values{"limit": {strconv.Itoa(cmp.Or(min(args.Limit, 100), 20))}}
		for k, v := range map[string]string{"workflow": args.Workflow, "status": args.Status, "cursor": args.Cursor} {
			if v != "" {
				q.Set(k, v)
			}
		}
		var page struct {
			Runs       []map[string]any `json:"workflow_runs"`
			NextCursor string           `json:"next_cursor,omitempty"`
		}
		if err := api.do(ctx, "GET", "/v1/workflow-runs", q, nil, &page); err != nil {
			return nil, err
		}
		for i, r := range page.Runs {
			page.Runs[i] = workflowRunSummary(r)
		}
		return page, nil
	})

var getWorkflowRun = newTool("get_workflow_run", "Get workflow run",
	`A workflow run and each of its steps, in definition order: status (blocked: waiting for blocked_by; pending: its task is queued or retrying; running: being delivered; waiting: its request succeeded and it waits for wait_for, a group, to complete; succeeded; failed, with error; skipped: never ran because the run stopped), its task (state, attempt_count, last_status, last_error; get_task shows the attempts) and timings (blocked_ms, waiting_ms, duration_ms). Start here to find out why a workflow hasn't finished: a waiting step's group is shown by get_group.`,
	object(map[string]any{"id": workflowRunIDProp}, "id"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		var r map[string]any
		if err := api.do(ctx, "GET", apiPath("/v1/workflow-runs/%s", args.ID), nil, nil, &r); err != nil {
			return nil, err
		}
		return workflowRunDetail(r), nil
	})

// workflowRunDetail is a run with its steps, as the tools that return one
// show it.
func workflowRunDetail(r map[string]any) map[string]any {
	out := workflowRunSummary(r)
	out["input"] = r["input"]
	steps, _ := r["steps"].([]any)
	for _, s := range steps {
		if s, ok := s.(map[string]any); ok {
			withNulls(s, "task", "wait_for", "error", "finished_at")
		}
	}
	out["steps"] = steps
	return out
}

var triggerWorkflow = newTool("trigger_workflow", "Start workflow run",
	"Starts a run of a workflow: its steps without dependencies are enqueued at once. input (any JSON) is sent to every step that has no body of its own. With an idempotency_key already used for a run of this workflow, that run is returned and nothing starts. Returns the run with its steps; follow up with get_workflow_run.",
	object(map[string]any{
		"workflow":        workflowProp,
		"input":           map[string]any{"description": "JSON passed to the steps (any JSON value)."},
		"idempotency_key": prop("string", "Deduplicates runs of the workflow.", "maxLength", 256),
	}, "workflow"),
	annotations{OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Workflow       string          `json:"workflow"`
		Input          json.RawMessage `json:"input"`
		IdempotencyKey string          `json:"idempotency_key"`
	}) (any, error) {
		body := map[string]any{}
		if len(args.Input) > 0 {
			body["input"] = args.Input
		}
		if args.IdempotencyKey != "" {
			body["idempotency_key"] = args.IdempotencyKey
		}
		var r map[string]any
		if err := api.do(ctx, "POST", apiPath("/v1/workflows/%s/runs", args.Workflow), nil, body, &r); err != nil {
			return nil, err
		}
		return workflowRunDetail(r), nil
	})

var deleteWorkflow = newTool("delete_workflow", "Delete workflow",
	"Deletes a workflow's definition, so it can't be triggered. Its runs are kept, and runs still going finish under the definition they started with. It can't be undone. A schedule that starts this workflow keeps going and its runs fail: delete or change it too.",
	object(map[string]any{"workflow": workflowProp}, "workflow"),
	annotations{DestructiveHint: true},
	func(ctx context.Context, api apiClient, args struct {
		Workflow string `json:"workflow"`
	}) (any, error) {
		if err := api.do(ctx, "DELETE", apiPath("/v1/workflows/%s", args.Workflow), nil, nil, nil); err != nil {
			return nil, err
		}
		return map[string]any{"workflow": args.Workflow, "deleted": true}, nil
	})

var cancelWorkflowRun = newTool("cancel_workflow_run", "Cancel workflow run",
	"Cancels a running workflow run: steps not yet started are skipped and their queued tasks cancelled. Requests already being delivered finish, but start nothing. It can't undo what steps already did. Returns the run.",
	object(map[string]any{"id": workflowRunIDProp}, "id"),
	annotations{DestructiveHint: true, IdempotentHint: true},
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		var r map[string]any
		if err := api.do(ctx, "POST", apiPath("/v1/workflow-runs/%s/cancel", args.ID), nil, nil, &r); err != nil {
			return nil, err
		}
		return workflowRunDetail(r), nil
	})

var retryWorkflowRun = newTool("retry_workflow_run", "Retry workflow run",
	"Reopens a failed or cancelled workflow run under the definition it started with: steps that succeeded stay, the rest run again (each on its old task, with a fresh set of attempts), and a step that was waiting for a group follows the group. Use it after fixing what made a step fail. The run's attempt goes up by one. Returns the run with its steps; follow up with get_workflow_run.",
	object(map[string]any{"id": workflowRunIDProp}, "id"),
	annotations{OpenWorldHint: true},
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		var r map[string]any
		if err := api.do(ctx, "POST", apiPath("/v1/workflow-runs/%s/retry", args.ID), nil, nil, &r); err != nil {
			return nil, err
		}
		return workflowRunDetail(r), nil
	})
