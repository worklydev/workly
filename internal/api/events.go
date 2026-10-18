package api

import (
	"io"
	"net/http"
	"time"

	"github.com/worklydev/workly/internal/live"
	"github.com/worklydev/workly/internal/workspace"
)

// heartbeatInterval keeps idle streams open through proxies that close
// quiet connections (Cloudflare after 100 s).
const heartbeatInterval = 15 * time.Second

// serveEvents streams GET /v1/events: a `change` event whenever the
// workspace's queues, tasks, schedules or runs change, at most a few a
// second, so the dashboard refreshes instead of polling. Events carry no
// data; clients refetch what they show. It sits outside the spec because
// the generated server can't stream.
func serveEvents(hub *live.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, _ := workspace.FromContext(r.Context())
		changes, cancel := hub.Subscribe(ws)
		defer cancel()

		rc := http.NewResponseController(w)
		// The server's read deadline would otherwise end the stream.
		_ = rc.SetReadDeadline(time.Time{})
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		// Stops nginx-style proxies from buffering the stream.
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		send := func(s string) bool {
			if _, err := io.WriteString(w, s); err != nil {
				return false
			}
			return rc.Flush() == nil
		}
		// ready tells the client it's connected, and to catch up on whatever
		// changed while it wasn't.
		if !send("retry: 2000\nevent: ready\ndata: {}\n\n") {
			return
		}
		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()
		for {
			var ok bool
			select {
			case <-r.Context().Done():
				return
			case <-hub.Stopped():
				// Shutting down; the client reconnects to another replica.
				return
			case <-changes:
				ok = send("event: change\ndata: {}\n\n")
			case <-heartbeat.C:
				ok = send(": heartbeat\n\n")
			}
			if !ok {
				return
			}
		}
	}
}

// markChanges tells the hub when a request may have changed its workspace:
// any successful request other than a read. It wraps the /v1 operations,
// which MCP tools call too.
func markChanges(hub *live.Hub) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			if ws, ok := workspace.FromContext(r.Context()); ok && rec.status < 400 {
				hub.Changed(ws)
			}
		})
	}
}
