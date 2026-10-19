package mcp

import (
	"cmp"
	"context"
	"net/url"
	"strconv"
)

// Groups track a set of tasks and enqueue a callback task when all of them
// have finished.

var (
	groupIDProp   = prop("string", `Group ID, e.g. "grp_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A".`)
	groupStatuses = []string{"open", "running", "succeeded", "failed"}
)

// groupFailures is how many failed tasks get_group shows.
const groupFailures = 10

var listGroups = newTool("list_groups", "List task groups",
	`Task groups, newest first: each with its status and how many of its tasks succeeded, failed, were cancelled or remain. "open" groups still accept tasks; "running" ones are closed and waiting for their tasks; "succeeded" and "failed" ones have completed and enqueued their callback (callback_task_id). Pass next_cursor as cursor for the next page.`,
	object(map[string]any{
		"status": prop("string", "Only groups in this status.", "enum", groupStatuses),
		"name":   prop("string", "Only groups with this name."),
		"limit":  prop("integer", "Groups per page (default 20).", "minimum", 1, "maximum", 100),
		"cursor": prop("string", "next_cursor from the previous page."),
	}), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		Status string `json:"status"`
		Name   string `json:"name"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}) (any, error) {
		q := url.Values{"limit": {strconv.Itoa(cmp.Or(min(args.Limit, 100), 20))}}
		for k, v := range map[string]string{"status": args.Status, "name": args.Name, "cursor": args.Cursor} {
			if v != "" {
				q.Set(k, v)
			}
		}
		var out map[string]any
		err := api.do(ctx, "GET", "/v1/groups", q, nil, &out)
		return out, err
	})

var getGroup = newTool("get_group", "Get task group",
	`A task group's status and counts, its most recently failed tasks (what keeps it from succeeding) and its callback task, once it has completed. A group with failed or cancelled tasks completes as "failed". List all its tasks with list_tasks and group_id.`,
	object(map[string]any{"id": groupIDProp}, "id"), readOnly,
	func(ctx context.Context, api apiClient, args struct {
		ID string `json:"id"`
	}) (any, error) {
		var group map[string]any
		if err := api.do(ctx, "GET", apiPath("/v1/groups/%s", args.ID), nil, nil, &group); err != nil {
			return nil, err
		}
		out := map[string]any{"group": group}
		if n, _ := group["failed"].(float64); n > 0 {
			var failed struct {
				Tasks []map[string]any `json:"tasks"`
			}
			q := url.Values{"group_id": {args.ID}, "state": {"failed"}, "sort": {"last_attempt_at"},
				"limit": {strconv.Itoa(groupFailures)}}
			if err := api.do(ctx, "GET", "/v1/tasks", q, nil, &failed); err != nil {
				return nil, err
			}
			for i, t := range failed.Tasks {
				failed.Tasks[i] = summary(t)
			}
			out["failed_tasks"] = failed.Tasks
		}
		if id, ok := group["callback_task_id"].(string); ok {
			var cb map[string]any
			if err := api.do(ctx, "GET", apiPath("/v1/tasks/%s", id), nil, nil, &cb); err != nil {
				return nil, err
			}
			out["callback_task"] = summary(cb)
		}
		return out, nil
	})
