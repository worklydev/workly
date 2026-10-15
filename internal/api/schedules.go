package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/cron"
	"github.com/worklydev/workly/internal/store"
)

func scheduleNotFound(name string) error {
	return &apiError{http.StatusNotFound, "schedule_not_found", fmt.Sprintf("schedule %q does not exist", name)}
}

func runNotFound(id string) error {
	return &apiError{http.StatusNotFound, "run_not_found", fmt.Sprintf("run %q does not exist", id)}
}

// nextRunsShown is how many upcoming slots a schedule lists.
const nextRunsShown = 5

// parseSchedule parses a schedule's cron expression and timezone.
func parseSchedule(expr, tz string) (cron.Schedule, *time.Location, error) {
	c, err := cron.Parse(expr)
	if err != nil {
		return c, nil, invalid("cron: %v", err)
	}
	loc, err := cron.LoadLocation(tz)
	if err != nil {
		return c, nil, invalid("timezone: %v", err)
	}
	return c, loc, nil
}

// scheduleConfig applies defaults to omitted fields and checks bounds.
func scheduleConfig(in ScheduleConfig, maxBody int64) (store.ScheduleConfig, cron.Schedule, *time.Location, error) {
	cfg := store.DefaultScheduleConfig()
	cfg.Cron = strings.TrimSpace(in.Cron)
	if in.Timezone != nil {
		cfg.Timezone = *in.Timezone
	}
	c, loc, err := parseSchedule(cfg.Cron, cfg.Timezone)
	if err != nil {
		return cfg, c, nil, err
	}
	r, err := validateRequest(request{in.Url, in.Method, in.Headers, in.Payload, in.Body, in.ContentType}, maxBody)
	if err != nil {
		return cfg, c, nil, err
	}
	cfg.Method, cfg.URL, cfg.Headers, cfg.Body = r.method, r.url, r.headers, r.body

	if in.TimeoutSeconds != nil {
		cfg.Timeout = duration(*in.TimeoutSeconds)
	}
	if in.MaxAttempts != nil {
		cfg.MaxAttempts = *in.MaxAttempts
	}
	if in.MinBackoffSeconds != nil {
		cfg.MinBackoff = duration(*in.MinBackoffSeconds)
	}
	if in.MaxBackoffSeconds != nil {
		cfg.MaxBackoff = duration(*in.MaxBackoffSeconds)
	}
	if in.BackoffMultiplier != nil {
		cfg.BackoffMultiplier = *in.BackoffMultiplier
	}
	if in.RetryOn4xx != nil {
		cfg.RetryOn4xx = *in.RetryOn4xx
	}
	if in.Overlap != nil {
		cfg.Overlap = store.OverlapPolicy(*in.Overlap)
	}
	if in.Missed != nil {
		cfg.Missed = store.MissedPolicy(*in.Missed)
	}

	switch {
	case cfg.Timeout < minTimeout || cfg.Timeout > maxTimeout:
		return cfg, c, nil, invalid("timeout_seconds must be between %d and %d", int(minTimeout.Seconds()), int(maxTimeout.Seconds()))
	case cfg.MaxAttempts < 1 || cfg.MaxAttempts > maxAttemptsLimit:
		return cfg, c, nil, invalid("max_attempts must be between 1 and %d", maxAttemptsLimit)
	case cfg.MinBackoff < 0:
		return cfg, c, nil, invalid("min_backoff_seconds must not be negative")
	case cfg.MaxBackoff < cfg.MinBackoff:
		return cfg, c, nil, invalid("max_backoff_seconds must be at least min_backoff_seconds")
	case cfg.BackoffMultiplier < 1:
		return cfg, c, nil, invalid("backoff_multiplier must be at least 1")
	case cfg.Overlap != store.OverlapSkip && cfg.Overlap != store.OverlapAllow:
		return cfg, c, nil, invalid("overlap must be skip or allow")
	case cfg.Missed != store.MissedSkip && cfg.Missed != store.MissedCatchUp:
		return cfg, c, nil, invalid("missed must be skip or catch_up")
	}
	return cfg, c, loc, nil
}

func toSchedule(sc store.Schedule, now time.Time) Schedule {
	out := Schedule{
		Name:              sc.Name,
		Cron:              sc.Cron,
		Timezone:          sc.Timezone,
		Method:            HTTPMethod(sc.Method),
		Url:               sc.URL,
		Headers:           sc.Headers,
		TimeoutSeconds:    seconds(sc.Timeout),
		MaxAttempts:       sc.MaxAttempts,
		MinBackoffSeconds: seconds(sc.MinBackoff),
		MaxBackoffSeconds: seconds(sc.MaxBackoff),
		BackoffMultiplier: sc.BackoffMultiplier,
		RetryOn4xx:        sc.RetryOn4xx,
		Overlap:           OverlapPolicy(sc.Overlap),
		Missed:            MissedPolicy(sc.Missed),
		Paused:            sc.Paused,
		NextRunAt:         sc.NextRunAt,
		NextRuns:          []time.Time{},
		CreatedAt:         sc.CreatedAt,
		UpdatedAt:         sc.UpdatedAt,
	}
	out.Payload, out.Body = bodyFields(sc.Headers, sc.Body)
	// A schedule saved by an older server can only fail to parse if its
	// zoneinfo went away; it then lists no upcoming slots.
	if c, loc, err := parseSchedule(sc.Cron, sc.Timezone); err == nil {
		t := now
		if sc.NextRunAt != nil {
			out.NextRuns = append(out.NextRuns, *sc.NextRunAt)
			t = *sc.NextRunAt
		}
		for len(out.NextRuns) < nextRunsShown {
			if t = c.Next(t, loc); t.IsZero() {
				break
			}
			out.NextRuns = append(out.NextRuns, t)
		}
	}
	if l := sc.LastRun; l != nil {
		out.LastRun = &LastRun{
			Id:           l.ID,
			State:        RunState(l.State),
			Trigger:      RunTrigger(l.Trigger),
			ScheduledFor: l.ScheduledFor,
			FinishedAt:   l.FinishedAt,
			LastStatus:   l.LastStatus,
			LastError:    optional(l.LastError),
		}
	}
	return out
}

func toRun(r store.Run) Run {
	out := Run{
		Id:            r.ID,
		Schedule:      r.Schedule,
		State:         RunState(r.State),
		Trigger:       RunTrigger(r.Trigger),
		ScheduledFor:  r.ScheduledFor,
		Method:        HTTPMethod(r.Method),
		Url:           r.URL,
		Headers:       r.Headers,
		RunAt:         r.RunAt,
		AttemptCount:  r.AttemptCount,
		LastStatus:    r.LastStatus,
		LastError:     optional(r.LastError),
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
		FinishedAt:    r.FinishedAt,
		LastAttemptAt: r.LastAttemptAt,
		RetriedAt:     r.RetriedAt,
	}
	out.Payload, out.Body = bodyFields(r.Headers, r.Body)
	if r.SkipReason != "" {
		reason := SkipReason(r.SkipReason)
		out.SkipReason = &reason
	}
	if r.LastOutcome != "" {
		o := Outcome(r.LastOutcome)
		out.LastOutcome = &o
	}
	return out
}

// bodyFields returns a body as JSON payload or text, as tasks show it.
func bodyFields(headers map[string]string, body []byte) (*json.RawMessage, *string) {
	if len(body) == 0 {
		return nil, nil
	}
	if isJSON(headers) && json.Valid(body) {
		raw := json.RawMessage(body)
		return &raw, nil
	}
	text := string(body)
	return nil, &text
}

func (h Handlers) ListSchedules(ctx context.Context, _ ListSchedulesRequestObject) (ListSchedulesResponseObject, error) {
	schedules, err := h.Store.ListSchedules(ctx, workspaceID(ctx))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := ListSchedules200JSONResponse{Schedules: make([]Schedule, len(schedules))}
	for i, sc := range schedules {
		out.Schedules[i] = toSchedule(sc, now)
	}
	return out, nil
}

func (h Handlers) GetSchedule(ctx context.Context, req GetScheduleRequestObject) (GetScheduleResponseObject, error) {
	sc, err := h.Store.GetSchedule(ctx, workspaceID(ctx), req.Schedule)
	if errors.Is(err, store.ErrNotFound) {
		return nil, scheduleNotFound(req.Schedule)
	}
	if err != nil {
		return nil, err
	}
	return GetSchedule200JSONResponse(toSchedule(sc, time.Now())), nil
}

func (h Handlers) PutSchedule(ctx context.Context, req PutScheduleRequestObject) (PutScheduleResponseObject, error) {
	if err := validateScheduleName(req.Schedule); err != nil {
		return nil, err
	}
	cfg, c, loc, err := scheduleConfig(*req.Body, h.MaxPayloadBytes)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	ws := workspaceID(ctx)
	if h.Admission != nil {
		_, err := h.Store.GetSchedule(ctx, ws, req.Schedule)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		err = h.Admission.AdmitSchedule(ctx, ws, ScheduleAdmission{
			Name: req.Schedule, Exists: err == nil, Interval: shortestInterval(c, loc, now),
		})
		if err != nil {
			return nil, err
		}
	}
	sc, created, err := h.Store.PutSchedule(ctx, ws, req.Schedule, cfg, c.Next(now, loc))
	if err != nil {
		return nil, err
	}
	if created {
		return PutSchedule201JSONResponse(toSchedule(sc, now)), nil
	}
	return PutSchedule200JSONResponse(toSchedule(sc, now)), nil
}

func (h Handlers) DeleteSchedule(ctx context.Context, req DeleteScheduleRequestObject) (DeleteScheduleResponseObject, error) {
	err := h.Store.DeleteSchedule(ctx, workspaceID(ctx), req.Schedule)
	if errors.Is(err, store.ErrNotFound) {
		return nil, scheduleNotFound(req.Schedule)
	}
	if err != nil {
		return nil, err
	}
	return DeleteSchedule204Response{}, nil
}

func (h Handlers) PauseSchedule(ctx context.Context, req PauseScheduleRequestObject) (PauseScheduleResponseObject, error) {
	sc, err := h.Store.SetSchedulePaused(ctx, workspaceID(ctx), req.Schedule, true, time.Time{})
	if errors.Is(err, store.ErrNotFound) {
		return nil, scheduleNotFound(req.Schedule)
	}
	if err != nil {
		return nil, err
	}
	return PauseSchedule200JSONResponse(toSchedule(sc, time.Now())), nil
}

func (h Handlers) ResumeSchedule(ctx context.Context, req ResumeScheduleRequestObject) (ResumeScheduleResponseObject, error) {
	ws := workspaceID(ctx)
	sc, err := h.Store.GetSchedule(ctx, ws, req.Schedule)
	if errors.Is(err, store.ErrNotFound) {
		return nil, scheduleNotFound(req.Schedule)
	}
	if err != nil {
		return nil, err
	}
	c, loc, err := parseSchedule(sc.Cron, sc.Timezone)
	if err != nil {
		return nil, err
	}
	// Slots that passed while paused aren't run.
	now := time.Now()
	sc, err = h.Store.SetSchedulePaused(ctx, ws, req.Schedule, false, c.Next(now, loc))
	if errors.Is(err, store.ErrNotFound) {
		return nil, scheduleNotFound(req.Schedule)
	}
	if err != nil {
		return nil, err
	}
	return ResumeSchedule200JSONResponse(toSchedule(sc, now)), nil
}

func (h Handlers) TriggerSchedule(ctx context.Context, req TriggerScheduleRequestObject) (TriggerScheduleResponseObject, error) {
	r, err := h.Store.TriggerSchedule(ctx, workspaceID(ctx), req.Schedule, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, scheduleNotFound(req.Schedule)
	}
	if err != nil {
		return nil, err
	}
	return TriggerSchedule201JSONResponse(toRun(r)), nil
}

func (h Handlers) ListRuns(ctx context.Context, req ListRunsRequestObject) (ListRunsResponseObject, error) {
	ws := workspaceID(ctx)
	f := store.RunFilter{Schedule: req.Schedule, Limit: 50}
	p := req.Params
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > 200 {
			return nil, invalid("limit must be between 1 and 200")
		}
		f.Limit = *p.Limit
	}
	if p.State != nil {
		switch s := store.RunState(*p.State); s {
		case store.RunPending, store.RunRunning, store.RunSucceeded, store.RunFailed, store.RunCancelled, store.RunSkipped:
			f.State = s
		default:
			return nil, invalid("unknown state %q", *p.State)
		}
	}
	if p.Cursor != nil {
		f.Cursor = *p.Cursor
	}
	// Distinguish a schedule with no runs from a missing one.
	if _, err := h.Store.GetSchedule(ctx, ws, req.Schedule); errors.Is(err, store.ErrNotFound) {
		return nil, scheduleNotFound(req.Schedule)
	} else if err != nil {
		return nil, err
	}
	runs, cursor, err := h.Store.ListRuns(ctx, ws, f)
	if err != nil {
		return nil, err
	}
	out := ListRuns200JSONResponse{Runs: make([]Run, len(runs)), NextCursor: optional(cursor)}
	for i, r := range runs {
		out.Runs[i] = toRun(r)
	}
	return out, nil
}

func (h Handlers) GetRun(ctx context.Context, req GetRunRequestObject) (GetRunResponseObject, error) {
	r, err := h.Store.GetRun(ctx, workspaceID(ctx), req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, runNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	return GetRun200JSONResponse(toRun(r)), nil
}

func (h Handlers) ListRunAttempts(ctx context.Context, req ListRunAttemptsRequestObject) (ListRunAttemptsResponseObject, error) {
	ws := workspaceID(ctx)
	if _, err := h.Store.GetRun(ctx, ws, req.Id); errors.Is(err, store.ErrNotFound) {
		return nil, runNotFound(req.Id)
	} else if err != nil {
		return nil, err
	}
	attempts, err := h.Store.ListRunAttempts(ctx, ws, req.Id)
	if err != nil {
		return nil, err
	}
	out := ListRunAttempts200JSONResponse{Attempts: make([]Attempt, len(attempts))}
	for i, a := range attempts {
		out.Attempts[i] = toAttempt(a)
	}
	return out, nil
}

func (h Handlers) GetRunDelivery(ctx context.Context, req GetRunDeliveryRequestObject) (GetRunDeliveryResponseObject, error) {
	r, err := h.Store.GetRun(ctx, workspaceID(ctx), req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, runNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	// As for tasks: the next attempt if one is to come, otherwise the latest.
	attempt := max(r.AttemptCount, 1)
	if r.State == store.RunPending || r.State == store.RunSkipped {
		attempt = r.AttemptCount + 1
	}
	out := GetRunDelivery200JSONResponse{
		Method:  HTTPMethod(r.Method),
		Url:     r.URL,
		Headers: map[string]string{},
		Body:    string(r.Body),
	}
	header, err := h.Signer.RunHeader(ctx, r, attempt, time.Now())
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		out.Headers[k] = strings.Join(v, ", ")
	}
	return out, nil
}

func (h Handlers) CancelRun(ctx context.Context, req CancelRunRequestObject) (CancelRunResponseObject, error) {
	r, err := h.Store.CancelRun(ctx, workspaceID(ctx), req.Id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, runNotFound(req.Id)
	case errors.Is(err, store.ErrInvalidState):
		return nil, invalidState("run %q already %s and can't be cancelled", req.Id, r.State)
	case err != nil:
		return nil, err
	}
	return CancelRun200JSONResponse(toRun(r)), nil
}

func (h Handlers) RetryRun(ctx context.Context, req RetryRunRequestObject) (RetryRunResponseObject, error) {
	r, err := h.Store.RetryRun(ctx, workspaceID(ctx), req.Id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, runNotFound(req.Id)
	case errors.Is(err, store.ErrInvalidState):
		return nil, invalidState("run %q has an attempt in flight; retry it once the attempt finishes", req.Id)
	case err != nil:
		return nil, err
	}
	return RetryRun200JSONResponse(toRun(r)), nil
}

func (h Handlers) PreviewCron(ctx context.Context, req PreviewCronRequestObject) (PreviewCronResponseObject, error) {
	tz := "UTC"
	if req.Params.Timezone != nil && *req.Params.Timezone != "" {
		tz = *req.Params.Timezone
	}
	c, loc, err := parseSchedule(strings.TrimSpace(req.Params.Cron), tz)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := PreviewCron200JSONResponse{NextRuns: make([]time.Time, 0, nextRunsShown)}
	for t := now; len(out.NextRuns) < nextRunsShown; {
		t = c.Next(t, loc)
		out.NextRuns = append(out.NextRuns, t.UTC())
	}
	out.IntervalSeconds = int(shortestInterval(c, loc, now).Seconds())
	return out, nil
}
