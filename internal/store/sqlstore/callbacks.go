package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// Callbacks (docs/callbacks.md) are tasks in store.CallbackQueue, enqueued
// in the transaction that finishes what they report on, so one is never
// lost or sent for something that didn't finish.

// enqueueCallback enqueues cb with body as a JSON POST and returns its
// task's ID. idemKey guards against enqueueing the same callback twice.
// The callback queue is created if it doesn't exist (or was deleted)
// rather than the callback dropped.
func (s *Store) enqueueCallback(ctx context.Context, q querier, ws workspace.ID, cb store.Callback,
	body []byte, idemKey string, now time.Time) (string, error) {
	headers := map[string]string{"Content-Type": "application/json"}
	for k, v := range cb.Headers {
		if strings.EqualFold(k, "Content-Type") {
			delete(headers, "Content-Type")
		}
		headers[k] = v
	}
	t, _, err := s.createTask(ctx, q, ws, store.NewTask{
		Queue: store.CallbackQueue, Method: "POST", URL: cb.URL, Headers: headers,
		Body: body, RunAt: now, IdempotencyKey: idemKey,
	}, true)
	return t.ID, err
}

// callbackArgs returns cb's columns for an insert or update: NULLs if cb
// is nil.
func callbackArgs(cb *store.Callback) (url, headers any, err error) {
	if cb == nil {
		return nil, nil, nil
	}
	h, err := marshalHeaders(cb.Headers)
	if err != nil {
		return nil, nil, err
	}
	return cb.URL, h, nil
}

func scanCallback(url sql.NullString, headers []byte) (*store.Callback, error) {
	if !url.Valid {
		return nil, nil
	}
	h, err := unmarshalHeaders(headers)
	if err != nil {
		return nil, err
	}
	return &store.Callback{URL: url.String, Headers: h}, nil
}

// finishKey is the idempotency key of the callback for a task or run
// finishing: each time it finishes (after a retry by hand, with more
// attempts, in another state or at another time) gets its own callback.
func finishKey(id string, attempts int, state string, t time.Time) string {
	return fmt.Sprintf("%s:%d:%s:%s", id, attempts, state, t.UTC().Format("20060102T150405.000Z"))
}

// callbackChunk bounds the IDs in one query, under SQLite's limit on
// parameters.
const callbackChunk = 500

// taskCallbacks enqueues the callbacks of the tasks with these IDs, which
// the transaction has just finished, and reports whether it enqueued any.
func (s *Store) taskCallbacks(ctx context.Context, tx *sql.Tx, ids []string) (bool, error) {
	enqueued := false
	for lo := 0; lo < len(ids); lo += callbackChunk {
		chunk := ids[lo:min(lo+callbackChunk, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		tasks, err := s.listTasks(ctx, tx, `SELECT `+taskColumns+` FROM tasks
			WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(chunk)), ", ")+`)
				AND callback_url IS NOT NULL AND finished_at IS NOT NULL
			ORDER BY id`, args...)
		if err != nil {
			return false, err
		}
		for _, t := range tasks {
			if !t.State.Finished() {
				continue
			}
			cbID, err := s.enqueueCallback(ctx, tx, t.WorkspaceID, *t.OnComplete, t.CallbackBody(),
				finishKey(t.ID, t.AttemptCount, string(t.State), *t.FinishedAt), *t.FinishedAt)
			if err != nil {
				return false, err
			}
			if _, err := s.exec(ctx, tx, `UPDATE tasks SET callback_task_id = ? WHERE id = ?`, cbID, t.ID); err != nil {
				return false, err
			}
			enqueued = true
		}
	}
	return enqueued, nil
}
