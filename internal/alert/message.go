package alert

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/store"
)

// Links builds links into the dashboard at its base URL, e.g.
// https://workly.example.com. The zero value builds none.
type Links string

func (l Links) to(path string) string {
	if l == "" {
		return ""
	}
	return strings.TrimRight(string(l), "/") + path
}

func (l Links) subject(f store.AlertFailures) string {
	if f.Kind == store.AlertSchedule {
		return l.to("/schedules/" + url.PathEscape(f.Name))
	}
	return l.to("/queues/" + url.PathEscape(f.Name) + "?state=failed")
}

func (l Links) alerts() string { return l.to("/alerts") }

// groupsShown is how many errors an alert lists per queue or schedule.
const groupsShown = 3

// maxErrorLen cuts long errors, which can include a response's text.
const maxErrorLen = 200

// failureMessage is the alert about failures in a workspace, named unless
// it's the only one.
func failureMessage(workspaceName string, failures []store.AlertFailures, links Links, cooldown time.Duration) Message {
	var text, slack strings.Builder
	for i, f := range failures {
		if i > 0 {
			text.WriteString("\n")
			slack.WriteString("\n\n")
		}
		noun := plural(f.Total, "task", "tasks")
		if f.Kind == store.AlertSchedule {
			noun = plural(f.Total, "run", "runs")
		}
		since := f.Since.UTC().Format("Jan 2, 15:04 UTC")
		fmt.Fprintf(&text, "%s %s: %d %s failed since %s.\n\n", title(f.Kind), f.Name, f.Total, noun, since)
		fmt.Fprintf(&slack, "*%s `%s`*: %d %s failed since %s", title(f.Kind), f.Name, f.Total, noun, since)
		for _, g := range f.Groups[:min(len(f.Groups), groupsShown)] {
			fmt.Fprintf(&text, "  %d × %s\n", g.Count, describe(g.Error))
			fmt.Fprintf(&slack, "\n> %d × %s", g.Count, slackEscape(describe(g.Error)))
		}
		if rest := f.Groups[min(len(f.Groups), groupsShown):]; len(rest) > 0 {
			n := 0
			for _, g := range rest {
				n += g.Count
			}
			fmt.Fprintf(&text, "  %d more with %d other %s\n", n, len(rest), plural(len(rest), "error", "errors"))
			fmt.Fprintf(&slack, "\n> %d more with %d other %s", n, len(rest), plural(len(rest), "error", "errors"))
		}
		if link := links.subject(f); link != "" {
			fmt.Fprintf(&text, "\n  %s\n", link)
			fmt.Fprintf(&slack, "\n<%s|Open in Workly>", link)
		}
	}

	footer := fmt.Sprintf("You get at most one alert per queue or schedule every %s, covering what failed since the previous one.",
		duration(cooldown))
	text.WriteString("\n--\n")
	if workspaceName != "" {
		fmt.Fprintf(&text, "Workspace: %s\n", workspaceName)
	}
	text.WriteString(footer + "\n")
	if link := links.alerts(); link != "" {
		fmt.Fprintf(&text, "Manage alerts: %s\n", link)
	}

	slack.WriteString("\n")
	var context []string
	if workspaceName != "" {
		context = append(context, "Workspace "+slackEscape(workspaceName))
	}
	context = append(context, footer)
	if link := links.alerts(); link != "" {
		context = append(context, fmt.Sprintf("<%s|Manage alerts>", link))
	}
	slack.WriteString("_" + strings.Join(context, " · ") + "_")

	return Message{Subject: prefixed(workspaceName, subjectLine(failures)), Text: text.String(), Slack: slack.String()}
}

func subjectLine(failures []store.AlertFailures) string {
	if len(failures) == 1 {
		f := failures[0]
		noun := plural(f.Total, "task", "tasks")
		if f.Kind == store.AlertSchedule {
			noun = plural(f.Total, "run", "runs")
		}
		return fmt.Sprintf("%s %s: %d %s failed", title(f.Kind), f.Name, f.Total, noun)
	}
	names := make([]string, len(failures))
	for i, f := range failures {
		names[i] = f.Name
	}
	if len(names) > 4 {
		names = append(names[:3], fmt.Sprintf("%d more", len(failures)-3))
	}
	return "Failures in " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// testMessage checks a channel works.
func testMessage(workspaceName string, links Links) Message {
	where := "this workspace"
	if workspaceName != "" {
		where = "workspace " + workspaceName
	}
	body := fmt.Sprintf("This is a test alert from Workly. Alerts about failed tasks and schedule runs in %s will arrive here.", where)
	text, slack := body+"\n", slackEscape(body)
	if link := links.alerts(); link != "" {
		text += "\nManage alerts: " + link + "\n"
		slack += fmt.Sprintf("\n<%s|Manage alerts>", link)
	}
	return Message{Subject: prefixed(workspaceName, "Test alert"), Text: text, Slack: slack}
}

func prefixed(workspaceName, subject string) string {
	if workspaceName == "" {
		return subject
	}
	// A header line: no line breaks from a user-chosen name.
	return "[" + strings.Join(strings.Fields(workspaceName), " ") + "] " + subject
}

func title(k store.AlertKind) string {
	if k == store.AlertSchedule {
		return "Schedule"
	}
	return "Queue"
}

func describe(err string) string {
	err = strings.Join(strings.Fields(err), " ")
	if err == "" {
		return "no error recorded"
	}
	if r := []rune(err); len(r) > maxErrorLen {
		return string(r[:maxErrorLen]) + "…"
	}
	return err
}

// slackEscape escapes the characters Slack's mrkdwn treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func duration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return plural(int(d/time.Hour), "hour", fmt.Sprintf("%d hours", d/time.Hour))
	}
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d minutes", d/time.Minute)
	}
	return d.String()
}
