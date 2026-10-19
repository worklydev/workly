package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/id"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// A group's counts change in the same transaction as its tasks' states, and
// the group completes (enqueueing its callback) in the transaction that
// finishes its last task or closes it. So there is nothing to recover after
// a crash, and the status guard on completion (WHERE status = 'running',
// under the row lock the count update took) makes it happen once.

const groupColumns = `id, workspace_id, name, status, idempotency_key, total, succeeded, failed, cancelled,
	callback_queue, callback_url, callback_headers, callback_task_id, created_at, closed_at, finished_at`

func scanGroup(row interface{ Scan(...any) error }) (store.Group, error) {
	var (
		g                                     store.Group
		name, idemKey, cbQueue, cbURL, cbTask sql.NullString
		cbHeaders                             []byte
		created, closed, finished             dbTime
	)
	err := row.Scan(&g.ID, &g.WorkspaceID, &name, &g.Status, &idemKey, &g.Total, &g.Succeeded, &g.Failed, &g.Cancelled,
		&cbQueue, &cbURL, &cbHeaders, &cbTask, &created, &closed, &finished)
	if err != nil {
		return g, err
	}
	g.Name, g.IdempotencyKey, g.CallbackTaskID = name.String, idemKey.String, cbTask.String
	if cbURL.Valid {
		g.OnComplete = &store.Callback{Queue: cbQueue.String, URL: cbURL.String}
		if g.OnComplete.Headers, err = unmarshalHeaders(cbHeaders); err != nil {
			return g, err
		}
	}
	g.CreatedAt, g.ClosedAt, g.FinishedAt = created.Time, closed.ptr(), finished.ptr()
	return g, nil
}

func (s *Store) CreateGroup(ctx context.Context, ws workspace.ID, ng store.NewGroup) (store.Group, bool, error) {
	var cbQueue, cbURL, cbHeaders any
	if cb := ng.OnComplete; cb != nil {
		headers, err := marshalHeaders(cb.Headers)
		if err != nil {
			return store.Group{}, false, err
		}
		cbQueue, cbURL, cbHeaders = cb.Queue, cb.URL, headers
	}
	var (
		g       store.Group
		created bool
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		groupID := id.New("grp")
		res, err := s.exec(ctx, tx, `INSERT INTO task_groups (id, workspace_id, name, status, idempotency_key,
				callback_queue, callback_url, callback_headers, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace_id, idempotency_key) WHERE idempotency_key IS NOT NULL
			DO NOTHING`,
			groupID, ws, nullString(ng.Name), store.GroupOpen, nullString(ng.IdempotencyKey),
			cbQueue, cbURL, cbHeaders, s.ts(time.Now()))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if created = n == 1; !created {
			g, err = scanGroup(s.queryRow(ctx, tx, `SELECT `+groupColumns+` FROM task_groups
				WHERE workspace_id = ? AND idempotency_key = ?`, ws, ng.IdempotencyKey))
			return err
		}
		g, err = s.getGroup(ctx, tx, ws, groupID)
		return err
	})
	return g, created, err
}

func (s *Store) GetGroup(ctx context.Context, ws workspace.ID, id string) (store.Group, error) {
	return s.getGroup(ctx, s.db, ws, id)
}

func (s *Store) getGroup(ctx context.Context, q querier, ws workspace.ID, id string) (store.Group, error) {
	g, err := scanGroup(s.queryRow(ctx, q,
		`SELECT `+groupColumns+` FROM task_groups WHERE workspace_id = ? AND id = ?`, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Group{}, store.ErrNotFound
	}
	return g, err
}

func (s *Store) ListGroups(ctx context.Context, ws workspace.ID, f store.GroupFilter) ([]store.Group, string, error) {
	query := `SELECT ` + groupColumns + ` FROM task_groups WHERE workspace_id = ?`
	args := []any{ws}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Name != "" {
		query += ` AND name = ?`
		args = append(args, f.Name)
	}
	if f.Cursor != "" {
		query += ` AND id < ?`
		args = append(args, f.Cursor)
	}
	// One extra row tells whether there is a next page.
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit+1)
	rows, err := s.query(ctx, s.db, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	groups := []store.Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, "", err
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var cursor string
	if len(groups) > f.Limit {
		groups = groups[:f.Limit]
		cursor = groups[len(groups)-1].ID
	}
	return groups, cursor, nil
}

func (s *Store) CloseGroup(ctx context.Context, ws workspace.ID, id string) (store.Group, error) {
	var (
		g        store.Group
		callback bool
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// Stored to the millisecond, so the group returned matches the stored one.
		now := time.Now().UTC().Truncate(time.Millisecond)
		var err error
		g, err = scanGroup(s.queryRow(ctx, tx, `UPDATE task_groups SET status = ?, closed_at = ?
			WHERE workspace_id = ? AND id = ? AND status = ?
			RETURNING `+groupColumns, store.GroupRunning, s.ts(now), ws, id, store.GroupOpen))
		if errors.Is(err, sql.ErrNoRows) {
			// Missing, or closed already.
			g, err = s.getGroup(ctx, tx, ws, id)
			return err
		}
		if err != nil {
			return err
		}
		callback, err = s.completeGroup(ctx, tx, &g, now)
		return err
	})
	if callback {
		s.wake()
	}
	return g, err
}

// completeGroup completes g, which the transaction has just updated, if it
// is closed and its tasks are all finished, and enqueues its callback. It
// reports whether it enqueued one.
func (s *Store) completeGroup(ctx context.Context, tx *sql.Tx, g *store.Group, now time.Time) (bool, error) {
	if g.Status != store.GroupRunning || g.Remaining() > 0 {
		return false, nil
	}
	status := store.GroupFailed
	if g.Succeeded == g.Total {
		status = store.GroupSucceeded
	}
	res, err := s.exec(ctx, tx, `UPDATE task_groups SET status = ?, finished_at = ?
		WHERE id = ? AND status = ?`, status, s.ts(now), g.ID, store.GroupRunning)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	g.Status, g.FinishedAt = status, &now
	if g.OnComplete == nil {
		return false, nil
	}
	// The callback queue may have been deleted since the group was created;
	// recreate it rather than drop the callback.
	if err := s.upsertQueue(ctx, tx, g.WorkspaceID, g.OnComplete.Queue, store.DefaultQueueConfig(), false); err != nil {
		return false, err
	}
	headers := map[string]string{"Content-Type": "application/json"}
	for k, v := range g.OnComplete.Headers {
		if strings.EqualFold(k, "Content-Type") {
			delete(headers, "Content-Type")
		}
		headers[k] = v
	}
	// The group's ID as idempotency key is a second guard against a
	// duplicate callback.
	cb, _, err := s.createTask(ctx, tx, g.WorkspaceID, store.NewTask{
		Queue: g.OnComplete.Queue, Method: "POST", URL: g.OnComplete.URL, Headers: headers,
		Body: g.CallbackBody(), RunAt: now, IdempotencyKey: g.ID,
	})
	if err != nil {
		return false, err
	}
	g.CallbackTaskID = cb.ID
	_, err = s.exec(ctx, tx, `UPDATE task_groups SET callback_task_id = ? WHERE id = ?`, cb.ID, g.ID)
	return err == nil, err
}

func (s *Store) DeleteFinishedGroups(ctx context.Context, before time.Time, limit int) (int, error) {
	res, err := s.exec(ctx, s.db, `DELETE FROM task_groups WHERE id IN (
			SELECT id FROM task_groups WHERE finished_at < ? ORDER BY finished_at LIMIT ?
		)`, s.ts(before), limit)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// joinGroups adds n new tasks to each group, which must be open.
func (s *Store) joinGroups(ctx context.Context, tx *sql.Tx, ws workspace.ID, added map[string]int) error {
	for _, groupID := range sortedKeys(added) {
		res, err := s.exec(ctx, tx, `UPDATE task_groups SET total = total + ?
			WHERE workspace_id = ? AND id = ? AND status = ?`, added[groupID], ws, groupID, store.GroupOpen)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 1 {
			continue
		}
		if _, err := s.getGroup(ctx, tx, ws, groupID); errors.Is(err, store.ErrNotFound) {
			return &store.GroupError{GroupID: groupID, Err: store.ErrGroupNotFound}
		} else if err != nil {
			return err
		}
		return &store.GroupError{GroupID: groupID, Err: store.ErrGroupClosed}
	}
	return nil
}

// groupCounts are changes to groups' task counts, by group ID.
type groupCounts map[string]*groupDelta

type groupDelta struct{ total, succeeded, failed, cancelled int }

func (d *groupDelta) of(state store.TaskState) *int {
	switch state {
	case store.TaskSucceeded:
		return &d.succeeded
	case store.TaskFailed:
		return &d.failed
	case store.TaskCancelled:
		return &d.cancelled
	}
	return nil // pending and running tasks are counted only in total
}

func (c groupCounts) delta(groupID string) *groupDelta {
	if c[groupID] == nil {
		c[groupID] = &groupDelta{}
	}
	return c[groupID]
}

// move records n tasks of the group going from one state to another.
func (c groupCounts) move(groupID string, from, to store.TaskState, n int) {
	if groupID == "" || from == to {
		return
	}
	d := c.delta(groupID)
	if p := d.of(from); p != nil {
		*p -= n
	}
	if p := d.of(to); p != nil {
		*p += n
	}
}

// remove records n tasks in the given state leaving the group.
func (c groupCounts) remove(groupID string, state store.TaskState, n int) {
	d := c.delta(groupID)
	d.total -= n
	if p := d.of(state); p != nil {
		*p -= n
	}
}

// apply updates the counts of the groups still open or running, completes
// those that are done, and reports whether any callback was enqueued.
// Groups are updated in ID order, so concurrent transactions lock them in
// the same order.
func (s *Store) applyGroupCounts(ctx context.Context, tx *sql.Tx, c groupCounts) (bool, error) {
	callbacks := false
	// Stored to the millisecond, so the group returned matches the stored one.
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, groupID := range sortedKeys(c) {
		d := c[groupID]
		if *d == (groupDelta{}) {
			continue
		}
		g, err := scanGroup(s.queryRow(ctx, tx, `UPDATE task_groups SET total = total + ?,
				succeeded = succeeded + ?, failed = failed + ?, cancelled = cancelled + ?
			WHERE id = ? AND status IN (?, ?)
			RETURNING `+groupColumns,
			d.total, d.succeeded, d.failed, d.cancelled, groupID, store.GroupOpen, store.GroupRunning))
		if errors.Is(err, sql.ErrNoRows) {
			continue // completed (counts are frozen) or deleted
		}
		if err != nil {
			return false, err
		}
		cb, err := s.completeGroup(ctx, tx, &g, now)
		if err != nil {
			return false, err
		}
		callbacks = callbacks || cb
	}
	return callbacks, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
