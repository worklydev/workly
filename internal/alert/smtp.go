package alert

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/id"
	"github.com/worklydev/workly/internal/workspace"
)

// SMTP sends alert emails through an SMTP server.
type SMTP struct {
	host     string // host:port
	implicit bool   // TLS from the start (smtps), rather than STARTTLS
	auth     smtp.Auth
	from     *mail.Address
}

// NewSMTP parses an SMTP URL: smtp://user:pass@host:587 upgrades the
// connection with STARTTLS when the server offers it (and refuses to send
// the password otherwise, except to localhost), smtps://user:pass@host:465
// uses TLS from the start. from is the sender, e.g. "Workly
// <alerts@example.com>".
func NewSMTP(rawURL, from string) (*SMTP, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("SMTP URL: %w", err)
	}
	s := &SMTP{}
	switch u.Scheme {
	case "smtp":
		s.host = withPort(u.Host, "587")
	case "smtps":
		s.host, s.implicit = withPort(u.Host, "465"), true
	default:
		return nil, fmt.Errorf("SMTP URL: use smtp:// or smtps://, not %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("SMTP URL: no host")
	}
	if u.User != nil {
		pass, _ := u.User.Password()
		s.auth = smtp.PlainAuth("", u.User.Username(), pass, u.Hostname())
	}
	if from == "" {
		return nil, fmt.Errorf("SMTP: a sender address is required")
	}
	if s.from, err = mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("SMTP sender %q: %w", from, err)
	}
	return s, nil
}

func withPort(host, port string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, port)
}

// CheckRecipient accepts any address: whoever runs the server chooses them.
func (s *SMTP) CheckRecipient(context.Context, workspace.ID, string) error { return nil }

func (s *SMTP) Send(ctx context.Context, _ workspace.ID, e Email) error {
	msg, err := s.message(e)
	if err != nil {
		return err
	}
	serverName, _, _ := net.SplitHostPort(s.host)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", s.host)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(time.Minute))
	}
	if s.implicit {
		conn = tls.Client(conn, &tls.Config{ServerName: serverName})
	}
	c, err := smtp.NewClient(conn, serverName)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok && !s.implicit {
		if err := c.StartTLS(&tls.Config{ServerName: serverName}); err != nil {
			return err
		}
	}
	if s.auth != nil {
		if err := c.Auth(s.auth); err != nil {
			return err
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(e.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (s *SMTP) message(e Email) ([]byte, error) {
	to, err := mail.ParseAddress(e.To)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	domain := s.from.Address[strings.LastIndex(s.from.Address, "@")+1:]
	for _, h := range [][2]string{
		{"From", s.from.String()},
		{"To", to.String()},
		{"Subject", mime.QEncoding.Encode("utf-8", strings.Join(strings.Fields(e.Subject), " "))},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", "<" + id.New("alert") + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
		// Tells mail servers it's automatic, so they don't auto-reply.
		{"Auto-Submitted", "auto-generated"},
	} {
		fmt.Fprintf(&b, "%s: %s\r\n", h[0], h[1])
	}
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(e.Text, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
