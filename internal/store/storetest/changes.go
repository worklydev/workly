package storetest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

func testChanges(t *testing.T, open func() store.Store) {
	s := migrated(t, open)
	got := Listen(t, s)

	if err := s.PublishChanges(t.Context(), []workspace.ID{"a", "b"}); err != nil {
		t.Fatalf("PublishChanges: %v", err)
	}
	if ws := got.Next(t); !slices.Equal(ws, []workspace.ID{"a", "b"}) {
		t.Fatalf("notice = %v, want [a b]", ws)
	}
}

// Notices receives a store's change notices.
type Notices chan []workspace.ID

// Listen listens to the store's change notices until the test ends.
func Listen(t *testing.T, s store.Store) Notices {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	got := make(Notices, 100)
	go func() {
		done <- s.ListenChanges(ctx, func() { close(ready) }, func(ws []workspace.ID) { got <- ws })
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("ListenChanges: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("ListenChanges not ready")
	}
	return got
}

// Next returns the next notice, failing the test if none arrives soon.
func (n Notices) Next(t *testing.T) []workspace.ID {
	t.Helper()
	select {
	case ws := <-n:
		return ws
	case <-time.After(5 * time.Second):
		t.Fatal("no change notice")
		return nil
	}
}

// None fails the test if a notice arrives soon.
func (n Notices) None(t *testing.T) {
	t.Helper()
	select {
	case ws := <-n:
		t.Fatalf("unexpected change notice: %v", ws)
	case <-time.After(300 * time.Millisecond):
	}
}
