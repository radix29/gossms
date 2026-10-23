package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// TestMain points os.UserConfigDir and the process-wide tracked-query set at a
// temp dir before any test runs. Otherwise every saveConfig (Options OK,
// connection edits) writes the config.json of whoever ran the tests:
// TestOptionsApplySwitchesColorTheme left the developer's theme Light, because
// its Dark save is queued behind a finish no event loop ever delivers.
// Config.Tracked() likewise reads — and Track Query writes — their list, and a
// panel is built by enough tests that opting in one by one would not hold.
//
// Each variable is the one os.UserConfigDir reads on some OS (XDG_CONFIG_HOME
// on Linux/BSD, HOME on macOS, AppData on Windows); setting all three keeps the
// one build free of runtime.GOOS.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gossms-test-config")
	if err != nil {
		panic(err)
	}
	for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "AppData"} {
		os.Setenv(env, dir)
	}
	config.UseTrackedQueries(config.LoadTrackedQueriesFrom(filepath.Join(dir, "tracked_queries.json")))
	code := m.Run()
	savesInFlight.Wait() // a Track Query's save may still be writing there
	os.RemoveAll(dir)
	os.Exit(code)
}
