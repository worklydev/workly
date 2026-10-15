package main

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/worklydev/workly/internal/client"
)

func queuesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "queues",
		Aliases: []string{"queue", "q"},
		Short:   "List, configure, pause and resume queues",
	}
	addClientFlags(cmd)
	cmd.AddCommand(queuesListCmd(), queuesGetCmd(), queuesPutCmd(),
		queueActionCmd("pause", "Stop delivering a queue's tasks (enqueueing still works)"),
		queueActionCmd("resume", "Resume delivering a paused queue's tasks"),
		queuesFailuresCmd(), queuesRetryFailedCmd(), queuesCancelPendingCmd(), queuesDeleteCmd())
	return cmd
}

func queuesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List queues with their current work",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.ListQueuesWithResponse(cmd.Context())
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				if len(res.JSON200.Queues) == 0 {
					fmt.Fprintln(w, "No queues yet. Enqueue a task to create one, or run `workly queues put NAME`.")
					return
				}
				var rows [][]string
				for _, q := range res.JSON200.Queues {
					status, due, scheduled, running, failed, oldest := "active", "-", "-", "-", "-", "-"
					if q.Paused {
						status = "paused"
					}
					if s := q.Stats; s != nil {
						due, scheduled, running, failed = count(s.Due), count(s.Scheduled), count(s.Running), count(s.Failed)
						if s.OldestDueAt != nil {
							oldest = short(time.Since(*s.OldestDueAt))
						}
					}
					rows = append(rows, []string{q.Name, status, due, scheduled, running, failed, oldest})
				}
				table(w, "NAME\tSTATUS\tDUE\tSCHEDULED\tRUNNING\tFAILED\tOLDEST DUE", rows)
			})
		},
	}
}

func queuesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get NAME",
		Short: "Show a queue's configuration and current work",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.GetQueueWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) { printQueue(w, res.JSON200) })
		},
	}
}

func queuesPutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "put NAME",
		Aliases: []string{"create", "update"},
		Short:   "Create or update a queue",
		Long: `Create or update a queue.

Declarative, like the API: the queue ends up with exactly the settings given,
and settings you leave out are reset to their defaults. Pausing is separate
(see pause and resume).`,
		Example: `  workly queues put emails --rate 50 --max-concurrency 10
  workly queues put webhooks --max-attempts 5 --timeout 10s --retry-on-4xx`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			var cfg client.QueueConfig
			f := cmd.Flags()
			setIf(f.Changed("max-attempts"), &cfg.MaxAttempts, must(f.GetInt("max-attempts")))
			setIf(f.Changed("min-backoff"), &cfg.MinBackoffSeconds, must(f.GetDuration("min-backoff")).Seconds())
			setIf(f.Changed("max-backoff"), &cfg.MaxBackoffSeconds, must(f.GetDuration("max-backoff")).Seconds())
			setIf(f.Changed("backoff-multiplier"), &cfg.BackoffMultiplier, must(f.GetFloat64("backoff-multiplier")))
			setIf(f.Changed("timeout"), &cfg.TimeoutSeconds, must(f.GetDuration("timeout")).Seconds())
			setIf(f.Changed("retry-on-4xx"), &cfg.RetryOn4xx, must(f.GetBool("retry-on-4xx")))
			setIf(f.Changed("rate"), &cfg.RatePerSecond, must(f.GetFloat64("rate")))
			setIf(f.Changed("max-concurrency"), &cfg.MaxConcurrency, must(f.GetInt("max-concurrency")))

			res, err := c.PutQueueWithResponse(cmd.Context(), args[0], cfg)
			if err := check(res, err); err != nil {
				return err
			}
			q := res.JSON200
			if q == nil {
				q = res.JSON201
			}
			return output(cmd, res.Body, func(w io.Writer) { printQueue(w, q) })
		},
	}
	f := cmd.Flags()
	f.Int("max-attempts", 5, "total delivery attempts before a task fails")
	f.Duration("min-backoff", 2*time.Second, "shortest wait before a retry")
	f.Duration("max-backoff", time.Hour, "longest wait before a retry")
	f.Float64("backoff-multiplier", 2, "how fast the wait between retries grows")
	f.Duration("timeout", 30*time.Second, "how long to wait for a response")
	f.Bool("retry-on-4xx", false, "retry 4xx responses (by default only 408 and 429 are retried)")
	f.Float64("rate", 0, "most deliveries started per second (default no limit)")
	f.Int("max-concurrency", 3, "most deliveries in flight at once")
	return cmd
}

// queueActionCmd is pause or resume: POST /v1/queues/{name}/{action}.
func queueActionCmd(action, short string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " NAME",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			var (
				body []byte
				q    *client.Queue
			)
			if action == "pause" {
				res, err := c.PauseQueueWithResponse(cmd.Context(), args[0])
				if err := check(res, err); err != nil {
					return err
				}
				body, q = res.Body, res.JSON200
			} else {
				res, err := c.ResumeQueueWithResponse(cmd.Context(), args[0])
				if err := check(res, err); err != nil {
					return err
				}
				body, q = res.Body, res.JSON200
			}
			return output(cmd, body, func(w io.Writer) {
				fmt.Fprintf(w, "Queue %s %sd.\n", q.Name, action)
			})
		},
	}
}

func queuesFailuresCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "failures NAME",
		Short: "Group a queue's failed tasks by error, most common first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.GetQueueFailuresWithResponse(cmd.Context(), args[0], nil)
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				if len(res.JSON200.Groups) == 0 {
					fmt.Fprintln(w, "No failed tasks.")
					return
				}
				var rows [][]string
				for _, g := range res.JSON200.Groups {
					rows = append(rows, []string{strconv.Itoa(g.Count), g.Error, ago(g.LastFailedAt), g.LatestTaskId})
				}
				table(w, "TASKS\tERROR\tLAST FAILED\tLATEST TASK", rows)
				fmt.Fprintf(cmd.ErrOrStderr(), "\nRetry one group: workly queues retry-failed %s --error '<ERROR>'\n", args[0])
			})
		},
	}
}

func queuesRetryFailedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry-failed NAME",
		Short: "Retry every failed task in a queue, e.g. after an endpoint outage",
		Example: `  workly queues retry-failed emails
  workly queues retry-failed emails --error 'HTTP 503 Service Unavailable'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			var params client.RetryFailedTasksParams
			setIf(cmd.Flags().Changed("error"), &params.LastError, must(cmd.Flags().GetString("error")))
			res, err := c.RetryFailedTasksWithResponse(cmd.Context(), args[0], &params)
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				fmt.Fprintf(w, "Retrying %d failed %s.\n", res.JSON200.Retried, plural(res.JSON200.Retried, "task"))
			})
		},
	}
	cmd.Flags().String("error", "", "only tasks whose last error is exactly this (see queues failures)")
	return cmd
}

func queuesCancelPendingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel-pending NAME",
		Short: "Cancel a queue's pending tasks, including scheduled ones",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirm(cmd, fmt.Sprintf("Cancel every pending task in queue %s?", args[0])); err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.CancelPendingTasksWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				fmt.Fprintf(w, "Cancelled %d pending %s.\n", res.JSON200.Cancelled, plural(res.JSON200.Cancelled, "task"))
			})
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "don't ask for confirmation")
	return cmd
}

func queuesDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete NAME",
		Aliases: []string{"rm"},
		Short:   "Delete a queue and all its tasks",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirm(cmd, fmt.Sprintf("Delete queue %s and all its tasks?", args[0])); err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.DeleteQueueWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); !asJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "Queue %s deleted.\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "don't ask for confirmation")
	return cmd
}

func printQueue(w io.Writer, q *client.Queue) {
	status := "active"
	if q.Paused {
		status = "paused"
	}
	rows := [][]string{
		{"Name", q.Name},
		{"Status", status},
		{"Max attempts", strconv.Itoa(q.MaxAttempts)},
		{"Backoff", fmt.Sprintf("%s to %s, x%g", seconds(q.MinBackoffSeconds), seconds(q.MaxBackoffSeconds), q.BackoffMultiplier)},
		{"Timeout", seconds(q.TimeoutSeconds)},
		{"Retry on 4xx", strconv.FormatBool(q.RetryOn4xx)},
		{"Rate", deref(q.RatePerSecond, "no limit") + perSecond(q.RatePerSecond)},
		{"Max concurrency", strconv.Itoa(q.MaxConcurrency)},
	}
	if s := q.Stats; s != nil {
		oldest := "-"
		if s.OldestDueAt != nil {
			oldest = short(time.Since(*s.OldestDueAt))
		}
		rows = append(rows,
			[]string{"Due", count(s.Due)},
			[]string{"Scheduled", count(s.Scheduled)},
			[]string{"Running", count(s.Running)},
			[]string{"Failed", count(s.Failed)},
			[]string{"Oldest due", oldest})
	}
	for _, r := range rows {
		r[0] += ":"
	}
	table(w, "", rows)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func perSecond(rate *float64) string {
	if rate == nil {
		return ""
	}
	return "/s"
}

// count shows the stats cap (10,000) as "10000+".
func count(n int) string {
	if n >= 10000 {
		return "10000+"
	}
	return strconv.Itoa(n)
}

func setIf[T any](ok bool, dst **T, v T) {
	if ok {
		*dst = &v
	}
}

// must drops the error from pflag getters, which only fail for flags that
// weren't registered.
func must[T any](v T, _ error) T { return v }
