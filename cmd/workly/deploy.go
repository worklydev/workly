package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strconv"
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
	return loadPath(path)
}

func loadPath(path string) (*deploy.File, error) {
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
set in the environment. It lists the ${NAME:-default} references that took
their default.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := loadFile(cmd)
			if err != nil {
				return err
			}
			counts := []string{fmt.Sprintf("%d %s", len(f.Queues), plural(len(f.Queues), "queue"))}
			if f.HasWorkflows {
				counts = append(counts, fmt.Sprintf("%d %s", len(f.Workflows), plural(len(f.Workflows), "workflow")))
			}
			counts = append(counts, fmt.Sprintf("%d %s", len(f.Schedules), plural(len(f.Schedules), "schedule")))
			fmt.Fprintf(cmd.OutOrStdout(), "%s is valid: %s.\n", f.Path, strings.Join(counts, ", "))
			printDefaults(cmd.OutOrStdout(), f)
			return nil
		},
	}
	addFileFlag(cmd)
	return cmd
}

// Exit statuses of plan --detailed-exitcode, as Terraform's: 1 stays an
// error.
const (
	exitChanges = 2
	exitDeletes = 3
)

func planCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show what workly deploy would change",
		Long: `Show what workly deploy would change, without changing anything.

--format markdown writes the plan for a pull request comment or GitHub's
step summary ($GITHUB_STEP_SUMMARY). --detailed-exitcode exits 0 when
there's nothing to change, 2 when there are changes, and 3 when they
include deletes, so CI can ask for a closer look; errors exit 1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, _ := cmd.Flags().GetString("format")
			if format != "text" && format != "markdown" {
				return fmt.Errorf("--format must be text or markdown, not %q", format)
			}
			f, _, plan, err := makePlan(cmd)
			if err != nil {
				return err
			}
			if format == "markdown" {
				printPlanMarkdown(cmd.OutOrStdout(), cmd, f, plan)
			} else {
				printPlan(cmd.OutOrStdout(), cmd, f, plan)
			}
			if detailed, _ := cmd.Flags().GetBool("detailed-exitcode"); detailed {
				switch {
				case plan.Count(deploy.Delete) > 0:
					return exitCode(exitDeletes)
				case plan.Count(deploy.Create)+plan.Count(deploy.Update) > 0:
					return exitCode(exitChanges)
				}
			}
			return nil
		},
	}
	addFileFlag(cmd)
	addServerFlags(cmd.Flags())
	cmd.Flags().String("format", "text", "text, or markdown for a pull request or GitHub's step summary")
	cmd.Flags().Bool("detailed-exitcode", false, "exit 2 when there are changes, 3 when they include deletes")
	return cmd
}

func deployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Create and update the queues, workflows and schedules in workly.yaml",
		Long: `Create and update the queues, workflows and schedules in workly.yaml, so
the server matches it. Shows the plan and asks first; --yes skips the
question, as CI needs. Running it again changes nothing. It never pauses or
resumes anything, and leaves what the file doesn't declare alone, unless
the file sets prune: true: then it deletes the queues, workflows and
schedules a section of the file leaves out. Workflow runs already going
keep the definition they started with.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, c, plan, err := makePlan(cmd)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			printPlan(w, cmd, f, plan)
			todo := plan.Count(deploy.Create) + plan.Count(deploy.Update) + plan.Count(deploy.Delete)
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
				if err := apply(cmd.Context(), c, ch); err != nil {
					return fmt.Errorf("%s %s: %w\n%d of %d changes applied", ch.Kind, ch.Name, err, done, todo)
				}
				done++
				verb := map[deploy.Action]string{deploy.Create: "Created", deploy.Update: "Updated", deploy.Delete: "Deleted"}[ch.Action]
				fmt.Fprintf(w, "%s %s %s\n", verb, ch.Kind, ch.Name)
			}
			fmt.Fprintf(w, "\nDeployed %s: %s.\n", f.Path, summary(plan, false))
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
	plan, err := planWith(cmd.Context(), c, f)
	return f, c, plan, err
}

// planWith compares the file with the server c talks to.
func planWith(ctx context.Context, c *client.ClientWithResponses, f *deploy.File) (deploy.Plan, error) {
	var queues api.QueueList
	var schedules api.ScheduleList
	var workflows api.WorkflowList
	if f.HasWorkflows {
		res, err := c.ListWorkflowsWithResponse(ctx)
		if err := check(res, err); err != nil {
			return deploy.Plan{}, err
		}
		if err := json.Unmarshal(res.Body, &workflows); err != nil {
			return deploy.Plan{}, err
		}
	}
	if f.HasQueues {
		res, err := c.ListQueuesWithResponse(ctx)
		if err := check(res, err); err != nil {
			return deploy.Plan{}, err
		}
		if err := json.Unmarshal(res.Body, &queues); err != nil {
			return deploy.Plan{}, err
		}
	}
	if f.HasSchedules {
		res, err := c.ListSchedulesWithResponse(ctx)
		if err := check(res, err); err != nil {
			return deploy.Plan{}, err
		}
		if err := json.Unmarshal(res.Body, &schedules); err != nil {
			return deploy.Plan{}, err
		}
	}
	return f.Plan(queues.Queues, workflows.Workflows, schedules.Schedules)
}

func printPlan(w io.Writer, cmd *cobra.Command, f *deploy.File, plan deploy.Plan) {
	creates, updates, deletes := plan.Count(deploy.Create), plan.Count(deploy.Update), plan.Count(deploy.Delete)
	if creates+updates+deletes == 0 {
		same := plan.Count(deploy.Unchanged)
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
			case deploy.Delete:
				fmt.Fprintf(w, "  - delete %s %s%s\n", ch.Kind, ch.Name, failedTasks(ch))
			}
		}
		fmt.Fprintf(w, "\n%s.\n", summary(plan, true))
	}
	printDefaults(w, f)
	if len(plan.UndeclaredWorkflows) > 0 {
		fmt.Fprintf(w, "\nNot in %s, left as they are: %s %s.\n", f.Path,
			plural(len(plan.UndeclaredWorkflows), "workflow"), strings.Join(plan.UndeclaredWorkflows, ", "))
	}
	if len(plan.Undeclared) > 0 {
		fmt.Fprintf(w, "\nNot in %s, left as they are: %s %s.\n", f.Path,
			plural(len(plan.Undeclared), "schedule"), strings.Join(plan.Undeclared, ", "))
	}
}

// printPlanMarkdown writes the plan as GitHub-flavoured Markdown. Deletes
// come first, in a warning, since they're what a reviewer must not miss.
func printPlanMarkdown(w io.Writer, cmd *cobra.Command, f *deploy.File, plan deploy.Plan) {
	creates, updates, deletes := plan.Count(deploy.Create), plan.Count(deploy.Update), plan.Count(deploy.Delete)
	fmt.Fprintf(w, "### Workly plan\n\n")
	if creates+updates+deletes == 0 {
		fmt.Fprintf(w, "No changes: %s matches %s (%d unchanged).\n",
			mdCode(serverURL(cmd)), mdCode(f.Path), plan.Count(deploy.Unchanged))
	} else {
		fmt.Fprintf(w, "%s on %s: %s.\n", mdCode(f.Path), mdCode(serverURL(cmd)), summary(plan, true))
		if deletes > 0 {
			fmt.Fprintf(w, "\n> [!WARNING]\n> Deploying this deletes these, with their tasks and runs. It can't be undone.\n")
			for _, ch := range plan.Changes {
				if ch.Action == deploy.Delete {
					fmt.Fprintf(w, "> - %s %s%s\n", ch.Kind, mdCode(ch.Name), failedTasks(ch))
				}
			}
		}
		fmt.Fprintln(w)
		for _, ch := range plan.Changes {
			switch ch.Action {
			case deploy.Create:
				fmt.Fprintf(w, "- **create** %s %s\n", ch.Kind, mdCode(ch.Name))
			case deploy.Update:
				paused := ""
				if ch.Paused {
					paused = " (paused, stays paused)"
				}
				fmt.Fprintf(w, "- **update** %s %s%s\n", ch.Kind, mdCode(ch.Name), paused)
				for _, d := range ch.Diffs {
					fmt.Fprintf(w, "  - %s: %s\n", d.Field, mdChange(d.Change))
				}
			case deploy.Delete:
				fmt.Fprintf(w, "- **delete** %s %s%s\n", ch.Kind, mdCode(ch.Name), failedTasks(ch))
			}
		}
	}
	if len(f.Defaults) > 0 {
		var buf strings.Builder
		printDefaults(&buf, f)
		fmt.Fprintf(w, "\n<details><summary>Defaults used</summary>\n\n```%s```\n</details>\n", buf.String())
	}
	for _, u := range []struct {
		names []string
		kind  string
	}{{plan.UndeclaredWorkflows, "workflow"}, {plan.Undeclared, "schedule"}} {
		if len(u.names) > 0 {
			quoted := make([]string, len(u.names))
			for i, n := range u.names {
				quoted[i] = mdCode(n)
			}
			fmt.Fprintf(w, "\nNot in %s, left as they are: %s %s.\n", mdCode(f.Path), plural(len(u.names), u.kind), strings.Join(quoted, ", "))
		}
	}
}

func failedTasks(ch deploy.Change) string {
	if ch.Failed == 0 {
		return ""
	}
	return fmt.Sprintf(" (and its %d failed %s)", ch.Failed, plural(ch.Failed, "task"))
}

// mdChange formats a Diff's change: "old → new" as two code spans, and the
// words that stand in for secret values ("changed") as they are.
func mdChange(change string) string {
	if before, after, ok := strings.Cut(change, " → "); ok {
		return mdCode(before) + " → " + mdCode(after)
	}
	return change
}

// mdCode puts s in a code span, with enough backticks that none in s ends
// it early.
func mdCode(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// summary counts a plan's changes: "1 to create, 2 to update, 3 unchanged"
// before applying it, "1 created, 2 updated" after. Deletes are left out
// unless the plan has some, so files without prune read as they always have.
func summary(plan deploy.Plan, planned bool) string {
	type count struct {
		a             deploy.Action
		before, after string
	}
	var parts []string
	for _, c := range []count{
		{deploy.Create, "to create", "created"},
		{deploy.Update, "to update", "updated"},
		{deploy.Delete, "to delete", "deleted"},
		{deploy.Unchanged, "unchanged", "unchanged"},
	} {
		n := plan.Count(c.a)
		if c.a == deploy.Delete && n == 0 || c.a == deploy.Unchanged && !planned {
			continue
		}
		word := c.after
		if planned {
			word = c.before
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, word))
	}
	return strings.Join(parts, ", ")
}

func apply(ctx context.Context, c *client.ClientWithResponses, ch deploy.Change) error {
	if ch.Action == deploy.Delete {
		switch ch.Kind {
		case "queue":
			res, err := c.DeleteQueueWithResponse(ctx, ch.Name)
			return check(res, err)
		case "workflow":
			res, err := c.DeleteWorkflowWithResponse(ctx, ch.Name)
			return check(res, err)
		}
		res, err := c.DeleteScheduleWithResponse(ctx, ch.Name)
		return check(res, err)
	}
	body, err := json.Marshal(ch.Body)
	if err != nil {
		return err
	}
	switch ch.Kind {
	case "queue":
		res, err := c.PutQueueWithBodyWithResponse(ctx, ch.Name, "application/json", bytes.NewReader(body))
		return check(res, err)
	case "workflow":
		res, err := c.PutWorkflowWithBodyWithResponse(ctx, ch.Name, "application/json", bytes.NewReader(body))
		return check(res, err)
	}
	res, err := c.PutScheduleWithBodyWithResponse(ctx, ch.Name, "application/json", bytes.NewReader(body))
	return check(res, err)
}

// printDefaults lists the variables that weren't set, so their defaults
// were used, with the fields that used them: in CI, a missing variable
// would otherwise deploy a local default unnoticed.
func printDefaults(w io.Writer, f *deploy.File) {
	if len(f.Defaults) == 0 {
		return
	}
	var names []string
	byName := map[string][]deploy.Default{}
	for _, d := range f.Defaults {
		if byName[d.Name] == nil {
			names = append(names, d.Name)
		}
		byName[d.Name] = append(byName[d.Name], d)
	}
	fmt.Fprintf(w, "\nNot set, so their defaults were used:\n")
	for _, name := range names {
		var fields, values []string
		for _, d := range byName[name] {
			fields = append(fields, d.Field)
			if !slices.Contains(values, d.Value) {
				values = append(values, d.Value)
			}
		}
		fmt.Fprintf(w, "  %s: %s (%s)\n", name, strings.Join(quoteAll(values), ", "), strings.Join(fields, ", "))
	}
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strconv.Quote(s)
	}
	return out
}
