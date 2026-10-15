package alert

import (
	"cmp"
	"context"
	"log/slog"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// Notifier sends the alerts. Run it on one process at a time (the
// dispatcher's leader).
type Notifier struct {
	Store  store.Store
	Sender *Sender
	Logger *slog.Logger
	// Interval is how often failures are checked. Default 1m.
	Interval time.Duration
	// Cooldown is how long a queue or schedule waits after an alert before
	// the next. Default 30m.
	Cooldown time.Duration
	// Settle leaves out failures this recent, whose results may still be
	// on their way to the database with an earlier finish time. Default
	// 15s.
	Settle time.Duration
}

// lookback bounds how far back a first alert, or one after a long outage,
// reaches.
const lookback = 24 * time.Hour

// sendTimeout bounds each message.
const sendTimeout = 20 * time.Second

// Run checks for failures every Interval until ctx is cancelled.
func (n *Notifier) Run(ctx context.Context) {
	interval := cmp.Or(n.Interval, time.Minute)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		n.Check(ctx, time.Now())
	}
}

// Check sends the alerts due at now.
func (n *Notifier) Check(ctx context.Context, now time.Time) {
	channels, err := n.Store.AllAlertChannels(ctx)
	if err != nil {
		if ctx.Err() == nil {
			n.Logger.Warn("could not list alert channels", "err", err)
		}
		return
	}
	byWorkspace := map[workspace.ID][]store.AlertChannel{}
	var order []workspace.ID
	for _, c := range channels {
		if byWorkspace[c.WorkspaceID] == nil {
			order = append(order, c.WorkspaceID)
		}
		byWorkspace[c.WorkspaceID] = append(byWorkspace[c.WorkspaceID], c)
	}
	for _, ws := range order {
		if ctx.Err() != nil {
			return
		}
		n.checkWorkspace(ctx, ws, byWorkspace[ws], now)
	}
}

func (n *Notifier) checkWorkspace(ctx context.Context, ws workspace.ID, channels []store.AlertChannel, now time.Time) {
	log := n.Logger
	if ws != workspace.Default {
		log = log.With("workspace", ws)
	}
	w := store.AlertWindow{
		Now:      now.Add(-cmp.Or(n.Settle, 15*time.Second)),
		Cooldown: cmp.Or(n.Cooldown, 30*time.Minute),
	}
	// Failures from before the first channel was added aren't news.
	first := channels[0].CreatedAt
	for _, c := range channels {
		if c.CreatedAt.Before(first) {
			first = c.CreatedAt
		}
	}
	w.Since = first
	if earliest := w.Now.Add(-lookback); w.Since.Before(earliest) {
		w.Since = earliest
	}
	failures, err := n.Store.FailuresToAlert(ctx, ws, w)
	if err != nil {
		log.Warn("could not check failures for alerts", "err", err)
		return
	}
	if len(failures) == 0 {
		return
	}
	name, err := WorkspaceName(ctx, n.Store, ws)
	if err != nil {
		log.Warn("could not check failures for alerts", "err", err)
		return
	}

	m := failureMessage(name, failures, n.Sender.Links, w.Cooldown)
	sent, tried := 0, 0
	for _, c := range channels {
		if !n.Sender.CanSend(c) {
			continue
		}
		tried++
		sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		err := n.Sender.Send(sendCtx, c, m)
		cancel()
		if err != nil {
			log.Warn("could not send alert", "channel_id", c.ID, "type", c.Type, "err", err)
			continue
		}
		sent++
	}
	// If no channel took it, the next check tries again, with any newer
	// failures. Otherwise channels that missed it miss it.
	if tried > 0 && sent == 0 {
		return
	}
	subjects := make([]store.AlertSubject, len(failures))
	for i, f := range failures {
		subjects[i] = f.AlertSubject
	}
	if err := n.Store.MarkAlerted(ctx, ws, subjects, w.Now); err != nil {
		log.Warn("could not record alerts", "err", err)
		return
	}
	log.Info("sent alert", "subjects", len(subjects), "channels", sent)
}

// WorkspaceName names the workspace in alerts, unless it's the only one.
func WorkspaceName(ctx context.Context, st store.Store, ws workspace.ID) (string, error) {
	if ws == workspace.Default {
		return "", nil
	}
	w, err := st.GetWorkspace(ctx, ws)
	if err != nil {
		return "", err
	}
	return cmp.Or(w.Name, string(ws)), nil
}

// Test sends a test alert to the channel of the named workspace.
func (s *Sender) Test(ctx context.Context, workspaceName string, c store.AlertChannel) error {
	return s.Send(ctx, c, testMessage(workspaceName, s.Links))
}
