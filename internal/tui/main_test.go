package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// TestMain points the process-wide tracked-query set at a temp file before any
// test runs. config.Tracked() otherwise reads — and Track Query writes — the
// list belonging to whoever ran the tests, and a panel is built by enough tests
// that opting in one by one would not hold.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gossms-tracked")
	if err != nil {
		panic(err)
	}
	config.UseTrackedQueries(config.LoadTrackedQueriesFrom(filepath.Join(dir, "tracked_queries.json")))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
