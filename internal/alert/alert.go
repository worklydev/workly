// Package alert tells people when tasks and schedule runs fail: by email,
// or in Slack through an incoming webhook.
//
// The Notifier runs on the dispatcher's leader. Every Interval it finds the
// queues and schedules with failures since their last alert and, once a
// subject's cooldown has passed, sends each of the workspace's channels one
// message covering them all. Alerts are sent at least once: a failed send
// is retried on the next check, and a channel that already got it may get
// it again.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// Email is an alert by email, in plain text.
type Email struct {
	To      string
	Subject string
	Text    string
}

// Mailer sends alert emails. A server without one can't have email
// channels.
type Mailer interface {
	// CheckRecipient is called before an email channel is added and may
	// refuse the address, e.g. to only send to a workspace's members.
	CheckRecipient(ctx context.Context, ws workspace.ID, address string) error
	Send(ctx context.Context, ws workspace.ID, e Email) error
}

// SlackPrefix is how every Slack incoming webhook URL starts. Only these
// are accepted, so a channel can't make Workly call other addresses.
const SlackPrefix = "https://hooks.slack.com/"

// MaxChannels is how many channels a workspace can have.
const MaxChannels = 10

// ParseTarget checks a channel's target and returns it as stored.
func ParseTarget(typ store.AlertChannelType, target string) (string, error) {
	target = strings.TrimSpace(target)
	switch typ {
	case store.AlertEmail:
		addr, err := mail.ParseAddress(target)
		if err != nil || addr.Address != target || strings.ContainsAny(target, "\r\n") {
			return "", fmt.Errorf("%q is not an email address", target)
		}
		return target, nil
	case store.AlertSlack:
		if !strings.HasPrefix(target, SlackPrefix) || len(target) == len(SlackPrefix) || strings.ContainsAny(target, " \r\n") {
			return "", fmt.Errorf("a Slack channel takes an incoming webhook URL, starting with %s", SlackPrefix)
		}
		return target, nil
	default:
		return "", fmt.Errorf("unknown channel type %q", typ)
	}
}

// Masked returns a channel's target to show: a Slack webhook URL is a
// secret, so only its start and last characters are shown.
func Masked(c store.AlertChannel) string {
	if c.Type != store.AlertSlack {
		return c.Target
	}
	const keep = 4
	rest := strings.TrimPrefix(c.Target, SlackPrefix)
	path, _, _ := strings.Cut(rest, "/")
	if len(rest) <= len(path)+1+keep {
		return SlackPrefix + path + "/…"
	}
	return SlackPrefix + path + "/…" + rest[len(rest)-keep:]
}

// Message is an alert, written for both kinds of channel.
type Message struct {
	Subject string
	// Text is the email body.
	Text string
	// Slack is the Slack message, in Slack's mrkdwn.
	Slack string
}

// Sender sends messages to channels.
type Sender struct {
	// Mailer is nil if the server can't send email.
	Mailer Mailer
	// Client posts to Slack. It must not follow redirects.
	Client *http.Client
	// Links builds links to the dashboard; it's empty when the server
	// doesn't know its URL.
	Links Links
}

// NewSender returns a Sender with a client fit for Slack webhooks.
func NewSender(mailer Mailer, links Links) *Sender {
	return &Sender{
		Mailer: mailer,
		Links:  links,
		Client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ErrNoMailer is returned when sending email from a server that can't.
var ErrNoMailer = errors.New("this server isn't set up to send email")

// Send sends m to the channel.
func (s *Sender) Send(ctx context.Context, c store.AlertChannel, m Message) error {
	switch c.Type {
	case store.AlertEmail:
		if s.Mailer == nil {
			return ErrNoMailer
		}
		return s.Mailer.Send(ctx, c.WorkspaceID, Email{To: c.Target, Subject: m.Subject, Text: m.Text})
	case store.AlertSlack:
		return s.postSlack(ctx, c.Target, m.Slack)
	default:
		return fmt.Errorf("unknown channel type %q", c.Type)
	}
}

// CanSend reports whether the channel's type can be sent to.
func (s *Sender) CanSend(c store.AlertChannel) bool {
	return c.Type != store.AlertEmail || s.Mailer != nil
}

func (s *Sender) postSlack(ctx context.Context, url, text string) error {
	body, err := json.Marshal(map[string]any{"text": text, "unfurl_links": false})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		// The error includes the URL, which is a secret.
		var urlErr interface{ Unwrap() error }
		if errors.As(err, &urlErr) {
			err = urlErr.Unwrap()
		}
		return fmt.Errorf("Slack didn't answer: %w", err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		// Slack explains in the body, e.g. "no_service" for a removed webhook.
		return fmt.Errorf("Slack answered %s: %s", resp.Status, strings.TrimSpace(string(reply)))
	}
	return nil
}
