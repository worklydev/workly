package store

import (
	"errors"
	"time"

	"github.com/worklydev/workly/internal/workspace"
)

// ErrExists is returned when creating something that already exists, such
// as an alert channel with the same target.
var ErrExists = errors.New("already exists")

type AlertChannelType string

const (
	AlertEmail AlertChannelType = "email"
	AlertSlack AlertChannelType = "slack"
)

// AlertChannel is where a workspace's alerts go. Target is an email address
// or a Slack incoming webhook URL, validated by the API.
type AlertChannel struct {
	ID          string
	WorkspaceID workspace.ID
	Type        AlertChannelType
	Target      string
	CreatedAt   time.Time
}

// AlertKind is what an alert is about.
type AlertKind string

const (
	AlertQueue    AlertKind = "queue"
	AlertSchedule AlertKind = "schedule"
)

// AlertSubject is a queue or schedule that alerts are about.
type AlertSubject struct {
	Kind AlertKind
	Name string
}

// AlertWindow selects the failures to alert about (see FailuresToAlert).
type AlertWindow struct {
	Now time.Time
	// Since is the earliest a failure counts from, for subjects never
	// alerted about or alerted about before it.
	Since time.Time
	// Cooldown is how long a subject waits after an alert before the next.
	Cooldown time.Duration
}

// AlertFailures are a subject's failures since its previous alert (or the
// window's Since), grouped by error, most common first.
type AlertFailures struct {
	AlertSubject
	// Since is when the counted failures start.
	Since  time.Time
	Total  int
	Groups []AlertErrorGroup
}

type AlertErrorGroup struct {
	Error string
	Count int
}
