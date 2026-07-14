package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionName(t *testing.T) {
	got := ConnectionName("myserver", 1433, "mydb", "sa")
	want := "myserver,1433,mydb,sa"
	if got != want {
		t.Errorf("ConnectionName(...) = %q, want %q", got, want)
	}
}

func TestConnectionDisplayName(t *testing.T) {
	cases := []struct {
		name string
		c    Connection
		want string
	}{
		{"explicit name wins", Connection{Name: "saved-name", Server: "srv"}, "saved-name"},
		{"falls back to server", Connection{Server: "srv"}, "srv"},
		{"falls back to unnamed", Connection{}, "(unnamed)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.c.DisplayName(); got != c.want {
				t.Errorf("DisplayName() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAddOrUpdateGeneratesName(t *testing.T) {
	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "srv", Port: 1433, Database: "db", User: "sa"})
	if len(cfg.Connections) != 1 {
		t.Fatalf("len(Connections) = %d, want 1", len(cfg.Connections))
	}
	want := "srv,1433,db,sa"
	if got := cfg.Connections[0].Name; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
}

func TestAddOrUpdateReplacesExistingAndMovesToEnd(t *testing.T) {
	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "a", Port: 1433, Database: "db", User: "u"})
	cfg.AddOrUpdate(Connection{Server: "b", Port: 1433, Database: "db", User: "u"})
	// Re-add "a" with a different password; should replace in place, not duplicate,
	// and become the most-recently-used (last) entry.
	cfg.AddOrUpdate(Connection{Server: "a", Port: 1433, Database: "db", User: "u", Password: "new"})

	if len(cfg.Connections) != 2 {
		t.Fatalf("len(Connections) = %d, want 2 (no duplicate)", len(cfg.Connections))
	}
	if cfg.Connections[0].Server != "b" {
		t.Errorf("Connections[0].Server = %q, want b", cfg.Connections[0].Server)
	}
	last := cfg.Connections[len(cfg.Connections)-1]
	if last.Server != "a" || last.Password != "new" {
		t.Errorf("Connections[last] = %+v, want Server=a Password=new", last)
	}
}

func TestAddOrUpdateEvictsOldestBeyondCap(t *testing.T) {
	cfg := &Config{}
	for i := 0; i < MaxSavedConnections+3; i++ {
		cfg.AddOrUpdate(Connection{Server: "srv", Port: i, Database: "db", User: "u"})
	}
	if len(cfg.Connections) != MaxSavedConnections {
		t.Fatalf("len(Connections) = %d, want %d", len(cfg.Connections), MaxSavedConnections)
	}
	// The oldest 3 (Port 0,1,2) should have been evicted; the most recent
	// (Port == MaxSavedConnections+2) should be last.
	first := cfg.Connections[0]
	if first.Port != 3 {
		t.Errorf("Connections[0].Port = %d, want 3 (oldest 3 evicted)", first.Port)
	}
	last := cfg.Connections[len(cfg.Connections)-1]
	if last.Port != MaxSavedConnections+2 {
		t.Errorf("Connections[last].Port = %d, want %d", last.Port, MaxSavedConnections+2)
	}
}

func TestMatchByServer(t *testing.T) {
	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "prod-db-1", Database: "db", User: "u"})
	cfg.AddOrUpdate(Connection{Server: "other", Database: "db", User: "u"})
	cfg.AddOrUpdate(Connection{Server: "prod-db-2", Database: "db", User: "u"})

	matches := cfg.MatchByServer("PROD-")
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	// Most-recently-used first.
	if matches[0].Server != "prod-db-2" || matches[1].Server != "prod-db-1" {
		t.Errorf("matches = [%q, %q], want [prod-db-2, prod-db-1] (MRU first)", matches[0].Server, matches[1].Server)
	}
}

func TestMatchByServerNoMatch(t *testing.T) {
	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "prod-db-1", Database: "db", User: "u"})
	if matches := cfg.MatchByServer("staging"); len(matches) != 0 {
		t.Errorf("len(matches) = %d, want 0", len(matches))
	}
}

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := Load()
	if cfg == nil {
		t.Fatal("Load() = nil, want an empty *Config")
	}
	if len(cfg.Connections) != 0 {
		t.Errorf("len(Connections) = %d, want 0", len(cfg.Connections))
	}
	if cfg.MaxResultRows != DefaultMaxResultRows {
		t.Errorf("MaxResultRows = %d, want default %d", cfg.MaxResultRows, DefaultMaxResultRows)
	}
}

// TestLoadCoercesNonPositiveMaxResultRows confirms a zero (predating this
// field) or negative (hand-edited) max_result_rows in config.json is
// coerced back to the default, the same way MaxCellLength already is —
// every other reader of *Config must always see a usable, positive cap.
func TestLoadCoercesNonPositiveMaxResultRows(t *testing.T) {
	for _, raw := range []int{0, -5} {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", dir)
		cfgDir := filepath.Join(dir, "gossms")
		if err := os.MkdirAll(cfgDir, 0o700); err != nil {
			t.Fatal(err)
		}
		data := fmt.Sprintf(`{"max_result_rows": %d}`, raw)
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Load()
		if cfg.MaxResultRows != DefaultMaxResultRows {
			t.Errorf("raw %d: MaxResultRows = %d, want default %d", raw, cfg.MaxResultRows, DefaultMaxResultRows)
		}
	}
}

func TestLoadCorruptFileReturnsEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "gossms")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Load()
	if len(cfg.Connections) != 0 {
		t.Errorf("len(Connections) = %d, want 0 for a corrupt file", len(cfg.Connections))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	cfg := &Config{}
	cfg.AddOrUpdate(Connection{
		Server:                 "myserver",
		Port:                   1433,
		Database:               "mydb",
		AuthMethod:             AuthSQLServer,
		User:                   "sa",
		Password:               "s3cr3t!",
		TrustServerCertificate: true,
		Encrypt:                true,
		ExtraProperties:        "packetsize=4096",
	})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	// The config directory (holding both config.json and the encryption
	// key) must end up owner-only, matching loadOrCreateKey's own
	// MkdirAll — Save used to create it 0755 on a fresh install since it
	// runs (and calls MkdirAll) before loadOrCreateKey does, and MkdirAll
	// never chmods an already-existing directory.
	info, err := os.Stat(filepath.Join(xdgDir, "gossms"))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir permissions = %o, want 0700", perm)
	}

	loaded := Load()
	if len(loaded.Connections) != 1 {
		t.Fatalf("len(loaded.Connections) = %d, want 1", len(loaded.Connections))
	}
	got := loaded.Connections[0]
	want := cfg.Connections[0]
	if got.Server != want.Server || got.Port != want.Port || got.Database != want.Database ||
		got.User != want.User || got.ExtraProperties != want.ExtraProperties {
		t.Errorf("loaded connection = %+v, want %+v", got, want)
	}
	if got.Password != "s3cr3t!" {
		t.Errorf("Password after round trip = %q, want s3cr3t! (encryption should be transparent)", got.Password)
	}
}

func TestIconStyleDefaultsToEmoji(t *testing.T) {
	cfg := &Config{}
	if cfg.IconStyle != IconStyleEmoji {
		t.Errorf("IconStyle = %v, want IconStyleEmoji (zero value)", cfg.IconStyle)
	}
}

func TestIconStyleRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := &Config{IconStyle: IconStylePortable}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	loaded := Load()
	if loaded.IconStyle != IconStylePortable {
		t.Errorf("loaded.IconStyle = %v, want IconStylePortable", loaded.IconStyle)
	}
}

func TestSavePasswordIsEncryptedOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "srv", Database: "db", User: "sa", Password: "s3cr3t!"})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "gossms", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cr3t!") {
		t.Error("config.json contains the plaintext password; it should be AES-GCM encrypted")
	}
}
