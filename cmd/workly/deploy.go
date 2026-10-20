package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/worklydev/workly/internal/api"
	"github.com/worklydev/workly/internal/client"
	"github.com/worklydev/workly/internal/deploy"
)

func addFileFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("file", "f", "workly.yaml", "the configuration file")
}

func loadFile(cmd *cobra.Command) (*deploy.File, error) {
	path, _ := cmd.Flags().GetString("file")
	f, err := deploy.Load(path, os.LookupEnv)
	var invalid *deploy.InvalidError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s not found: create it (see https://workly.run/docs/deploy/) or pass --file", path)
	case errors.As(err, &invalid):
		return nil, fmt.Errorf("%s is invalid:\n  %s", path, strings.ReplaceAll(err.Error(), "\n", "\n  "))
	}
	return f, err
}

func validateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check workly.yaml, without contacting the server",
		Long: `Check workly.yaml, without contacting the server: its fields, cron
expressions, timezones, URLs and headers, and that every ${NAME} it uses is
set in the environment.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := loadFile(cmd)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is valid: %d %s, %d %s.\n", f.Path,
				len(f.Queues), plural(len(f.Queues), "queue"), len(f.Schedules), plural(len(f.Schedules), "schedule"))
			return nil
		},
	}
	addFileFlag(cmd)
	return cmd
}

func planCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show what workly deploy would change",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, _, plan, err := makePlan(cmd)
			if err != nil {
				return err
			}
			printPlan(cmd.OutOrStdout(), cmd, f, plan)
			return nil
		},
	}
	addFileFlag(cmd)
	addServerFlags(cmd.Flags())
	return cmd
}

func deployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Create and update the queues and schedules in workly.yaml",
		Long: `Create and update the queues and schedules in workly.yaml, so the server
matches it. Shows the plan and asks first; --yes skips the question, as CI
needs. Running it again changes nothing. It never deletes, pauses or resumes
anything, and leaves queues and schedules the file doesn't declare alone.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, c, plan, err := makePlan(cmd)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			printPlan(w, cmd, f, plan)
			todo := plan.Count(deploy.Create) + plan.Count(deploy.Update)
			if todo == 0 {
				return nil
			}
			if err := confirm(cmd, fmt.Sprintf("Apply these changes to %s?", serverURL(cmd))); err != nil {
				return err
			}
			fmt.Fprintln(w)
			done := 0
			for _, ch := range plan.Changes {
				if ch.Action == deploy.Unchanged {
					continue
				}
				if err := apply(cmd, c, ch); err != nil {
					return fmt.Errorf("%s %s: %w\n%d of %d changes applied", ch.Kind, ch.Name, err, done, todo)
				}
				done++
				verb := "Created"
				if ch.Action == deploy.Update {
					verb = "Updated"
				}
				fmt.Fprintf(w, "%s %s %s\n", verb, ch.Kind, ch.Name)
			}
			fmt.Fprintf(w, "\nDeployed %s: %d created, %d updated.\n", f.Path, plan.Count(deploy.Create), plan.Count(deploy.Update))
			return nil
		},
	}
	addFileFlag(cmd)
	addServerFlags(cmd.Flags())
	cmd.Flags().BoolP("yes", "y", false, "apply without asking")
	return cmd
}

// makePlan loads the file, then the server's queues and schedules.
func makePlan(cmd *cobra.Command) (*deploy.File, *client.ClientWithResponses, deploy.Plan, error) {
	f, err := loadFile(cmd)
	if err != nil {
		return nil, nil, deploy.Plan{}, err
	}
	c, err := newClient(cmd)
	if err != nil {
		return nil, nil, deploy.Plan{}, err
	}
	ctx := cmd.Context()
	var queues api.QueueList
	var schedules api.ScheduleList
	if f.HasQueues {
		res, err := c.ListQueuesWithResponse(ctx)
		if err := check(res, err); err != nil {
			return nil, nil, deploy.Plan{}, err
		}
		if err := json.Unmarshal(res.Body, &queues); err != nil {
			return nil, nil, deploy.Plan{}, err
		}
	}
	if f.HasSchedules {
		res, err := c.ListSchedulesWithResponse(ctx)
		if err := check(res, err); err != nil {
			return nil, nil, deploy.Plan{}, err
		}
		if err := json.Unmarshal(res.Body, &schedules); err != nil {
			return nil, nil, deploy.Plan{}, err
		}
	}
	plan, err := f.Plan(queues.Queues, schedules.Schedules)
	return f, c, plan, err
}

func printPlan(w io.Writer, cmd *cobra.Command, f *deploy.File, plan deploy.Plan) {
	creates, updates, same := plan.Count(deploy.Create), plan.Count(deploy.Update), plan.Count(deploy.Unchanged)
	if creates+updates == 0 {
		fmt.Fprintf(w, "No changes: %s matches %s (%d unchanged).\n", serverURL(cmd), f.Path, same)
	} else {
		fmt.Fprintf(w, "Plan for %s on %s:\n\n", f.Path, serverURL(cmd))
		for _, ch := range plan.Changes {
			paused := ""
			if ch.Paused {
				paused = " (paused, stays paused)"
			}
			switch ch.Action {
			case deploy.Create:
				fmt.Fprintf(w, "  + create %s %s\n", ch.Kind, ch.Name)
			case deploy.Update:
				fmt.Fprintf(w, "  ~ update %s %s%s\n", ch.Kind, ch.Name, paused)
				for _, d := range ch.Diffs {
					fmt.Fprintf(w, "      %s: %s\n", d.Field, d.Change)
				}
			}
		}
		fmt.Fprintf(w, "\n%d to create, %d to update, %d unchanged.\n", creates, updates, same)
	}
	if len(plan.Undeclared) > 0 {
		fmt.Fprintf(w, "\nNot in %s, left as they are: %s %s.\n", f.Path,
			plural(len(plan.Undeclared), "schedule"), strings.Join(plan.Undeclared, ", "))
	}
}

func apply(cmd *cobra.Command, c *client.ClientWithResponses, ch deploy.Change) error {
	body, err := json.Marshal(ch.Body)
	if err != nil {
		return err
	}
	if ch.Kind == "queue" {
		res, err := c.PutQueueWithBodyWithResponse(cmd.Context(), ch.Name, "application/json", bytes.NewReader(body))
		return check(res, err)
	}
	res, err := c.PutScheduleWithBodyWithResponse(cmd.Context(), ch.Name, "application/json", bytes.NewReader(body))
	return check(res, err)
}
