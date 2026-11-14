package config

import (
	"os"
	"path/filepath"
	"testing"
)

// configHomeEnv names the variable os.UserConfigDir reads on each OS:
// XDG_CONFIG_HOME on Linux/BSD, HOME on macOS (…/Library/Application Support)
// and AppData on Windows. Setting all three keeps the one build free of
// runtime.GOOS. Redirecting XDG_CONFIG_HOME alone left Windows and macOS test
// runs saving synthetic connections ("from-a", "from-b") and profiles into
// the developer's real config.json (issue #23).
var configHomeEnv = []string{"XDG_CONFIG_HOME", "HOME", "AppData"}

// TestMain points os.UserConfigDir at a temp dir before any test runs, so a
// test that never calls scratchConfigDir still cannot reach the user's real
// config.json, config key or tracked-query file. Tests that save should still
// call scratchConfigDir: this directory is shared by the whole package.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gossms-config-test")
	if err != nil {
		panic(err)
	}
	for _, env := range configHomeEnv {
		os.Setenv(env, dir)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// scratchConfigDir points os.UserConfigDir at a fresh temp dir for this test
// on every OS and returns gossms's config directory inside it — the one
// holding config.json and the key. It is not created: Save makes it.
func scratchConfigDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, env := range configHomeEnv {
		t.Setenv(env, home)
	}
	return filepath.Dir(configPath())
}
