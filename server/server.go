// Package server is Workly's public Go API: the one package other modules
// (such as the hosted control plane) import to run and extend the server.
// Everything else lives in internal/.
package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/worklydev/workly/dashboard"
	"github.com/worklydev/workly/internal/alert"
	"github.com/worklydev/workly/internal/api"
	"github.com/worklydev/workly/internal/config"
	"github.com/worklydev/workly/internal/dispatch"
	"github.com/worklydev/workly/internal/live"
	"github.com/worklydev/workly/internal/mcp"
	"github.com/worklydev/workly/internal/metrics"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/postgres"
	"github.com/worklydev/workly/internal/store/sqlite"
	"github.com/worklydev/workly/internal/workspace"
)

// Aliases expose internal types without making their packages public.
type (
	WorkspaceID       = workspace.ID
	WorkspaceResolver = workspace.Resolver
	Store             = store.Store
	Config            = config.Config
	MCPMode           = mcp.Mode
	Admission         = api.Admission
	ScheduleAdmission = api.ScheduleAdmission
	Mailer            = alert.Mailer
	Email             = alert.Email
)

// SMTPMailer sends alert emails through an SMTP server, given as
// smtp://user:pass@host:587 (STARTTLS) or smtps://user:pass@host:465, from
// the sender address from, e.g. "Workly <alerts@example.com>".
func SMTPMailer(url, from string) (Mailer, error) { return alert.NewSMTP(url, from) }

// LimitError refuses a request from an Admission: the client gets status,
// code and message in the API's error shape, e.g. 403 "quota_exceeded". Use
// 429 only when retrying soon will work: the SDKs retry every 429.
func LimitError(status int, code, message string) error { return api.LimitError(status, code, message) }

// MCP modes, for Options.MCP.
const (
	MCPReadOnly = mcp.ReadOnly
	MCPFull     = mcp.Full
	MCPOff      = mcp.Off
)

// LoadConfig reads the WORKLY_* environment variables (via getenv), so that
// builds wrapping the server share its configuration. Call Validate on the
// result after applying any overrides.
func LoadConfig(getenv func(string) string) (Config, error) { return config.Load(getenv) }

// NewLogger returns the logger described by cfg: JSON or text on stderr.
func NewLogger(cfg Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

// DefaultWorkspace is the single workspace used by dev and self-hosted servers.
const DefaultWorkspace = workspace.Default

// ErrUnauthorized should be returned by a WorkspaceResolver for missing or
// invalid credentials; the server responds 401.
var ErrUnauthorized = workspace.ErrUnauthorized

// StaticResolver resolves every request to DefaultWorkspace. If apiKey is
// non-empty, /v1 requests must present it as a bearer token.
func StaticResolver(apiKey string) WorkspaceResolver {
	return workspace.Static{ID: workspace.Default, APIKey: apiKey}
}

// OpenStore opens a store from a database URL:
//
//	postgres://user:pass@host:5432/db
//	sqlite://relative/path.db
//	sqlite:///absolute/path.db
func OpenStore(ctx context.Context, databaseURL string) (Store, error) {
	switch {
	case strings.HasPrefix(databaseURL, "postgres://"), strings.HasPrefix(databaseURL, "postgresql://"):
		return postgres.Open(ctx, databaseURL)
	case strings.HasPrefix(databaseURL, "sqlite:"):
		path, err := SQLitePath(databaseURL)
		if err != nil {
			return nil, err
		}
		return sqlite.Open(path)
	case databaseURL == "":
		return nil, errors.New("no database URL configured (set WORKLY_DATABASE_URL)")
	default:
		return nil, fmt.Errorf("unsupported database URL %q: use postgres:// or sqlite://", databaseURL)
	}
}

// SQLitePath returns the file path in a sqlite:// URL.
func SQLitePath(databaseURL string) (string, error) {
	path, ok := strings.CutPrefix(databaseURL, "sqlite://")
	if !ok {
		path, _ = strings.CutPrefix(databaseURL, "sqlite:")
	}
	if path == "" {
		return "", fmt.Errorf("database URL %q has no file path", databaseURL)
	}
	return path, nil
}

type Options struct {
	// Addr is the listen address, e.g. ":7337".
	Addr  string
	Store Store
	// WorkspaceResolver defaults to StaticResolver("").
	WorkspaceResolver WorkspaceResolver
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// ShutdownTimeout bounds graceful shutdown: in-flight HTTP requests and
	// deliveries get this long to finish. Defaults to 25s, which fits in
	// Kubernetes' default 30s termination grace period.
	ShutdownTimeout time.Duration
	// OnReady, if set, is called once the server is listening.
	OnReady func(addr net.Addr)

	// SigningSecret signs deliveries (the Workly-Signature header). Required
	// unless DisableDispatcher is set.
	SigningSecret string
	// PreviousSigningSecret, if set, adds a second signature to each
	// delivery, so the signing secret can be rotated without failing
	// deliveries: endpoints verifying with either secret accept them.
	PreviousSigningSecret string
	// SigningSecrets, if set, gives each workspace its own secrets for
	// signing deliveries (the current one, and the previous one during a
	// rotation), instead of SigningSecret and PreviousSigningSecret. It's
	// called for every delivery, so it should cache. For a server shared by
	// untrusted users, who mustn't be able to sign for each other.
	SigningSecrets func(ctx context.Context, ws WorkspaceID) (current, previous []byte, err error)
	// DisableDispatcher runs the API only, without delivering tasks.
	DisableDispatcher bool
	// DisableQueueAutoCreate makes enqueueing to a missing queue an error
	// instead of creating the queue with defaults.
	DisableQueueAutoCreate bool
	// MaxPayloadBytes limits task bodies. Defaults to 1 MiB.
	MaxPayloadBytes int64
	// Retention is how long finished tasks (succeeded, failed or cancelled)
	// are kept before they are deleted. 0 keeps them forever.
	Retention time.Duration
	// RunRetention is the same for schedule runs, except that each
	// schedule's latest run is always kept. 0 keeps them forever.
	RunRetention time.Duration
	// DevEndpoints serves the /_dev/echo delivery target. For `workly dev`.
	DevEndpoints bool
	// DeliveryDialControl, if set, is called with the resolved address of
	// each connection a delivery makes, before connecting (see
	// net.Dialer.Control); an error fails the attempt with it. A server
	// open to untrusted users can refuse private networks with it. Proxy
	// settings from the environment are ignored while it's set, since a
	// proxy would connect on Workly's behalf.
	DeliveryDialControl func(network, address string, c syscall.RawConn) error
	// Admission, if set, is asked before tasks are enqueued and schedules
	// created or replaced, through the API and so through MCP, and can
	// refuse with LimitError. For quotas on a hosted service.
	Admission Admission
	// Mailer, if set, sends alert emails, so workspaces can have email
	// alert channels; SMTPMailer builds one. Slack channels need nothing.
	Mailer Mailer
	// PublicURL is where people open Workly, e.g.
	// https://workly.example.com. Alerts link to its dashboard; without it
	// they have no links.
	PublicURL string
	// Version is sent in the User-Agent of deliveries.
	Version string
	// Dashboard serves the web UI at /. Defaults to the dashboard embedded
	// in this module; a wrapping build passes its own, e.g.
	// dashboard.HandlerFS over its embedded dist.
	Dashboard http.Handler
	// WrapHandler, if set, wraps the server's HTTP handler, e.g. to mount a
	// wrapping build's own routes in front of Workly's.
	WrapHandler func(http.Handler) http.Handler
	// DisableMetrics doesn't serve /metrics on the API listener, e.g. to
	// serve MetricsHandler on an internal port instead. With WORKLY_API_KEY
	// set, /metrics requires it like /v1.
	DisableMetrics bool
	// MetricsAddr, if set, serves /metrics on this address (e.g. ":9090")
	// without authentication, and not on Addr. Keep the port internal to
	// the cluster; it lets Prometheus scrape without an API key.
	MetricsAddr string
	// MCP sets which tools the MCP endpoint (/mcp) serves to coding agents:
	// MCPReadOnly (the default) serves the tools that only read, MCPFull
	// adds ones that enqueue, retry, cancel and reconfigure, and MCPOff
	// doesn't serve it. /mcp takes the same credentials as /v1.
	MCP MCPMode
}

type Server struct {
	opts       Options
	handler    http.Handler
	dispatcher *dispatch.Dispatcher
	metrics    *metrics.Workly
	live       *live.Hub
}

func New(opts Options) (*Server, error) {
	if opts.Store == nil {
		return nil, errors.New("server: Store is required")
	}
	if opts.WorkspaceResolver == nil {
		opts.WorkspaceResolver = StaticResolver("")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ShutdownTimeout == 0 {
		opts.ShutdownTimeout = 25 * time.Second
	}
	if opts.MaxPayloadBytes == 0 {
		opts.MaxPayloadBytes = 1 << 20
	}
	if opts.Dashboard == nil {
		opts.Dashboard = dashboard.Handler()
	}
	if opts.SigningSecret == "" && opts.SigningSecrets == nil && !opts.DisableDispatcher {
		return nil, errors.New("server: SigningSecret is required to deliver tasks")
	}

	m := metrics.New()
	hub := live.New(opts.Store, opts.Logger)
	alerts := alert.NewSender(opts.Mailer, alert.Links(opts.PublicURL))
	apiOpts := api.Options{
		Store:            opts.Store,
		Resolver:         opts.WorkspaceResolver,
		Logger:           opts.Logger,
		Dashboard:        opts.Dashboard,
		AutoCreateQueues: !opts.DisableQueueAutoCreate,
		MaxPayloadBytes:  opts.MaxPayloadBytes,
		Metrics:          m,
		ServeMetrics:     !opts.DisableMetrics && opts.MetricsAddr == "",
		Retention:        opts.Retention,
		Admission:        opts.Admission,
		Alerts:           alerts,
		Signer: dispatch.NewSigner([]byte(opts.SigningSecret), []byte(opts.PreviousSigningSecret),
			userAgent(opts.Version), opts.SigningSecrets),
		MCP:     cmp.Or(opts.MCP, MCPReadOnly),
		Version: cmp.Or(opts.Version, "dev"),
		Live:    hub,
	}
	if opts.DevEndpoints {
		apiOpts.DevEcho = api.DevEcho([]byte(opts.SigningSecret), opts.Logger)
	}
	s := &Server{opts: opts, handler: api.NewHandler(apiOpts), metrics: m, live: hub}
	if opts.WrapHandler != nil {
		s.handler = opts.WrapHandler(s.handler)
	}
	if !opts.DisableDispatcher {
		s.dispatcher = dispatch.New(dispatch.Options{
			Store:                 opts.Store,
			Logger:                opts.Logger,
			SigningSecret:         []byte(opts.SigningSecret),
			PreviousSigningSecret: []byte(opts.PreviousSigningSecret),
			SigningSecrets:        opts.SigningSecrets,
			UserAgent:             userAgent(opts.Version),
			ShutdownTimeout:       opts.ShutdownTimeout,
			Retention:             opts.Retention,
			RunRetention:          opts.RunRetention,
			Metrics:               m,
			DialControl:           opts.DeliveryDialControl,
			Alerts:                &alert.Notifier{Store: opts.Store, Sender: alerts, Logger: opts.Logger},
			Changed:               hub.Changed,
		})
	}
	return s, nil
}

func userAgent(version string) string { return "Workly/" + cmp.Or(version, "dev") }

// Handler returns the server's HTTP handler, for embedding or tests.
func (s *Server) Handler() http.Handler { return s.handler }

// MetricsHandler serves the metrics in the Prometheus text format, without
// authentication. See Options.DisableMetrics.
func (s *Server) MetricsHandler() http.Handler { return s.metrics.Handler() }

// Run serves and delivers tasks until ctx is cancelled, then shuts down
// gracefully: in-flight requests and deliveries get ShutdownTimeout to finish.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return err
	}
	servers := []*http.Server{s.httpServer(s.handler, ctx)}
	listeners := []net.Listener{ln}
	if s.opts.MetricsAddr != "" {
		mln, err := net.Listen("tcp", s.opts.MetricsAddr)
		if err != nil {
			ln.Close()
			return fmt.Errorf("metrics listener: %w", err)
		}
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", s.metrics.Handler())
		servers = append(servers, s.httpServer(mux, ctx))
		listeners = append(listeners, mln)
	}
	ctx, cancel := context.WithCancel(ctx)

	var dispatcherDone sync.WaitGroup
	if s.dispatcher != nil {
		dispatcherDone.Go(func() { _ = s.dispatcher.Run(ctx) })
	}
	// Event streams end when the hub stops, before shutdown waits for
	// requests to finish.
	dispatcherDone.Go(func() { s.live.Run(ctx) })
	// Shutdown waits for in-flight deliveries whichever way Run returns.
	defer dispatcherDone.Wait()
	defer cancel()

	errc := make(chan error, len(servers))
	for i, srv := range servers {
		go func() { errc <- srv.Serve(listeners[i]) }()
	}
	s.opts.Logger.Info("listening", "addr", ln.Addr().String())
	if len(listeners) > 1 {
		s.opts.Logger.Info("serving metrics", "addr", listeners[1].Addr().String())
	}
	if s.opts.OnReady != nil {
		s.opts.OnReady(ln.Addr())
	}

	select {
	case err := <-errc:
		for _, srv := range servers {
			srv.Close()
		}
		return err
	case <-ctx.Done():
	}

	s.opts.Logger.Info("shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), s.opts.ShutdownTimeout)
	defer cancelShutdown()
	var errs []error
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) httpServer(h http.Handler, ctx context.Context) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		// Bounds slow uploads; the largest request is an enqueue of
		// MaxPayloadBytes.
		ReadTimeout: time.Minute,
		IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
}
