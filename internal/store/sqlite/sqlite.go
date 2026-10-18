// Package sqlite implements store.Store on SQLite, for `workly dev` and small
// single-replica self-hosted installs.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/migrate"
	"github.com/worklydev/workly/internal/store/sqlstore"
	"github.com/worklydev/workly/internal/workspace"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	*sqlstore.Store
	db         *sql.DB
	migrations []migrate.Migration
	// wake signals the in-process dispatcher when a task is enqueued.
	wake chan struct{}

	// listeners receive change notices; they're all in this process, since
	// one process serves a SQLite database.
	mu        sync.Mutex
	listeners map[*func([]workspace.ID)]struct{}
}

var _ store.Store = (*Store)(nil)

// Open opens (creating if needed) the database file at path.
func Open(path string) (*Store, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	migrations, err := migrate.Load(sub)
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("sqlite: create directory: %w", err)
		}
	}

	// _txlock=immediate takes the write lock at BEGIN, so concurrent writers
	// wait on busy_timeout instead of failing with SQLITE_BUSY when a read
	// lock can't be upgraded.
	dsn := "file:" + path + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	// SQLite allows one writer at a time. A single connection serializes all
	// access in-process, which avoids SQLITE_BUSY and is plenty for dev and
	// small installs.
	db.SetMaxOpenConns(1)
	if err := enableWAL(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, migrations: migrations, wake: make(chan struct{}, 1), listeners: map[*func([]workspace.ID)]struct{}{}}
	s.Store = sqlstore.New(db, sqlstore.Options{Notify: s.notify})
	return s, nil
}

// enableWAL switches the database file to WAL mode. The setting persists in
// the file, but switching needs an exclusive lock and SQLite doesn't apply
// busy_timeout to it, so retry briefly if another process has the file open.
func enableWAL(db *sql.DB) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var mode string
		err := db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("sqlite: enable WAL: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) Close() error { return s.db.Close() }

// Migrate relies on SQLite's own write lock (see _txlock) rather than a
// separate migration lock.
func (s *Store) Migrate(ctx context.Context) error {
	return migrate.Apply(ctx, s.db, s.migrations, nil)
}

func (s *Store) MigrationsPending(ctx context.Context) (bool, error) {
	return migrate.Pending(ctx, s.db, s.migrations)
}

func (s *Store) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Store) PublishChanges(ctx context.Context, ws []workspace.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for fn := range s.listeners {
		(*fn)(ws)
	}
	return nil
}

func (s *Store) ListenChanges(ctx context.Context, ready func(), fn func([]workspace.ID)) error {
	s.mu.Lock()
	s.listeners[&fn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.listeners, &fn)
		s.mu.Unlock()
	}()
	ready()
	<-ctx.Done()
	return ctx.Err()
}

// TryLead always succeeds: a SQLite database is served by a single process.
func (s *Store) TryLead(ctx context.Context) (store.Leader, error) {
	return &leader{wake: s.wake, lost: make(chan struct{})}, nil
}

type leader struct {
	wake chan struct{}
	lost chan struct{}
}

func (l *leader) Wake() <-chan struct{} { return l.wake }
func (l *leader) Lost() <-chan struct{} { return l.lost }
func (l *leader) Release()              {}
