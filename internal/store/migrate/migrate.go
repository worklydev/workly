// Package migrate applies embedded SQL migrations. It is shared by the
// postgres and sqlite stores, which each embed their own migration files.
//
// Migration files are named NNNN_description.sql and applied in version
// order, each in its own transaction. Applied versions are recorded in the
// schema_migrations table.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"time"
)

type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LockFunc acquires an exclusive, cross-process migration lock on conn and
// returns a function that releases it.
type LockFunc func(ctx context.Context, conn *sql.Conn) (unlock func(), err error)

var fileRe = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Load reads migrations from the root of fsys.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		m := fileRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migrate: invalid migration file name %q", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: version, Name: m[2], SQL: string(body)})
	}
	slices.SortFunc(out, func(a, b Migration) int { return a.Version - b.Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("migrate: duplicate version %04d", out[i].Version)
		}
	}
	return out, nil
}

const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at BIGINT NOT NULL
)`

// Apply runs all migrations that have not been applied yet.
func Apply(ctx context.Context, db *sql.DB, migrations []Migration, lock LockFunc) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if lock != nil {
		unlock, err := lock(ctx, conn)
		if err != nil {
			return fmt.Errorf("migrate: acquire lock: %w", err)
		}
		defer unlock()
	}

	if _, err := conn.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if err := applyOne(ctx, conn, m); err != nil {
			return fmt.Errorf("migrate: %04d_%s: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

// Pending reports whether any migration has not been applied. It returns an
// error if the database is unreachable or has never been migrated.
func Pending(ctx context.Context, db *sql.DB, migrations []Migration) (bool, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return false, err
	}
	for _, m := range migrations {
		if !applied[m.Version] {
			return true, nil
		}
	}
	return false, nil
}

func appliedVersions(ctx context.Context, conn *sql.Conn) (map[int]bool, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func applyOne(ctx context.Context, conn *sql.Conn, m Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Re-check inside the transaction: without a cross-process lock (SQLite),
	// another process may have applied this migration since we last looked.
	var done int
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM schema_migrations WHERE version = %d`, m.Version)).Scan(&done)
	if err != nil {
		return err
	}
	if done > 0 {
		return nil
	}

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	// Version and name are validated by fileRe, so formatting them into the
	// statement is safe and avoids placeholder differences between drivers.
	record := fmt.Sprintf(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (%d, '%s', %d)`,
		m.Version, m.Name, time.Now().UnixMilli())
	if _, err := tx.ExecContext(ctx, record); err != nil {
		return err
	}
	return tx.Commit()
}
