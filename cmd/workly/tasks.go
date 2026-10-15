package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/worklydev/workly/internal/client"
)

func tasksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tasks",
		Aliases: []string{"task", "t"},
		Short:   "Enqueue, inspect, retry and cancel tasks",
	}
	addClientFlags(cmd)
	cmd.AddCommand(tasksEnqueueCmd(), tasksGetCmd(), tasksListCmd(),
		taskActionCmd("retry", "Deliver a task now; replays a finished task with fresh attempts"),
		taskActionCmd("cancel", "Cancel a pending or running task"))
	return cmd
}

func tasksEnqueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enqueue QUEUE URL",
		Short: "Enqueue a task and print its ID",
		Long: `Enqueue a task to deliver to URL, and print its ID.

The queue is created with default settings if it doesn't exist.
--payload and --body take a value, @file, or - for stdin.`,
		Example: `  workly tasks enqueue emails https://api.example.com/send -d '{"user_id": 123}'
  workly tasks enqueue reports https://api.example.com/build --delay 10m -k report-2026-09
  cat event.json | workly tasks enqueue events https://api.example.com/ingest -d -`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := enqueueRequest(cmd, args[1])
			if err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.CreateTaskWithResponse(cmd.Context(), args[0], nil, req)
			if err := check(res, err); err != nil {
				return err
			}
			task := res.JSON201
			if task == nil {
				task = res.JSON200
				fmt.Fprintln(cmd.ErrOrStderr(), "A task with this idempotency key already exists; nothing was enqueued.")
			}
			// Just the ID, so scripts can capture it.
			return output(cmd, res.Body, func(w io.Writer) { fmt.Fprintln(w, task.Id) })
		},
	}
	addRequestFlags(cmd, `header sent with every delivery, "Name: value" (repeatable)`)
	f := cmd.Flags()
	f.Duration("delay", 0, "deliver after this delay, e.g. 30s or 2h")
	f.String("at", "", "deliver at this time (RFC 3339, e.g. 2026-10-01T09:00:00Z)")
	f.StringP("idempotency-key", "k", "", "don't enqueue if the queue already has a task with this key")
	f.Int("max-attempts", 0, "override the queue's max attempts")
	f.Duration("timeout", 0, "override the queue's response timeout")
	cmd.MarkFlagsMutuallyExclusive("delay", "at")
	return cmd
}

func enqueueRequest(cmd *cobra.Command, url string) (client.EnqueueRequest, error) {
	req := client.EnqueueRequest{Url: url}
	r, err := readRequestFlags(cmd)
	if err != nil {
		return req, err
	}
	req.Payload, req.Body, req.ContentType, req.Method, req.Headers = r.payload, r.body, r.contentType, r.method, r.headers
	f := cmd.Flags()
	setIf(f.Changed("delay"), &req.DelaySeconds, must(f.GetDuration("delay")).Seconds())
	if f.Changed("at") {
		at, err := time.Parse(time.RFC3339, must(f.GetString("at")))
		if err != nil {
			return req, fmt.Errorf("--at: %w", err)
		}
		req.RunAt = &at
	}
	setIf(f.Changed("idempotency-key"), &req.IdempotencyKey, must(f.GetString("idempotency-key")))
	setIf(f.Changed("max-attempts"), &req.MaxAttempts, must(f.GetInt("max-attempts")))
	setIf(f.Changed("timeout"), &req.TimeoutSeconds, must(f.GetDuration("timeout")).Seconds())
	return req, nil
}

// requestFlags is the HTTP request given by --payload, --body,
// --content-type, --method and --header, shared by tasks and schedules.
type requestFlags struct {
	payload     *json.RawMessage
	body        *string
	contentType *string
	method      *client.HTTPMethod
	headers     *map[string]string
}

func addRequestFlags(cmd *cobra.Command, headerUsage string) {
	f := cmd.Flags()
	f.StringP("payload", "d", "", "JSON body: a value, @file, or - for stdin")
	f.String("body", "", "raw body: a value, @file, or - for stdin")
	f.String("content-type", "", "content type of --body (default text/plain; charset=utf-8)")
	f.StringP("method", "X", "", "HTTP method (default POST)")
	f.StringArrayP("header", "H", nil, headerUsage)
	cmd.MarkFlagsMutuallyExclusive("payload", "body")
}

func readRequestFlags(cmd *cobra.Command) (requestFlags, error) {
	var r requestFlags
	f := cmd.Flags()
	if f.Changed("payload") {
		data, err := readArg(cmd, must(f.GetString("payload")))
		if err != nil {
			return r, err
		}
		if !json.Valid(data) {
			return r, errors.New("--payload is not valid JSON (use --body for other content)")
		}
		raw := json.RawMessage(data)
		r.payload = &raw
	}
	if f.Changed("body") {
		data, err := readArg(cmd, must(f.GetString("body")))
		if err != nil {
			return r, err
		}
		r.body = ptr(string(data))
	}
	setIf(f.Changed("content-type"), &r.contentType, must(f.GetString("content-type")))
	setIf(f.Changed("method"), &r.method, client.HTTPMethod(strings.ToUpper(must(f.GetString("method")))))
	if hs := must(f.GetStringArray("header")); len(hs) > 0 {
		headers := map[string]string{}
		for _, h := range hs {
			name, value, ok := strings.Cut(h, ":")
			if !ok {
				return r, fmt.Errorf(`--header %q: want "Name: value"`, h)
			}
			headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
		r.headers = &headers
	}
	return r, nil
}

// readArg resolves a flag value that may be @file or - (stdin).
func readArg(cmd *cobra.Command, v string) ([]byte, error) {
	switch {
	case v == "-":
		return io.ReadAll(cmd.InOrStdin())
	case strings.HasPrefix(v, "@"):
		return os.ReadFile(v[1:])
	}
	return []byte(v), nil
}

func tasksGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get ID",
		Short: "Show a task and its delivery attempts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.GetTaskWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return output(cmd, res.Body, nil)
			}
			attempts, err := c.ListAttemptsWithResponse(cmd.Context(), args[0])
			if err := check(attempts, err); err != nil {
				return err
			}
			printTask(cmd.OutOrStdout(), res.JSON200, attempts.JSON200.Attempts)
			return nil
		},
	}
}

func tasksListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List tasks, newest first",
		Example: `  workly tasks ls --queue emails --state failed
  workly tasks ls -k report-2026-09`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var params client.SearchTasksParams
			setIf(f.Changed("queue"), &params.Queue, must(f.GetString("queue")))
			setIf(f.Changed("state"), &params.State, client.TaskState(must(f.GetString("state"))))
			setIf(f.Changed("idempotency-key"), &params.IdempotencyKey, must(f.GetString("idempotency-key")))
			setIf(f.Changed("error"), &params.LastError, must(f.GetString("error")))
			setIf(f.Changed("cursor"), &params.Cursor, must(f.GetString("cursor")))
			setIf(true, &params.Limit, must(f.GetInt("limit")))

			res, err := c.SearchTasksWithResponse(cmd.Context(), &params)
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				page := res.JSON200
				if len(page.Tasks) == 0 {
					fmt.Fprintln(w, "No tasks.")
					return
				}
				var rows [][]string
				for _, t := range page.Tasks {
					rows = append(rows, []string{t.Id, t.Queue, state(&t), attemptCount(&t), ago(t.CreatedAt), t.Url})
				}
				table(w, "ID\tQUEUE\tSTATE\tATTEMPTS\tCREATED\tURL", rows)
				if page.NextCursor != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "\nMore tasks: add --cursor %s\n", *page.NextCursor)
				}
			})
		},
	}
	f := cmd.Flags()
	f.String("queue", "", "only this queue's tasks")
	f.String("state", "", "only tasks in this state: pending, running, succeeded, failed or cancelled")
	f.StringP("idempotency-key", "k", "", "only tasks with this idempotency key")
	f.String("error", "", "only tasks whose last error is exactly this (see queues failures)")
	f.Int("limit", 20, "tasks per page (at most 200)")
	f.String("cursor", "", "continue from a previous page")
	return cmd
}

// taskActionCmd is retry or cancel: POST /v1/tasks/{id}/{action}.
func taskActionCmd(action, short string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " ID",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			var (
				body []byte
				task *client.Task
			)
			if action == "retry" {
				res, err := c.RetryTaskWithResponse(cmd.Context(), args[0])
				if err := check(res, err); err != nil {
					return err
				}
				body, task = res.Body, res.JSON200
			} else {
				res, err := c.CancelTaskWithResponse(cmd.Context(), args[0])
				if err := check(res, err); err != nil {
					return err
				}
				body, task = res.Body, res.JSON200
			}
			return output(cmd, body, func(w io.Writer) {
				fmt.Fprintf(w, "Task %s is %s.\n", task.Id, state(task))
			})
		},
	}
}

func printTask(w io.Writer, t *client.Task, attempts []client.Attempt) {
	rows := [][]string{
		{"ID", t.Id},
		{"Queue", t.Queue},
		{"State", state(t)},
		{"Request", string(t.Method) + " " + t.Url},
	}
	if t.State == client.TaskStatePending {
		rows = append(rows, []string{"Runs", ago(t.RunAt)})
	}
	rows = append(rows, []string{"Attempts", attemptCount(t)})
	if t.IdempotencyKey != nil {
		rows = append(rows, []string{"Idempotency key", *t.IdempotencyKey})
	}
	if t.LastError != nil {
		rows = append(rows, []string{"Last error", *t.LastError})
	}
	rows = append(rows, []string{"Created", t.CreatedAt.Local().Format(time.DateTime) + " (" + ago(t.CreatedAt) + ")"})
	if t.FinishedAt != nil {
		rows = append(rows, []string{"Finished", t.FinishedAt.Local().Format(time.DateTime) + " (" + ago(*t.FinishedAt) + ")"})
	}
	for _, r := range rows {
		r[0] += ":"
	}
	table(w, "", rows)
	printBody(w, t.Payload, t.Body)
	printAttempts(w, attempts)
}

func printAttempts(w io.Writer, attempts []client.Attempt) {
	if len(attempts) == 0 {
		return
	}
	fmt.Fprintln(w)
	var arows [][]string
	for _, a := range attempts {
		arows = append(arows, []string{
			strconv.Itoa(a.Number), string(a.Outcome), deref(a.StatusCode, "-"),
			fmt.Sprintf("%dms", a.DurationMs), ago(a.StartedAt), deref(a.Error, ""),
		})
	}
	table(w, "#\tOUTCOME\tSTATUS\tDURATION\tSTARTED\tERROR", arows)
}

// state shows a pending task that isn't due yet as scheduled, like the dashboard.
func state(t *client.Task) string {
	if t.State == client.TaskStatePending && t.RunAt.After(time.Now()) {
		return "scheduled"
	}
	return string(t.State)
}

func attemptCount(t *client.Task) string {
	if t.MaxAttempts != nil {
		return fmt.Sprintf("%d/%d", t.AttemptCount, *t.MaxAttempts)
	}
	return strconv.Itoa(t.AttemptCount)
}

func ptr[T any](v T) *T { return &v }
