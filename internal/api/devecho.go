package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/worklydev/workly/internal/signing"
)

// DevEcho is a built-in delivery target for `workly dev`, so the quickstart
// and demos work without writing an endpoint first. Query parameters shape
// its response:
//
//	status=503      respond with this status (default 200)
//	fail_rate=0.3   respond 500 with this probability
//	delay_ms=2000   wait before responding
//
// It verifies the delivery signature and echoes the request back.
func DevEcho(secret []byte, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))

		if ms, err := strconv.Atoi(q.Get("delay_ms")); err == nil && ms > 0 {
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		status := http.StatusOK
		if s, err := strconv.Atoi(q.Get("status")); err == nil && s >= 200 && s <= 599 {
			status = s
		}
		if rate, err := strconv.ParseFloat(q.Get("fail_rate"), 64); err == nil && rand.Float64() < rate {
			status = http.StatusInternalServerError
		}

		signature := "missing"
		if r.Header.Get(signing.HeaderSignature) != "" {
			signature = "valid"
			if err := signing.Verify(secret, r.Header.Get(signing.HeaderSignature),
				r.Header.Get(signing.HeaderTimestamp), body, 5*time.Minute, time.Now()); err != nil {
				signature = "invalid: " + err.Error()
			}
		}
		logger.Info("dev echo received",
			"task_id", r.Header.Get("Workly-Task-Id"),
			"attempt", r.Header.Get("Workly-Attempt"),
			"respond", status,
			"signature", signature,
		)

		headers := map[string]string{}
		for k := range r.Header {
			headers[k] = r.Header.Get(k)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    status,
			"method":    r.Method,
			"headers":   headers,
			"body":      string(body),
			"signature": signature,
		})
	})
}
