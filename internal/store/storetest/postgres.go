package storetest

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// PostgresURL returns a connection URL for a fresh schema, dropped when the
// test ends. It skips the test unless WORKLY_TEST_POSTGRES_URL is set (`just
// test-all` sets it).
func PostgresURL(t *testing.T) string {
	t.Helper()
	baseURL := os.Getenv("WORKLY_TEST_POSTGRES_URL")
	if baseURL == "" {
		t.Skip("WORKLY_TEST_POSTGRES_URL not set")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	schema := "workly_test_" + strings.ToLower(rand.Text()[:12])
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, baseURL)
		if err != nil {
			t.Logf("cleanup connect: %v", err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)); err != nil {
			t.Logf("drop schema: %v", err)
		}
	})

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
