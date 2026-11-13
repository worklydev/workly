package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/worklydev/workly/internal/client"
)

func workflowsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workflows",
		Aliases: []string{"workflow", "wf"},
		Short:   "Start and inspect workflows: DAGs of HTTP steps",
		Long: `A workflow runs HTTP steps in dependency order, each delivered as a task. A
step's endpoint can hand its completion to a task group by answering with
Workly-Wait-For: group/<id>. Define workflows in workly.yaml and apply them
with workly deploy.`,
	}
	addClientFlags(cmd)
	cmd.AddCommand(workflowsListCmd(), workflowsGetCmd(), workflowsDeleteCmd(), workflowsTriggerCmd(),
		workflowRunsCmd(), workflowRunCmd(), workflowCancelCmd(), workflowRetryCmd())
	return cmd
}

func workflowsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List workflows and how each last ran",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.ListWorkflowsWithResponse(cmd.Context())
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				if len(res.JSON200.Workflows) == 0 {
					fmt.Fprintln(w, "No workflows yet. Declare one in workly.yaml and run workly deploy.")
					return
				}
				var rows [][]string
				for _, wf := range res.JSON200.Workflows {
					last := "-"
					if r := wf.LastRun; r != nil {
						last = string(r.Status) + " " + ago(r.CreatedAt) + " (" + r.Id + ")"
					}
					rows = append(rows, []string{wf.Name, strconv.Itoa(len(wf.Steps)), "v" + strconv.Itoa(wf.Version), last})
				}
				table(w, "NAME\tSTEPS\tVERSION\tLAST RUN", rows)
			})
		},
	}
}

func workflowsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get NAME",
		Short: "Show a workflow's steps and their dependencies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.GetWorkflowWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				wf := res.JSON200
				rows := [][]string{
					{"Name:", wf.Name},
					{"Version:", strconv.Itoa(wf.Version)},
					{"Max duration:", short(time.Duration(wf.MaxDurationSeconds * float64(time.Second)))},
				}
				if r := wf.LastRun; r != nil {
					rows = append(rows, []string{"Last run:", r.Id + " " + string(r.Status) + " " + ago(r.CreatedAt)})
				}
				table(w, "", rows)
				fmt.Fprintln(w)
				var steps [][]string
				for _, s := range wf.Steps {
					after := "-"
					if s.DependsOn != nil && len(*s.DependsOn) > 0 {
						after = strings.Join(*s.DependsOn, ", ")
					}
					steps = append(steps, []string{s.Name, after, deref(s.Queue, "_workflows"), string(deref(s.Method, "POST")) + " " + s.Url})
				}
				table(w, "STEP\tAFTER\tQUEUE\tREQUEST", steps)
			})
		},
	}
}

func workflowsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Delete a workflow's definition; its runs are kept",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirm(cmd, fmt.Sprintf("Delete workflow %s? Runs still going finish under the definition they started with.", args[0])); err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.DeleteWorkflowWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted workflow %s.\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "delete without asking")
	return cmd
}

func workflowsTriggerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "trigger NAME",
		Aliases: []string{"start"},
		Short:   "Start a run of a workflow and print its ID",
		Long: `Start a run of a workflow and print its ID. --input is JSON passed to every
step without a body of its own; it takes a value, @file, or - for stdin.`,
		Example: `  workly workflows trigger daily-refresh --input '{"date": "2026-11-13"}'
  run=$(workly workflows trigger daily-refresh -k 2026-11-13) && workly workflows run $run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			var req client.StartWorkflowRun
			if f.Changed("input") {
				data, err := readArg(cmd, must(f.GetString("input")))
				if err != nil {
					return err
				}
				if !json.Valid(data) {
					return fmt.Errorf("--input must be JSON")
				}
				raw := json.RawMessage(data)
				req.Input = &raw
			}
			setIf(f.Changed("idempotency-key"), &req.IdempotencyKey, must(f.GetString("idempotency-key")))
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.StartWorkflowRunWithResponse(cmd.Context(), args[0], req)
			if err := check(res, err); err != nil {
				return err
			}
			r := res.JSON201
			if r == nil {
				r = res.JSON200
				fmt.Fprintln(cmd.ErrOrStderr(), "A run with this idempotency key already exists; nothing was started.")
			}
			// Just the ID, so scripts can capture it.
			return output(cmd, res.Body, func(w io.Writer) { fmt.Fprintln(w, r.Id) })
		},
	}
	f := cmd.Flags()
	f.StringP("input", "i", "", "JSON input for the run: a value, @file or - for stdin")
	f.StringP("idempotency-key", "k", "", "return the existing run with this key instead of starting one")
	return cmd
}

func workflowRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs [NAME]",
		Short: "List workflow runs, newest first",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var params client.ListWorkflowRunsParams
			if len(args) == 1 {
				params.Workflow = &args[0]
			}
			setIf(f.Changed("status"), &params.Status, client.WorkflowRunStatus(must(f.GetString("status"))))
			setIf(f.Changed("cursor"), &params.Cursor, must(f.GetString("cursor")))
			setIf(true, &params.Limit, must(f.GetInt("limit")))
			res, err := c.ListWorkflowRunsWithResponse(cmd.Context(), &params)
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				page := res.JSON200
				if len(page.WorkflowRuns) == 0 {
					fmt.Fprintln(w, "No runs.")
					return
				}
				var rows [][]string
				for _, r := range page.WorkflowRuns {
					took := "-"
					if r.DurationMs != nil {
						took = short(time.Duration(*r.DurationMs) * time.Millisecond)
					}
					rows = append(rows, []string{r.Id, r.Workflow, string(r.Status), ago(r.CreatedAt), took, deref(r.Error, "")})
				}
				table(w, "ID\tWORKFLOW\tSTATUS\tSTARTED\tTOOK\tERROR", rows)
				if page.NextCursor != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "\nMore runs: add --cursor %s\n", *page.NextCursor)
				}
			})
		},
	}
	f := cmd.Flags()
	f.String("status", "", "only runs in this status: running, succeeded, failed or cancelled")
	f.Int("limit", 20, "runs per page (at most 200)")
	f.String("cursor", "", "continue from a previous page")
	return cmd
}

func workflowRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run ID",
		Short: "Show a workflow run: each step, and why it is where it is",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.GetWorkflowRunWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) { printWorkflowRun(w, res.JSON200) })
		},
	}
}

func workflowCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel ID",
		Short: "Cancel a workflow run: steps not yet started are skipped",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.CancelWorkflowRunWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) { printWorkflowRun(w, res.JSON200) })
		},
	}
}

func workflowRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry ID",
		Short: "Retry a failed or cancelled workflow run: its failed and skipped steps run again",
		Long: `Reopen a failed or cancelled workflow run, under the definition it
started with and with the same input. Steps that succeeded stay; the rest
run again, each on its old task with a fresh set of attempts, and a step
that was waiting for a group follows the group.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.RetryWorkflowRunWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) { printWorkflowRun(w, res.JSON200) })
		},
	}
}

func printWorkflowRun(w io.Writer, r *client.WorkflowRun) {
	rows := [][]string{
		{"ID:", r.Id},
		{"Workflow:", r.Workflow + " (v" + strconv.Itoa(r.Version) + ")"},
		{"Status:", string(r.Status)},
		{"Started:", r.CreatedAt.Local().Format(time.DateTime) + " (" + ago(r.CreatedAt) + ")"},
	}
	if r.DurationMs != nil {
		rows = append(rows, []string{"Took:", short(time.Duration(*r.DurationMs) * time.Millisecond)})
	}
	if r.Error != nil {
		rows = append(rows, []string{"Error:", *r.Error})
	}
	if r.ScheduleRunId != nil {
		rows = append(rows, []string{"Started by:", "schedule run " + *r.ScheduleRunId})
	}
	if r.Attempt > 1 {
		retried := ""
		if r.RetriedAt != nil {
			retried = ", last retried " + ago(*r.RetriedAt)
		}
		rows = append(rows, []string{"Attempt:", strconv.Itoa(r.Attempt) + retried})
	}
	table(w, "", rows)
	fmt.Fprintln(w)
	var steps [][]string
	for _, s := range r.Steps {
		took := "-"
		if s.DurationMs != nil {
			took = short(time.Duration(*s.DurationMs) * time.Millisecond)
		}
		steps = append(steps, []string{s.Name, string(s.Status), took, deref(s.TaskId, "-"), stepWhy(s)})
	}
	table(w, "STEP\tSTATUS\tTOOK\tTASK\tDETAIL", steps)
}

// stepWhy says why a step is where it is.
func stepWhy(s client.WorkflowStep) string {
	switch s.Status {
	case client.StepStatusBlocked:
		return "waiting for " + strings.Join(s.BlockedBy, ", ")
	case client.StepStatusWaiting:
		if s.WaitFor != nil {
			return fmt.Sprintf("waiting for %s %s (workly groups get %s)", s.WaitFor.Type, s.WaitFor.Id, s.WaitFor.Id)
		}
	case client.StepStatusPending, client.StepStatusRunning:
		if t := s.Task; t != nil && t.AttemptCount > 0 {
			why := fmt.Sprintf("attempt %d", t.AttemptCount)
			if t.LastError != nil {
				why += " after " + *t.LastError
			}
			if t.RunAt != nil {
				why += ", retrying " + ago(*t.RunAt)
			}
			return why
		}
		if s.Status == client.StepStatusPending {
			return "queued"
		}
	case client.StepStatusSucceeded:
		if s.WaitFor != nil {
			return fmt.Sprintf("after %s %s completed", s.WaitFor.Type, s.WaitFor.Id)
		}
	}
	return deref(s.Error, "")
}
