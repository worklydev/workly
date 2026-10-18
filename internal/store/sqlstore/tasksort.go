package sqlstore

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/store"
)

// taskOrder is how ListTasks orders and pages tasks: by an expression, then
// by ID, both in the same direction, so a page continues after the
// (value, id) pair of the last task on the previous one.
type taskOrder struct {
	sort store.TaskSort
	asc  bool
	// expr is the sort expression; empty for SortCreated, which is ID order
	// (IDs are time-ordered ULIDs).
	expr string
	// key returns a task's value of expr, as an integer: microseconds for
	// times, which is finer than either database stores them.
	key func(store.Task) int64
	// arg turns a key back into a query argument.
	arg func(int64) any
}

func (s *Store) taskOrder(f store.TaskFilter) (taskOrder, error) {
	o := taskOrder{sort: f.Sort, asc: f.Ascending}
	micros := func(v int64) any { return s.ts(time.UnixMicro(v)) }
	switch f.Sort {
	case "", store.SortCreated:
		o.sort = store.SortCreated
	case store.SortRunAt:
		o.expr = "run_at"
		o.key = func(t store.Task) int64 { return t.RunAt.UnixMicro() }
		o.arg = micros
	case store.SortLastAttempt:
		// Never attempted counts as oldest, in both databases (which put
		// NULLs at different ends).
		o.expr = "COALESCE(last_attempt_at, 0)"
		if s.opts.Postgres {
			o.expr = "COALESCE(last_attempt_at, 'epoch'::timestamptz)"
		}
		o.key = func(t store.Task) int64 {
			if t.LastAttemptAt == nil {
				return 0
			}
			return t.LastAttemptAt.UnixMicro()
		}
		o.arg = micros
	case store.SortAttempts:
		o.expr = "attempt_count"
		o.key = func(t store.Task) int64 { return int64(t.AttemptCount) }
		o.arg = func(v int64) any { return v }
	default:
		return o, fmt.Errorf("unknown task sort %q", f.Sort)
	}
	return o, nil
}

// clause returns the ORDER BY clause and, for a cursor, the condition that
// continues after it.
func (o taskOrder) clause(cursor string) (where string, args []any, orderBy string, err error) {
	dir, cmp := "DESC", "<"
	if o.asc {
		dir, cmp = "ASC", ">"
	}
	if o.expr == "" {
		orderBy = " ORDER BY id " + dir
	} else {
		orderBy = fmt.Sprintf(" ORDER BY %s %s, id %s", o.expr, dir, dir)
	}
	if cursor == "" {
		return "", nil, orderBy, nil
	}
	if o.expr == "" {
		// A bare task ID, as cursors were before tasks could be sorted.
		if !strings.HasPrefix(cursor, "task_") {
			return "", nil, "", store.ErrInvalidCursor
		}
		return " AND id " + cmp + " ?", []any{cursor}, orderBy, nil
	}
	v, id, err := o.decode(cursor)
	if err != nil {
		return "", nil, "", err
	}
	// Row values compare (expr, id) pairs in one step, in both databases.
	return fmt.Sprintf(" AND (%s, id) %s (?, ?)", o.expr, cmp), []any{o.arg(v), id}, orderBy, nil
}

// cursor encodes where the next page starts, after t.
func (o taskOrder) cursor(t store.Task) string {
	if o.expr == "" {
		return t.ID
	}
	raw := fmt.Sprintf("%s|%t|%d|%s", o.sort, o.asc, o.key(t), t.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func (o taskOrder) decode(cursor string) (v int64, id string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", store.ErrInvalidCursor
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 || parts[0] != string(o.sort) || parts[1] != strconv.FormatBool(o.asc) ||
		!strings.HasPrefix(parts[3], "task_") {
		return 0, "", store.ErrInvalidCursor
	}
	v, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, "", store.ErrInvalidCursor
	}
	return v, parts[3], nil
}
