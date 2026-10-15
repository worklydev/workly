// Package config loads server configuration from WORKLY_* environment
// variables. Command-line flags override these values in cmd/workly.
package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/mcp"
)

type Config struct {
	Port          int
	DatabaseURL   string
	APIKey        string
	SigningSecret string
	// PreviousSigningSecret also signs deliveries during a secret rotation.
	PreviousSigningSecret string
	LogLevel              slog.Level
	LogFormat             string // "json" or "text"
	AutoMigrate           bool
	AutoCreateQueues      bool
	MaxPayloadBytes       int64
	// Retention is how long finished tasks are kept. 0 keeps them forever.
	Retention time.Duration
	// RunRetention is how long finished schedule runs are kept. It
	// defaults to Retention. 0 keeps them forever.
	RunRetention time.Duration
	// MetricsPort, if set, serves /metrics on its own port without
	// authentication instead of on Port.
	MetricsPort int
	// ShutdownTimeout is how long in-flight requests and deliveries get to
	// finish on shutdown.
	ShutdownTimeout time.Duration
	// MCP sets which tools /mcp serves: read-only (the default), full or off.
	MCP mcp.Mode
	// PublicURL is where people open Workly; alerts link to it.
	PublicURL string
	// SMTPURL, if set, sends alert emails through this server, from
	// SMTPFrom.
	SMTPURL  string
	SMTPFrom string
}

func Default() Config {
	return Config{
		Port:             7337,
		LogLevel:         slog.LevelInfo,
		LogFormat:        "json",
		AutoMigrate:      true,
		AutoCreateQueues: true,
		MaxPayloadBytes:  1 << 20,
		Retention:        7 * 24 * time.Hour,
		RunRetention:     7 * 24 * time.Hour,
		ShutdownTimeout:  25 * time.Second,
		MCP:              mcp.ReadOnly,
	}
}

// Load returns Default() overridden by any WORKLY_* variables found via getenv.
func Load(getenv func(string) string) (Config, error) {
	c := Default()

	for name, dst := range map[string]*int{
		"WORKLY_PORT":         &c.Port,
		"WORKLY_METRICS_PORT": &c.MetricsPort,
	} {
		v := getenv(name)
		if strings.HasPrefix(v, "tcp://") {
			// Kubernetes sets WORKLY_PORT=tcp://<ip>:<port> in pods when a
			// Service named "workly" exists (service links). It isn't ours.
			continue
		}
		if v != "" {
			port, err := strconv.Atoi(v)
			if err != nil {
				return c, fmt.Errorf("%s: %q is not a number", name, v)
			}
			*dst = port
		}
	}
	if v := getenv("WORKLY_DATABASE_URL"); v != "" {
		c.DatabaseURL = v
	}
	if v := getenv("WORKLY_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := getenv("WORKLY_LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return c, fmt.Errorf("WORKLY_LOG_LEVEL: %q is not one of debug, info, warn, error", v)
		}
	}
	if v := getenv("WORKLY_LOG_FORMAT"); v != "" {
		c.LogFormat = strings.ToLower(v)
	}
	if v := getenv("WORKLY_SIGNING_SECRET"); v != "" {
		c.SigningSecret = v
	}
	if v := getenv("WORKLY_SIGNING_SECRET_PREVIOUS"); v != "" {
		c.PreviousSigningSecret = v
	}
	for name, dst := range map[string]*bool{
		"WORKLY_AUTO_MIGRATE":       &c.AutoMigrate,
		"WORKLY_AUTO_CREATE_QUEUES": &c.AutoCreateQueues,
	} {
		if v := getenv(name); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return c, fmt.Errorf("%s: %q is not a boolean", name, v)
			}
			*dst = b
		}
	}
	if v := getenv("WORKLY_MAX_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, fmt.Errorf("WORKLY_MAX_PAYLOAD_BYTES: %q is not a number", v)
		}
		c.MaxPayloadBytes = n
	}
	if v := getenv("WORKLY_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("WORKLY_RETENTION: %q is not a duration like 168h (0 keeps tasks forever)", v)
		}
		c.Retention = d
	}
	c.RunRetention = c.Retention
	if v := getenv("WORKLY_RUN_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("WORKLY_RUN_RETENTION: %q is not a duration like 168h (0 keeps runs forever)", v)
		}
		c.RunRetention = d
	}
	if v := getenv("WORKLY_SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("WORKLY_SHUTDOWN_TIMEOUT: %q is not a duration like 25s", v)
		}
		c.ShutdownTimeout = d
	}
	c.PublicURL = strings.TrimRight(getenv("WORKLY_PUBLIC_URL"), "/")
	c.SMTPURL = getenv("WORKLY_SMTP_URL")
	c.SMTPFrom = getenv("WORKLY_SMTP_FROM")
	if v := getenv("WORKLY_MCP"); v != "" {
		m, err := mcp.ParseMode(v)
		if err != nil {
			return c, fmt.Errorf("WORKLY_MCP: %w", err)
		}
		c.MCP = m
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range", c.Port)
	}
	if c.MetricsPort < 0 || c.MetricsPort > 65535 {
		return fmt.Errorf("metrics port %d out of range", c.MetricsPort)
	}
	if c.MetricsPort != 0 && c.MetricsPort == c.Port {
		return fmt.Errorf("metrics port %d must differ from port", c.MetricsPort)
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return fmt.Errorf("log format %q must be json or text", c.LogFormat)
	}
	if c.MaxPayloadBytes < 1 || c.MaxPayloadBytes > 100<<20 {
		return fmt.Errorf("max payload bytes %d must be between 1 and 100 MiB", c.MaxPayloadBytes)
	}
	if c.Retention < 0 {
		return fmt.Errorf("retention %v must not be negative", c.Retention)
	}
	if c.RunRetention < 0 {
		return fmt.Errorf("run retention %v must not be negative", c.RunRetention)
	}
	if c.PublicURL != "" && !strings.HasPrefix(c.PublicURL, "http://") && !strings.HasPrefix(c.PublicURL, "https://") {
		return fmt.Errorf("public URL %q must start with http:// or https://", c.PublicURL)
	}
	if c.SMTPURL != "" && c.SMTPFrom == "" {
		return fmt.Errorf("WORKLY_SMTP_URL needs WORKLY_SMTP_FROM, the address alerts are sent from")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown timeout %v must be positive", c.ShutdownTimeout)
	}
	return nil
}
