package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/sqlite"
	"github.com/worklydev/workly/internal/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) func() store.Store {
		path := filepath.Join(t.TempDir(), "nested", "workly.db")
		return func() store.Store {
			s, err := sqlite.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			return s
		}
	})
}
