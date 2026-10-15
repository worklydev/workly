package api

import (
	"encoding/json"
	"mime"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/store"
)

func seconds(d time.Duration) float64 { return d.Seconds() }

func duration(secs float64) time.Duration {
	return time.Duration(secs * float64(time.Second)).Round(time.Millisecond)
}

func toQueue(q store.Queue) Queue {
	return Queue{
		Name:              q.Name,
		Paused:            q.Paused,
		MaxAttempts:       q.MaxAttempts,
		MinBackoffSeconds: seconds(q.MinBackoff),
		MaxBackoffSeconds: seconds(q.MaxBackoff),
		BackoffMultiplier: q.BackoffMultiplier,
		TimeoutSeconds:    seconds(q.Timeout),
		RetryOn4xx:        q.RetryOn4xx,
		RatePerSecond:     optionalLimit(q.RatePerSecond),
		MaxConcurrency:    q.MaxConcurrency,
		CreatedAt:         q.CreatedAt,
		UpdatedAt:         q.UpdatedAt,
	}
}

// optionalLimit omits 0, which means unlimited.
func optionalLimit[T int | float64](v T) *T {
	if v == 0 {
		return nil
	}
	return &v
}

func toTask(t store.Task) Task {
	out := Task{
		Id:            t.ID,
		Queue:         t.Queue,
		State:         TaskState(t.State),
		Method:        HTTPMethod(t.Method),
		Url:           t.URL,
		Headers:       t.Headers,
		RunAt:         t.RunAt,
		AttemptCount:  t.AttemptCount,
		MaxAttempts:   t.MaxAttempts,
		LastStatus:    t.LastStatus,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		FinishedAt:    t.FinishedAt,
		LastAttemptAt: t.LastAttemptAt,
		RetriedAt:     t.RetriedAt,
	}
	if t.Timeout != nil {
		s := seconds(*t.Timeout)
		out.TimeoutSeconds = &s
	}
	out.IdempotencyKey = optional(t.IdempotencyKey)
	out.LastError = optional(t.LastError)
	if t.LastOutcome != "" {
		o := Outcome(t.LastOutcome)
		out.LastOutcome = &o
	}
	if len(t.Body) > 0 {
		if isJSON(t.Headers) && json.Valid(t.Body) {
			raw := json.RawMessage(t.Body)
			out.Payload = &raw
		} else {
			body := string(t.Body)
			out.Body = &body
		}
	}
	return out
}

func toAttempt(a store.Attempt) Attempt {
	return Attempt{
		Id:              a.ID,
		Number:          a.Number,
		StartedAt:       a.StartedAt,
		FinishedAt:      a.FinishedAt,
		DurationMs:      int(a.Duration().Milliseconds()),
		StatusCode:      a.StatusCode,
		Outcome:         Outcome(a.Outcome),
		Error:           optional(a.Error),
		ResponseBody:    optional(a.ResponseBody),
		ResponseHeaders: a.ResponseHeaders,
		RetriedAt:       a.RetriedAt,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// isJSON reports whether the headers declare a JSON body (application/json
// or a +json type).
func isJSON(headers map[string]string) bool {
	for k, v := range headers {
		if strings.EqualFold(k, "Content-Type") {
			mt, _, err := mime.ParseMediaType(v)
			return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
		}
	}
	return false
}
