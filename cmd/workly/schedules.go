package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/worklydev/workly/internal/client"
)

func schedulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedules",
		Aliases: []string{"schedule", "s"},
		Short:   "Create, inspect, run and pause schedules (cron for HTTP endpoints)",
		Long: `Create, inspect, run and pause schedules.

A schedule calls a URL on a cron timetable. Each call is a run, with its own
attempts and retries. Schedules are separate from queues and tasks.`,
	}
	addClientFlags(cmd)
	cmd.AddCommand(schedulesListCmd(), schedulesGetCmd(), schedulesPutCmd(),
		scheduleActionCmd("pause", "Stop a schedule from running on its timetable"),
		scheduleActionCmd("resume", "Resume a paused schedule from its next slot"),
		schedulesTriggerCmd(), schedulesRunsCmd(), schedulesDeleteCmd())
	return cmd
}

func runsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "runs",
		Aliases: []string{"run", "r"},
		Short:   "Inspect, retry and cancel schedule runs",
	}
	addClientFlags(cmd)
	cmd.AddCommand(runsGetCmd(),
		runActionCmd("retry", "Deliver a run now; a finished or skipped run gets fresh attempts"),
		runActionCmd("cancel", "Cancel a pending or running run"))
	return cmd
}

func schedulesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List schedules with their next and last run",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.ListSchedulesWithResponse(cmd.Context())
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				if len(res.JSON200.Schedules) == 0 {
					fmt.Fprintln(w, "No schedules yet. Create one with `workly schedules put NAME URL --cron '0 3 * * *'`.")
					return
				}
				var rows [][]string
				for _, s := range res.JSON200.Schedules {
					next, last := "paused", "-"
					if s.NextRunAt != nil {
						next = ago(*s.NextRunAt)
					}
					if l := s.LastRun; l != nil {
						last = string(l.State) + " " + ago(l.ScheduledFor)
					}
					rows = append(rows, []string{s.Name, s.Cron, s.Timezone, next, last, s.Url})
				}
				table(w, "NAME\tCRON\tTIMEZONE\tNEXT RUN\tLAST RUN\tURL", rows)
			})
		},
	}
}

func schedulesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get NAME",
		Short: "Show a schedule's settings, next slots and recent runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.GetScheduleWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return output(cmd, res.Body, nil)
			}
			runs, err := c.ListRunsWithResponse(cmd.Context(), args[0], &client.ListRunsParams{Limit: ptr(10)})
			if err := check(runs, err); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			printSchedule(w, res.JSON200)
			if len(runs.JSON200.Runs) > 0 {
				fmt.Fprintln(w, "\nRecent runs:")
				printRuns(w, runs.JSON200.Runs)
			}
			return nil
		},
	}
}

func schedulesPutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "put NAME URL",
		Aliases: []string{"create", "update"},
		Short:   "Create or update a schedule that calls URL",
		Long: `Create or update a schedule that calls URL.

Declarative, like the API: the schedule ends up with exactly the settings
given, and settings you leave out are reset to their defaults. Pausing is
separate (see pause and resume). --payload and --body take a value, @file, or
- for stdin.

--cron takes five fields (minute hour day-of-month month day-of-week), or
@hourly, @daily, @weekly, @monthly or @yearly.`,
		Example: `  workly schedules put nightly-report https://api.example.com/reports/nightly \
      --cron '0 3 * * *' --timezone Europe/Stockholm -d '{"kind": "daily"}'
  workly schedules put sync https://api.example.com/sync --cron '*/15 * * * mon-fri' --overlap allow`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			r, err := readRequestFlags(cmd)
			if err != nil {
				return err
			}
			cfg := client.ScheduleConfig{
				Cron: must(f.GetString("cron")), Url: args[1],
				Payload: r.payload, Body: r.body, ContentType: r.contentType, Method: r.method, Headers: r.headers,
			}
			setIf(f.Changed("timezone"), &cfg.Timezone, must(f.GetString("timezone")))
			setIf(f.Changed("timeout"), &cfg.TimeoutSeconds, must(f.GetDuration("timeout")).Seconds())
			setIf(f.Changed("max-attempts"), &cfg.MaxAttempts, must(f.GetInt("max-attempts")))
			setIf(f.Changed("min-backoff"), &cfg.MinBackoffSeconds, must(f.GetDuration("min-backoff")).Seconds())
			setIf(f.Changed("max-backoff"), &cfg.MaxBackoffSeconds, must(f.GetDuration("max-backoff")).Seconds())
			setIf(f.Changed("backoff-multiplier"), &cfg.BackoffMultiplier, must(f.GetFloat64("backoff-multiplier")))
			setIf(f.Changed("retry-on-4xx"), &cfg.RetryOn4xx, must(f.GetBool("retry-on-4xx")))
			setIf(f.Changed("overlap"), &cfg.Overlap, client.OverlapPolicy(must(f.GetString("overlap"))))
			setIf(f.Changed("missed"), &cfg.Missed, client.MissedPolicy(must(f.GetString("missed"))))

			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.PutScheduleWithResponse(cmd.Context(), args[0], cfg)
			if err := check(res, err); err != nil {
				return err
			}
			s := res.JSON200
			if s == nil {
				s = res.JSON201
			}
			return output(cmd, res.Body, func(w io.Writer) { printSchedule(w, s) })
		},
	}
	f := cmd.Flags()
	f.String("cron", "", "when to run, e.g. '0 3 * * *' or @hourly (required)")
	f.String("timezone", "UTC", "IANA timezone the cron expression is read in, e.g. Europe/Stockholm")
	addRequestFlags(cmd, `header sent with every run, "Name: value" (repeatable)`)
	f.Duration("timeout", 30*time.Second, "how long to wait for a response")
	f.Int("max-attempts", 3, "total delivery attempts before a run fails")
	f.Duration("min-backoff", 5*time.Second, "shortest wait before a retry")
	f.Duration("max-backoff", 5*time.Minute, "longest wait before a retry")
	f.Float64("backoff-multiplier", 2, "how fast the wait between retries grows")
	f.Bool("retry-on-4xx", false, "retry 4xx responses (by default only 408 and 429 are retried)")
	f.String("overlap", "skip", "a slot that comes while the previous run is still going: skip or allow")
	f.String("missed", "skip", "slots missed while the server was down: skip, or catch_up (run the latest once)")
	_ = cmd.MarkFlagRequired("cron")
	return cmd
}

// scheduleActionCmd is pause or resume: POST /v1/schedules/{name}/{action}.
func scheduleActionCmd(action, short string) *cobra.Command {
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
				s    *client.Schedule
			)
			if action == "pause" {
				res, err := c.PauseScheduleWithResponse(cmd.Context(), args[0])
				if err := check(res, err); err != nil {
					return err
				}
				body, s = res.Body, res.JSON200
			} else {
				res, err := c.ResumeScheduleWithResponse(cmd.Context(), args[0])
				if err := check(res, err); err != nil {
					return err
				}
				body, s = res.Body, res.JSON200
			}
			return output(cmd, body, func(w io.Writer) {
				fmt.Fprintf(w, "Schedule %s %sd.", s.Name, action)
				if s.NextRunAt != nil {
					fmt.Fprintf(w, " Next run %s.", ago(*s.NextRunAt))
				}
				fmt.Fprintln(w)
			})
		},
	}
}

func schedulesTriggerCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "trigger NAME",
		Aliases: []string{"run"},
		Short:   "Run a schedule now and print the run's ID",
		Long: `Run a schedule now, outside its timetable, and print the run's ID.

The run is delivered right away, even if the schedule is paused or a run is in
flight. It doesn't change when the schedule next runs.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.TriggerScheduleWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			// Just the ID, so scripts can capture it.
			return output(cmd, res.Body, func(w io.Writer) { fmt.Fprintln(w, res.JSON201.Id) })
		},
	}
}

func schedulesRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "runs NAME",
		Short:   "List a schedule's runs, newest first",
		Example: `  workly schedules runs nightly-report --state failed`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			params := client.ListRunsParams{Limit: ptr(must(f.GetInt("limit")))}
			setIf(f.Changed("state"), &params.State, client.RunState(must(f.GetString("state"))))
			setIf(f.Changed("cursor"), &params.Cursor, must(f.GetString("cursor")))
			res, err := c.ListRunsWithResponse(cmd.Context(), args[0], &params)
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				page := res.JSON200
				if len(page.Runs) == 0 {
					fmt.Fprintln(w, "No runs.")
					return
				}
				printRuns(w, page.Runs)
				if page.NextCursor != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "\nMore runs: add --cursor %s\n", *page.NextCursor)
				}
			})
		},
	}
	f := cmd.Flags()
	f.String("state", "", "only runs in this state: pending, running, succeeded, failed, cancelled or skipped")
	f.Int("limit", 20, "runs per page (at most 200)")
	f.String("cursor", "", "continue from a previous page")
	return cmd
}

func schedulesDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete NAME",
		Aliases: []string{"rm"},
		Short:   "Delete a schedule and all its runs",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirm(cmd, fmt.Sprintf("Delete schedule %s and all its runs?", args[0])); err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.DeleteScheduleWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); !asJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "Schedule %s deleted.\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "don't ask for confirmation")
	return cmd
}

func runsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get ID",
		Short: "Show a run and its delivery attempts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.GetRunWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return output(cmd, res.Body, nil)
			}
			attempts, err := c.ListRunAttemptsWithResponse(cmd.Context(), args[0])
			if err := check(attempts, err); err != nil {
				return err
			}
			printRun(cmd.OutOrStdout(), res.JSON200, attempts.JSON200.Attempts)
			return nil
		},
	}
}

// runActionCmd is retry or cancel: POST /v1/runs/{id}/{action}.
func runActionCmd(action, short string) *cobra.Command {
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
				run  *client.Run
			)
			if action == "retry" {
				res, err := c.RetryRunWithResponse(cmd.Context(), args[0])
				if err := check(res, err); err != nil {
					return err
				}
				body, run = res.Body, res.JSON200
			} else {
				res, err := c.CancelRunWithResponse(cmd.Context(), args[0])
				if err := check(res, err); err != nil {
					return err
				}
				body, run = res.Body, res.JSON200
			}
			return output(cmd, body, func(w io.Writer) {
				fmt.Fprintf(w, "Run %s is %s.\n", run.Id, run.State)
			})
		},
	}
}

func printSchedule(w io.Writer, s *client.Schedule) {
	status := "active"
	if s.Paused {
		status = "paused"
	}
	rows := [][]string{
		{"Name", s.Name},
		{"Status", status},
		{"Cron", s.Cron + " (" + s.Timezone + ")"},
		{"Request", string(s.Method) + " " + s.Url},
		{"Timeout", seconds(s.TimeoutSeconds)},
		{"Max attempts", strconv.Itoa(s.MaxAttempts)},
		{"Backoff", fmt.Sprintf("%s to %s, x%g", seconds(s.MinBackoffSeconds), seconds(s.MaxBackoffSeconds), s.BackoffMultiplier)},
		{"Retry on 4xx", strconv.FormatBool(s.RetryOn4xx)},
		{"Overlap", string(s.Overlap)},
		{"Missed", string(s.Missed)},
	}
	for i, t := range s.NextRuns {
		label := ""
		if i == 0 {
			label = "Next runs"
			if s.Paused {
				label = "Next runs if resumed"
			}
		}
		rows = append(rows, []string{label, t.Local().Format(time.DateTime) + " (" + ago(t) + ")"})
	}
	for _, r := range rows {
		if r[0] != "" {
			r[0] += ":"
		}
	}
	table(w, "", rows)
	printBody(w, s.Payload, s.Body)
}

func printRuns(w io.Writer, runs []client.Run) {
	var rows [][]string
	for _, r := range runs {
		state := string(r.State)
		if r.SkipReason != nil {
			state += " (" + string(*r.SkipReason) + ")"
		}
		rows = append(rows, []string{r.Id, state, string(r.Trigger), r.ScheduledFor.Local().Format(time.DateTime),
			strconv.Itoa(r.AttemptCount), deref(r.LastStatus, "-"), deref(r.LastError, "")})
	}
	table(w, "ID\tSTATE\tTRIGGER\tSCHEDULED FOR\tATTEMPTS\tSTATUS\tERROR", rows)
}

func printRun(w io.Writer, r *client.Run, attempts []client.Attempt) {
	rows := [][]string{
		{"ID", r.Id},
		{"Schedule", r.Schedule},
		{"State", string(r.State)},
	}
	if r.SkipReason != nil {
		rows = append(rows, []string{"Skipped because", map[client.SkipReason]string{
			client.Overlap: "the previous run was still going",
			client.Missed:  "the server was down at this time",
		}[*r.SkipReason]})
	}
	rows = append(rows,
		[]string{"Trigger", string(r.Trigger)},
		[]string{"Scheduled for", r.ScheduledFor.Local().Format(time.DateTime) + " (" + ago(r.ScheduledFor) + ")"},
		[]string{"Request", string(r.Method) + " " + r.Url})
	if r.State == client.RunStatePending {
		rows = append(rows, []string{"Runs", ago(r.RunAt)})
	}
	rows = append(rows, []string{"Attempts", strconv.Itoa(r.AttemptCount)})
	if r.LastError != nil {
		rows = append(rows, []string{"Last error", *r.LastError})
	}
	if r.FinishedAt != nil {
		rows = append(rows, []string{"Finished", r.FinishedAt.Local().Format(time.DateTime) + " (" + ago(*r.FinishedAt) + ")"})
	}
	for _, row := range rows {
		row[0] += ":"
	}
	table(w, "", rows)
	printBody(w, r.Payload, r.Body)
	printAttempts(w, attempts)
}

func printBody(w io.Writer, payload *json.RawMessage, body *string) {
	switch {
	case payload != nil:
		var buf bytes.Buffer
		if json.Indent(&buf, *payload, "  ", "  ") != nil {
			buf.Write(*payload)
		}
		fmt.Fprintf(w, "\nPayload:\n  %s\n", buf.Bytes())
	case body != nil:
		fmt.Fprintf(w, "\nBody:\n  %s\n", *body)
	}
}
