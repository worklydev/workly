package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/worklydev/workly/server"
)

// mailbox is a Mailer that keeps what it sends, and only sends to
// example.com.
type mailbox struct {
	mu   sync.Mutex
	sent []server.Email
	fail error
}

func (m *mailbox) CheckRecipient(_ context.Context, _ server.WorkspaceID, address string) error {
	if !strings.HasSuffix(address, "@example.com") {
		return server.LimitError(http.StatusForbidden, "not_a_member", address+" isn't a member of this workspace")
	}
	return nil
}

func (m *mailbox) Send(_ context.Context, _ server.WorkspaceID, e server.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, e)
	return nil
}

func TestAlertChannels(t *testing.T) {
	mail := &mailbox{}
	h := newServer(t, server.Options{Mailer: mail, PublicURL: "https://workly.example.com"}).Handler()

	res := do(t, h, "GET", "/v1/alert-channels", "")
	if res.status != 200 || res.json["email_enabled"] != true || len(res.json["channels"].([]any)) != 0 {
		t.Fatalf("GET /v1/alert-channels = %d %s", res.status, res.body)
	}

	email := doBody(t, h, "POST", "/v1/alert-channels", "", `{"type": "email", "target": "ops@example.com"}`)
	if email.status != 201 || email.json["type"] != "email" || email.json["target"] != "ops@example.com" ||
		!strings.HasPrefix(email.json["id"].(string), "ach_") {
		t.Fatalf("add email = %d %s", email.status, email.body)
	}
	slack := doBody(t, h, "POST", "/v1/alert-channels", "",
		`{"type": "slack", "target": "https://hooks.slack.com/services/T0/B0/secretsecret"}`)
	if slack.status != 201 || slack.json["target"] != "https://hooks.slack.com/services/…cret" {
		t.Fatalf("add slack = %d %s", slack.status, slack.body)
	}
	if strings.Contains(do(t, h, "GET", "/v1/alert-channels", "").body, "secretsecret") {
		t.Fatal("the list shows the Slack webhook's secret")
	}

	for _, tt := range []struct {
		body, code string
		status     int
	}{
		{`{"type": "email", "target": "ops@example.com"}`, "alert_channel_exists", 409},
		{`{"type": "email", "target": "someone@elsewhere.test"}`, "not_a_member", 403},
		{`{"type": "email", "target": "not an address"}`, "invalid_request", 400},
		{`{"type": "slack", "target": "https://example.com/hook"}`, "invalid_request", 400},
		{`{"type": "sms", "target": "+46700000000"}`, "invalid_request", 400},
	} {
		if r := doBody(t, h, "POST", "/v1/alert-channels", "", tt.body); r.status != tt.status || errorCode(r) != tt.code {
			t.Errorf("add %s = %d %s; want %d %s", tt.body, r.status, r.body, tt.status, tt.code)
		}
	}

	id := email.json["id"].(string)
	if r := do(t, h, "POST", "/v1/alert-channels/"+id+"/test", ""); r.status != 204 {
		t.Fatalf("test email = %d %s", r.status, r.body)
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "ops@example.com" || mail.sent[0].Subject != "Test alert" ||
		!strings.Contains(mail.sent[0].Text, "https://workly.example.com/alerts") {
		t.Fatalf("sent %+v", mail.sent)
	}
	mail.fail = errors.New("mailbox full")
	if r := do(t, h, "POST", "/v1/alert-channels/"+id+"/test", ""); r.status != 502 || errorCode(r) != "alert_failed" ||
		!strings.Contains(r.body, "mailbox full") {
		t.Fatalf("failed test = %d %s", r.status, r.body)
	}

	if r := do(t, h, "DELETE", "/v1/alert-channels/"+id, ""); r.status != 204 {
		t.Fatalf("delete = %d %s", r.status, r.body)
	}
	for _, path := range []string{"DELETE /v1/alert-channels/" + id, "POST /v1/alert-channels/" + id + "/test"} {
		method, p, _ := strings.Cut(path, " ")
		if r := do(t, h, method, p, ""); r.status != 404 || errorCode(r) != "alert_channel_not_found" {
			t.Errorf("%s after delete = %d %s", path, r.status, r.body)
		}
	}

	for i := range 10 {
		body := fmt.Sprintf(`{"type": "email", "target": "ops%d@example.com"}`, i)
		r := doBody(t, h, "POST", "/v1/alert-channels", "", body)
		if want := 201; i == 9 {
			if r.status != 400 || !strings.Contains(r.body, "at most 10") {
				t.Fatalf("11th channel = %d %s", r.status, r.body)
			}
		} else if r.status != want {
			t.Fatalf("channel %d = %d %s", i, r.status, r.body)
		}
	}
}

func TestAlertChannelsWithoutEmail(t *testing.T) {
	h := newServer(t, server.Options{}).Handler()
	if r := do(t, h, "GET", "/v1/alert-channels", ""); r.json["email_enabled"] != false {
		t.Fatalf("GET /v1/alert-channels = %s", r.body)
	}
	r := doBody(t, h, "POST", "/v1/alert-channels", "", `{"type": "email", "target": "ops@example.com"}`)
	if r.status != 400 || errorCode(r) != "email_unavailable" {
		t.Fatalf("add email = %d %s", r.status, r.body)
	}
}

func TestAlertChannelsPerWorkspace(t *testing.T) {
	st := newStore(t, true)
	for _, ws := range []server.WorkspaceID{"ws_a", "ws_b"} {
		if _, err := st.EnsureWorkspace(t.Context(), ws, string(ws)); err != nil {
			t.Fatal(err)
		}
	}
	h := newServer(t, server.Options{Store: st, WorkspaceResolver: byHeader{}}).Handler()
	req := func(ws, method, path, body string) response {
		t.Helper()
		return doBody(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Workspace", ws)
			h.ServeHTTP(w, r)
		}), method, path, "", body)
	}
	a := req("ws_a", "POST", "/v1/alert-channels", `{"type": "slack", "target": "https://hooks.slack.com/services/a"}`)
	if a.status != 201 {
		t.Fatalf("add = %d %s", a.status, a.body)
	}
	if r := req("ws_b", "GET", "/v1/alert-channels", ""); len(r.json["channels"].([]any)) != 0 {
		t.Fatalf("ws_b sees %s", r.body)
	}
	if r := req("ws_b", "DELETE", "/v1/alert-channels/"+a.json["id"].(string), ""); r.status != 404 {
		t.Fatalf("ws_b deletes ws_a's channel: %d", r.status)
	}
}
