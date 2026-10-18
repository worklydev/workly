package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/worklydev/workly/internal/workspace"
)

// changesChannel carries change notices between replicas. Like
// notifyChannel it is database-wide, so notices carry their schema.
const changesChannel = "workly_changes"

// maxChangesPayload keeps a notice under Postgres's 8000-byte payload limit,
// with room for the schema and JSON.
const maxChangesPayload = 6000

type changesNotice struct {
	Schema     string         `json:"schema"`
	Workspaces []workspace.ID `json:"workspaces"`
}

func (s *Store) PublishChanges(ctx context.Context, ws []workspace.ID) error {
	for len(ws) > 0 {
		n, size := 0, 0
		for n < len(ws) && (n == 0 || size+len(ws[n])+3 <= maxChangesPayload) {
			size += len(ws[n]) + 3
			n++
		}
		_, err := s.pool.Exec(ctx,
			`SELECT pg_notify($1, json_build_object('schema', current_schema(), 'workspaces', $2::text[])::text)`,
			changesChannel, ws[:n])
		if err != nil {
			return err
		}
		ws = ws[n:]
	}
	return nil
}

// ListenChanges listens on a connection of its own, taken out of the pool:
// a pooled connection would carry the LISTEN to whoever uses it next.
func (s *Store) ListenChanges(ctx context.Context, ready func(), fn func([]workspace.ID)) error {
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := pooled.Hijack()
	defer conn.Close(context.Background())

	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `LISTEN `+changesChannel); err != nil {
		return err
	}
	ready()
	for {
		wctx, cancel := context.WithTimeout(ctx, watchInterval)
		n, err := conn.WaitForNotification(wctx)
		cancel()
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			// Quiet for a while; make sure the connection is still there.
			pctx, cancel := context.WithTimeout(ctx, watchInterval)
			err = conn.Ping(pctx)
			cancel()
			if err != nil && ctx.Err() == nil {
				return err
			}
		case err != nil:
			return err
		default:
			var notice changesNotice
			if json.Unmarshal([]byte(n.Payload), &notice) == nil && notice.Schema == schema {
				fn(notice.Workspaces)
			}
		}
	}
}
