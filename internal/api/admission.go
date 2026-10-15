package api

import (
	"context"
	"time"

	"github.com/worklydev/workly/internal/cron"
	"github.com/worklydev/workly/internal/workspace"
)

// Admission lets a wrapping server limit what workspaces add, e.g. quotas on
// a hosted service. Workly itself sets no limits.
type Admission interface {
	// AdmitTasks is called before up to n tasks are enqueued.
	AdmitTasks(ctx context.Context, ws workspace.ID, n int) error
	// TasksCreated reports how many of them were new, not repeats of an
	// idempotency key.
	TasksCreated(ctx context.Context, ws workspace.ID, n int)
	// AdmitSchedule is called before a schedule is created or replaced.
	AdmitSchedule(ctx context.Context, ws workspace.ID, s ScheduleAdmission) error
}

type ScheduleAdmission struct {
	Name string
	// Exists is true when the schedule is being replaced.
	Exists bool
	// Interval is the shortest time between two of its runs.
	Interval time.Duration
}

// LimitError is an error an Admission returns to refuse a request; the
// client gets it as is, in the API's error shape.
func LimitError(status int, code, message string) error {
	return &apiError{status, code, message}
}

// shortestInterval is the shortest time between consecutive runs over the
// next few hundred, enough to see any cron expression's pattern.
func shortestInterval(c cron.Schedule, loc *time.Location, now time.Time) time.Duration {
	shortest := time.Duration(1<<63 - 1)
	prev := c.Next(now, loc)
	for range 500 {
		next := c.Next(prev, loc)
		if d := next.Sub(prev); d < shortest {
			shortest = d
		}
		if shortest <= time.Minute {
			break
		}
		prev = next
	}
	return shortest
}
