package sqlstore

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/worklydev/workly/internal/id"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

const alertChannelColumns = `id, workspace_id, type, target, created_at`

func scanAlertChannel(row interface{ Scan(...any) error }) (store.AlertChannel, error) {
	var (
		c         store.AlertChannel
		createdAt dbTime
	)
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.Type, &c.Target, &createdAt)
	c.CreatedAt = createdAt.Time
	return c, err
}

func (s *Store) CreateAlertChannel(ctx context.Context, ws workspace.ID, typ store.AlertChannelType, target string) (store.AlertChannel, error) {
	c := store.AlertChannel{
		ID:          id.New("ach"),
		WorkspaceID: ws,
		Type:        typ,
		Target:      cleanText(target),
		// Stored to the millisecond, as SQLite keeps it.
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	res, err := s.exec(ctx, s.db, `INSERT INTO alert_channels (`+alertChannelColumns+`) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (workspace_id, type, target) DO NOTHING`,
		c.ID, ws, c.Type, c.Target, s.ts(c.CreatedAt))
	if err != nil {
		return store.AlertChannel{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return store.AlertChannel{}, err
	} else if n == 0 {
		return store.AlertChannel{}, store.ErrExists
	}
	return c, nil
}

func (s *Store) GetAlertChannel(ctx context.Context, ws workspace.ID, channelID string) (store.AlertChannel, error) {
	c, err := scanAlertChannel(s.queryRow(ctx, s.db,
		`SELECT `+alertChannelColumns+` FROM alert_channels WHERE workspace_id = ? AND id = ?`, ws, channelID))
	if errors.Is(err, sql.ErrNoRows) {
		return store.AlertChannel{}, store.ErrNotFound
	}
	return c, err
}

func (s *Store) ListAlertChannels(ctx context.Context, ws workspace.ID) ([]store.AlertChannel, error) {
	return s.alertChannels(ctx, `SELECT `+alertChannelColumns+` FROM alert_channels WHERE workspace_id = ? ORDER BY id`, ws)
}

func (s *Store) AllAlertChannels(ctx context.Context) ([]store.AlertChannel, error) {
	return s.alertChannels(ctx, `SELECT `+alertChannelColumns+` FROM alert_channels ORDER BY id`)
}

func (s *Store) alertChannels(ctx context.Context, query string, args ...any) ([]store.AlertChannel, error) {
	rows, err := s.query(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := []store.AlertChannel{}
	for rows.Next() {
		c, err := scanAlertChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

func (s *Store) DeleteAlertChannel(ctx context.Context, ws workspace.ID, channelID string) error {
	res, err := s.exec(ctx, s.db, `DELETE FROM alert_channels WHERE workspace_id = ? AND id = ?`, ws, channelID)
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

// failuresToAlert selects a subject's failures in (later of its last alert
// and Since, Now], for subjects whose cooldown has passed. %[1]s is the
// table, %[2]s the subject column and %[3]s the alert kind.
const failuresToAlert = `SELECT f.%[2]s, COALESCE(f.last_error, ''), count(*), max(a.alerted_at)
	FROM %[1]s f
		LEFT JOIN alert_state a ON a.workspace_id = f.workspace_id AND a.kind = '%[3]s' AND a.subject = f.%[2]s
	WHERE f.workspace_id = ? AND f.state = 'failed' AND f.finished_at > ? AND f.finished_at <= ?
		AND (a.alerted_at IS NULL OR (a.alerted_at <= ? AND f.finished_at > a.alerted_at))
	GROUP BY f.%[2]s, COALESCE(f.last_error, '')`

func (s *Store) FailuresToAlert(ctx context.Context, ws workspace.ID, w store.AlertWindow) ([]store.AlertFailures, error) {
	bySubject := map[store.AlertSubject]*store.AlertFailures{}
	for _, src := range []struct {
		table, column string
		kind          store.AlertKind
	}{
		{"tasks", "queue", store.AlertQueue},
		{"runs", "schedule", store.AlertSchedule},
	} {
		rows, err := s.query(ctx, s.db, fmt.Sprintf(failuresToAlert, src.table, src.column, src.kind),
			ws, s.ts(w.Since), s.ts(w.Now), s.ts(w.Now.Add(-w.Cooldown)))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var (
				subject = store.AlertSubject{Kind: src.kind}
				g       store.AlertErrorGroup
				alerted dbTime
			)
			if err := rows.Scan(&subject.Name, &g.Error, &g.Count, &alerted); err != nil {
				rows.Close()
				return nil, err
			}
			f := bySubject[subject]
			if f == nil {
				f = &store.AlertFailures{AlertSubject: subject, Since: w.Since.UTC()}
				if alerted.Valid && alerted.Time.After(f.Since) {
					f.Since = alerted.Time
				}
				bySubject[subject] = f
			}
			f.Total += g.Count
			f.Groups = append(f.Groups, g)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	failures := make([]store.AlertFailures, 0, len(bySubject))
	for _, f := range bySubject {
		slices.SortFunc(f.Groups, func(a, b store.AlertErrorGroup) int {
			return cmp.Or(b.Count-a.Count, cmp.Compare(a.Error, b.Error))
		})
		failures = append(failures, *f)
	}
	slices.SortFunc(failures, func(a, b store.AlertFailures) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
	return failures, nil
}

func (s *Store) MarkAlerted(ctx context.Context, ws workspace.ID, subjects []store.AlertSubject, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, sub := range subjects {
			_, err := s.exec(ctx, tx, `INSERT INTO alert_state (workspace_id, kind, subject, alerted_at) VALUES (?, ?, ?, ?)
				ON CONFLICT (workspace_id, kind, subject) DO UPDATE SET alerted_at = excluded.alerted_at`,
				ws, sub.Kind, sub.Name, s.ts(at))
			if err != nil {
				return err
			}
		}
		return nil
	})
}
