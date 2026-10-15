// Package postgres implements store.Store on PostgreSQL (14+).
//
// To keep Workly's tables in a dedicated schema, create the schema and pass
// search_path in the connection URL, e.g. postgres://…/app?search_path=workly.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/migrate"
	"github.com/worklydev/workly/internal/store/sqlstore"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockKey is the pg_advisory_lock key held while migrating, so
// replicas starting at the same time don't race.
const migrationLockKey int64 = 7337_0001

// leaderLockClass is the first key of the two-key advisory lock held by the
// dispatcher leader. The second is a hash of the schema, so separate Workly
// installs sharing a database (in different schemas) each get a leader.
const leaderLockClass = 7337

// notifyChannel carries enqueue wake-ups to the leader. It is database-wide,
// so each wake-up carries its schema and leaders ignore other installs'.
const notifyChannel = "workly_tasks"

type Store struct {
	*sqlstore.Store
	pool       *pgxpool.Pool
	db         *sql.DB
	migrations []migrate.Migration
	notifier   *notifier
}

var _ store.Store = (*Store)(nil)

// Open creates a connection pool. It does not connect until first use.
func Open(ctx context.Context, url string) (*Store, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	n := newNotifier(pool)
	return &Store{
		Store:      sqlstore.New(db, sqlstore.Options{Postgres: true, Notify: n.notify}),
		pool:       pool,
		db:         db,
		migrations: migrations,
		notifier:   n,
	}, nil
}

// notifier sends wake-ups to the leader, wherever it runs. It sends them
// outside the enqueueing transaction: a transaction that notifies holds a
// database-wide lock through its commit, which would serialize every enqueue
// behind one WAL flush at a time. Wake-ups are coalesced so at most one is in
// flight; the leader only needs to know that something changed.
type notifier struct {
	pool    *pgxpool.Pool
	pending chan struct{}
	stop    context.CancelFunc
	done    chan struct{}
}

func newNotifier(pool *pgxpool.Pool) *notifier {
	ctx, cancel := context.WithCancel(context.Background())
	n := &notifier{pool: pool, pending: make(chan struct{}, 1), stop: cancel, done: make(chan struct{})}
	go n.run(ctx)
	return n
}

func (n *notifier) notify() {
	select {
	case n.pending <- struct{}{}:
	default:
	}
}

func (n *notifier) run(ctx context.Context) {
	defer close(n.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.pending:
			// A lost wake-up only delays dispatch until the next poll.
			_, _ = n.pool.Exec(ctx, `SELECT pg_notify($1, current_schema())`, notifyChannel)
		}
	}
}

func (n *notifier) close() {
	n.stop()
	<-n.done
}

func loadMigrations() ([]migrate.Migration, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	return migrate.Load(sub)
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Close() error {
	s.notifier.close()
	err := s.db.Close()
	s.pool.Close()
	return err
}

func (s *Store) Migrate(ctx context.Context) error {
	return migrate.Apply(ctx, s.db, s.migrations, advisoryLock)
}

func (s *Store) MigrationsPending(ctx context.Context) (bool, error) {
	return migrate.Pending(ctx, s.db, s.migrations)
}

func advisoryLock(ctx context.Context, conn *sql.Conn) (func(), error) {
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return nil, err
	}
	return func() {
		// Use a fresh context: the caller's may already be cancelled.
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}, nil
}

// TryLead takes the leader advisory lock on a dedicated connection, which
// also LISTENs for enqueue notifications. If the process dies the connection
// drops, Postgres releases the lock and another replica takes over.
func (s *Store) TryLead(ctx context.Context) (store.Leader, error) {
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	// Take the connection out of the pool: the lock lives as long as it does.
	conn := pooled.Hijack()

	var ok bool
	var schema string
	err = conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock($1, hashtext(current_schema())), current_schema()`, leaderLockClass,
	).Scan(&ok, &schema)
	if err == nil && !ok {
		err = store.ErrNotLeader
	}
	if err == nil {
		_, err = conn.Exec(ctx, `LISTEN `+notifyChannel)
	}
	if err != nil {
		conn.Close(context.Background())
		return nil, err
	}

	lctx, cancel := context.WithCancel(context.Background())
	l := &leader{
		conn:   conn,
		schema: schema,
		cancel: cancel,
		wake:   make(chan struct{}, 1),
		lost:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go l.watch(lctx)
	return l, nil
}

type leader struct {
	conn     *pgx.Conn
	schema   string
	cancel   context.CancelFunc
	wake     chan struct{}
	lost     chan struct{}
	done     chan struct{}
	lostOnce sync.Once
}

// watchInterval bounds how long a dead leader connection goes unnoticed.
const watchInterval = 5 * time.Second

// watch forwards notifications and checks the connection is alive. It owns
// the connection until it returns.
func (l *leader) watch(ctx context.Context) {
	defer close(l.done)
	for {
		wctx, cancel := context.WithTimeout(ctx, watchInterval)
		n, err := l.conn.WaitForNotification(wctx)
		cancel()
		switch {
		case ctx.Err() != nil:
			return
		case err == nil && n.Payload != "" && n.Payload != l.schema:
			// Another install's wake-up. An empty payload is from a replica
			// that predates schema payloads, mid rolling deploy.
			continue
		case err == nil:
			select {
			case l.wake <- struct{}{}:
			default:
			}
			continue
		case errors.Is(err, context.DeadlineExceeded):
			// No notifications for a while; make sure we still hold the lock.
			pctx, cancel := context.WithTimeout(ctx, watchInterval)
			err = l.conn.Ping(pctx)
			cancel()
			if err == nil || ctx.Err() != nil {
				continue
			}
		}
		l.lostOnce.Do(func() { close(l.lost) })
		return
	}
}

func (l *leader) Wake() <-chan struct{} { return l.wake }
func (l *leader) Lost() <-chan struct{} { return l.lost }

// Release closes the connection, which releases the lock.
func (l *leader) Release() {
	l.cancel()
	<-l.done
	l.lostOnce.Do(func() { close(l.lost) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l.conn.Close(ctx)
}
