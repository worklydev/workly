package store

import (
	"time"

	"github.com/worklydev/workly/internal/workspace"
)

// Schedules are separate from queues: a schedule calls its endpoint on a
// cron schedule, and each call is a Run with its own attempts. Nothing here
// creates tasks or reads a queue.

// OverlapPolicy decides what happens to a slot that comes up while the
// previous run is still pending or running.
type OverlapPolicy string

const (
	// OverlapSkip records the slot as a skipped run.
	OverlapSkip OverlapPolicy = "skip"
	// OverlapAllow runs it alongside.
	OverlapAllow OverlapPolicy = "allow"
)

// MissedPolicy decides what happens to slots missed while no dispatcher
// was running. Either way at most one run is created for them.
type MissedPolicy string

const (
	// MissedSkip records the most recent missed slot as a skipped run.
	MissedSkip MissedPolicy = "skip"
	// MissedCatchUp runs the most recent missed slot.
	MissedCatchUp MissedPolicy = "catch_up"
)

// ScheduleConfig is a schedule's settings. The cron expression and
// timezone are validated by the API; the store keeps them as text.
type ScheduleConfig struct {
	Cron     string
	Timezone string

	Method  string
	URL     string
	Headers map[string]string
	Body    []byte

	Timeout           time.Duration
	MaxAttempts       int
	MinBackoff        time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
	RetryOn4xx        bool

	Overlap OverlapPolicy
	Missed  MissedPolicy
}

// DefaultScheduleConfig is the settings a schedule gets for omitted
// fields. Retries are fewer and shorter than a queue's, so a failing run
// gives up well before a frequent schedule's next slot.
func DefaultScheduleConfig() ScheduleConfig {
	return ScheduleConfig{
		Timezone:          "UTC",
		Method:            "POST",
		Timeout:           30 * time.Second,
		MaxAttempts:       3,
		MinBackoff:        5 * time.Second,
		MaxBackoff:        5 * time.Minute,
		BackoffMultiplier: 2,
		Overlap:           OverlapSkip,
		Missed:            MissedSkip,
	}
}

type Schedule struct {
	WorkspaceID workspace.ID
	Name        string
	ScheduleConfig
	Paused bool
	// NextRunAt is the next slot to fire; nil while paused.
	NextRunAt *time.Time
	// LastRun is the most recent run, if any. Only GetSchedule and
	// ListSchedules set it.
	LastRun   *RunSummary
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RunSummary describes a schedule's latest run.
type RunSummary struct {
	ID           string
	State        RunState
	Trigger      RunTrigger
	ScheduledFor time.Time
	FinishedAt   *time.Time
	LastStatus   *int
	LastError    string
}

type RunState string

const (
	RunPending   RunState = "pending"
	RunRunning   RunState = "running"
	RunSucceeded RunState = "succeeded"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
	// RunSkipped is a slot that wasn't delivered; SkipReason says why.
	RunSkipped RunState = "skipped"
)

type RunTrigger string

const (
	TriggerSchedule RunTrigger = "schedule"
	TriggerManual   RunTrigger = "manual"
)

type SkipReason string

const (
	SkipOverlap SkipReason = "overlap"
	SkipMissed  SkipReason = "missed"
)

// Run is one firing of a schedule. It snapshots the schedule's request when
// it is created, and is retried under the schedule's current policy.
type Run struct {
	ID           string
	WorkspaceID  workspace.ID
	Schedule     string
	State        RunState
	Trigger      RunTrigger
	ScheduledFor time.Time
	SkipReason   SkipReason

	Method  string
	URL     string
	Headers map[string]string
	Body    []byte

	// RunAt is when the run is next eligible for delivery.
	RunAt            time.Time
	AttemptCount     int
	ReplayedAttempts int
	LeaseExpiresAt   *time.Time
	LastStatus       *int
	LastOutcome      Outcome
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	FinishedAt       *time.Time
	LastAttemptAt    *time.Time
	RetriedAt        *time.Time
}

// AttemptsSinceReplay is the number of attempts counted against the
// schedule's MaxAttempts.
func (r Run) AttemptsSinceReplay() int { return r.AttemptCount - r.ReplayedAttempts }

// RunFilter selects a schedule's runs. Zero fields match everything.
type RunFilter struct {
	Schedule string
	State    RunState
	Cursor   string
	Limit    int
}

// Fire fires one slot of a schedule (see Dispatch.FireSchedule).
type Fire struct {
	WorkspaceID workspace.ID
	Schedule    string
	// Due is the schedule's NextRunAt as the dispatcher read it. The fire
	// only happens if it is unchanged, so a slot can't fire twice.
	Due time.Time
	// Slot is the slot the run is for.
	Slot time.Time
	// SkipReason, if set, records the slot as skipped without delivering it.
	SkipReason SkipReason
	// NextRunAt is the slot after this one.
	NextRunAt time.Time
}

// RunClaim is a claimed run with its schedule, whose policy applies to it.
type RunClaim struct {
	Run      Run
	Schedule Schedule
}

// RunAttemptResult is a run's attempt plus the run's resulting state.
type RunAttemptResult struct {
	Attempt
	// NextState is pending (retry at RunAt), succeeded or failed.
	NextState RunState
	RunAt     time.Time
}

// ScheduleStatus is what metrics report per schedule.
type ScheduleStatus struct {
	WorkspaceID workspace.ID
	Schedule    string
	Paused      bool
	// LastSuccessAt is when the latest successful run finished.
	LastSuccessAt *time.Time
}
