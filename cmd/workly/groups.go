package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/worklydev/workly/internal/client"
)

func groupsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "groups",
		Aliases: []string{"group", "g"},
		Short:   "Create, close and inspect task groups",
		Long: `A task group tracks a set of tasks and enqueues a callback task once it is
closed and all of them have finished. Enqueue into a group with
workly tasks enqueue --group.`,
	}
	addClientFlags(cmd)
	cmd.AddCommand(groupsCreateCmd(), groupsGetCmd(), groupsListCmd(), groupsCloseCmd())
	return cmd
}

func groupsCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an open group and print its ID",
		Example: `  group=$(workly groups create --name daily-prices --callback-queue callbacks --callback-url https://api.example.com/refresh)
  workly tasks enqueue prices https://api.example.com/price -d '{"symbol": "NET"}' --group $group
  workly groups close $group`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			var req client.NewGroup
			setIf(f.Changed("name"), &req.Name, must(f.GetString("name")))
			setIf(f.Changed("idempotency-key"), &req.IdempotencyKey, must(f.GetString("idempotency-key")))
			if f.Changed("callback-url") {
				req.OnComplete = &client.GroupCallback{Queue: must(f.GetString("callback-queue")), Url: must(f.GetString("callback-url"))}
				if hs := must(f.GetStringArray("header")); len(hs) > 0 {
					headers := map[string]string{}
					for _, h := range hs {
						name, value, ok := strings.Cut(h, ":")
						if !ok {
							return fmt.Errorf(`--header %q: want "Name: value"`, h)
						}
						headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
					}
					req.OnComplete.Headers = &headers
				}
			} else if f.Changed("header") {
				return errors.New("--header applies to the callback: add --callback-url")
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.CreateGroupWithResponse(cmd.Context(), req)
			if err := check(res, err); err != nil {
				return err
			}
			g := res.JSON201
			if g == nil {
				g = res.JSON200
				fmt.Fprintln(cmd.ErrOrStderr(), "A group with this idempotency key already exists; nothing was created.")
			}
			// Just the ID, so scripts can capture it.
			return output(cmd, res.Body, func(w io.Writer) { fmt.Fprintln(w, g.Id) })
		},
	}
	f := cmd.Flags()
	f.String("name", "", "a label, e.g. the job this group is one run of")
	f.StringP("idempotency-key", "k", "", "return the existing group with this key instead of creating one")
	f.String("callback-url", "", "URL of the callback task, enqueued when the group completes")
	f.String("callback-queue", "", "queue of the callback task")
	f.StringArrayP("header", "H", nil, `header sent with the callback, "Name: value" (repeatable)`)
	cmd.MarkFlagsRequiredTogether("callback-url", "callback-queue")
	return cmd
}

func groupsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get ID",
		Short: "Show a group's status and progress",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.GetGroupWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) { printGroup(w, res.JSON200) })
		},
	}
}

func groupsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List groups, newest first",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var params client.ListGroupsParams
			setIf(f.Changed("status"), &params.Status, client.GroupStatus(must(f.GetString("status"))))
			setIf(f.Changed("name"), &params.Name, must(f.GetString("name")))
			setIf(f.Changed("cursor"), &params.Cursor, must(f.GetString("cursor")))
			setIf(true, &params.Limit, must(f.GetInt("limit")))
			res, err := c.ListGroupsWithResponse(cmd.Context(), &params)
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				page := res.JSON200
				if len(page.Groups) == 0 {
					fmt.Fprintln(w, "No groups.")
					return
				}
				var rows [][]string
				for _, g := range page.Groups {
					rows = append(rows, []string{g.Id, deref(g.Name, "-"), string(g.Status), progress(&g), ago(g.CreatedAt)})
				}
				table(w, "ID\tNAME\tSTATUS\tPROGRESS\tCREATED", rows)
				if page.NextCursor != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "\nMore groups: add --cursor %s\n", *page.NextCursor)
				}
			})
		},
	}
	f := cmd.Flags()
	f.String("status", "", "only groups in this status: open, running, succeeded or failed")
	f.String("name", "", "only groups with this name")
	f.Int("limit", 20, "groups per page (at most 200)")
	f.String("cursor", "", "continue from a previous page")
	return cmd
}

func groupsCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close ID",
		Short: "Stop a group accepting tasks, so it completes once they finish",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.CloseGroupWithResponse(cmd.Context(), args[0])
			if err := check(res, err); err != nil {
				return err
			}
			return output(cmd, res.Body, func(w io.Writer) {
				fmt.Fprintf(w, "Group %s is %s.\n", res.JSON200.Id, res.JSON200.Status)
			})
		},
	}
}

func printGroup(w io.Writer, g *client.Group) {
	rows := [][]string{
		{"ID", g.Id},
		{"Name", deref(g.Name, "-")},
		{"Status", string(g.Status)},
		{"Progress", progress(g)},
		{"Tasks", fmt.Sprintf("%d succeeded, %d failed, %d cancelled, %d remaining", g.Succeeded, g.Failed, g.Cancelled, g.Remaining)},
	}
	if cb := g.OnComplete; cb != nil {
		callback := cb.Queue + " → " + cb.Url
		if g.CallbackTaskId != nil {
			callback += " (task " + *g.CallbackTaskId + ")"
		}
		rows = append(rows, []string{"Callback", callback})
	}
	rows = append(rows, []string{"Created", g.CreatedAt.Local().Format(time.DateTime) + " (" + ago(g.CreatedAt) + ")"})
	if g.FinishedAt != nil {
		rows = append(rows, []string{"Finished", g.FinishedAt.Local().Format(time.DateTime) + " (" + ago(*g.FinishedAt) + ")"})
	}
	for _, r := range rows {
		r[0] += ":"
	}
	table(w, "", rows)
}

// progress is how many of a group's tasks have finished.
func progress(g *client.Group) string {
	return strconv.Itoa(g.Total-g.Remaining) + "/" + strconv.Itoa(g.Total)
}
