package config

import (
	"log/slog"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/mcp"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c != Default() {
		t.Fatalf("Load() = %+v, want %+v", c, Default())
	}
}

func TestLoadIgnoresKubernetesServiceLinks(t *testing.T) {
	// A Service named "workly" makes Kubernetes set WORKLY_PORT like this.
	c, err := Load(env(map[string]string{"WORKLY_PORT": "tcp://10.96.0.12:7337"}))
	if err != nil || c.Port != Default().Port {
		t.Fatalf("Load() = port %d, %v; want the default port", c.Port, err)
	}
}

func TestLoadOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"WORKLY_PORT":               "8080",
		"WORKLY_DATABASE_URL":       "postgres://localhost/workly",
		"WORKLY_API_KEY":            "secret",
		"WORKLY_LOG_LEVEL":          "debug",
		"WORKLY_LOG_FORMAT":         "TEXT",
		"WORKLY_AUTO_MIGRATE":       "false",
		"WORKLY_SIGNING_SECRET":     "whsec",
		"WORKLY_AUTO_CREATE_QUEUES": "false",
		"WORKLY_MAX_PAYLOAD_BYTES":  "2048",
		"WORKLY_RETENTION":          "24h",
		"WORKLY_METRICS_PORT":       "9090",
		"WORKLY_SHUTDOWN_TIMEOUT":   "1m",
		"WORKLY_MCP":                "Full",

		"WORKLY_SIGNING_SECRET_PREVIOUS": "old",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Port:             8080,
		DatabaseURL:      "postgres://localhost/workly",
		APIKey:           "secret",
		LogLevel:         slog.LevelDebug,
		LogFormat:        "text",
		AutoMigrate:      false,
		SigningSecret:    "whsec",
		AutoCreateQueues: false,
		MaxPayloadBytes:  2048,
		Retention:        24 * time.Hour,
		RunRetention:     24 * time.Hour,
		MetricsPort:      9090,
		ShutdownTimeout:  time.Minute,
		MCP:              mcp.Full,

		PreviousSigningSecret: "old",
	}
	if c != want {
		t.Fatalf("Load() = %+v, want %+v", c, want)
	}
}

func TestLoadInvalid(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"port not a number":      {"WORKLY_PORT": "abc"},
		"port out of range":      {"WORKLY_PORT": "70000"},
		"bad log level":          {"WORKLY_LOG_LEVEL": "loud"},
		"bad log format":         {"WORKLY_LOG_FORMAT": "xml"},
		"bad auto migrate":       {"WORKLY_AUTO_MIGRATE": "maybe"},
		"bad auto create":        {"WORKLY_AUTO_CREATE_QUEUES": "maybe"},
		"bad payload limit":      {"WORKLY_MAX_PAYLOAD_BYTES": "1MB"},
		"payload limit 0":        {"WORKLY_MAX_PAYLOAD_BYTES": "0"},
		"retention in days":      {"WORKLY_RETENTION": "7d"},
		"negative retention":     {"WORKLY_RETENTION": "-1h"},
		"bad run retention":      {"WORKLY_RUN_RETENTION": "a week"},
		"negative run retention": {"WORKLY_RUN_RETENTION": "-1h"},
		"bad metrics port":       {"WORKLY_METRICS_PORT": "70000"},
		"same metrics port":      {"WORKLY_METRICS_PORT": "7337"},
		"shutdown timeout 0":     {"WORKLY_SHUTDOWN_TIMEOUT": "0s"},
		"bad shutdown":           {"WORKLY_SHUTDOWN_TIMEOUT": "25"},
		"bad mcp":                {"WORKLY_MCP": "on"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(vars)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
