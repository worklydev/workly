package live_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/live"
	"github.com/worklydev/workly/internal/store/sqlite"
)

// A stream hears about its own workspace's changes only.
func TestHub(t *testing.T) {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "workly.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := live.New(st, slog.New(slog.DiscardHandler))
	a, cancelA := hub.Subscribe("a")
	defer cancelA()
	b, cancelB := hub.Subscribe("b")
	defer cancelB()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()
	// Listening starts with a catch-up signal to every stream.
	for _, c := range []<-chan struct{}{a, b} {
		receive(t, c)
	}

	for range 10 {
		hub.Changed("a")
	}
	receive(t, a)
	quiet(t, b)

	cancel()
	<-done
	select {
	case <-hub.Stopped():
	default:
		t.Fatal("Stopped not closed after Run returned")
	}
}

func receive(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("no signal")
	}
}

func quiet(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
		t.Fatal("unexpected signal")
	case <-time.After(400 * time.Millisecond):
	}
}
