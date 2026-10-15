package alert

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/sqlite"
	"github.com/worklydev/workly/internal/workspace"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		typ    store.AlertChannelType
		target string
		want   string // "" for an error
	}{
		{store.AlertEmail, "ops@example.com", "ops@example.com"},
		{store.AlertEmail, " ops@example.com ", "ops@example.com"},
		{store.AlertEmail, "Ops <ops@example.com>", ""},
		{store.AlertEmail, "ops", ""},
		{store.AlertEmail, "ops@example.com\r\nBcc: x@example.com", ""},
		{store.AlertSlack, "https://hooks.slack.com/services/T0/B0/abc", "https://hooks.slack.com/services/T0/B0/abc"},
		{store.AlertSlack, "https://hooks.slack.com/", ""},
		{store.AlertSlack, "https://hooks.slack.com.evil.test/services/x", ""},
		{store.AlertSlack, "http://hooks.slack.com/services/x", ""},
		{store.AlertSlack, "https://example.com/hook", ""},
		{"sms", "+46700000000", ""},
	}
	for _, tt := range tests {
		got, err := ParseTarget(tt.typ, tt.target)
		if tt.want == "" && err == nil || tt.want != "" && (err != nil || got != tt.want) {
			t.Errorf("ParseTarget(%s, %q) = %q, %v; want %q", tt.typ, tt.target, got, err, tt.want)
		}
	}
}

func TestMasked(t *testing.T) {
	tests := []struct {
		c    store.AlertChannel
		want string
	}{
		{store.AlertChannel{Type: store.AlertEmail, Target: "ops@example.com"}, "ops@example.com"},
		{store.AlertChannel{Type: store.AlertSlack, Target: "https://hooks.slack.com/services/T0/B0/abcdefgh1234"},
			"https://hooks.slack.com/services/…1234"},
		{store.AlertChannel{Type: store.AlertSlack, Target: "https://hooks.slack.com/services/ab"},
			"https://hooks.slack.com/services/…"},
	}
	for _, tt := range tests {
		if got := Masked(tt.c); got != tt.want {
			t.Errorf("Masked(%q) = %q, want %q", tt.c.Target, got, tt.want)
		}
	}
}

func TestFailureMessage(t *testing.T) {
	since := time.Date(2026, 10, 6, 14, 2, 0, 0, time.UTC)
	emails := store.AlertFailures{
		AlertSubject: store.AlertSubject{Kind: store.AlertQueue, Name: "emails"},
		Since:        since,
		Total:        16,
		Groups: []store.AlertErrorGroup{
			{Error: "HTTP 503 <html>", Count: 9},
			{Error: "no response within 30s", Count: 3},
			{Error: "", Count: 2},
			{Error: "HTTP 500", Count: 1},
			{Error: "HTTP 502", Count: 1},
		},
	}
	nightly := store.AlertFailures{
		AlertSubject: store.AlertSubject{Kind: store.AlertSchedule, Name: "nightly"},
		Since:        since,
		Total:        1,
		Groups:       []store.AlertErrorGroup{{Error: "HTTP 500", Count: 1}},
	}

	m := failureMessage("", []store.AlertFailures{emails}, "https://workly.example.com/", 30*time.Minute)
	if m.Subject != "Queue emails: 16 tasks failed" {
		t.Errorf("subject = %q", m.Subject)
	}
	for _, want := range []string{
		"Queue emails: 16 tasks failed since Oct 6, 14:02 UTC.",
		"  9 × HTTP 503 <html>\n  3 × no response within 30s\n  2 × no error recorded\n  2 more with 2 other errors\n",
		"https://workly.example.com/queues/emails?state=failed",
		"every 30 minutes",
		"Manage alerts: https://workly.example.com/alerts",
	} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text doesn't contain %q:\n%s", want, m.Text)
		}
	}
	for _, want := range []string{
		"*Queue `emails`*: 16 tasks failed",
		"> 9 × HTTP 503 &lt;html&gt;",
		"<https://workly.example.com/queues/emails?state=failed|Open in Workly>",
	} {
		if !strings.Contains(m.Slack, want) {
			t.Errorf("Slack text doesn't contain %q:\n%s", want, m.Slack)
		}
	}
	if strings.Contains(m.Text, "Workspace") {
		t.Errorf("the only workspace is named:\n%s", m.Text)
	}

	m = failureMessage("Acme\r\nBcc: x", []store.AlertFailures{emails, nightly}, "", time.Hour)
	if m.Subject != "[Acme Bcc: x] Failures in emails and nightly" {
		t.Errorf("subject = %q", m.Subject)
	}
	if !strings.Contains(m.Text, "Schedule nightly: 1 run failed") || !strings.Contains(m.Text, "every hour") ||
		strings.Contains(m.Text, "http") {
		t.Errorf("text without links:\n%s", m.Text)
	}
}

func TestSlack(t *testing.T) {
	var got map[string]any
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "no_service")
	}))
	defer srv.Close()
	s := NewSender(nil, "")
	s.Client.Transport = to(srv)

	c := store.AlertChannel{Type: store.AlertSlack, Target: SlackPrefix + "services/T0/B0/secret"}
	if err := s.Send(t.Context(), c, Message{Slack: "hi"}); err != nil || got["text"] != "hi" {
		t.Fatalf("Send = %v, posted %v", err, got)
	}
	status = http.StatusNotFound
	if err := s.Send(t.Context(), c, Message{Slack: "hi"}); err == nil || !strings.Contains(err.Error(), "no_service") {
		t.Fatalf("Send to a removed webhook = %v", err)
	}
	srv.Close()
	err := s.Send(t.Context(), c, Message{Slack: "hi"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("Send to nowhere = %v; want an error without the URL", err)
	}

	if err := s.Send(t.Context(), store.AlertChannel{Type: store.AlertEmail, Target: "a@example.com"}, Message{}); err != ErrNoMailer {
		t.Fatalf("email without a mailer = %v", err)
	}
}

// to sends every request to srv, whatever its URL.
func to(srv *httptest.Server) http.RoundTripper {
	target, _ := url.Parse(srv.URL)
	return roundTripper(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSMTP(t *testing.T) {
	smtpd := fakeSMTP(t)
	m, err := NewSMTP("smtp://"+smtpd.addr, "Workly <alerts@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	err = m.Send(t.Context(), workspace.Default, Email{To: "ops@example.com", Subject: "Queue émails: 2 tasks failed", Text: "line one\nline two\n"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := smtpd.messages()
	if len(got) != 1 {
		t.Fatalf("got %d messages", len(got))
	}
	msg := got[0]
	for _, want := range []string{
		"MAIL FROM:<alerts@example.com>",
		"RCPT TO:<ops@example.com>",
		`From: "Workly" <alerts@example.com>`,
		"Subject: =?utf-8?q?Queue_=C3=A9mails:_2_tasks_failed?=",
		"Auto-Submitted: auto-generated",
		"line one\r\nline two\r\n",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message doesn't contain %q:\n%s", want, msg)
		}
	}

	for _, bad := range []string{"http://mail.example.com", "smtp://", "smtp://mail.example.com"} {
		from := "alerts@example.com"
		if bad == "smtp://mail.example.com" {
			from = ""
		}
		if _, err := NewSMTP(bad, from); err == nil {
			t.Errorf("NewSMTP(%q, %q) = nil error", bad, from)
		}
	}
}

type smtpServer struct {
	addr string
	mu   sync.Mutex
	msgs []string
}

func (s *smtpServer) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.msgs
}

// fakeSMTP accepts mail on localhost, without TLS or auth, and keeps each
// conversation's commands and data.
func fakeSMTP(t *testing.T) *smtpServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &smtpServer{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				var transcript strings.Builder
				reply := func(line string) { _, _ = io.WriteString(conn, line+"\r\n") }
				reply("220 localhost")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					transcript.WriteString(line)
					switch cmd := strings.ToUpper(strings.Fields(line + " x")[0]); cmd {
					case "EHLO", "HELO":
						reply("250 localhost")
					case "DATA":
						reply("354 go ahead")
						for {
							l, err := r.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
							transcript.WriteString(l)
						}
						reply("250 ok")
					case "QUIT":
						reply("221 bye")
						s.mu.Lock()
						s.msgs = append(s.msgs, transcript.String())
						s.mu.Unlock()
						return
					default:
						reply("250 ok")
					}
				}
			}()
		}
	}()
	return s
}

func TestNotifier(t *testing.T) {
	ctx := t.Context()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "workly.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureWorkspace(ctx, workspace.Default, "default"); err != nil {
		t.Fatal(err)
	}

	var (
		mu     sync.Mutex
		posted []string
		down   bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if down {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		var body struct{ Text string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		posted = append(posted, body.Text)
	}))
	defer srv.Close()
	sender := NewSender(nil, "http://workly.test")
	sender.Client.Transport = to(srv)
	n := &Notifier{Store: st, Sender: sender, Logger: slog.New(slog.DiscardHandler), Cooldown: time.Hour, Settle: time.Nanosecond}
	takePosted := func() []string {
		mu.Lock()
		defer mu.Unlock()
		p := posted
		posted = nil
		return p
	}

	fail := func(errText string) {
		task, _, err := st.CreateTask(ctx, workspace.Default, store.NewTask{Queue: "emails", Method: "POST", URL: "http://example.test", RunAt: time.Now()}, true)
		if err != nil {
			t.Fatal(err)
		}
		q, _ := st.GetQueue(ctx, workspace.Default, "emails")
		claimed, err := st.ClaimTasks(ctx, q, 1, time.Now(), time.Minute)
		if err != nil || len(claimed) != 1 || claimed[0].ID != task.ID {
			t.Fatalf("ClaimTasks = %v, %v", claimed, err)
		}
		_, err = st.FinishAttempt(ctx, workspace.Default, store.AttemptResult{
			Attempt:   store.Attempt{TaskID: task.ID, Number: 1, StartedAt: time.Now(), FinishedAt: time.Now(), Outcome: store.OutcomeHTTPError, Error: errText},
			NextState: store.TaskFailed,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Failures before there's a channel aren't alerted about.
	fail("HTTP 500")
	time.Sleep(2 * time.Millisecond)
	if _, err := st.CreateAlertChannel(ctx, workspace.Default, store.AlertSlack, SlackPrefix+"services/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAlertChannel(ctx, workspace.Default, store.AlertEmail, "ops@example.com"); err != nil {
		t.Fatal(err)
	}
	n.Check(ctx, time.Now())
	if p := takePosted(); len(p) != 0 {
		t.Fatalf("posted %q before any new failure", p)
	}

	fail("HTTP 503")
	fail("HTTP 503")
	time.Sleep(2 * time.Millisecond)
	// Slack is down: nothing is sent (email can't be), so the next check tries again.
	mu.Lock()
	down = true
	mu.Unlock()
	n.Check(ctx, time.Now())
	mu.Lock()
	down = false
	mu.Unlock()
	n.Check(ctx, time.Now())
	p := takePosted()
	if len(p) != 1 || !strings.Contains(p[0], "2 tasks failed") || !strings.Contains(p[0], "> 2 × HTTP 503") ||
		!strings.Contains(p[0], "http://workly.test/queues/emails?state=failed") {
		t.Fatalf("posted %q; want one alert about 2 tasks", p)
	}

	// Within the cooldown, new failures wait.
	fail("timeout")
	time.Sleep(2 * time.Millisecond)
	n.Check(ctx, time.Now())
	if p := takePosted(); len(p) != 0 {
		t.Fatalf("posted %q during the cooldown", p)
	}
	n.Check(ctx, time.Now().Add(time.Hour+time.Minute))
	if p := takePosted(); len(p) != 1 || !strings.Contains(p[0], "1 task failed") || !strings.Contains(p[0], "1 × timeout") {
		t.Fatalf("posted %q after the cooldown; want the one new failure", p)
	}
}

var _ Mailer = (*SMTP)(nil)
