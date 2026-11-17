package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

var update = flag.Bool("update", false, "rewrite docs/cli.md from the commands' help")

// TestCLIReference keeps docs/cli.md in step with the commands: it's the
// help of every command on one page, for people and for agents reading the
// docs, and this test fails when a command changes without it. Regenerate it
// with `go test ./cmd/workly -run TestCLIReference -update`.
func TestCLIReference(t *testing.T) {
	const path = "../../docs/cli.md"
	// In the order the commands are added, which puts dev and serve first and
	// related commands together, rather than alphabetically as --help does.
	cobra.EnableCommandSorting = false
	t.Cleanup(func() { cobra.EnableCommandSorting = true })
	got := cliReference(rootCmd())
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Error("docs/cli.md is out of date: run go test ./cmd/workly -run TestCLIReference -update")
	}
}

const cliIntro = `# CLI reference

<!-- Generated from the commands' help by cmd/workly/clidoc_test.go: run go test ./cmd/workly -run TestCLIReference -update after changing a command. -->

The ` + "`workly`" + ` binary is the server and the CLI. Install it with ` + "`pip install \"workly[cli]\"`" + `, or see the [quickstart](quickstart.md#install) for the other ways. ` + "`workly COMMAND --help`" + ` prints the same help as this page.

The commands that call the API (queues, tasks, groups, workflows, schedules, runs, plan and deploy) connect to ` + "`--url`" + ` or ` + "`WORKLY_URL`" + `, ` + "`http://localhost:7337`" + ` by default, with ` + "`--api-key`" + ` or ` + "`WORKLY_API_KEY`" + `. For hosted Workly, that is ` + "`https://api.workly.run`" + ` and a key from the console. They print a table, or the API's JSON response with ` + "`--json`" + `.
`

func cliReference(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString(cliIntro)
	walk(root, func(c *cobra.Command) {
		if c == root {
			return
		}
		// Commands are h2 and their subcommands h3, so the page's table of
		// contents follows the tree.
		fmt.Fprintf(&b, "\n%s %s\n\n", strings.Repeat("#", depth(c)+1), c.CommandPath())
		text := strings.TrimSpace(c.Long)
		if text == "" {
			text = strings.TrimSuffix(c.Short, ".") + "."
		}
		// Help text wraps at 80 columns; Markdown reflows it, but a blank
		// line still separates paragraphs. Flags and the - for stdin go in
		// code spans, or typography turns them into dashes.
		text = flagRef.ReplaceAllString(text, "$1`$2`")
		text = strings.ReplaceAll(text, " - for stdin", " `-` for stdin")
		b.WriteString(text + "\n")
		if c.Runnable() {
			fmt.Fprintf(&b, "\n```text\n%s\n```\n", c.UseLine())
		}
		if len(c.Aliases) > 0 {
			fmt.Fprintf(&b, "\nAlso: `%s`.\n", strings.Join(c.Aliases, "`, `"))
		}
		if c.Example != "" {
			fmt.Fprintf(&b, "\nExamples:\n\n```sh\n%s\n```\n", dedent(c.Example))
		}
		if flags := c.LocalNonPersistentFlags(); flags.HasAvailableFlags() {
			fmt.Fprintf(&b, "\nFlags:\n\n```text\n%s\n```\n", dedent(flags.FlagUsages()))
		}
		if flags := c.PersistentFlags(); flags.HasAvailableFlags() {
			fmt.Fprintf(&b, "\nFlags for every `%s` command:\n\n```text\n%s\n```\n", c.CommandPath(), dedent(flags.FlagUsages()))
		}
	})
	return b.String()
}

// walk visits c and its commands depth first, leaving out help and completion.
func walk(c *cobra.Command, visit func(*cobra.Command)) {
	visit(c)
	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() || sub.Name() == "help" {
			continue
		}
		walk(sub, visit)
	}
}

func depth(c *cobra.Command) int {
	n := 0
	for ; c.HasParent(); c = c.Parent() {
		n++
	}
	return n
}

var flagRef = regexp.MustCompile(`(^|[\s(])(--[a-z][a-z0-9-]*)`)

// dedent removes the indent the lines share, which cobra's help puts before
// examples and flags.
func dedent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, " ")); indent < 0 || n < indent {
			indent = n
		}
	}
	for i, l := range lines {
		if len(l) >= indent {
			lines[i] = l[indent:]
		}
	}
	return strings.Join(lines, "\n")
}
