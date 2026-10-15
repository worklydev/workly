package dispatch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
