package postgres_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/postgres"
	"github.com/worklydev/workly/internal/store/storetest"
	"github.com/worklydev/workly/internal/workspace"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) func() store.Store {
		url := storetest.PostgresURL(t)
		return func() store.Store { return open(t, url) }
	})
}

func open(t *testing.T, url string) *postgres.Store {
	t.Helper()
	s, err := postgres.Open(t.Context(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Only one replica may lead; releasing (or dying) hands leadership over.
// Installs in different schemas of one database each get their own leader.
func TestLeaderElection(t *testing.T) {
	url := storetest.PostgresURL(t)
	a, b := open(t, url), open(t, url)
	ctx := t.Context()

	leader, err := a.TryLead(ctx)
	if err != nil {
		t.Fatalf("TryLead(a): %v", err)
	}
	if _, err := b.TryLead(ctx); !errors.Is(err, store.ErrNotLeader) {
		t.Fatalf("TryLead(b) err = %v, want ErrNotLeader", err)
	}

	other := open(t, storetest.PostgresURL(t))
	otherLeader, err := other.TryLead(ctx)
	if err != nil {
		t.Fatalf("TryLead in another schema: %v", err)
	}
	otherLeader.Release()

	leader.Release()
	select {
	case <-leader.Lost():
	default:
		t.Fatal("Lost not closed after Release")
	}
	leaderB, err := b.TryLead(ctx)
	if err != nil {
		t.Fatalf("TryLead(b) after release: %v", err)
	}
	leaderB.Release()
}

// Wake-ups share one database-wide channel; a leader ignores other schemas'.
func TestLeaderWakeIgnoresOtherSchemas(t *testing.T) {
	ctx := t.Context()
	a, b := open(t, storetest.PostgresURL(t)), open(t, storetest.PostgresURL(t))
	for _, s := range []*postgres.Store{a, b} {
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := s.EnsureWorkspace(ctx, workspace.Default, "Default"); err != nil {
			t.Fatalf("EnsureWorkspace: %v", err)
		}
	}
	leader, err := a.TryLead(ctx)
	if err != nil {
		t.Fatalf("TryLead: %v", err)
	}
	defer leader.Release()

	enqueue := func(s *postgres.Store) {
		nt := store.NewTask{Queue: "q", Method: "POST", URL: "http://example.test", RunAt: time.Now()}
		if _, _, err := s.CreateTask(ctx, workspace.Default, nt, true); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}
	enqueue(b)
	select {
	case <-leader.Wake():
		t.Fatal("woken by an enqueue in another schema")
	case <-time.After(300 * time.Millisecond):
	}
	enqueue(a)
	select {
	case <-leader.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("no wake-up after an enqueue in its own schema")
	}
}

// Change notices reach every replica on the schema, in batches small enough
// for Postgres, and no replica of another install.
func TestChangesAcrossReplicas(t *testing.T) {
	ctx := t.Context()
	url := storetest.PostgresURL(t)
	a, b, other := open(t, url), open(t, url), open(t, storetest.PostgresURL(t))
	got, otherGot := storetest.Listen(t, b), storetest.Listen(t, other)

	if err := a.PublishChanges(ctx, []workspace.ID{"one"}); err != nil {
		t.Fatalf("PublishChanges: %v", err)
	}
	if ws := got.Next(t); len(ws) != 1 || ws[0] != "one" {
		t.Fatalf("notice = %v, want [one]", ws)
	}
	otherGot.None(t)

	var many []workspace.ID
	for i := range 2000 {
		many = append(many, workspace.ID(fmt.Sprintf("ws_%026d", i)))
	}
	if err := a.PublishChanges(ctx, many); err != nil {
		t.Fatalf("PublishChanges(2000): %v", err)
	}
	var all []workspace.ID
	for len(all) < len(many) {
		all = append(all, got.Next(t)...)
	}
	if len(all) != len(many) || all[0] != many[0] || all[len(all)-1] != many[len(many)-1] {
		t.Fatalf("got %d workspaces, want %d in order", len(all), len(many))
	}
}
