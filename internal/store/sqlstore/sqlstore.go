// Package sqlstore holds the SQL shared by the postgres and sqlite stores.
//
// Queries are written once with ? placeholders and portable SQL. The dialect
// differences are small: placeholder syntax, how timestamps are stored
// (TIMESTAMPTZ vs Unix milliseconds), row locking and wake-up notifications.
// The postgres and sqlite packages embed Store and add what is truly
// database-specific (migrations, leader election).
package sqlstore

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/worklydev/workly/internal/id"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

type Options struct {
	// Postgres selects $n placeholders, TIMESTAMPTZ values and row locking.
	// Otherwise the SQLite dialect is used.
	Postgres bool
	// Notify is called after a commit that may have made tasks due sooner:
	// an enqueue, a retry, a resumed queue or new limits. It wakes the
	// dispatcher, which may run in another process.
	Notify func()
}

type Store struct {
	db   *sql.DB
	opts Options
	// insertTask is the enqueue statement, built once.
	insertTask string
}

func New(db *sql.DB, opts Options) *Store {
	s := &Store{db: db, opts: opts}
	s.insertTask = s.insertTaskQuery()
	return s
}

// rebind converts ? placeholders to $1, $2, … for Postgres. Queries in this
// package never contain a literal ?.
func (s *Store) rebind(query string) string {
	if !s.opts.Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ts converts a time to the dialect's storage type.
func (s *Store) ts(t time.Time) any {
	if s.opts.Postgres {
		return t.UTC()
	}
	return t.UnixMilli()
}

// durationMillis is an attempt's duration in whole milliseconds, as its
// stored start and finish give it, so it matches the attempt's duration_ms.
func (s *Store) durationMillis(a store.Attempt) int64 {
	precision := time.Millisecond
	if s.opts.Postgres {
		precision = time.Microsecond
	}
	return a.FinishedAt.Truncate(precision).Sub(a.StartedAt.Truncate(precision)).Milliseconds()
}

func (s *Store) tsOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return s.ts(*t)
}

// dbTime scans either storage type into a UTC time.
type dbTime struct {
	Time  time.Time
	Valid bool
}

func (t *dbTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*t = dbTime{}
	case time.Time:
		*t = dbTime{Time: v.UTC(), Valid: true}
	case int64:
		*t = dbTime{Time: time.UnixMilli(v).UTC(), Valid: true}
	default:
		return fmt.Errorf("cannot scan %T into a timestamp", src)
	}
	return nil
}

func (t dbTime) ptr() *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) exec(ctx context.Context, q querier, query string, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, s.rebind(query), args...)
}

func (s *Store) query(ctx context.Context, q querier, query string, args ...any) (*sql.Rows, error) {
	return q.QueryContext(ctx, s.rebind(query), args...)
}

func (s *Store) queryRow(ctx context.Context, q querier, query string, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, s.rebind(query), args...)
}

// inTx runs fn in a transaction, committing if it returns nil.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// wake tells the dispatcher that tasks may be due sooner. Call it after
// committing.
func (s *Store) wake() {
	if s.opts.Notify != nil {
		s.opts.Notify()
	}
}

// cleanText makes externally sourced text storable: Postgres TEXT rejects
// invalid UTF-8 and NUL bytes.
func cleanText(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return cleanText(s)
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return int64(*p)
}

// nullIfZero stores 0 ("unlimited") as NULL.
func nullIfZero[T int | float64](v T) any {
	if v == 0 {
		return nil
	}
	return v
}

// millis is a nullable millisecond column as a duration.
func millis(v sql.NullInt64) *time.Duration {
	if !v.Valid {
		return nil
	}
	d := time.Duration(v.Int64) * time.Millisecond
	return &d
}

func nullMillis(p *time.Duration) any {
	if p == nil {
		return nil
	}
	return p.Milliseconds()
}

func marshalHeaders(h map[string]string) (string, error) {
	if h == nil {
		h = map[string]string{}
	}
	clean := make(map[string]string, len(h))
	for k, v := range h {
		clean[cleanText(k)] = cleanText(v)
	}
	b, err := json.Marshal(clean)
	return string(b), err
}

func unmarshalHeaders(b []byte) (map[string]string, error) {
	h := map[string]string{}
	if len(b) == 0 {
		return h, nil
	}
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("decode headers: %w", err)
	}
	return h, nil
}

// Workspaces

func (s *Store) EnsureWorkspace(ctx context.Context, id workspace.ID, name string) (store.Workspace, error) {
	_, err := s.exec(ctx, s.db,
		`INSERT INTO workspaces (id, name, created_at) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		id, name, s.ts(time.Now()))
	if err != nil {
		return store.Workspace{}, err
	}
	return s.GetWorkspace(ctx, id)
}

func (s *Store) GetWorkspace(ctx context.Context, id workspace.ID) (store.Workspace, error) {
	var (
		w         store.Workspace
		createdAt dbTime
	)
	err := s.queryRow(ctx, s.db,
		`SELECT id, name, created_at FROM workspaces WHERE id = ?`, id,
	).Scan(&w.ID, &w.Name, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Workspace{}, store.ErrNotFound
	}
	w.CreatedAt = createdAt.Time
	return w, err
}

// Queues

const queueColumns = `workspace_id, name, paused, max_attempts, min_backoff_ms, max_backoff_ms,
	backoff_multiplier, timeout_ms, retry_on_4xx, rate_per_second, max_concurrency,
	created_at, updated_at`

func scanQueue(row interface{ Scan(...any) error }) (store.Queue, error) {
	var (
		q                               store.Queue
		minBackoff, maxBackoff, timeout int64
		rate                            sql.NullFloat64
		concurrency                     sql.NullInt64
		createdAt, updatedAt            dbTime
	)
	err := row.Scan(&q.WorkspaceID, &q.Name, &q.Paused, &q.MaxAttempts, &minBackoff, &maxBackoff,
		&q.BackoffMultiplier, &timeout, &q.RetryOn4xx, &rate, &concurrency,
		&createdAt, &updatedAt)
	q.RatePerSecond, q.MaxConcurrency = rate.Float64, int(concurrency.Int64)
	q.MinBackoff = time.Duration(minBackoff) * time.Millisecond
	q.MaxBackoff = time.Duration(maxBackoff) * time.Millisecond
	q.Timeout = time.Duration(timeout) * time.Millisecond
	q.CreatedAt, q.UpdatedAt = createdAt.Time, updatedAt.Time
	return q, err
}

func (s *Store) PutQueue(ctx context.Context, ws workspace.ID, name string, cfg store.QueueConfig) (store.Queue, bool, error) {
	var (
		q       store.Queue
		created bool
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := s.getQueue(ctx, tx, ws, name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			created = true
		case err != nil:
			return err
		}
		if err := s.upsertQueue(ctx, tx, ws, name, cfg, true); err != nil {
			return err
		}
		q, err = s.getQueue(ctx, tx, ws, name)
		return err
	})
	if err != nil {
		return store.Queue{}, false, err
	}
	// New limits may let the dispatcher deliver more right away.
	s.wake()
	return q, created, nil
}

// upsertQueue inserts the queue, and replaces the configuration of an
// existing one if replace is set.
func (s *Store) upsertQueue(ctx context.Context, q querier, ws workspace.ID, name string, cfg store.QueueConfig, replace bool) error {
	onConflict := `DO NOTHING`
	if replace {
		onConflict = `DO UPDATE SET
			max_attempts = excluded.max_attempts,
			min_backoff_ms = excluded.min_backoff_ms,
			max_backoff_ms = excluded.max_backoff_ms,
			backoff_multiplier = excluded.backoff_multiplier,
			timeout_ms = excluded.timeout_ms,
			retry_on_4xx = excluded.retry_on_4xx,
			rate_per_second = excluded.rate_per_second,
			max_concurrency = excluded.max_concurrency,
			updated_at = excluded.updated_at`
	}
	now := s.ts(time.Now())
	_, err := s.exec(ctx, q, `INSERT INTO queues (`+queueColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (workspace_id, name) `+onConflict,
		ws, name, false, cfg.MaxAttempts, cfg.MinBackoff.Milliseconds(), cfg.MaxBackoff.Milliseconds(),
		cfg.BackoffMultiplier, cfg.Timeout.Milliseconds(), cfg.RetryOn4xx,
		nullIfZero(cfg.RatePerSecond), nullIfZero(cfg.MaxConcurrency), now, now)
	return err
}

func (s *Store) GetQueue(ctx context.Context, ws workspace.ID, name string) (store.Queue, error) {
	return s.getQueue(ctx, s.db, ws, name)
}

func (s *Store) getQueue(ctx context.Context, q querier, ws workspace.ID, name string) (store.Queue, error) {
	queue, err := scanQueue(s.queryRow(ctx, q,
		`SELECT `+queueColumns+` FROM queues WHERE workspace_id = ? AND name = ?`, ws, name))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Queue{}, store.ErrNotFound
	}
	return queue, err
}

func (s *Store) ListQueues(ctx context.Context, ws workspace.ID) ([]store.Queue, error) {
	return s.listQueues(ctx,
		`SELECT `+queueColumns+` FROM queues WHERE workspace_id = ? ORDER BY name`, ws)
}

func (s *Store) listQueues(ctx context.Context, query string, args ...any) ([]store.Queue, error) {
	rows, err := s.query(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	queues := []store.Queue{}
	for rows.Next() {
		q, err := scanQueue(rows)
		if err != nil {
			return nil, err
		}
		queues = append(queues, q)
	}
	return queues, rows.Err()
}

func (s *Store) DeleteQueue(ctx context.Context, ws workspace.ID, name string) error {
	var callbacks bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// Locked, so no task joins the queue (an insert's foreign key check
		// waits) between counting its grouped tasks and deleting it.
		var found int
		err := s.queryRow(ctx, tx, `SELECT 1 FROM queues WHERE workspace_id = ? AND name = ?`+s.forUpdate(),
			ws, name).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		// The queue's tasks leave their groups, which may complete without
		// them. Deleting them reports the state each one was in, even if an
		// attempt finished or it was cancelled a moment ago; counting first
		// and deleting after would not.
		rows, err := s.query(ctx, tx, `DELETE FROM tasks
			WHERE workspace_id = ? AND queue = ? AND group_id IS NOT NULL
			RETURNING group_id, state`, ws, name)
		if err != nil {
			return err
		}
		counts := groupCounts{}
		for rows.Next() {
			var (
				groupID string
				state   store.TaskState
			)
			if err := rows.Scan(&groupID, &state); err != nil {
				rows.Close()
				return err
			}
			counts.remove(groupID, state, 1)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// The rest of its tasks go by cascade.
		if _, err := s.exec(ctx, tx, `DELETE FROM queues WHERE workspace_id = ? AND name = ?`, ws, name); err != nil {
			return err
		}
		callbacks, err = s.applyGroupCounts(ctx, tx, counts)
		return err
	})
	if callbacks {
		s.wake()
	}
	return err
}

func (s *Store) SetQueuePaused(ctx context.Context, ws workspace.ID, name string, paused bool) (store.Queue, error) {
	var q store.Queue
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := s.exec(ctx, tx, `UPDATE queues SET paused = ?, updated_at = ?
			WHERE workspace_id = ? AND name = ?`, paused, s.ts(time.Now()), ws, name)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return store.ErrNotFound
		}
		q, err = s.getQueue(ctx, tx, ws, name)
		return err
	})
	if err != nil {
		return store.Queue{}, err
	}
	if !paused {
		s.wake()
	}
	return q, nil
}

// cappedCount counts a queue's tasks matching cond, stopping at
// store.StatsCountLimit. It's a correlated subquery on queues q.
func cappedCount(cond string) string {
	return fmt.Sprintf(`(SELECT count(*) FROM (SELECT 1 FROM tasks t
		WHERE t.workspace_id = q.workspace_id AND t.queue = q.name AND %s LIMIT %d) c)`,
		cond, store.StatsCountLimit)
}

func queueStatsQuery() string {
	return `SELECT q.workspace_id, q.name, q.paused, ` +
		cappedCount(`t.state = 'pending' AND t.run_at <= ?`) + `, ` +
		cappedCount(`t.state = 'pending' AND t.run_at > ?`) + `, ` +
		cappedCount(`t.state = 'running'`) + `, ` +
		// A failed task always has finished_at; saying so lets the count use
		// tasks_queue_finished.
		cappedCount(`t.state = 'failed' AND t.finished_at IS NOT NULL`) + `,
		(SELECT min(t.run_at) FROM tasks t WHERE t.workspace_id = q.workspace_id AND t.queue = q.name
			AND t.state = 'pending' AND t.run_at <= ?)
		FROM queues q`
}

func (s *Store) QueueStats(ctx context.Context, ws workspace.ID, name string, now time.Time) (map[string]store.QueueStats, error) {
	where, args := ` WHERE q.workspace_id = ?`, []any{ws}
	if name != "" {
		where += ` AND q.name = ?`
		args = append(args, name)
	}
	rows, err := s.queueStats(ctx, now, where, args...)
	if err != nil {
		return nil, err
	}
	stats := make(map[string]store.QueueStats, len(rows))
	for _, r := range rows {
		stats[r.Queue] = r.QueueStats
	}
	return stats, nil
}

func (s *Store) AllQueueStats(ctx context.Context, now time.Time) ([]store.QueueStatus, error) {
	return s.queueStats(ctx, now, ` ORDER BY q.workspace_id, q.name`)
}

func (s *Store) queueStats(ctx context.Context, now time.Time, where string, args ...any) ([]store.QueueStatus, error) {
	n := s.ts(now)
	rows, err := s.query(ctx, s.db, queueStatsQuery()+where, append([]any{n, n, n}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.QueueStatus
	for rows.Next() {
		var (
			st     store.QueueStatus
			oldest dbTime
		)
		if err := rows.Scan(&st.WorkspaceID, &st.Queue, &st.Paused, &st.Due, &st.Scheduled, &st.Running, &st.Failed, &oldest); err != nil {
			return nil, err
		}
		st.OldestDueAt = oldest.ptr()
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) QueueActivity(ctx context.Context, ws workspace.ID, queue string, since time.Time) (store.QueueActivity, error) {
	count := func(state store.TaskState) string {
		return fmt.Sprintf(`(SELECT count(*) FROM (SELECT 1 FROM tasks
			WHERE workspace_id = ? AND queue = ? AND state = '%s' AND finished_at >= ? LIMIT %d) c)`,
			state, store.StatsCountLimit)
	}
	var a store.QueueActivity
	args := []any{ws, queue, s.ts(since)}
	err := s.queryRow(ctx, s.db, `SELECT `+count(store.TaskSucceeded)+`, `+count(store.TaskFailed)+`, `+
		count(store.TaskCancelled), append(append(args, args...), args...)...).
		Scan(&a.Succeeded, &a.Failed, &a.Cancelled)
	if err != nil {
		return a, err
	}
	// Two scans of tasks_queue_finished, one per state, rather than an IN
	// that would need the rows sorted afterwards.
	rows, err := s.query(ctx, s.db, `SELECT d FROM (
			SELECT last_duration_ms AS d, finished_at FROM tasks
			WHERE workspace_id = ? AND queue = ? AND state = 'succeeded' AND finished_at >= ?
				AND last_duration_ms IS NOT NULL ORDER BY finished_at DESC LIMIT ?) s
		UNION ALL SELECT d FROM (
			SELECT last_duration_ms AS d, finished_at FROM tasks
			WHERE workspace_id = ? AND queue = ? AND state = 'failed' AND finished_at >= ?
				AND last_duration_ms IS NOT NULL ORDER BY finished_at DESC LIMIT ?) f`,
		ws, queue, s.ts(since), store.DurationSampleLimit, ws, queue, s.ts(since), store.DurationSampleLimit)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	var ms []int64
	for rows.Next() {
		var d int64
		if err := rows.Scan(&d); err != nil {
			return a, err
		}
		ms = append(ms, d)
	}
	if err := rows.Err(); err != nil {
		return a, err
	}
	a.P50Duration, a.P95Duration = percentile(ms, 50), percentile(ms, 95)
	a.DurationSamples = len(ms)
	return a, nil
}

// percentile is the nearest-rank percentile of millisecond durations, or nil
// for none. It sorts ms.
func percentile(ms []int64, p int) *time.Duration {
	if len(ms) == 0 {
		return nil
	}
	slices.Sort(ms)
	rank := (p*len(ms) + 99) / 100 // ceil(p/100 * n)
	d := time.Duration(ms[max(rank, 1)-1]) * time.Millisecond
	return &d
}

// epochMillis is the SQL for a timestamp column as Unix milliseconds:
// Postgres keeps TIMESTAMPTZ, SQLite already stores milliseconds.
func (s *Store) epochMillis(col string) string {
	if s.opts.Postgres {
		return "(extract(epoch FROM " + col + ") * 1000)::bigint"
	}
	return col
}

func (s *Store) TaskActivity(ctx context.Context, ws workspace.ID, since time.Time, bucket time.Duration) ([]store.ActivityCount, error) {
	ms := bucket.Milliseconds()
	// Integer division floors the creation time to its bucket in both databases.
	rows, err := s.query(ctx, s.db, `SELECT queue, (`+s.epochMillis("created_at")+` / ?) * ? AS bucket, count(*)
		FROM tasks WHERE workspace_id = ? AND created_at >= ?
		GROUP BY queue, bucket ORDER BY bucket, queue`, ms, ms, ws, s.ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ActivityCount
	for rows.Next() {
		var c store.ActivityCount
		var start int64
		if err := rows.Scan(&c.Queue, &start, &c.Count); err != nil {
			return nil, err
		}
		c.Start = time.UnixMilli(start).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) FinishedActivity(ctx context.Context, ws workspace.ID, since time.Time, bucket time.Duration) ([]store.FinishedCount, error) {
	ms := bucket.Milliseconds()
	rows, err := s.query(ctx, s.db, `SELECT state, (`+s.epochMillis("finished_at")+` / ?) * ? AS bucket, count(*)
		FROM tasks WHERE workspace_id = ? AND finished_at >= ? AND state IN ('succeeded', 'failed')
		GROUP BY state, bucket ORDER BY bucket, state`, ms, ms, ws, s.ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.FinishedCount
	for rows.Next() {
		var c store.FinishedCount
		var start int64
		if err := rows.Scan(&c.State, &start, &c.Count); err != nil {
			return nil, err
		}
		c.Start = time.UnixMilli(start).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// maxGroupedBody is how much of a response body a failure group carries.
const maxGroupedBody = 500

func (s *Store) QueueFailures(ctx context.Context, ws workspace.ID, queue string, limit int) ([]store.FailureGroup, error) {
	// Grouped by last_error alone, so a group is exactly what the
	// last_error filter selects. The error determines the outcome and status
	// (max picks them, and skips tasks that failed before last_outcome).
	rows, err := s.query(ctx, s.db, fmt.Sprintf(`SELECT last_error, max(last_outcome), max(last_status),
			count(*), max(finished_at)
		FROM (SELECT COALESCE(last_error, '') AS last_error, last_outcome, last_status, finished_at
			FROM tasks WHERE workspace_id = ? AND queue = ? AND state = 'failed' AND finished_at IS NOT NULL
			ORDER BY finished_at DESC LIMIT %d) f
		GROUP BY last_error
		ORDER BY count(*) DESC, max(finished_at) DESC
		LIMIT ?`, store.StatsCountLimit), ws, queue, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []store.FailureGroup{}
	for rows.Next() {
		var (
			g       store.FailureGroup
			outcome sql.NullString
			status  sql.NullInt64
			last    dbTime
		)
		if err := rows.Scan(&g.Error, &outcome, &status, &g.Count, &last); err != nil {
			return nil, err
		}
		g.Outcome, g.LastFailed = store.Outcome(outcome.String), last.Time
		if status.Valid {
			n := int(status.Int64)
			g.StatusCode = &n
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, g := range groups {
		var body sql.NullString
		err := s.queryRow(ctx, s.db, `SELECT t.id, a.response_body FROM tasks t
				LEFT JOIN attempts a ON a.task_id = t.id AND a.number = t.attempt_count
			WHERE t.workspace_id = ? AND t.queue = ? AND t.state = 'failed' AND t.finished_at IS NOT NULL
				AND COALESCE(t.last_error, '') = ?
			ORDER BY t.finished_at DESC LIMIT 1`, ws, queue, g.Error).Scan(&groups[i].LatestTaskID, &body)
		if err != nil {
			return nil, err
		}
		groups[i].ResponseBody = truncateUTF8(body.String, maxGroupedBody)
	}
	return groups, nil
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (s *Store) CancelPendingTasks(ctx context.Context, ws workspace.ID, queue string) (int, error) {
	var (
		n         int
		callbacks bool
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.getQueue(ctx, tx, ws, queue); err != nil {
			return err
		}
		now := s.ts(time.Now())
		u, err := s.updateTasks(ctx, tx, `UPDATE tasks SET state = 'cancelled', finished_at = ?, updated_at = ?
			WHERE workspace_id = ? AND queue = ? AND state = 'pending'`, now, now, ws, queue)
		if err != nil {
			return err
		}
		n = u.n
		counts := groupCounts{}
		for groupID, k := range u.groups {
			counts.move(groupID, store.TaskPending, store.TaskCancelled, k)
		}
		if callbacks, err = s.applyGroupCounts(ctx, tx, counts); err != nil {
			return err
		}
		cb, err := s.taskCallbacks(ctx, tx, u.callbacks)
		callbacks = callbacks || cb
		return err
	})
	if callbacks {
		s.wake()
	}
	return n, err
}

// updatedTasks is what an UPDATE of tasks changed: how many rows, how
// many of those belong to each group, and which have a callback.
type updatedTasks struct {
	n         int
	groups    map[string]int
	callbacks []string
}

// updateTasks runs an UPDATE of tasks and reports what it changed.
func (s *Store) updateTasks(ctx context.Context, tx *sql.Tx, query string, args ...any) (updatedTasks, error) {
	u := updatedTasks{groups: map[string]int{}}
	rows, err := s.query(ctx, tx, query+` RETURNING id, COALESCE(group_id, ''), COALESCE(callback_url, '')`, args...)
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, groupID, cbURL string
		if err := rows.Scan(&taskID, &groupID, &cbURL); err != nil {
			return u, err
		}
		u.n++
		if groupID != "" {
			u.groups[groupID]++
		}
		if cbURL != "" {
			u.callbacks = append(u.callbacks, taskID)
		}
	}
	return u, rows.Err()
}

// Tasks

const taskColumns = `id, workspace_id, queue, state, method, url, headers, body, run_at,
	attempt_count, replayed_attempts, max_attempts, timeout_ms, idempotency_key, trace_context, lease_expires_at,
	last_status, last_outcome, last_error, created_at, updated_at, finished_at, last_attempt_at, retried_at, group_id,
	started_at, last_duration_ms, callback_url, callback_headers, callback_task_id`

func scanTask(row interface{ Scan(...any) error }) (store.Task, error) {
	var (
		t                                                  store.Task
		headers, cbHeaders                                 []byte
		maxAttempts, timeoutMS, lastStatus, lastDuration   sql.NullInt64
		idemKey, traceCtx, lastOutcome, lastError, groupID sql.NullString
		cbURL, cbTask                                      sql.NullString
		runAt, lease, created, updated, fin, last, retried dbTime
		started                                            dbTime
	)
	err := row.Scan(&t.ID, &t.WorkspaceID, &t.Queue, &t.State, &t.Method, &t.URL, &headers, &t.Body,
		&runAt, &t.AttemptCount, &t.ReplayedAttempts, &maxAttempts, &timeoutMS, &idemKey, &traceCtx, &lease,
		&lastStatus, &lastOutcome, &lastError, &created, &updated, &fin, &last, &retried, &groupID,
		&started, &lastDuration, &cbURL, &cbHeaders, &cbTask)
	if err != nil {
		return t, err
	}
	if t.OnComplete, err = scanCallback(cbURL, cbHeaders); err != nil {
		return t, err
	}
	t.CallbackTaskID = cbTask.String
	if t.Headers, err = unmarshalHeaders(headers); err != nil {
		return t, err
	}
	if t.Body == nil {
		t.Body = []byte{}
	}
	if maxAttempts.Valid {
		n := int(maxAttempts.Int64)
		t.MaxAttempts = &n
	}
	if timeoutMS.Valid {
		d := time.Duration(timeoutMS.Int64) * time.Millisecond
		t.Timeout = &d
	}
	if lastStatus.Valid {
		n := int(lastStatus.Int64)
		t.LastStatus = &n
	}
	t.IdempotencyKey, t.TraceContext, t.LastError, t.GroupID = idemKey.String, traceCtx.String, lastError.String, groupID.String
	t.LastOutcome = store.Outcome(lastOutcome.String)
	t.RunAt, t.CreatedAt, t.UpdatedAt = runAt.Time, created.Time, updated.Time
	t.LeaseExpiresAt, t.FinishedAt, t.LastAttemptAt, t.RetriedAt = lease.ptr(), fin.ptr(), last.ptr(), retried.ptr()
	t.StartedAt, t.LastDuration = started.ptr(), millis(lastDuration)
	return t, nil
}

func (s *Store) CreateTask(ctx context.Context, ws workspace.ID, nt store.NewTask, autoCreateQueue bool) (store.Task, bool, error) {
	tasks, created, err := s.CreateTasks(ctx, ws, []store.NewTask{nt}, autoCreateQueue)
	if err != nil {
		return store.Task{}, false, err
	}
	return tasks[0], created[0], nil
}

func (s *Store) CreateTasks(ctx context.Context, ws workspace.ID, nts []store.NewTask, autoCreateQueue bool) ([]store.Task, []bool, error) {
	tasks := make([]store.Task, len(nts))
	created := make([]bool, len(nts))
	anyCreated := false
	create := func(q querier) error {
		joined := map[string]int{}
		for i, nt := range nts {
			var err error
			if tasks[i], created[i], err = s.createTask(ctx, q, ws, nt, autoCreateQueue); err != nil {
				return err
			}
			anyCreated = anyCreated || created[i]
			if created[i] && nt.GroupID != "" {
				joined[nt.GroupID]++
			}
		}
		if len(joined) == 0 {
			return nil
		}
		// In the same transaction as the inserts, so a group can't close
		// (and complete) between the check that it is open and its new
		// tasks being counted.
		return s.joinGroups(ctx, q, ws, joined)
	}
	var err error
	if len(nts) == 1 && nts[0].GroupID == "" {
		// The common enqueue is one statement, so it needs no transaction
		// around it: that saves two round trips out of three.
		err = create(s.db)
	} else {
		err = s.inTx(ctx, func(tx *sql.Tx) error { return create(tx) })
	}
	if err != nil {
		return nil, nil, err
	}
	if anyCreated {
		s.wake()
	}
	return tasks, created, nil
}

// insertTaskQuery inserts a task if its queue exists and its idempotency key is
// free, and returns it. Otherwise it inserts and returns nothing.
func (s *Store) insertTaskQuery() string {
	return `INSERT INTO tasks (id, workspace_id, queue, state, method, url,
			headers, body, run_at, max_attempts, timeout_ms, idempotency_key, trace_context,
			created_at, updated_at, group_id, callback_url, callback_headers)
		SELECT * FROM (VALUES ` + s.values("text", "text", "text", "text", "text", "text",
		"json", "bytes", "ts", "int", "int", "text", "text", "ts", "ts", "text", "text", "json") + `) AS v
		WHERE EXISTS (SELECT 1 FROM queues q WHERE q.workspace_id = ? AND q.name = ?)
		ON CONFLICT (workspace_id, queue, idempotency_key) WHERE idempotency_key IS NOT NULL
		DO NOTHING
		RETURNING ` + taskColumns
}

// createTask inserts a task, or returns the task with the same idempotency
// key. A missing queue is created if autoCreateQueue is set, and is
// ErrNotFound otherwise.
func (s *Store) createTask(ctx context.Context, q querier, ws workspace.ID, nt store.NewTask, autoCreateQueue bool) (store.Task, bool, error) {
	headers, err := marshalHeaders(nt.Headers)
	if err != nil {
		return store.Task{}, false, err
	}
	if nt.Body == nil {
		nt.Body = []byte{}
	}
	cbURL, cbHeaders, err := callbackArgs(nt.OnComplete)
	if err != nil {
		return store.Task{}, false, err
	}
	now := s.ts(time.Now())
	args := []any{id.New("task"), ws, nt.Queue, store.TaskPending, nt.Method, nt.URL, headers, nt.Body,
		s.ts(nt.RunAt), nullInt(nt.MaxAttempts), nullMillis(nt.Timeout),
		nullString(nt.IdempotencyKey), nullString(nt.TraceContext), now, now, nullString(nt.GroupID),
		cbURL, cbHeaders, ws, nt.Queue}
	for createdQueue := false; ; createdQueue = true {
		t, err := scanTask(s.queryRow(ctx, q, s.insertTask, args...))
		if err == nil {
			return t, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return store.Task{}, false, err
		}
		// Nothing was inserted: the idempotency key is taken, or there is no
		// such queue. Queues almost always exist, so the insert checks
		// rather than every enqueue making sure first.
		if nt.IdempotencyKey != "" {
			t, err := scanTask(s.queryRow(ctx, q, `SELECT `+taskColumns+` FROM tasks
				WHERE workspace_id = ? AND queue = ? AND idempotency_key = ?`,
				ws, nt.Queue, nt.IdempotencyKey))
			if err == nil {
				return t, false, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return store.Task{}, false, err
			}
		}
		if !autoCreateQueue || createdQueue {
			return store.Task{}, false, store.ErrNotFound
		}
		if err := s.upsertQueue(ctx, q, ws, nt.Queue, store.DefaultQueueConfig(), false); err != nil {
			return store.Task{}, false, err
		}
	}
}

func (s *Store) GetTask(ctx context.Context, ws workspace.ID, id string) (store.Task, error) {
	return s.getTask(ctx, s.db, ws, id)
}

func (s *Store) getTask(ctx context.Context, q querier, ws workspace.ID, id string) (store.Task, error) {
	t, err := scanTask(s.queryRow(ctx, q,
		`SELECT `+taskColumns+` FROM tasks WHERE workspace_id = ? AND id = ?`, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Task{}, store.ErrNotFound
	}
	return t, err
}

func (s *Store) ListTasks(ctx context.Context, ws workspace.ID, f store.TaskFilter) ([]store.Task, string, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks WHERE workspace_id = ?`
	args := []any{ws}
	if f.Queue != "" {
		query += ` AND queue = ?`
		args = append(args, f.Queue)
	}
	if f.GroupID != "" {
		query += ` AND group_id = ?`
		args = append(args, f.GroupID)
	}
	if f.State != "" {
		query += ` AND state = ?`
		args = append(args, f.State)
	}
	if f.IdempotencyKey != "" {
		query += ` AND idempotency_key = ?`
		args = append(args, f.IdempotencyKey)
	}
	if f.LastError != "" {
		query += ` AND last_error = ?`
		args = append(args, f.LastError)
	}
	if !f.CreatedAfter.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, s.ts(f.CreatedAfter))
	}
	if !f.CreatedBefore.IsZero() {
		query += ` AND created_at < ?`
		args = append(args, s.ts(f.CreatedBefore))
	}
	order, err := s.taskOrder(f)
	if err != nil {
		return nil, "", err
	}
	after, afterArgs, orderBy, err := order.clause(f.Cursor)
	if err != nil {
		return nil, "", err
	}
	query += after
	args = append(args, afterArgs...)
	// Fetch one extra row to know whether there is a next page.
	query += orderBy + ` LIMIT ?`
	args = append(args, f.Limit+1)

	tasks, err := s.listTasks(ctx, s.db, query, args...)
	if err != nil {
		return nil, "", err
	}
	var cursor string
	if len(tasks) > f.Limit {
		tasks = tasks[:f.Limit]
		cursor = order.cursor(tasks[len(tasks)-1])
	}
	return tasks, cursor, nil
}

func (s *Store) listTasks(ctx context.Context, q querier, query string, args ...any) ([]store.Task, error) {
	rows, err := s.query(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []store.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// Attempts

func (s *Store) ListAttempts(ctx context.Context, ws workspace.ID, taskID string) ([]store.Attempt, error) {
	return s.listAttempts(ctx, "attempts", "task_id", ws, taskID, func(a *store.Attempt) *string { return &a.TaskID })
}

// listAttempts lists the attempts of a task (table attempts) or a run
// (run_attempts), oldest first. owner picks the field its ID goes in.
func (s *Store) listAttempts(ctx context.Context, table, ownerColumn string, ws workspace.ID, ownerID string,
	owner func(*store.Attempt) *string) ([]store.Attempt, error) {
	rows, err := s.query(ctx, s.db, `SELECT id, `+ownerColumn+`, number, started_at, finished_at,
			status_code, outcome, error, response_body, response_headers, retried_at
		FROM `+table+` WHERE workspace_id = ? AND `+ownerColumn+` = ? ORDER BY number`, ws, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := []store.Attempt{}
	for rows.Next() {
		var (
			a                          store.Attempt
			started, finished, retried dbTime
			status                     sql.NullInt64
			errText, body              sql.NullString
			headers                    []byte
		)
		if err := rows.Scan(&a.ID, owner(&a), &a.Number, &started, &finished,
			&status, &a.Outcome, &errText, &body, &headers, &retried); err != nil {
			return nil, err
		}
		a.StartedAt, a.FinishedAt, a.RetriedAt = started.Time, finished.Time, retried.ptr()
		if status.Valid {
			n := int(status.Int64)
			a.StatusCode = &n
		}
		a.Error, a.ResponseBody = errText.String, body.String
		if a.ResponseHeaders, err = unmarshalHeaders(headers); err != nil {
			return nil, err
		}
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}

func (s *Store) CancelTask(ctx context.Context, ws workspace.ID, id string) (store.Task, error) {
	var (
		t         store.Task
		callbacks bool
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.ts(time.Now())
		// A running task keeps its lease, so its in-flight attempt can still
		// be recorded (see FinishAttempt).
		u, err := s.updateTasks(ctx, tx, `UPDATE tasks SET state = 'cancelled', finished_at = ?, updated_at = ?
			WHERE workspace_id = ? AND id = ? AND state IN ('pending', 'running')`, now, now, ws, id)
		if err != nil {
			return err
		}
		counts := groupCounts{}
		for groupID := range u.groups {
			// Pending and running count alike.
			counts.move(groupID, store.TaskPending, store.TaskCancelled, 1)
		}
		if callbacks, err = s.applyGroupCounts(ctx, tx, counts); err != nil {
			return err
		}
		cb, err := s.taskCallbacks(ctx, tx, u.callbacks)
		if err != nil {
			return err
		}
		callbacks = callbacks || cb
		if t, err = s.getTask(ctx, tx, ws, id); err != nil {
			return err
		}
		if u.n == 0 && t.State != store.TaskCancelled {
			return store.ErrInvalidState
		}
		return nil
	})
	if callbacks {
		s.wake()
	}
	return t, err
}

func (s *Store) RetryTask(ctx context.Context, ws workspace.ID, id string) (store.Task, error) {
	var t store.Task
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// The state it leaves, for its group's counts. Locked, so it is
		// still the state when the update below runs.
		var (
			from    store.TaskState
			groupID sql.NullString
		)
		err := s.queryRow(ctx, tx, `SELECT state, group_id FROM tasks WHERE workspace_id = ? AND id = ?`+s.forUpdate(),
			ws, id).Scan(&from, &groupID)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.ts(time.Now())
		// A pending task just runs sooner; a finished one gets a fresh
		// attempt budget.
		res, err := s.exec(ctx, tx, `UPDATE tasks SET
				replayed_attempts = CASE WHEN state = 'pending' THEN replayed_attempts ELSE attempt_count END,
				state = 'pending', run_at = ?, finished_at = NULL, updated_at = ?, retried_at = ?
			WHERE workspace_id = ? AND id = ? AND state <> 'running' AND lease_expires_at IS NULL`,
			now, now, now, ws, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if t, err = s.getTask(ctx, tx, ws, id); err != nil {
			return err
		}
		if n == 0 {
			return store.ErrInvalidState
		}
		counts := groupCounts{}
		counts.move(groupID.String, from, store.TaskPending, 1)
		_, err = s.applyGroupCounts(ctx, tx, counts)
		return err
	})
	if err == nil {
		s.wake()
	}
	return t, err
}

// forUpdate locks the rows a SELECT in a transaction reads, on Postgres.
// SQLite transactions hold the database's write lock from the start.
func (s *Store) forUpdate() string {
	if s.opts.Postgres {
		return ` FOR UPDATE`
	}
	return ""
}

func (s *Store) RetryFailedTasks(ctx context.Context, ws workspace.ID, queue, lastError string) (int, error) {
	var n int
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.getQueue(ctx, tx, ws, queue); err != nil {
			return err
		}
		now := s.ts(time.Now())
		// Failed tasks never hold a lease, so no attempt is in flight.
		query := `UPDATE tasks SET
				replayed_attempts = attempt_count, state = 'pending', run_at = ?,
				finished_at = NULL, updated_at = ?, retried_at = ?
			WHERE workspace_id = ? AND queue = ? AND state = 'failed' AND finished_at IS NOT NULL`
		args := []any{now, now, now, ws, queue}
		if lastError != "" {
			query += ` AND last_error = ?`
			args = append(args, lastError)
		}
		u, err := s.updateTasks(ctx, tx, query, args...)
		if err != nil {
			return err
		}
		n = u.n
		counts := groupCounts{}
		for groupID, k := range u.groups {
			counts.move(groupID, store.TaskFailed, store.TaskPending, k)
		}
		_, err = s.applyGroupCounts(ctx, tx, counts)
		return err
	})
	if err == nil && n > 0 {
		s.wake()
	}
	return n, err
}

// Dispatch

func (s *Store) DueQueues(ctx context.Context, now time.Time) ([]store.Queue, error) {
	return s.listQueues(ctx, `SELECT `+queueColumns+` FROM queues q
		WHERE NOT q.paused AND EXISTS (
			SELECT 1 FROM tasks t
			WHERE t.workspace_id = q.workspace_id AND t.queue = q.name
				AND t.state = 'pending' AND t.run_at <= ?
		)
		ORDER BY q.workspace_id, q.name`, s.ts(now))
}

func (s *Store) NextRunAt(ctx context.Context, after time.Time) (time.Time, bool, error) {
	var next dbTime
	err := s.queryRow(ctx, s.db, `SELECT MIN(run_at) FROM tasks WHERE state = 'pending' AND run_at > ?`,
		s.ts(after)).Scan(&next)
	return next.Time, next.Valid, err
}

func (s *Store) ClaimTasks(ctx context.Context, q store.Queue, limit int, now time.Time, grace time.Duration) ([]store.Task, error) {
	// One statement, since the claim loop is serial and round trips bound
	// dispatch throughput. The queue may have been paused since DueQueues
	// listed it; a pause stops deliveries from starting as soon as it
	// returns.
	query := `WITH due AS (
			SELECT id FROM tasks
			WHERE workspace_id = ? AND queue = ? AND state = 'pending' AND run_at <= ?
				AND EXISTS (SELECT 1 FROM queues q
					WHERE q.workspace_id = tasks.workspace_id AND q.name = tasks.queue AND NOT q.paused)
			ORDER BY run_at LIMIT ?`
	if s.opts.Postgres {
		// Redundant with a single leader, but keeps a stale leader that
		// hasn't noticed it lost its lock from double-claiming.
		query += ` FOR UPDATE SKIP LOCKED`
	}
	// The lease runs for the task's timeout, or else the queue's, plus grace.
	lease := `? + COALESCE(timeout_ms, ?) + ?`
	if s.opts.Postgres {
		lease = `CAST(? AS TIMESTAMPTZ) + (COALESCE(timeout_ms, ?) + ?) * interval '1 millisecond'`
	}
	query += `)
		UPDATE tasks SET state = 'running', attempt_count = attempt_count + 1,
			lease_expires_at = ` + lease + `, updated_at = ?, started_at = COALESCE(started_at, ?)
		WHERE id IN (SELECT id FROM due)
		RETURNING ` + taskColumns
	tasks, err := s.listTasks(ctx, s.db, query, q.WorkspaceID, q.Name, s.ts(now), limit,
		s.ts(now), q.Timeout.Milliseconds(), grace.Milliseconds(), s.ts(now), s.ts(now))
	if err != nil {
		return nil, err
	}
	// Returned in no particular order; deliveries start oldest first.
	slices.SortFunc(tasks, func(a, b store.Task) int {
		return cmp.Or(a.RunAt.Compare(b.RunAt), cmp.Compare(a.ID, b.ID))
	})
	return tasks, nil
}

func (s *Store) FinishAttempt(ctx context.Context, ws workspace.ID, r store.AttemptResult) (store.TaskState, error) {
	out, err := s.FinishAttempts(ctx, []store.FinishedAttempt{{Workspace: ws, AttemptResult: r}})
	if err != nil {
		return "", err
	}
	return out[0].State, out[0].Err
}

// finishChunk is how many results one statement of FinishAttempts carries,
// well below either database's limit on parameters.
const finishChunk = 100

func (s *Store) FinishAttempts(ctx context.Context, rs []store.FinishedAttempt) ([]store.FinishOutcome, error) {
	type row struct {
		update, insert []any
	}
	rows := make([]row, len(rs))
	for i, r := range rs {
		var runAt, finishedAt *time.Time
		switch r.NextState {
		case store.TaskPending:
			runAt = &r.RunAt
		case store.TaskSucceeded, store.TaskFailed:
			finishedAt = &r.FinishedAt
		default:
			return nil, fmt.Errorf("invalid next state %q", r.NextState)
		}
		respHeaders, err := marshalHeaders(r.ResponseHeaders)
		if err != nil {
			return nil, err
		}
		rows[i] = row{
			update: []any{r.Workspace, r.TaskID, r.Number, r.NextState, s.tsOrNil(runAt), nullInt(r.StatusCode),
				r.Outcome, nullString(r.Error), s.ts(r.FinishedAt), s.tsOrNil(finishedAt), s.durationMillis(r.Attempt)},
			insert: []any{id.New("att"), r.TaskID, r.Workspace, r.Number, s.ts(r.StartedAt), s.ts(r.FinishedAt),
				nullInt(r.StatusCode), r.Outcome, nullString(r.Error), nullString(r.ResponseBody), respHeaders},
		}
	}
	updateCols := s.values("text", "text", "int", "text", "ts", "int", "text", "text", "ts", "ts", "int")
	insertCols := s.values("text", "text", "text", "int", "ts", "ts", "int", "text", "text", "text", "json")

	out := make([]store.FinishOutcome, len(rs))
	var callbacks bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		counts := groupCounts{}
		var withCallback []string
		for lo := 0; lo < len(rows); lo += finishChunk {
			chunk := rows[lo:min(lo+finishChunk, len(rows))]
			var args []any
			for _, r := range chunk {
				args = append(args, r.update...)
			}
			// The lease and attempt guard makes a late result from a
			// delivery whose lease was already reaped a no-op. A task
			// cancelled mid-attempt stays cancelled.
			res, err := s.query(ctx, tx, `UPDATE tasks SET
					state = CASE WHEN tasks.state = 'cancelled' THEN tasks.state ELSE v.column4 END,
					run_at = COALESCE(v.column5, tasks.run_at), lease_expires_at = NULL,
					last_status = v.column6, last_outcome = v.column7, last_error = v.column8,
					updated_at = v.column9, last_attempt_at = v.column9, last_duration_ms = v.column11,
					finished_at = CASE WHEN tasks.state = 'cancelled' THEN tasks.finished_at ELSE v.column10 END
				FROM (VALUES `+repeatRows(updateCols, len(chunk))+`) AS v
				WHERE tasks.workspace_id = v.column1 AND tasks.id = v.column2
					AND tasks.lease_expires_at IS NOT NULL AND tasks.attempt_count = v.column3
				RETURNING tasks.id, tasks.state, COALESCE(tasks.group_id, ''), COALESCE(tasks.callback_url, '')`, args...)
			if err != nil {
				return err
			}
			states := map[string]store.TaskState{}
			for res.Next() {
				var taskID, groupID, cbURL string
				var state store.TaskState
				if err := res.Scan(&taskID, &state, &groupID, &cbURL); err != nil {
					res.Close()
					return err
				}
				states[taskID] = state
				// The task was running; cancelled means it was cancelled
				// mid-attempt and already counted (and called back) as such.
				if state != store.TaskCancelled {
					counts.move(groupID, store.TaskRunning, state, 1)
					if cbURL != "" && state.Finished() {
						withCallback = append(withCallback, taskID)
					}
				}
			}
			if err := res.Close(); err != nil {
				return err
			}
			if err := res.Err(); err != nil {
				return err
			}

			args = args[:0]
			n := 0
			for i, r := range chunk {
				o := &out[lo+i]
				state, ok := states[rs[lo+i].TaskID]
				if !ok {
					o.Err = store.ErrLeaseLost
					continue
				}
				o.State = state
				args = append(args, r.insert...)
				n++
			}
			if n == 0 {
				continue
			}
			// A manual retry since the previous attempt finished is
			// recorded with this one.
			if _, err := s.exec(ctx, tx, `INSERT INTO attempts (id, task_id, workspace_id, number,
					started_at, finished_at, status_code, outcome, error, response_body, response_headers, retried_at)
				SELECT v.column1, v.column2, v.column3, v.column4, v.column5, v.column6,
					v.column7, v.column8, v.column9, v.column10, v.column11,
					CASE WHEN t.retried_at > COALESCE(
						(SELECT max(a.finished_at) FROM attempts a WHERE a.task_id = t.id), t.created_at)
					THEN t.retried_at END
				FROM (VALUES `+repeatRows(insertCols, n)+`) AS v
				JOIN tasks t ON t.workspace_id = v.column3 AND t.id = v.column2`, args...); err != nil {
				return err
			}
		}
		var err error
		if callbacks, err = s.applyGroupCounts(ctx, tx, counts); err != nil {
			return err
		}
		cb, err := s.taskCallbacks(ctx, tx, withCallback)
		callbacks = callbacks || cb
		return err
	})
	if err != nil {
		return nil, err
	}
	if callbacks {
		s.wake()
	}
	return out, nil
}

// values returns one row of a VALUES list: a placeholder per column, cast
// to the column's type ("text", "int", "ts", "json" or "bytes"), because Postgres
// would otherwise take every parameter in a VALUES list for text.
func (s *Store) values(kinds ...string) string {
	types := map[string]string{"text": "TEXT", "int": "INTEGER", "ts": "INTEGER", "json": "TEXT", "bytes": "BLOB"}
	if s.opts.Postgres {
		types["ts"], types["json"], types["bytes"] = "TIMESTAMPTZ", "JSONB", "BYTEA"
	}
	cols := make([]string, len(kinds))
	for i, k := range kinds {
		cols[i] = "CAST(? AS " + types[k] + ")"
	}
	return "(" + strings.Join(cols, ", ") + ")"
}

func repeatRows(row string, n int) string {
	return strings.TrimSuffix(strings.Repeat(row+", ", n), ", ")
}

// sweepWhere is the condition for rows (with the column prefix, e.g. "r.")
// that sw covers, and its arguments.
func (s *Store) sweepWhere(prefix string, sw store.Sweep) (string, []any) {
	where, args := prefix+"finished_at < ?", []any{s.ts(sw.Before)}
	switch {
	case sw.Workspace != "":
		where += " AND " + prefix + "workspace_id = ?"
		args = append(args, sw.Workspace)
	case len(sw.Except) > 0:
		where += " AND " + prefix + "workspace_id NOT IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(sw.Except)), ", ") + ")"
		for _, ws := range sw.Except {
			args = append(args, ws)
		}
	}
	return where, args
}

func (s *Store) ExpiredLeases(ctx context.Context, now time.Time, limit int) ([]store.Task, error) {
	return s.listTasks(ctx, s.db, `SELECT `+taskColumns+` FROM tasks
		WHERE lease_expires_at IS NOT NULL AND lease_expires_at < ?
		ORDER BY lease_expires_at LIMIT ?`, s.ts(now), limit)
}

func (s *Store) DeleteFinishedTasks(ctx context.Context, sw store.Sweep, limit int) (int, error) {
	// Attempts are deleted by cascade. A task cancelled mid-attempt is kept
	// until its attempt is recorded.
	where, args := s.sweepWhere("", sw)
	res, err := s.exec(ctx, s.db, `DELETE FROM tasks WHERE id IN (
			SELECT id FROM tasks
			WHERE `+where+` AND lease_expires_at IS NULL
			ORDER BY finished_at LIMIT ?
		)`, append(args, limit)...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
