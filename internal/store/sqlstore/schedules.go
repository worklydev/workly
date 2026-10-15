package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/worklydev/workly/internal/id"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// Schedules

const scheduleColumns = `workspace_id, name, cron, timezone, method, url, headers, body,
	timeout_ms, max_attempts, min_backoff_ms, max_backoff_ms, backoff_multiplier, retry_on_4xx,
	overlap, missed, paused, next_run_at, created_at, updated_at`

func scanSchedule(row interface{ Scan(...any) error }) (store.Schedule, error) {
	var (
		sc                              store.Schedule
		headers                         []byte
		timeout, minBackoff, maxBackoff int64
		next, created, updated          dbTime
	)
	err := row.Scan(&sc.WorkspaceID, &sc.Name, &sc.Cron, &sc.Timezone, &sc.Method, &sc.URL, &headers, &sc.Body,
		&timeout, &sc.MaxAttempts, &minBackoff, &maxBackoff, &sc.BackoffMultiplier, &sc.RetryOn4xx,
		&sc.Overlap, &sc.Missed, &sc.Paused, &next, &created, &updated)
	if err != nil {
		return sc, err
	}
	if sc.Headers, err = unmarshalHeaders(headers); err != nil {
		return sc, err
	}
	if sc.Body == nil {
		sc.Body = []byte{}
	}
	sc.Timeout = time.Duration(timeout) * time.Millisecond
	sc.MinBackoff = time.Duration(minBackoff) * time.Millisecond
	sc.MaxBackoff = time.Duration(maxBackoff) * time.Millisecond
	sc.NextRunAt, sc.CreatedAt, sc.UpdatedAt = next.ptr(), created.Time, updated.Time
	return sc, nil
}

func (s *Store) PutSchedule(ctx context.Context, ws workspace.ID, name string, cfg store.ScheduleConfig, nextRunAt time.Time) (store.Schedule, bool, error) {
	headers, err := marshalHeaders(cfg.Headers)
	if err != nil {
		return store.Schedule{}, false, err
	}
	if cfg.Body == nil {
		cfg.Body = []byte{}
	}
	var (
		sc      store.Schedule
		created bool
	)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := s.getSchedule(ctx, tx, ws, name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			created = true
		case err != nil:
			return err
		}
		now := s.ts(time.Now())
		// A paused schedule stays paused, with no next slot.
		_, err = s.exec(ctx, tx, `INSERT INTO schedules (`+scheduleColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace_id, name) DO UPDATE SET
				cron = excluded.cron, timezone = excluded.timezone, method = excluded.method,
				url = excluded.url, headers = excluded.headers, body = excluded.body,
				timeout_ms = excluded.timeout_ms, max_attempts = excluded.max_attempts,
				min_backoff_ms = excluded.min_backoff_ms, max_backoff_ms = excluded.max_backoff_ms,
				backoff_multiplier = excluded.backoff_multiplier, retry_on_4xx = excluded.retry_on_4xx,
				overlap = excluded.overlap, missed = excluded.missed,
				next_run_at = CASE WHEN schedules.paused THEN NULL ELSE excluded.next_run_at END,
				updated_at = excluded.updated_at`,
			ws, name, cfg.Cron, cfg.Timezone, cfg.Method, cfg.URL, headers, cfg.Body,
			cfg.Timeout.Milliseconds(), cfg.MaxAttempts, cfg.MinBackoff.Milliseconds(), cfg.MaxBackoff.Milliseconds(),
			cfg.BackoffMultiplier, cfg.RetryOn4xx, cfg.Overlap, cfg.Missed, false, s.ts(nextRunAt), now, now)
		if err != nil {
			return err
		}
		sc, err = s.getSchedule(ctx, tx, ws, name)
		return err
	})
	if err != nil {
		return store.Schedule{}, false, err
	}
	s.wake()
	return sc, created, nil
}

func (s *Store) GetSchedule(ctx context.Context, ws workspace.ID, name string) (store.Schedule, error) {
	sc, err := s.getSchedule(ctx, s.db, ws, name)
	if err != nil {
		return sc, err
	}
	last, err := s.lastRuns(ctx, ws, name)
	sc.LastRun = last[name]
	return sc, err
}

func (s *Store) getSchedule(ctx context.Context, q querier, ws workspace.ID, name string) (store.Schedule, error) {
	sc, err := scanSchedule(s.queryRow(ctx, q,
		`SELECT `+scheduleColumns+` FROM schedules WHERE workspace_id = ? AND name = ?`, ws, name))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Schedule{}, store.ErrNotFound
	}
	return sc, err
}

func (s *Store) ListSchedules(ctx context.Context, ws workspace.ID) ([]store.Schedule, error) {
	schedules, err := s.listSchedules(ctx, s.db,
		`SELECT `+scheduleColumns+` FROM schedules WHERE workspace_id = ? ORDER BY name`, ws)
	if err != nil {
		return nil, err
	}
	last, err := s.lastRuns(ctx, ws, "")
	if err != nil {
		return nil, err
	}
	for i := range schedules {
		schedules[i].LastRun = last[schedules[i].Name]
	}
	return schedules, nil
}

func (s *Store) listSchedules(ctx context.Context, q querier, query string, args ...any) ([]store.Schedule, error) {
	rows, err := s.query(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schedules := []store.Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, sc)
	}
	return schedules, rows.Err()
}

// lastRuns returns the latest run of the named schedule, or of every
// schedule in the workspace if name is empty, keyed by schedule.
func (s *Store) lastRuns(ctx context.Context, ws workspace.ID, name string) (map[string]*store.RunSummary, error) {
	query := `SELECT r.schedule, r.id, r.state, r.trigger_type, r.scheduled_for, r.finished_at,
			r.last_status, r.last_error
		FROM runs r
		WHERE r.workspace_id = ? AND r.id = (SELECT max(l.id) FROM runs l
			WHERE l.workspace_id = r.workspace_id AND l.schedule = r.schedule)`
	args := []any{ws}
	if name != "" {
		query += ` AND r.schedule = ?`
		args = append(args, name)
	}
	rows, err := s.query(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*store.RunSummary{}
	for rows.Next() {
		var (
			schedule      string
			r             store.RunSummary
			slot, fin     dbTime
			status        sql.NullInt64
			lastErrorText sql.NullString
		)
		if err := rows.Scan(&schedule, &r.ID, &r.State, &r.Trigger, &slot, &fin, &status, &lastErrorText); err != nil {
			return nil, err
		}
		r.ScheduledFor, r.FinishedAt, r.LastError = slot.Time, fin.ptr(), lastErrorText.String
		if status.Valid {
			n := int(status.Int64)
			r.LastStatus = &n
		}
		out[schedule] = &r
	}
	return out, rows.Err()
}

func (s *Store) DeleteSchedule(ctx context.Context, ws workspace.ID, name string) error {
	res, err := s.exec(ctx, s.db, `DELETE FROM schedules WHERE workspace_id = ? AND name = ?`, ws, name)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) SetSchedulePaused(ctx context.Context, ws workspace.ID, name string, paused bool, nextRunAt time.Time) (store.Schedule, error) {
	var sc store.Schedule
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		query, args := `UPDATE schedules SET paused = ?, next_run_at = NULL, updated_at = ?
			WHERE workspace_id = ? AND name = ?`, []any{true, s.ts(time.Now()), ws, name}
		if !paused {
			// Only a paused schedule restarts from nextRunAt.
			query, args = `UPDATE schedules SET
					next_run_at = CASE WHEN paused THEN ? ELSE next_run_at END,
					paused = ?, updated_at = ?
				WHERE workspace_id = ? AND name = ?`, []any{s.ts(nextRunAt), false, s.ts(time.Now()), ws, name}
		}
		res, err := s.exec(ctx, tx, query, args...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return store.ErrNotFound
		}
		sc, err = s.getSchedule(ctx, tx, ws, name)
		return err
	})
	if err != nil {
		return store.Schedule{}, err
	}
	if !paused {
		s.wake()
	}
	last, err := s.lastRuns(ctx, ws, name)
	sc.LastRun = last[name]
	return sc, err
}

func (s *Store) TriggerSchedule(ctx context.Context, ws workspace.ID, name string, now time.Time) (store.Run, error) {
	var r store.Run
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		sc, err := s.getSchedule(ctx, tx, ws, name)
		if err != nil {
			return err
		}
		r, _, err = s.insertRun(ctx, tx, sc, store.TriggerManual, now, store.RunPending, "", now)
		return err
	})
	if err != nil {
		return store.Run{}, err
	}
	s.wake()
	return r, nil
}

// insertRun creates a run of sc for slot, with a snapshot of its request.
// A schedule-triggered run for a slot that already has one is not created:
// inserted is false and r is the zero Run.
func (s *Store) insertRun(ctx context.Context, tx *sql.Tx, sc store.Schedule, trigger store.RunTrigger, slot time.Time,
	state store.RunState, reason store.SkipReason, now time.Time) (r store.Run, inserted bool, err error) {
	headers, err := marshalHeaders(sc.Headers)
	if err != nil {
		return store.Run{}, false, err
	}
	var finished *time.Time
	if state == store.RunSkipped {
		finished = &now
	}
	runID := id.New("run")
	res, err := s.exec(ctx, tx, `INSERT INTO runs (id, workspace_id, schedule, trigger_type, scheduled_for,
			state, skip_reason, method, url, headers, body, run_at, created_at, updated_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (workspace_id, schedule, scheduled_for) WHERE trigger_type = 'schedule' DO NOTHING`,
		runID, sc.WorkspaceID, sc.Name, trigger, s.ts(slot), state, nullString(string(reason)),
		sc.Method, sc.URL, headers, sc.Body, s.ts(now), s.ts(now), s.ts(now), s.tsOrNil(finished))
	if err != nil {
		return store.Run{}, false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return store.Run{}, false, err
	}
	r, err = s.getRun(ctx, tx, sc.WorkspaceID, runID)
	return r, err == nil, err
}

// Runs

const runColumns = `id, workspace_id, schedule, state, trigger_type, scheduled_for, skip_reason,
	method, url, headers, body, run_at, attempt_count, replayed_attempts, lease_expires_at,
	last_status, last_outcome, last_error, created_at, updated_at, finished_at, last_attempt_at, retried_at`

func scanRun(row interface{ Scan(...any) error }) (store.Run, error) {
	var (
		r                                                        store.Run
		headers                                                  []byte
		lastStatus                                               sql.NullInt64
		skipReason, lastOutcome, lastError                       sql.NullString
		slot, runAt, lease, created, updated, fin, last, retried dbTime
	)
	err := row.Scan(&r.ID, &r.WorkspaceID, &r.Schedule, &r.State, &r.Trigger, &slot, &skipReason,
		&r.Method, &r.URL, &headers, &r.Body, &runAt, &r.AttemptCount, &r.ReplayedAttempts, &lease,
		&lastStatus, &lastOutcome, &lastError, &created, &updated, &fin, &last, &retried)
	if err != nil {
		return r, err
	}
	if r.Headers, err = unmarshalHeaders(headers); err != nil {
		return r, err
	}
	if r.Body == nil {
		r.Body = []byte{}
	}
	if lastStatus.Valid {
		n := int(lastStatus.Int64)
		r.LastStatus = &n
	}
	r.SkipReason, r.LastOutcome, r.LastError = store.SkipReason(skipReason.String), store.Outcome(lastOutcome.String), lastError.String
	r.ScheduledFor, r.RunAt, r.CreatedAt, r.UpdatedAt = slot.Time, runAt.Time, created.Time, updated.Time
	r.LeaseExpiresAt, r.FinishedAt, r.LastAttemptAt, r.RetriedAt = lease.ptr(), fin.ptr(), last.ptr(), retried.ptr()
	return r, nil
}

func (s *Store) GetRun(ctx context.Context, ws workspace.ID, id string) (store.Run, error) {
	return s.getRun(ctx, s.db, ws, id)
}

func (s *Store) getRun(ctx context.Context, q querier, ws workspace.ID, id string) (store.Run, error) {
	r, err := scanRun(s.queryRow(ctx, q, `SELECT `+runColumns+` FROM runs WHERE workspace_id = ? AND id = ?`, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Run{}, store.ErrNotFound
	}
	return r, err
}

func (s *Store) ListRuns(ctx context.Context, ws workspace.ID, f store.RunFilter) ([]store.Run, string, error) {
	query := `SELECT ` + runColumns + ` FROM runs WHERE workspace_id = ? AND schedule = ?`
	args := []any{ws, f.Schedule}
	if f.State != "" {
		query += ` AND state = ?`
		args = append(args, f.State)
	}
	if f.Cursor != "" {
		query += ` AND id < ?`
		args = append(args, f.Cursor)
	}
	// One extra row tells whether there is a next page.
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit+1)
	runs, err := s.listRuns(ctx, s.db, query, args...)
	if err != nil {
		return nil, "", err
	}
	var cursor string
	if len(runs) > f.Limit {
		runs = runs[:f.Limit]
		cursor = runs[len(runs)-1].ID
	}
	return runs, cursor, nil
}

func (s *Store) listRuns(ctx context.Context, q querier, query string, args ...any) ([]store.Run, error) {
	rows, err := s.query(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []store.Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func (s *Store) ListRunAttempts(ctx context.Context, ws workspace.ID, runID string) ([]store.Attempt, error) {
	rows, err := s.query(ctx, s.db, `SELECT id, run_id, number, started_at, finished_at,
			status_code, outcome, error, response_body, response_headers, retried_at
		FROM run_attempts WHERE workspace_id = ? AND run_id = ? ORDER BY number`, ws, runID)
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
		if err := rows.Scan(&a.ID, &a.RunID, &a.Number, &started, &finished,
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

func (s *Store) CancelRun(ctx context.Context, ws workspace.ID, id string) (store.Run, error) {
	var r store.Run
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.ts(time.Now())
		// A running run keeps its lease, so its in-flight attempt is still
		// recorded (see FinishRunAttempt).
		res, err := s.exec(ctx, tx, `UPDATE runs SET state = 'cancelled', finished_at = ?, updated_at = ?
			WHERE workspace_id = ? AND id = ? AND state IN ('pending', 'running')`, now, now, ws, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if r, err = s.getRun(ctx, tx, ws, id); err != nil {
			return err
		}
		if n == 0 && r.State != store.RunCancelled {
			return store.ErrInvalidState
		}
		return nil
	})
	return r, err
}

func (s *Store) RetryRun(ctx context.Context, ws workspace.ID, id string) (store.Run, error) {
	var r store.Run
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.ts(time.Now())
		res, err := s.exec(ctx, tx, `UPDATE runs SET
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
		if r, err = s.getRun(ctx, tx, ws, id); err != nil {
			return err
		}
		if n == 0 {
			return store.ErrInvalidState
		}
		return nil
	})
	if err == nil {
		s.wake()
	}
	return r, err
}

// Schedule dispatch

func (s *Store) DueSchedules(ctx context.Context, now time.Time) ([]store.Schedule, error) {
	return s.listSchedules(ctx, s.db, `SELECT `+scheduleColumns+` FROM schedules
		WHERE next_run_at IS NOT NULL AND next_run_at <= ? AND NOT paused
		ORDER BY next_run_at`, s.ts(now))
}

func (s *Store) FireSchedule(ctx context.Context, f store.Fire, now time.Time) (store.Run, bool, error) {
	var (
		r     store.Run
		fired bool
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// The guard on next_run_at makes a second fire of the same slot (a
		// stale leader, or the schedule changed meanwhile) a no-op.
		res, err := s.exec(ctx, tx, `UPDATE schedules SET next_run_at = ?
			WHERE workspace_id = ? AND name = ? AND NOT paused AND next_run_at = ?`,
			s.ts(f.NextRunAt), f.WorkspaceID, f.Schedule, s.ts(f.Due))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		sc, err := s.getSchedule(ctx, tx, f.WorkspaceID, f.Schedule)
		if err != nil {
			return err
		}
		state, reason := store.RunPending, f.SkipReason
		if reason == "" && sc.Overlap == store.OverlapSkip {
			var active int
			err := s.queryRow(ctx, tx, `SELECT count(*) FROM (SELECT 1 FROM runs
				WHERE workspace_id = ? AND schedule = ? AND state IN ('pending', 'running') LIMIT 1) a`,
				f.WorkspaceID, f.Schedule).Scan(&active)
			if err != nil {
				return err
			}
			if active > 0 {
				reason = store.SkipOverlap
			}
		}
		if reason != "" {
			state = store.RunSkipped
		}
		r, fired, err = s.insertRun(ctx, tx, sc, store.TriggerSchedule, f.Slot, state, reason, now)
		return err
	})
	if err != nil {
		return store.Run{}, false, err
	}
	return r, fired, nil
}

func (s *Store) NextScheduleWork(ctx context.Context, after time.Time) (time.Time, bool, error) {
	var fire, run dbTime
	a := s.ts(after)
	err := s.queryRow(ctx, s.db, `SELECT
			(SELECT min(next_run_at) FROM schedules WHERE next_run_at > ?),
			(SELECT min(run_at) FROM runs WHERE state = 'pending' AND run_at > ?)`, a, a).Scan(&fire, &run)
	switch {
	case err != nil:
		return time.Time{}, false, err
	case fire.Valid && run.Valid && run.Time.Before(fire.Time):
		return run.Time, true, nil
	case fire.Valid:
		return fire.Time, true, nil
	case run.Valid:
		return run.Time, true, nil
	}
	return time.Time{}, false, nil
}

func (s *Store) ClaimRuns(ctx context.Context, limit int, now time.Time, grace time.Duration) ([]store.RunClaim, error) {
	query := `SELECT ` + runColumns + ` FROM runs
		WHERE state = 'pending' AND run_at <= ? ORDER BY run_at LIMIT ?`
	if s.opts.Postgres {
		query += ` FOR UPDATE SKIP LOCKED`
	}
	var claims []store.RunClaim
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		runs, err := s.listRuns(ctx, tx, query, s.ts(now), limit)
		if err != nil {
			return err
		}
		schedules := map[[2]string]store.Schedule{}
		for _, r := range runs {
			key := [2]string{string(r.WorkspaceID), r.Schedule}
			sc, ok := schedules[key]
			if !ok {
				if sc, err = s.getSchedule(ctx, tx, r.WorkspaceID, r.Schedule); err != nil {
					return err
				}
				schedules[key] = sc
			}
			lease := now.Add(sc.Timeout + grace).UTC()
			if _, err := s.exec(ctx, tx, `UPDATE runs
				SET state = 'running', attempt_count = attempt_count + 1, lease_expires_at = ?, updated_at = ?
				WHERE id = ?`, s.ts(lease), s.ts(now), r.ID); err != nil {
				return err
			}
			r.State = store.RunRunning
			r.AttemptCount++
			r.LeaseExpiresAt = &lease
			r.UpdatedAt = now.UTC()
			claims = append(claims, store.RunClaim{Run: r, Schedule: sc})
		}
		return nil
	})
	return claims, err
}

func (s *Store) FinishRunAttempt(ctx context.Context, ws workspace.ID, r store.RunAttemptResult) (store.RunState, error) {
	var runAt, finishedAt *time.Time
	switch r.NextState {
	case store.RunPending:
		runAt = &r.RunAt
	case store.RunSucceeded, store.RunFailed:
		finishedAt = &r.FinishedAt
	default:
		return "", fmt.Errorf("invalid next state %q", r.NextState)
	}
	respHeaders, err := marshalHeaders(r.ResponseHeaders)
	if err != nil {
		return "", err
	}
	var state store.RunState
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		// As for tasks: a late result for a reaped lease is a no-op, and a
		// run cancelled mid-attempt stays cancelled.
		err := s.queryRow(ctx, tx, `UPDATE runs SET
				state = CASE WHEN state = 'cancelled' THEN state ELSE ? END,
				run_at = COALESCE(?, run_at), lease_expires_at = NULL,
				last_status = ?, last_outcome = ?, last_error = ?, updated_at = ?, last_attempt_at = ?,
				finished_at = CASE WHEN state = 'cancelled' THEN finished_at ELSE ? END
			WHERE workspace_id = ? AND id = ? AND lease_expires_at IS NOT NULL AND attempt_count = ?
			RETURNING state`,
			r.NextState, s.tsOrNil(runAt), nullInt(r.StatusCode), r.Outcome, nullString(r.Error),
			s.ts(r.FinishedAt), s.ts(r.FinishedAt), s.tsOrNil(finishedAt), ws, r.RunID, r.Number,
		).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrLeaseLost
		}
		if err != nil {
			return err
		}
		_, err = s.exec(ctx, tx, `INSERT INTO run_attempts (id, run_id, workspace_id, number,
				started_at, finished_at, status_code, outcome, error, response_body, response_headers, retried_at)
			SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
				CASE WHEN r.retried_at > COALESCE(
					(SELECT max(a.finished_at) FROM run_attempts a WHERE a.run_id = r.id), r.created_at)
				THEN r.retried_at END
			FROM runs r WHERE r.workspace_id = ? AND r.id = ?`,
			id.New("att"), r.RunID, ws, r.Number, s.ts(r.StartedAt), s.ts(r.FinishedAt),
			nullInt(r.StatusCode), r.Outcome, nullString(r.Error), nullString(r.ResponseBody), respHeaders,
			ws, r.RunID)
		return err
	})
	return state, err
}

func (s *Store) ExpiredRunLeases(ctx context.Context, now time.Time, limit int) ([]store.Run, error) {
	return s.listRuns(ctx, s.db, `SELECT `+runColumns+` FROM runs
		WHERE lease_expires_at IS NOT NULL AND lease_expires_at < ?
		ORDER BY lease_expires_at LIMIT ?`, s.ts(now), limit)
}

func (s *Store) DeleteFinishedRuns(ctx context.Context, before time.Time, limit int) (int, error) {
	// Each schedule's latest run is kept, so a monthly schedule still shows
	// how it last went after a week's retention. Attempts go by cascade.
	res, err := s.exec(ctx, s.db, `DELETE FROM runs WHERE id IN (
			SELECT r.id FROM runs r
			WHERE r.finished_at < ? AND r.lease_expires_at IS NULL
				AND r.id <> (SELECT max(l.id) FROM runs l
					WHERE l.workspace_id = r.workspace_id AND l.schedule = r.schedule)
			ORDER BY r.finished_at LIMIT ?
		)`, s.ts(before), limit)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (s *Store) AllScheduleStatus(ctx context.Context) ([]store.ScheduleStatus, error) {
	rows, err := s.query(ctx, s.db, `SELECT sc.workspace_id, sc.name, sc.paused,
			(SELECT max(r.finished_at) FROM runs r
				WHERE r.workspace_id = sc.workspace_id AND r.schedule = sc.name AND r.state = 'succeeded')
		FROM schedules sc ORDER BY sc.workspace_id, sc.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ScheduleStatus
	for rows.Next() {
		var (
			st   store.ScheduleStatus
			last dbTime
		)
		if err := rows.Scan(&st.WorkspaceID, &st.Schedule, &st.Paused, &last); err != nil {
			return nil, err
		}
		st.LastSuccessAt = last.ptr()
		out = append(out, st)
	}
	return out, rows.Err()
}
