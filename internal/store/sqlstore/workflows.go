package sqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/id"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// A workflow run advances only inside the transactions that finish its
// steps' tasks (FinishAttempts, CancelTask, CancelPendingTasks,
// DeleteQueue) and that complete the groups its steps wait for
// (completeGroup). Each records the step's outcome and enqueues the steps
// it unblocks before committing, so a crash can't lose progress, and the
// status guards on steps (pending or waiting → finished, blocked → pending)
// make a duplicate event a no-op: a step gets one task, ever.

// Workflows

const workflowColumns = `workspace_id, name, config, version, created_at, updated_at`

func scanWorkflow(row interface{ Scan(...any) error }) (store.Workflow, error) {
	var (
		w                store.Workflow
		cfg              []byte
		created, updated dbTime
	)
	if err := row.Scan(&w.WorkspaceID, &w.Name, &cfg, &w.Version, &created, &updated); err != nil {
		return w, err
	}
	if err := json.Unmarshal(cfg, &w.WorkflowConfig); err != nil {
		return w, fmt.Errorf("decode workflow config: %w", err)
	}
	w.CreatedAt, w.UpdatedAt = created.Time, updated.Time
	return w, nil
}

func (s *Store) PutWorkflow(ctx context.Context, ws workspace.ID, name string, cfg store.WorkflowConfig) (store.Workflow, bool, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return store.Workflow{}, false, err
	}
	var (
		w       store.Workflow
		created bool
	)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.ts(time.Now())
		old, err := s.getWorkflow(ctx, tx, ws, name, true)
		switch {
		case errors.Is(err, store.ErrNotFound):
			created = true
			_, err = s.exec(ctx, tx, `INSERT INTO workflows (`+workflowColumns+`) VALUES (?, ?, ?, 1, ?, ?)`,
				ws, name, string(b), now, now)
		case err != nil:
			return err
		default:
			// Compared as Go marshals them, since Postgres reformats JSONB.
			// Only a changed definition is a new version.
			prev, _ := json.Marshal(old.WorkflowConfig)
			version := old.Version
			if !bytes.Equal(prev, b) {
				version++
			}
			_, err = s.exec(ctx, tx, `UPDATE workflows SET config = ?, version = ?, updated_at = ?
				WHERE workspace_id = ? AND name = ?`, string(b), version, now, ws, name)
		}
		if err != nil {
			return err
		}
		w, err = s.getWorkflow(ctx, tx, ws, name, false)
		return err
	})
	return w, created, err
}

func (s *Store) GetWorkflow(ctx context.Context, ws workspace.ID, name string) (store.Workflow, error) {
	w, err := s.getWorkflow(ctx, s.db, ws, name, false)
	if err != nil {
		return w, err
	}
	last, err := s.lastWorkflowRuns(ctx, ws, name)
	w.LastRun = last[name]
	return w, err
}

func (s *Store) getWorkflow(ctx context.Context, q querier, ws workspace.ID, name string, lock bool) (store.Workflow, error) {
	query := `SELECT ` + workflowColumns + ` FROM workflows WHERE workspace_id = ? AND name = ?`
	if lock {
		query += s.forUpdate()
	}
	w, err := scanWorkflow(s.queryRow(ctx, q, query, ws, name))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Workflow{}, store.ErrNotFound
	}
	return w, err
}

func (s *Store) ListWorkflows(ctx context.Context, ws workspace.ID) ([]store.Workflow, error) {
	rows, err := s.query(ctx, s.db, `SELECT `+workflowColumns+` FROM workflows WHERE workspace_id = ? ORDER BY name`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.Workflow{}
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	last, err := s.lastWorkflowRuns(ctx, ws, "")
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].LastRun = last[out[i].Name]
	}
	return out, nil
}

// lastWorkflowRuns returns the latest run of the named workflow, or of
// every workflow in the workspace if name is empty, by workflow.
func (s *Store) lastWorkflowRuns(ctx context.Context, ws workspace.ID, name string) (map[string]*store.WorkflowRunSummary, error) {
	query := `SELECT r.workflow, r.id, r.status, r.created_at, r.finished_at, r.error FROM workflow_runs r
		WHERE r.workspace_id = ? AND r.id = (SELECT max(l.id) FROM workflow_runs l
			WHERE l.workspace_id = r.workspace_id AND l.workflow = r.workflow)`
	args := []any{ws}
	if name != "" {
		query += ` AND r.workflow = ?`
		args = append(args, name)
	}
	rows, err := s.query(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*store.WorkflowRunSummary{}
	for rows.Next() {
		var (
			workflow          string
			r                 store.WorkflowRunSummary
			created, finished dbTime
			errText           sql.NullString
		)
		if err := rows.Scan(&workflow, &r.ID, &r.Status, &created, &finished, &errText); err != nil {
			return nil, err
		}
		r.CreatedAt, r.FinishedAt, r.Error = created.Time, finished.ptr(), errText.String
		out[workflow] = &r
	}
	return out, rows.Err()
}

func (s *Store) DeleteWorkflow(ctx context.Context, ws workspace.ID, name string) error {
	res, err := s.exec(ctx, s.db, `DELETE FROM workflows WHERE workspace_id = ? AND name = ?`, ws, name)
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

// Runs

const workflowRunColumns = `id, workspace_id, workflow, version, config, input, status, error,
	idempotency_key, schedule_run_id, deadline_at, created_at, finished_at, attempt, retried_at`

func scanWorkflowRun(row interface{ Scan(...any) error }) (store.WorkflowRun, error) {
	var (
		r                           store.WorkflowRun
		cfg, input                  []byte
		errText, idemKey, schedRun  sql.NullString
		deadline, created, finished dbTime
		retried                     dbTime
	)
	err := row.Scan(&r.ID, &r.WorkspaceID, &r.Workflow, &r.Version, &cfg, &input, &r.Status, &errText,
		&idemKey, &schedRun, &deadline, &created, &finished, &r.Attempt, &retried)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(cfg, &r.Config); err != nil {
		return r, fmt.Errorf("decode workflow run config: %w", err)
	}
	if len(input) > 0 {
		r.Input = json.RawMessage(input)
	}
	r.Error, r.IdempotencyKey, r.ScheduleRunID = errText.String, idemKey.String, schedRun.String
	r.DeadlineAt, r.CreatedAt, r.FinishedAt, r.RetriedAt = deadline.Time, created.Time, finished.ptr(), retried.ptr()
	return r, nil
}

func (s *Store) StartWorkflowRun(ctx context.Context, ws workspace.ID, name string, nr store.NewWorkflowRun, now time.Time) (store.WorkflowRun, bool, error) {
	var (
		r        store.WorkflowRun
		created  bool
		enqueued bool
	)
	now = now.UTC().Truncate(time.Millisecond)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		w, err := s.getWorkflow(ctx, tx, ws, name, false)
		if err != nil {
			return err
		}
		cfg, err := json.Marshal(w.WorkflowConfig)
		if err != nil {
			return err
		}
		var input any
		if len(nr.Input) > 0 && string(nr.Input) != "null" {
			input = string(nr.Input)
		}
		maxDuration := w.EffectiveMaxDuration()
		runID := id.New("wfr")
		res, err := s.exec(ctx, tx, `INSERT INTO workflow_runs (`+workflowRunColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, NULL, 1, NULL)
			ON CONFLICT (workspace_id, workflow, idempotency_key) WHERE idempotency_key IS NOT NULL
			DO NOTHING`,
			runID, ws, name, w.Version, string(cfg), input, store.WorkflowRunning,
			nullString(nr.IdempotencyKey), nullString(nr.ScheduleRunID), s.ts(now.Add(maxDuration)), s.ts(now))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			r, err = scanWorkflowRun(s.queryRow(ctx, tx, `SELECT `+workflowRunColumns+` FROM workflow_runs
				WHERE workspace_id = ? AND workflow = ? AND idempotency_key = ?`, ws, name, nr.IdempotencyKey))
			return err
		}
		created = true
		if r, err = s.getWorkflowRunRow(ctx, tx, ws, runID); err != nil {
			return err
		}
		if nr.ScheduleRunID != "" {
			if _, err := s.exec(ctx, tx, `UPDATE runs SET workflow_run_id = ? WHERE workspace_id = ? AND id = ?`,
				runID, ws, nr.ScheduleRunID); err != nil {
				return err
			}
		}
		for i, sc := range r.Config.Steps {
			deps, err := json.Marshal(nonNil(sc.DependsOn))
			if err != nil {
				return err
			}
			if _, err := s.exec(ctx, tx, `INSERT INTO workflow_steps (id, run_id, workspace_id, position, name,
					depends_on, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				id.New("wfs"), runID, ws, i, sc.Name, string(deps), store.StepBlocked, s.ts(now)); err != nil {
				return err
			}
		}
		steps, err := s.listSteps(ctx, tx, runID)
		if err != nil {
			return err
		}
		for _, st := range steps {
			if len(st.DependsOn) > 0 {
				continue
			}
			e, err := s.startStep(ctx, tx, &r, st, now)
			if err != nil {
				return err
			}
			enqueued = enqueued || e
			if r.Status.Finished() {
				break
			}
		}
		return nil
	})
	if err != nil {
		return store.WorkflowRun{}, false, err
	}
	if enqueued {
		s.wake()
	}
	return r, created, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (s *Store) GetWorkflowRun(ctx context.Context, ws workspace.ID, id string) (store.WorkflowRun, error) {
	r, err := s.getWorkflowRunRow(ctx, s.db, ws, id)
	if err != nil {
		return r, err
	}
	if r.Steps, err = s.listSteps(ctx, s.db, id); err != nil {
		return r, err
	}
	var taskIDs []any
	for _, st := range r.Steps {
		if st.TaskID != "" {
			taskIDs = append(taskIDs, st.TaskID)
		}
	}
	if len(taskIDs) == 0 {
		return r, nil
	}
	// At most store.MaxWorkflowSteps, under SQLite's limit on parameters.
	tasks, err := s.listTasks(ctx, s.db, `SELECT `+taskColumns+` FROM tasks WHERE workspace_id = ? AND id IN (`+
		placeholders(len(taskIDs))+`)`, append([]any{ws}, taskIDs...)...)
	if err != nil {
		return r, err
	}
	byID := map[string]*store.Task{}
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}
	for i := range r.Steps {
		r.Steps[i].Task = byID[r.Steps[i].TaskID]
	}
	return r, nil
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

func (s *Store) getWorkflowRunRow(ctx context.Context, q querier, ws workspace.ID, id string) (store.WorkflowRun, error) {
	r, err := scanWorkflowRun(s.queryRow(ctx, q,
		`SELECT `+workflowRunColumns+` FROM workflow_runs WHERE workspace_id = ? AND id = ?`, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.WorkflowRun{}, store.ErrNotFound
	}
	return r, err
}

func (s *Store) ListWorkflowRuns(ctx context.Context, ws workspace.ID, f store.WorkflowRunFilter) ([]store.WorkflowRun, string, error) {
	query := `SELECT ` + workflowRunColumns + ` FROM workflow_runs WHERE workspace_id = ?`
	args := []any{ws}
	if f.Workflow != "" {
		query += ` AND workflow = ?`
		args = append(args, f.Workflow)
	}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
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
	runs := []store.WorkflowRun{}
	for rows.Next() {
		r, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, "", err
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var cursor string
	if len(runs) > f.Limit {
		runs = runs[:f.Limit]
		cursor = runs[len(runs)-1].ID
	}
	return runs, cursor, nil
}

func (s *Store) CancelWorkflowRun(ctx context.Context, ws workspace.ID, id string) (store.WorkflowRun, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		r, err := s.lockRun(ctx, tx, ws, id)
		if err != nil {
			return err
		}
		switch r.Status {
		case store.WorkflowCancelled:
			return nil
		case store.WorkflowRunning:
		default:
			return store.ErrInvalidState
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		return s.stopRun(ctx, tx, &r, store.WorkflowCancelled, "cancelled", store.StepSkipped, now)
	})
	if err != nil {
		return store.WorkflowRun{}, err
	}
	return s.GetWorkflowRun(ctx, ws, id)
}

func (s *Store) RetryWorkflowRun(ctx context.Context, ws workspace.ID, id string, now time.Time) (store.WorkflowRun, error) {
	now = now.UTC().Truncate(time.Millisecond)
	enqueued := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		r, err := s.getWorkflowRunRow(ctx, tx, ws, id)
		if err != nil {
			return err
		}
		steps, err := s.listSteps(ctx, tx, id)
		if err != nil {
			return err
		}
		// The groups steps were waiting for are locked first, in ID order, as
		// completeGroup holds a group when it reaches the run: lock order is
		// group, run, step.
		var groupIDs []string
		for _, st := range steps {
			if st.WaitType == "group" && st.Status != store.StepSucceeded && !slices.Contains(groupIDs, st.WaitID) {
				groupIDs = append(groupIDs, st.WaitID)
			}
		}
		slices.Sort(groupIDs)
		groups := map[string]store.Group{}
		for _, gid := range groupIDs {
			g, err := scanGroup(s.queryRow(ctx, tx, `UPDATE task_groups SET status = status
				WHERE workspace_id = ? AND id = ? RETURNING `+groupColumns, ws, gid))
			if errors.Is(err, sql.ErrNoRows) {
				continue // swept: its step runs again
			}
			if err != nil {
				return err
			}
			groups[gid] = g
		}
		if r, err = s.lockRun(ctx, tx, ws, id); err != nil {
			return err
		}
		if r.Status != store.WorkflowFailed && r.Status != store.WorkflowCancelled {
			return store.ErrInvalidState
		}
		if steps, err = s.listSteps(ctx, tx, id); err != nil {
			return err
		}
		r.Attempt++
		r.Status, r.Error, r.FinishedAt, r.RetriedAt = store.WorkflowRunning, "", nil, &now
		r.DeadlineAt = now.Add(r.Config.EffectiveMaxDuration())
		if _, err := s.exec(ctx, tx, `UPDATE workflow_runs SET status = ?, error = NULL, finished_at = NULL,
				attempt = ?, retried_at = ?, deadline_at = ? WHERE id = ?`,
			r.Status, r.Attempt, s.ts(now), s.ts(r.DeadlineAt), r.ID); err != nil {
			return err
		}

		status := map[string]store.StepStatus{}
		for i, st := range steps {
			next := st.Status
			switch st.Status {
			case store.StepSucceeded, store.StepPending, store.StepWaiting:
				// Done, or a request still in flight from before the run
				// stopped, which now advances the run again.
			case store.StepFailed, store.StepSkipped, store.StepBlocked:
				next = store.StepBlocked
				if g, ok := groups[st.WaitID]; ok && st.WaitType == "group" && g.Status != store.GroupFailed {
					// Its request succeeded and handed over to a group that
					// didn't fail: the step follows the group.
					next = store.StepWaiting
					if g.Status == store.GroupSucceeded {
						next = store.StepSucceeded
					}
				}
				if err := s.reopenStep(ctx, tx, st, next, now); err != nil {
					return err
				}
			}
			steps[i].Status, status[st.Name] = next, next
		}

		// Steps whose dependencies have all succeeded start, as at the start
		// of a run. A step that just succeeded through its group may have
		// readied others, so this goes round in definition order until
		// nothing more starts; each pass starts at least one step or ends.
		for started := true; started && r.Status == store.WorkflowRunning; {
			started = false
			for i, st := range steps {
				if st.Status != store.StepBlocked {
					continue
				}
				ready := true
				for _, dep := range st.DependsOn {
					ready = ready && status[dep] == store.StepSucceeded
				}
				if !ready {
					continue
				}
				e, err := s.startStep(ctx, tx, &r, st, now)
				if err != nil {
					return err
				}
				enqueued = enqueued || e
				steps[i].Status, status[st.Name], started = store.StepPending, store.StepPending, true
				if r.Status.Finished() {
					return nil
				}
			}
		}
		for _, v := range status {
			if v != store.StepSucceeded {
				return nil
			}
		}
		// Every step left was waiting for a group that has since succeeded.
		_, err = s.exec(ctx, tx, `UPDATE workflow_runs SET status = ?, finished_at = ? WHERE id = ?`,
			store.WorkflowSucceeded, s.ts(now), r.ID)
		return err
	})
	if err != nil {
		return store.WorkflowRun{}, err
	}
	if enqueued {
		s.wake()
	}
	return s.GetWorkflowRun(ctx, ws, id)
}

// reopenStep puts a stopped step of a retried run back to status: blocked
// to run again, waiting for its group, or succeeded through it. What it
// waited for stays on a step that follows its group.
func (s *Store) reopenStep(ctx context.Context, tx *sql.Tx, st store.WorkflowStep, status store.StepStatus, now time.Time) error {
	switch status {
	case store.StepBlocked:
		_, err := s.exec(ctx, tx, `UPDATE workflow_steps SET status = ?, error = NULL, finished_at = NULL,
			ready_at = NULL, http_done_at = NULL, wait_type = NULL, wait_id = NULL WHERE id = ?`, status, st.ID)
		return err
	case store.StepWaiting:
		_, err := s.exec(ctx, tx, `UPDATE workflow_steps SET status = ?, error = NULL, finished_at = NULL
			WHERE id = ?`, status, st.ID)
		return err
	default:
		_, err := s.exec(ctx, tx, `UPDATE workflow_steps SET status = ?, error = NULL, finished_at = ?
			WHERE id = ?`, status, s.ts(now), st.ID)
		return err
	}
}

// lockRun reads a run, locking its row so that its status can't change
// under the transaction. Steps finishing at once, in concurrent
// transactions, take turns on it.
func (s *Store) lockRun(ctx context.Context, tx *sql.Tx, ws workspace.ID, id string) (store.WorkflowRun, error) {
	r, err := scanWorkflowRun(s.queryRow(ctx, tx, `UPDATE workflow_runs SET status = status
		WHERE workspace_id = ? AND id = ? RETURNING `+workflowRunColumns, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.WorkflowRun{}, store.ErrNotFound
	}
	return r, err
}

func (s *Store) ExpireWorkflowRuns(ctx context.Context, now time.Time, limit int) (int, error) {
	rows, err := s.query(ctx, s.db, `SELECT workspace_id, id FROM workflow_runs
		WHERE status = 'running' AND deadline_at < ? ORDER BY deadline_at LIMIT ?`, s.ts(now), limit)
	if err != nil {
		return 0, err
	}
	type key struct {
		ws workspace.ID
		id string
	}
	var due []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.ws, &k.id); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, k := range due {
		err := s.inTx(ctx, func(tx *sql.Tx) error {
			r, err := s.lockRun(ctx, tx, k.ws, k.id)
			if err != nil || r.Status != store.WorkflowRunning {
				return err
			}
			now := now.UTC().Truncate(time.Millisecond)
			reason := "run exceeded its max_duration of " + r.Config.EffectiveMaxDuration().String()
			n++
			// A waiting step is what it timed out on.
			return s.stopRun(ctx, tx, &r, store.WorkflowFailed, reason, store.StepFailed, now)
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (s *Store) DeleteFinishedWorkflowRuns(ctx context.Context, sw store.Sweep, limit int) (int, error) {
	// Each workflow's latest run is kept, as each schedule's is. Steps go by
	// cascade; their tasks age out with the tasks.
	where, args := s.sweepWhere("r.", sw)
	res, err := s.exec(ctx, s.db, `DELETE FROM workflow_runs WHERE id IN (
			SELECT r.id FROM workflow_runs r
			WHERE `+where+`
				AND r.id <> (SELECT max(l.id) FROM workflow_runs l
					WHERE l.workspace_id = r.workspace_id AND l.workflow = r.workflow)
			ORDER BY r.finished_at LIMIT ?
		)`, append(args, limit)...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Steps

const stepColumns = `id, run_id, name, depends_on, status, task_id, wait_type, wait_id, error,
	created_at, ready_at, http_done_at, finished_at`

func scanStep(row interface{ Scan(...any) error }) (store.WorkflowStep, error) {
	var (
		st                               store.WorkflowStep
		deps                             []byte
		taskID, waitType, waitID, errTxt sql.NullString
		created, ready, httpDone, fin    dbTime
	)
	err := row.Scan(&st.ID, &st.RunID, &st.Name, &deps, &st.Status, &taskID, &waitType, &waitID, &errTxt,
		&created, &ready, &httpDone, &fin)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(deps, &st.DependsOn); err != nil {
		return st, fmt.Errorf("decode depends_on: %w", err)
	}
	st.TaskID, st.WaitType, st.WaitID, st.Error = taskID.String, waitType.String, waitID.String, errTxt.String
	st.CreatedAt, st.ReadyAt, st.HTTPDoneAt, st.FinishedAt = created.Time, ready.ptr(), httpDone.ptr(), fin.ptr()
	return st, nil
}

func (s *Store) listSteps(ctx context.Context, q querier, runID string) ([]store.WorkflowStep, error) {
	rows, err := s.query(ctx, q, `SELECT `+stepColumns+` FROM workflow_steps WHERE run_id = ? ORDER BY position`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := []store.WorkflowStep{}
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		steps = append(steps, st)
	}
	return steps, rows.Err()
}

// stepConfig is the definition of the run's step with this name.
func stepConfig(r *store.WorkflowRun, name string) (store.WorkflowStepConfig, bool) {
	for _, sc := range r.Config.Steps {
		if sc.Name == name {
			return sc, true
		}
	}
	return store.WorkflowStepConfig{}, false
}

// startStep enqueues a blocked step's task and reports whether it did. A
// step that has a task from an earlier attempt of the run gets it back,
// pending again with a fresh set of attempts, so a step keeps one task
// however often its run is retried. A step whose queue no longer exists
// fails, which stops the run.
func (s *Store) startStep(ctx context.Context, tx *sql.Tx, r *store.WorkflowRun, st store.WorkflowStep, now time.Time) (bool, error) {
	// The guard makes a step that two events unblock start once.
	res, err := s.exec(ctx, tx, `UPDATE workflow_steps SET status = ?, ready_at = ?
		WHERE id = ? AND status = ?`, store.StepPending, s.ts(now), st.ID, store.StepBlocked)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	sc, ok := stepConfig(r, st.Name)
	if !ok {
		return false, fmt.Errorf("workflow run %s has no step %q", r.ID, st.Name)
	}
	headers := map[string]string{}
	for k, v := range sc.Headers {
		headers[k] = v
	}
	// Workly-* headers can't be set by users, so these can't be spoofed by
	// a step's own configuration.
	headers[store.HeaderWorkflow] = r.Workflow
	headers[store.HeaderWorkflowRunID] = r.ID
	headers[store.HeaderWorkflowStep] = st.Name
	headers[store.HeaderWorkflowStepID] = st.ID
	headers[store.HeaderWorkflowRunAttempt] = strconv.Itoa(r.Attempt)
	body := sc.Body
	if body == nil {
		body, err = json.Marshal(store.WorkflowStepBody{
			WorkflowRunID: r.ID, Workflow: r.Workflow, Step: st.Name, StepID: st.ID, RunAttempt: r.Attempt,
			Input: inputOrNull(r.Input),
		})
		if err != nil {
			return false, err
		}
		if !hasHeader(headers, "Content-Type") {
			headers["Content-Type"] = "application/json"
		}
	}
	if st.TaskID != "" {
		retried, err := s.replayStepTask(ctx, tx, r.WorkspaceID, st.TaskID, headers, body, now)
		if err != nil || retried {
			return retried, err
		}
		// Swept with old tasks: a new one takes its place.
	}
	t, _, err := s.createTask(ctx, tx, r.WorkspaceID, store.NewTask{
		Queue: sc.Queue, Method: sc.Method, URL: sc.URL, Headers: headers, Body: body, RunAt: now,
		MaxAttempts: sc.MaxAttempts, Timeout: sc.Timeout,
		// The step's ID: a second guard against a step getting two tasks.
		IdempotencyKey: st.ID, WorkflowStepID: st.ID,
	}, sc.Queue == store.WorkflowQueue)
	if errors.Is(err, store.ErrNotFound) {
		reason := fmt.Sprintf("queue %q does not exist", sc.Queue)
		if err := s.setStep(ctx, tx, st.ID, store.StepFailed, reason, now, store.StepPending); err != nil {
			return false, err
		}
		return false, s.stopRun(ctx, tx, r, store.WorkflowFailed, stepFailure(st.Name, reason), store.StepSkipped, now)
	}
	if err != nil {
		return false, err
	}
	_, err = s.exec(ctx, tx, `UPDATE workflow_steps SET task_id = ? WHERE id = ?`, t.ID, st.ID)
	return err == nil, err
}

// replayStepTask makes a finished step task pending again, as RetryTask
// does, with its request as of the run's new attempt. It reports false if
// the task no longer exists.
func (s *Store) replayStepTask(ctx context.Context, tx *sql.Tx, ws workspace.ID, taskID string,
	headers map[string]string, body []byte, now time.Time) (bool, error) {
	h, err := marshalHeaders(headers)
	if err != nil {
		return false, err
	}
	res, err := s.exec(ctx, tx, `UPDATE tasks SET replayed_attempts = attempt_count, state = 'pending',
			run_at = ?, finished_at = NULL, updated_at = ?, retried_at = ?, headers = ?, body = ?
		WHERE workspace_id = ? AND id = ? AND state IN ('succeeded', 'failed', 'cancelled')`,
		s.ts(now), s.ts(now), s.ts(now), h, body, ws, taskID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func inputOrNull(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return json.RawMessage("null")
	}
	return in
}

func hasHeader(h map[string]string, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func stepFailure(step, reason string) string { return fmt.Sprintf("step %q failed: %s", step, reason) }

// setStep finishes a step that is in one of the from states.
func (s *Store) setStep(ctx context.Context, tx *sql.Tx, stepID string, status store.StepStatus, reason string,
	now time.Time, from ...store.StepStatus) error {
	args := []any{status, nullString(reason), s.ts(now), stepID}
	for _, f := range from {
		args = append(args, f)
	}
	_, err := s.exec(ctx, tx, `UPDATE workflow_steps SET status = ?, error = ?, finished_at = ?
		WHERE id = ? AND status IN (`+placeholders(len(from))+`)`, args...)
	return err
}

// stopRun finishes a running run: its blocked steps are skipped, and so
// are pending ones whose task hasn't started (which is cancelled). Tasks
// being delivered finish, and their steps record how they went, but start
// nothing. Steps waiting for a group become waiting (skipped, or failed
// when the run timed out); their group goes on, it just no longer decides
// anything.
func (s *Store) stopRun(ctx context.Context, tx *sql.Tx, r *store.WorkflowRun, status store.WorkflowRunStatus,
	reason string, waiting store.StepStatus, now time.Time) error {
	res, err := s.exec(ctx, tx, `UPDATE workflow_runs SET status = ?, error = ?, finished_at = ?
		WHERE id = ? AND status = ?`, status, nullString(reason), s.ts(now), r.ID, store.WorkflowRunning)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	r.Status, r.Error, r.FinishedAt = status, reason, &now
	skipped := "not run: workflow run " + string(status)
	if _, err := s.exec(ctx, tx, `UPDATE workflow_steps SET status = ?, error = ?, finished_at = ?
		WHERE run_id = ? AND status = ?`, store.StepSkipped, skipped, s.ts(now), r.ID, store.StepBlocked); err != nil {
		return err
	}
	// Step tasks have no group or callback, so cancelling them needs none
	// of CancelTask's bookkeeping.
	rows, err := s.query(ctx, tx, `UPDATE tasks SET state = 'cancelled', finished_at = ?, updated_at = ?
		WHERE workspace_id = ? AND state = 'pending' AND workflow_step_id IN (
			SELECT id FROM workflow_steps WHERE run_id = ? AND status = ?)
		RETURNING workflow_step_id`, s.ts(now), s.ts(now), r.WorkspaceID, r.ID, store.StepPending)
	if err != nil {
		return err
	}
	var cancelled []string
	for rows.Next() {
		var stepID string
		if err := rows.Scan(&stepID); err != nil {
			rows.Close()
			return err
		}
		cancelled = append(cancelled, stepID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, stepID := range cancelled {
		if err := s.setStep(ctx, tx, stepID, store.StepSkipped, skipped, now, store.StepPending); err != nil {
			return err
		}
	}
	msg := skipped
	if waiting == store.StepFailed {
		msg = reason
	}
	_, err = s.exec(ctx, tx, `UPDATE workflow_steps SET status = ?, error = ?, finished_at = ?
		WHERE run_id = ? AND status = ?`, waiting, msg, s.ts(now), r.ID, store.StepWaiting)
	return err
}

// stepEvent is a step's task finishing, or the group it waits for
// completing.
type stepEvent struct {
	stepID string
	// state is the task's (or for a group, succeeded or failed).
	state  store.TaskState
	reason string
	// waitFor is the Workly-Wait-For header of a succeeded task's response.
	waitFor string
	// group is set for a group completing, which only a waiting step
	// expects.
	group bool
}

// advanceSteps records what the events say about their steps, which the
// transaction has just finished the tasks (or groups) of, and starts the
// steps they unblock. It reports whether it enqueued any task.
func (s *Store) advanceSteps(ctx context.Context, tx *sql.Tx, events []stepEvent) (bool, error) {
	enqueued := false
	for _, e := range events {
		en, err := s.advanceStep(ctx, tx, e)
		if err != nil {
			return false, err
		}
		enqueued = enqueued || en
	}
	return enqueued, nil
}

func (s *Store) advanceStep(ctx context.Context, tx *sql.Tx, e stepEvent) (bool, error) {
	var (
		runID string
		ws    workspace.ID
	)
	err := s.queryRow(ctx, tx, `SELECT run_id, workspace_id FROM workflow_steps WHERE id = ?`, e.stepID).Scan(&runID, &ws)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // its run was swept
	}
	if err != nil {
		return false, err
	}
	// A group the step hands its completion to is locked first, as
	// completeGroup holds it when it reaches the run, so the two always
	// lock in the same order.
	var wait waitTarget
	if !e.group && e.state == store.TaskSucceeded && e.waitFor != "" {
		if wait, err = s.lockWaitTarget(ctx, tx, ws, e.waitFor); err != nil {
			return false, err
		}
	}
	// Then the run, then the step: every transaction that changes a run's
	// steps locks it, so they take turns and the last step to finish sees
	// the others.
	r, err := s.lockRun(ctx, tx, ws, runID)
	if err != nil {
		return false, err
	}
	st, err := scanStep(s.queryRow(ctx, tx, `SELECT `+stepColumns+` FROM workflow_steps WHERE id = ?`, e.stepID))
	if err != nil {
		return false, err
	}
	expect := store.StepPending
	if e.group {
		expect = store.StepWaiting
	}
	if st.Status != expect {
		return false, nil // a duplicate, or the run stopped it
	}
	now := time.Now().UTC().Truncate(time.Millisecond)

	switch e.state {
	case store.TaskCancelled, store.TaskFailed:
		return false, s.failStep(ctx, tx, &r, st, e.reason, now)
	case store.TaskSucceeded:
	default:
		return false, fmt.Errorf("step %s: unexpected task state %q", st.ID, e.state)
	}
	if !e.group {
		if _, err := s.exec(ctx, tx, `UPDATE workflow_steps SET http_done_at = ? WHERE id = ?`, s.ts(now), st.ID); err != nil {
			return false, err
		}
		if e.waitFor != "" {
			if wait.invalid != "" {
				return false, s.failStep(ctx, tx, &r, st, wait.invalid, now)
			}
			// Recorded even if the group has completed already, so the step
			// shows what it waited for.
			if _, err := s.exec(ctx, tx, `UPDATE workflow_steps SET wait_type = ?, wait_id = ? WHERE id = ?`,
				"group", wait.group.ID, st.ID); err != nil {
				return false, err
			}
			if !wait.group.Status.Finished() {
				if r.Status != store.WorkflowRunning {
					// A request in flight when the run stopped: skipped like
					// the run's other waiting steps. What it waits for stays,
					// for a retry of the run to follow.
					return false, s.setStep(ctx, tx, st.ID, store.StepSkipped,
						"not run: workflow run "+string(r.Status), now, store.StepPending)
				}
				// Until the group completes; completeGroup finishes the step.
				_, err := s.exec(ctx, tx, `UPDATE workflow_steps SET status = ? WHERE id = ? AND status = ?`,
					store.StepWaiting, st.ID, store.StepPending)
				return false, err
			}
			// It completed before the response was processed.
			if ok, reason := groupOutcome(wait.group); !ok {
				return false, s.failStep(ctx, tx, &r, st, reason, now)
			}
		}
	}
	return s.succeedStep(ctx, tx, &r, st, now)
}

func (s *Store) failStep(ctx context.Context, tx *sql.Tx, r *store.WorkflowRun, st store.WorkflowStep, reason string, now time.Time) error {
	if err := s.setStep(ctx, tx, st.ID, store.StepFailed, reason, now, store.StepPending, store.StepWaiting); err != nil {
		return err
	}
	// Fail-stop: nothing else starts. Other branches' tasks already being
	// delivered, and steps waiting on groups, finish as they would.
	return s.stopRun(ctx, tx, r, store.WorkflowFailed, stepFailure(st.Name, reason), store.StepSkipped, now)
}

func (s *Store) succeedStep(ctx context.Context, tx *sql.Tx, r *store.WorkflowRun, st store.WorkflowStep, now time.Time) (bool, error) {
	if err := s.setStep(ctx, tx, st.ID, store.StepSucceeded, "", now, store.StepPending, store.StepWaiting); err != nil {
		return false, err
	}
	if r.Status != store.WorkflowRunning {
		return false, nil // recorded, but the run stopped
	}
	steps, err := s.listSteps(ctx, tx, r.ID)
	if err != nil {
		return false, err
	}
	status := map[string]store.StepStatus{}
	for _, other := range steps {
		status[other.Name] = other.Status
	}
	enqueued := false
	for _, next := range steps {
		if next.Status != store.StepBlocked || !slices.Contains(next.DependsOn, st.Name) {
			continue
		}
		ready := true
		for _, dep := range next.DependsOn {
			ready = ready && status[dep] == store.StepSucceeded
		}
		if !ready {
			continue
		}
		e, err := s.startStep(ctx, tx, r, next, now)
		if err != nil {
			return false, err
		}
		enqueued = enqueued || e
		if r.Status.Finished() {
			return enqueued, nil
		}
		status[next.Name] = store.StepPending
	}
	for _, v := range status {
		if v != store.StepSucceeded {
			return enqueued, nil
		}
	}
	_, err = s.exec(ctx, tx, `UPDATE workflow_runs SET status = ?, finished_at = ? WHERE id = ? AND status = ?`,
		store.WorkflowSucceeded, s.ts(now), r.ID, store.WorkflowRunning)
	r.Status, r.FinishedAt = store.WorkflowSucceeded, &now
	return enqueued, err
}

// waitTarget is the resource a step's response handed its completion to,
// or why the handing over is invalid.
type waitTarget struct {
	group   store.Group
	invalid string
}

// lockWaitTarget finds and locks the resource a Workly-Wait-For header
// names. The no-op update locks the group's row, as completeGroup's count
// update does: whichever of the step starting to wait and the group
// completing commits second sees the other. Another workspace's group is
// not found.
func (s *Store) lockWaitTarget(ctx context.Context, tx *sql.Tx, ws workspace.ID, header string) (waitTarget, error) {
	typ, groupID, err := parseWaitFor(header)
	if err != nil {
		return waitTarget{invalid: fmt.Sprintf("invalid %s header %q: %v", store.WaitForHeader, header, err)}, nil
	}
	g, err := scanGroup(s.queryRow(ctx, tx, `UPDATE task_groups SET status = status
		WHERE workspace_id = ? AND id = ? RETURNING `+groupColumns, ws, groupID))
	if errors.Is(err, sql.ErrNoRows) {
		return waitTarget{invalid: fmt.Sprintf("invalid %s header: %s %q does not exist", store.WaitForHeader, typ, groupID)}, nil
	}
	return waitTarget{group: g}, err
}

// parseWaitFor parses a Workly-Wait-For value: one "<type>/<id>", where
// the only type so far is group.
func parseWaitFor(v string) (typ, id string, err error) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, ",") {
		return "", "", errors.New("it may name only one resource")
	}
	typ, id, found := strings.Cut(v, "/")
	switch {
	case !found || typ == "" || id == "" || strings.ContainsAny(id, " \t/"):
		return "", "", errors.New(`want "<type>/<id>", e.g. "group/grp_123"`)
	case typ != "group":
		return "", "", fmt.Errorf("unsupported resource type %q (supported: group)", typ)
	}
	return typ, id, nil
}

// groupOutcome is what a completed group means for a step waiting on it.
func groupOutcome(g store.Group) (ok bool, reason string) {
	if g.Status == store.GroupSucceeded {
		return true, ""
	}
	return false, fmt.Sprintf("group %s failed: %d of %d tasks succeeded, %d failed, %d cancelled",
		g.ID, g.Succeeded, g.Total, g.Failed, g.Cancelled)
}

// groupSteps finishes the steps waiting for g, which the transaction has
// just completed, and reports whether that enqueued any task.
func (s *Store) groupSteps(ctx context.Context, tx *sql.Tx, g store.Group) (bool, error) {
	rows, err := s.query(ctx, tx, `SELECT id FROM workflow_steps
		WHERE workspace_id = ? AND wait_type = 'group' AND wait_id = ? AND status = ?`,
		g.WorkspaceID, g.ID, store.StepWaiting)
	if err != nil {
		return false, err
	}
	var events []stepEvent
	ok, reason := groupOutcome(g)
	state := store.TaskFailed
	if ok {
		state = store.TaskSucceeded
	}
	for rows.Next() {
		var stepID string
		if err := rows.Scan(&stepID); err != nil {
			rows.Close()
			return false, err
		}
		events = append(events, stepEvent{stepID: stepID, state: state, reason: reason, group: true})
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	return s.advanceSteps(ctx, tx, events)
}

// taskEvents are the events for step tasks that a transaction finished
// other than by an attempt: cancelled, or deleted with their queue.
func taskEvents(stepIDs []string, reason string) []stepEvent {
	events := make([]stepEvent, len(stepIDs))
	for i, stepID := range stepIDs {
		events[i] = stepEvent{stepID: stepID, state: store.TaskCancelled, reason: reason}
	}
	return events
}
