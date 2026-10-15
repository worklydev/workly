package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/alert"
	"github.com/worklydev/workly/internal/dispatch"
	"github.com/worklydev/workly/internal/mcp"
	"github.com/worklydev/workly/internal/metrics"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

type Options struct {
	Store            store.Store
	Resolver         workspace.Resolver
	Logger           *slog.Logger
	Dashboard        http.Handler
	AutoCreateQueues bool
	// MaxPayloadBytes limits task bodies.
	MaxPayloadBytes int64
	// DevEcho, if set, is served at /_dev/echo (`workly dev` only).
	DevEcho http.Handler
	// Metrics counts enqueues; it defaults to a private set.
	Metrics *metrics.Workly
	// ServeMetrics serves Metrics at /metrics, behind the same credentials
	// as /v1.
	ServeMetrics bool
	// Retention is how long finished tasks are kept; 0 is forever.
	Retention time.Duration
	// Signer builds the requests shown by GET /v1/tasks/{id}/delivery.
	Signer dispatch.Signer
	// MCP sets which tools /mcp serves; mcp.Off doesn't serve it.
	MCP mcp.Mode
	// Version is reported to MCP clients.
	Version string
	// Admission, if set, may refuse new tasks and schedules.
	Admission Admission
	// Alerts sends test alerts; it defaults to one that can't send email.
	Alerts *alert.Sender
}

// requestOverhead is allowed on top of MaxPayloadBytes for the rest of an
// enqueue request (URL, headers, JSON escaping of the body).
const requestOverhead = 256 << 10

// NewHandler builds the full HTTP handler: operational endpoints, the /v1 API
// and the dashboard.
func NewHandler(opts Options) http.Handler {
	mux := http.NewServeMux()

	if opts.Metrics == nil {
		opts.Metrics = metrics.New()
	}
	handlers := Handlers{
		Store:            opts.Store,
		AutoCreateQueues: opts.AutoCreateQueues,
		MaxPayloadBytes:  opts.MaxPayloadBytes,
		Metrics:          opts.Metrics,
		Signer:           opts.Signer,
		Retention:        opts.Retention,
		Admission:        opts.Admission,
		Alerts:           opts.Alerts,
	}
	if handlers.Alerts == nil {
		handlers.Alerts = alert.NewSender(nil, "")
	}
	strict := NewStrictHandlerWithOptions(handlers, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				writeHandlerError(w, payloadTooLarge(opts.MaxPayloadBytes))
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			if writeHandlerError(w, err) {
				opts.Logger.ErrorContext(r.Context(), "handler error", "path", r.URL.Path, "err", err)
			}
		},
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		},
	})

	// Unknown API paths get a JSON 404 rather than the dashboard.
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint: "+r.Method+" "+r.URL.Path)
	})
	if opts.MCP != mcp.Off {
		mux.Handle("/mcp", mcp.NewHandler(mcp.Options{API: mux, Mode: opts.MCP, Version: opts.Version, Logger: opts.Logger}))
	} else {
		mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "not_found", "MCP is turned off on this server (WORKLY_MCP=off)")
		})
	}
	if opts.DevEcho != nil {
		mux.Handle("/_dev/echo", opts.DevEcho)
	}
	if opts.ServeMetrics {
		mux.Handle("GET /metrics", authorize(opts.Resolver, opts.Metrics.Handler()))
	} else {
		// Rather than the dashboard, which would answer 200.
		mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "not_found", "metrics are not served on this port")
		})
	}
	if opts.Dashboard != nil {
		mux.Handle("/", opts.Dashboard)
	}

	var h http.Handler = mux
	h = resolveWorkspace(opts.Resolver, h)
	h = limitBody(opts.MaxPayloadBytes+requestOverhead, h)
	h = securityHeaders(h)
	h = logRequests(opts.Logger, h)
	h = recoverPanics(opts.Logger, h)
	return h
}

func newError(code, message string) Error {
	return Error{Error: ErrorBody{Code: code, Message: message}}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(newError(code, message))
}

// authorize requires the credentials that /v1 requires, e.g. WORKLY_API_KEY
// for /metrics, without scoping the request to a workspace.
func authorize(resolver workspace.Resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := resolver.Resolve(r); errors.Is(err, workspace.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API key")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "could not resolve workspace")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// scoped reports whether a path is workspace-scoped: /v1, and /mcp, whose
// tools call /v1. Health checks and the dashboard shell are not.
func scoped(path string) bool { return strings.HasPrefix(path, "/v1/") || path == "/mcp" }

// resolveWorkspace attaches the request's workspace to its context.
func resolveWorkspace(resolver workspace.Resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !scoped(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		id, err := resolver.Resolve(r)
		if errors.Is(err, workspace.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API key")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "could not resolve workspace")
			return
		}
		next.ServeHTTP(w, r.WithContext(workspace.WithID(r.Context(), id)))
	})
}

// securityHeaders keeps other sites from framing the dashboard (whose
// buttons purge queues and cancel tasks) and browsers from sniffing
// response types.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func limitBody(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scoped(r.URL.Path) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Probes hit these every few seconds; keep them out of info logs.
		level := slog.LevelInfo
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			level = slog.LevelDebug
		}
		logger.Log(r.Context(), level, "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				logger.ErrorContext(r.Context(), "panic", "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "internal", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
