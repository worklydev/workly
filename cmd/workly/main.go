// Command workly is the Workly server and CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/worklydev/workly/internal/config"
	"github.com/worklydev/workly/server"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

const (
	devDatabaseURL = "sqlite://.workly/dev.db"
	// devSigningSecret is deliberately well known: dev servers are local and
	// unauthenticated, and a fixed secret lets apps verify signatures in
	// development without configuration.
	devSigningSecret = "workly-dev-secret"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The first signal shuts down gracefully; restoring the default handling
	// lets a second Ctrl-C exit at once instead of waiting for deliveries.
	go func() {
		<-ctx.Done()
		stop()
	}()

	if err := rootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "workly",
		Short:         "Workly: a simple, reliable HTTP task queue",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(devCmd(), serveCmd(), migrateCmd(), healthcheckCmd(), queuesCmd(), tasksCmd(), groupsCmd(), schedulesCmd(), runsCmd(), versionCmd())
	return root
}

// loadConfig reads WORKLY_* env vars; flags registered by bindFlags override them.
func loadConfig(cmd *cobra.Command) (config.Config, error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return cfg, err
	}
	flags := cmd.Flags()
	if flags.Changed("port") {
		cfg.Port, _ = flags.GetInt("port")
	}
	if flags.Changed("database-url") {
		cfg.DatabaseURL, _ = flags.GetString("database-url")
	}
	return cfg, cfg.Validate()
}

func bindFlags(cmd *cobra.Command) {
	cmd.Flags().Int("port", config.Default().Port, "port to listen on (env WORKLY_PORT)")
	cmd.Flags().String("database-url", "", "postgres:// or sqlite:// URL (env WORKLY_DATABASE_URL)")
}

func devCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Run a local development server (SQLite, no auth)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if cfg.DatabaseURL == "" {
				cfg.DatabaseURL = devDatabaseURL
			}
			if os.Getenv("WORKLY_LOG_FORMAT") == "" {
				cfg.LogFormat = "text"
			}
			if cfg.SigningSecret == "" {
				cfg.SigningSecret = devSigningSecret
			}
			cfg.APIKey = "" // dev never requires auth
			cfg.AutoMigrate = true
			if os.Getenv("WORKLY_MCP") == "" {
				cfg.MCP = server.MCPFull // local agents may enqueue and retry
			}

			return run(cmd.Context(), cfg, true, func(addr net.Addr) {
				port := addr.(*net.TCPAddr).Port
				fmt.Fprintf(os.Stderr, "\n  Workly %s running\n\n", version)
				fmt.Fprintf(os.Stderr, "  API:             http://localhost:%d\n", port)
				fmt.Fprintf(os.Stderr, "  Dashboard:       http://localhost:%d\n", port)
				fmt.Fprintf(os.Stderr, "  Echo target:     http://localhost:%d/_dev/echo\n", port)
				fmt.Fprintf(os.Stderr, "  Storage:         %s\n", cfg.DatabaseURL)
				fmt.Fprintf(os.Stderr, "  Signing secret:  %s\n", cfg.SigningSecret)
				if cfg.MCP != server.MCPOff {
					fmt.Fprintf(os.Stderr, "  MCP:             http://localhost:%d/mcp (%s)\n", port, cfg.MCP)
					fmt.Fprintf(os.Stderr, "\n  Connect Claude Code: claude mcp add --transport http workly http://localhost:%d/mcp\n", port)
				}
				fmt.Fprintln(os.Stderr)
			})
		},
	}
	bindFlags(cmd)
	return cmd
}

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Workly server (for self-hosting)",
		Long: `Run the Workly server (for self-hosting).

Requires WORKLY_DATABASE_URL (postgres://…) and WORKLY_SIGNING_SECRET. Other
settings: WORKLY_API_KEY, WORKLY_PORT, WORKLY_METRICS_PORT, WORKLY_RETENTION,
WORKLY_RUN_RETENTION, WORKLY_SHUTDOWN_TIMEOUT, WORKLY_SIGNING_SECRET_PREVIOUS, WORKLY_MCP and more; see
https://github.com/worklydev/workly/blob/main/docs/configuration.md`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if cfg.DatabaseURL == "" {
				return errors.New("set WORKLY_DATABASE_URL or --database-url (use `workly dev` for a local SQLite server)")
			}
			if cfg.SigningSecret == "" {
				return errors.New("set WORKLY_SIGNING_SECRET: deliveries are signed with it so your endpoints can verify them (e.g. `openssl rand -hex 32`)")
			}
			return run(cmd.Context(), cfg, false, nil)
		},
	}
	bindFlags(cmd)
	return cmd
}

func migrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations and exit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			st, err := server.OpenStore(cmd.Context(), cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.Migrate(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "migrations applied")
			return nil
		},
	}
	bindFlags(cmd)
	return cmd
}

// healthcheckCmd is for container health checks: the image is distroless,
// so there is no curl to probe with.
func healthcheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 if the local server is ready (for Docker HEALTHCHECK)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			url, _ := cmd.Flags().GetString("url")
			if url == "" {
				cfg, err := config.Load(os.Getenv)
				if err != nil {
					return err
				}
				url = fmt.Sprintf("http://localhost:%d", cfg.Port)
			}
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(strings.TrimRight(url, "/") + "/readyz")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
				return fmt.Errorf("not ready: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
			return nil
		},
	}
	cmd.Flags().String("url", "", "server URL (default http://localhost:$WORKLY_PORT)")
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Println(version)
		},
	}
}

func run(ctx context.Context, cfg config.Config, dev bool, onReady func(net.Addr)) error {
	logger := server.NewLogger(cfg)
	slog.SetDefault(logger)
	if !dev && cfg.APIKey == "" {
		logger.Warn("WORKLY_API_KEY is not set: anyone who can reach this server can use the API, the dashboard and /mcp")
	}

	st, err := server.OpenStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if cfg.AutoMigrate {
		if err := st.Migrate(ctx); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if _, err := st.EnsureWorkspace(ctx, server.DefaultWorkspace, "Default"); err != nil {
		return fmt.Errorf("ensure default workspace (have migrations been applied?): %w", err)
	}

	var metricsAddr string
	if cfg.MetricsPort != 0 {
		metricsAddr = fmt.Sprintf(":%d", cfg.MetricsPort)
	}
	var mailer server.Mailer
	if cfg.SMTPURL != "" {
		if mailer, err = server.SMTPMailer(cfg.SMTPURL, cfg.SMTPFrom); err != nil {
			return err
		}
	}
	publicURL := cfg.PublicURL
	if publicURL == "" && dev {
		publicURL = fmt.Sprintf("http://localhost:%d", cfg.Port)
	}
	srv, err := server.New(server.Options{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		MetricsAddr:       metricsAddr,
		ShutdownTimeout:   cfg.ShutdownTimeout,
		Store:             st,
		WorkspaceResolver: server.StaticResolver(cfg.APIKey),
		Logger:            logger,
		OnReady:           onReady,

		SigningSecret:          cfg.SigningSecret,
		PreviousSigningSecret:  cfg.PreviousSigningSecret,
		DisableQueueAutoCreate: !cfg.AutoCreateQueues,
		MaxPayloadBytes:        cfg.MaxPayloadBytes,
		Retention:              cfg.Retention,
		RunRetention:           cfg.RunRetention,
		DevEndpoints:           dev,
		Version:                version,
		MCP:                    cfg.MCP,
		Mailer:                 mailer,
		PublicURL:              publicURL,
	})
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}
