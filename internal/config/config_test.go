package config

import (
	"bytes"
	"encoding/json"
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
	if cfg.MaxCellLength != DefaultMaxCellLength {
		t.Errorf("MaxCellLength = %d, want default %d", cfg.MaxCellLength, DefaultMaxCellLength)
	}
}

// TestLoadIgnoresRemovedMaxResultRows confirms a config.json written by a
// version that still had the Max Result Rows option loads cleanly — the
// field is gone, results are never capped, and the stale key must not stop
// the rest of the file from parsing.
func TestLoadIgnoresRemovedMaxResultRows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "gossms")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"max_result_rows": 500, "max_cell_length": 42}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg := Load(); cfg.MaxCellLength != 42 {
		t.Errorf("MaxCellLength = %d, want 42", cfg.MaxCellLength)
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

// A config file that exists but can't be read is not the same as not having
// one. Load used to return the same empty Config for both, so a transient
// read failure came up with no saved connections and the next Save — of some
// unrelated setting — wrote that emptiness over a file that was still fine.
func TestLoadRefusesToSaveOverAnUnreadableConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a 0000 file is still readable")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "gossms")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "config.json")
	original := `{"connections":[{"name":"prod","server":"sql-prod"}],"max_cell_length":42}`
	if err := os.WriteFile(path, []byte(original), 0o000); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if len(cfg.Connections) != 0 {
		t.Fatalf("len(Connections) = %d, want 0 — the file was never read", len(cfg.Connections))
	}

	if err := cfg.Save(); err == nil {
		t.Error("Save() = nil error over an unreadable config, want a refusal")
	}

	// The refusal is only worth anything if the file is still intact.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("config.json was rewritten:\n got %s\nwant %s", after, original)
	}
}

// The other half of the same branch: no file at all is an ordinary first run,
// and must stay saveable.
func TestLoadMissingConfigStillSaves(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := Load()
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() after a first-run Load: %v", err)
	}
	if _, err := os.Stat(configPath()); err != nil {
		t.Errorf("config.json was not written: %v", err)
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
	// key) must end up owner-only, matching loadOrCreateKey's own MkdirAll.
	// Save runs (and calls MkdirAll) first on a fresh install, and MkdirAll
	// never chmods an already-existing directory, so Save's own mode is
	// what sticks.
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

// TestSaveIsAtomic confirms Save never leaves a partially written
// config.json behind: the write goes to a temp file in the same directory
// and is renamed into place. A plain in-place truncate+write would let a
// crash mid-write produce invalid JSON, which Load discards wholesale —
// silently losing every saved connection.
func TestSaveIsAtomic(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "myserver", Port: 1433, User: "sa", Password: "hunter2"})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	dir := filepath.Dir(configPath())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("Save left a temp file behind: %s", e.Name())
		}
	}

	// The renamed-into-place file must still be owner-only, not whatever
	// permissions CreateTemp happened to give the temp file.
	info, err := os.Stat(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.json mode = %04o, want 0600", perm)
	}

	if got := Load(); len(got.Connections) != 1 || got.Connections[0].Password != "hunter2" {
		t.Errorf("Load() after atomic Save didn't round-trip: %+v", got.Connections)
	}
}

// TestSaveCarriesFieldsItDoesNotNameExplicitly confirms Save copies the
// whole Config rather than re-listing its fields — the hand-written literal
// it replaced silently dropped any field added to Config later.
func TestSaveCarriesUnnamedFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := &Config{
		IconStyle:            IconStylePortable,
		MaxCellLength:        123,
		IntelliSenseDisabled: true,
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	got := Load()
	if got.IconStyle != IconStylePortable || got.MaxCellLength != 123 ||
		!got.IntelliSenseDisabled {
		t.Errorf("round-tripped config = %+v, want every field preserved", got)
	}
}

// TestLoadCorruptFileKeepsACopy confirms a config.json that exists but
// doesn't parse is preserved under .corrupt before being discarded — the
// passwords are unrecoverable either way, but the server/user/database
// fields are readable by hand.
func TestLoadCorruptFileKeepsACopy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "gossms")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"connections":[{"server":"myserver"`) // truncated mid-write
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	Load()

	saved, err := os.ReadFile(filepath.Join(cfgDir, "config.json.corrupt"))
	if err != nil {
		t.Fatalf("no .corrupt copy kept: %v", err)
	}
	if string(saved) != string(raw) {
		t.Errorf(".corrupt copy = %q, want the original bytes %q", saved, raw)
	}
}

// A config.json written before password binding must survive the upgrade:
// Load still returns the plaintext, and the next Save rewrites the entry in
// the bound format. Getting this wrong silently empties every saved password.
func TestLoadMigratesLegacyUnboundPasswordOnNextSave(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := loadOrCreateKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateKey: %v", err)
	}

	const plaintext = "legacy-s3cr3t"
	legacy, err := sealLegacyForTest(key, plaintext)
	if err != nil {
		t.Fatalf("sealLegacyForTest: %v", err)
	}
	onDisk := &Config{Connections: []Connection{{
		Name: "prod", Server: "sql-prod", Port: 1433, Database: "app",
		AuthMethod: AuthSQLServer, User: "sa", Password: legacy,
	}}}
	data, err := json.MarshalIndent(onDisk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Load reads the pre-binding format.
	cfg := Load()
	if len(cfg.Connections) != 1 {
		t.Fatalf("len(Connections) = %d, want 1", len(cfg.Connections))
	}
	if got := cfg.Connections[0].Password; got != plaintext {
		t.Fatalf("Load() password = %q, want %q — legacy config did not migrate", got, plaintext)
	}

	// Saving rewrites it bound, and it still round-trips.
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	raw, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	var reread Config
	if err := json.Unmarshal(raw, &reread); err != nil {
		t.Fatal(err)
	}
	stored := reread.Connections[0].Password
	if !strings.HasPrefix(stored, aadPrefix) {
		t.Errorf("stored password = %q, want the %q prefix after re-save", stored, aadPrefix)
	}
	if strings.Contains(string(raw), plaintext) {
		t.Error("plaintext password appears in the saved file")
	}
	if got := Load().Connections[0].Password; got != plaintext {
		t.Errorf("password after migration round trip = %q, want %q", got, plaintext)
	}
}

// TestUndecryptablePasswordSurvivesAnUnrelatedSave is the regression test for
// the data-loss path the sealed field closes. A password whose ciphertext no
// longer opens — here because the key file was replaced — used to be
// re-encrypted from the "" Load handed back, so saving any unrelated setting
// overwrote the one copy a restored key could still have read.
func TestUndecryptablePasswordSurvivesAnUnrelatedSave(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "srv", User: "sa", Password: "s3cr3t!"})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	cfgPath := configPath()
	sealedBefore := readStoredPassword(t, cfgPath)
	if sealedBefore == "" {
		t.Fatal("nothing was stored for the password")
	}

	// Replace the key so the stored ciphertext can no longer be opened,
	// standing in for a regenerated or wrongly-restored key file. The real
	// one is kept so the recovery assertion at the end can put it back.
	keyPath := filepath.Join(filepath.Dir(cfgPath), keyFileName)
	origKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{0xAB}, 32), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded := Load()
	if len(loaded.Connections) != 1 {
		t.Fatalf("Load() returned %d connections, want 1", len(loaded.Connections))
	}
	if got := loaded.Connections[0].Password; got != "" {
		t.Errorf("Password = %q, want \"\" — an unopenable ciphertext must not surface as plaintext", got)
	}

	// An unrelated setting changes and the config is written back.
	loaded.MaxCellLength = 99
	if err := loaded.Save(); err != nil {
		t.Fatalf("Save() after failed decrypt: %v", err)
	}

	if got := readStoredPassword(t, cfgPath); got != sealedBefore {
		t.Errorf("stored password after an unrelated Save = %q, want the original ciphertext %q — "+
			"it was overwritten and is now unrecoverable", got, sealedBefore)
	}

	// Restoring the original key brings the password back — the whole point
	// of not overwriting the ciphertext.
	if err := os.WriteFile(keyPath, origKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(); len(got.Connections) != 1 || got.Connections[0].Password != "s3cr3t!" {
		t.Errorf("Load() after restoring the key = %+v, want the password back as %q",
			got.Connections, "s3cr3t!")
	}
}

// TestReenteredPasswordReplacesAnUnopenableOne confirms the preserved
// ciphertext isn't sticky: reconnecting with a real password goes through
// AddOrUpdate as a fresh Connection, whose sealed field is empty, so the
// unopenable blob is replaced for good.
func TestReenteredPasswordReplacesAnUnopenableOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "srv", User: "sa", Password: "old-secret"})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	cfgPath := configPath()
	stale := readStoredPassword(t, cfgPath)

	keyPath := filepath.Join(filepath.Dir(cfgPath), keyFileName)
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{0xCD}, 32), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded := Load()
	loaded.AddOrUpdate(Connection{Server: "srv", User: "sa", Password: "new-secret"})
	if err := loaded.Save(); err != nil {
		t.Fatalf("Save() after re-entry: %v", err)
	}

	if got := readStoredPassword(t, cfgPath); got == stale {
		t.Error("re-entering the password left the old unopenable ciphertext in place")
	}
	if got := Load(); len(got.Connections) != 1 || got.Connections[0].Password != "new-secret" {
		t.Errorf("Load() after re-entry didn't return the new password: %+v", got.Connections)
	}
}

// readStoredPassword reads the raw (encrypted) password field of the single
// saved connection straight out of config.json.
func readStoredPassword(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		Connections []struct {
			Password string `json:"password"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if len(onDisk.Connections) != 1 {
		t.Fatalf("config.json has %d connections, want 1", len(onDisk.Connections))
	}
	return onDisk.Connections[0].Password
}
