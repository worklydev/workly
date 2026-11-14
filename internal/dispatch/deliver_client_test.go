package dispatch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

func TestDialControl(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer endpoint.Close()

	var dialed string
	refuse := func(network, address string, _ syscall.RawConn) error {
		dialed = address
		return errors.New("address not allowed")
	}
	_, err := newHTTPClient(1, refuse).Get(endpoint.URL)
	if err == nil || !strings.Contains(err.Error(), "address not allowed") {
		t.Fatalf("Get = %v, want the control's error", err)
	}
	if dialed != endpoint.Listener.Addr().String() {
		t.Fatalf("dialed %q, want %q", dialed, endpoint.Listener.Addr())
	}

	resp, err := newHTTPClient(1, func(string, string, syscall.RawConn) error { return nil }).Get(endpoint.URL)
	if err != nil {
		t.Fatalf("Get with an allowing control: %v", err)
	}
	resp.Body.Close()
}

// A DialControl's Refused error fails the attempt for good, with the
// control's message rather than Go's "dial tcp" wrapping.
func TestDialControlRefused(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer endpoint.Close()
	for name, tc := range map[string]struct {
		err   error
		final bool
	}{
		"refused": {Refused(errors.New("127.0.0.1 is a private address")), true},
		"other":   {errors.New("127.0.0.1 is a private address"), false},
	} {
		t.Run(name, func(t *testing.T) {
			d := deliverer{
				client: newHTTPClient(1, func(string, string, syscall.RawConn) error { return tc.err }),
				signer: NewSigner(nil, nil, "Workly/test", nil),
			}
			r := d.deliver(t.Context(), store.Task{WorkspaceID: "ws_a", Method: "POST", URL: endpoint.URL}, time.Second)
			if r.outcome != store.OutcomeNetworkError || r.final != tc.final || !strings.Contains(r.err, "127.0.0.1 is a private address") {
				t.Fatalf("result = %+v", r)
			}
			if tc.final && r.err != "127.0.0.1 is a private address" {
				t.Errorf("error = %q, want the control's message alone", r.err)
			}
		})
	}
}

func TestDescribeError(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 443}
	local := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 51234}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"no such host",
			&url.Error{Op: "Post", URL: "https://x.invalid", Err: &net.OpError{Op: "dial", Net: "tcp",
				Err: &net.DNSError{Err: "no such host", Name: "x.invalid", Server: "[fd12::10]:53", IsNotFound: true}}},
			"DNS lookup for x.invalid failed: no such host"},
		{"DNS timeout",
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "i/o timeout", Name: "slow.test", Server: "10.0.0.2:53", IsTimeout: true}},
			"DNS lookup for slow.test failed: timed out"},
		{"connection refused",
			&url.Error{Op: "Post", URL: "https://x", Err: &net.OpError{Op: "dial", Net: "tcp", Addr: addr,
				Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}},
			"connecting to 203.0.113.7:443: connection refused"},
		{"reset while reading",
			&net.OpError{Op: "read", Net: "tcp", Source: local, Addr: addr, Err: os.NewSyscallError("read", syscall.ECONNRESET)},
			"reading the response from 203.0.113.7:443: connection reset by peer"},
		{"other", &url.Error{Op: "Post", URL: "https://x", Err: errors.New("tls: handshake failure")}, "tls: handshake failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeError(tt.err)
			if got != tt.want {
				t.Errorf("describeError = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "fd12") || strings.Contains(got, "10.0.0") {
				t.Errorf("describeError = %q shows our own addresses", got)
			}
		})
	}
}

func TestSigningSecretLookupFails(t *testing.T) {
	called := false
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer endpoint.Close()
	d := deliverer{
		client: newHTTPClient(1, nil),
		signer: NewSigner(nil, nil, "Workly/test", func(context.Context, workspace.ID) ([]byte, []byte, error) {
			return nil, nil, errors.New("database down")
		}),
	}
	r := d.deliver(t.Context(), store.Task{WorkspaceID: "ws_a", Method: "POST", URL: endpoint.URL}, time.Second)
	if called || r.outcome != store.OutcomeNetworkError || !strings.Contains(r.err, "database down") {
		t.Fatalf("result = %+v, endpoint called: %v", r, called)
	}
}
