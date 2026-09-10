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

// S7: the generated name — AddOrUpdate's dedup key — tells apart every pair
// of connections that are not the same one. Built from server, port,
// database and User alone, two service principals on one server (User greyed
// for the method, so empty for both) replaced each other, as did Windows and
// every Entra method without a User.
func TestGeneratedNameSeparatesIdentitiesAndMethods(t *testing.T) {
	base := Connection{Server: "srv", Port: 1433, Database: "db"}
	with := func(m AuthMethod, user, client string) Connection {
		c := base
		c.AuthMethod, c.User, c.ClientID = m, user, client
		return c
	}
	cases := []struct {
		c    Connection
		want string
	}{
		// SQL Server Authentication keeps the name every saved list already has.
		{with(AuthSQLServer, "sa", ""), "srv,1433,db,sa"},
		{with(AuthWindows, "", ""), "srv,1433,db, (Windows)"},
		{with(AuthEntraDefault, "", ""), "srv,1433,db, (Entra Default)"},
		{with(AuthEntraAzCLI, "", ""), "srv,1433,db, (Entra Azure CLI)"},
		{with(AuthEntraServicePrincipal, "", "app-1"), "srv,1433,db,app-1 (Entra Service Principal)"},
		{with(AuthEntraServicePrincipal, "", "app-2"), "srv,1433,db,app-2 (Entra Service Principal)"},
		// Saved before ClientID was the method's field.
		{with(AuthEntraServicePrincipal, "app-3", ""), "srv,1433,db,app-3 (Entra Service Principal)"},
		{with(AuthEntraMSI, "", "mi-1"), "srv,1433,db,mi-1 (Entra Managed Identity)"},
		{with(AuthEntraMSI, "", ""), "srv,1433,db, (Entra Managed Identity)"},
		{with(AuthEntraInteractive, "ann@contoso.com", "app"), "srv,1433,db,ann@contoso.com (Entra MFA)"},
		{with(AuthEntraInteractive, "bob@contoso.com", "app"), "srv,1433,db,bob@contoso.com (Entra MFA)"},
		{with(AuthEntraPassword, "ann@contoso.com", "app"), "srv,1433,db,ann@contoso.com (Entra Password)"},
		{with(AuthEntraDeviceCode, "", ""), "srv,1433,db, (Entra Device Code)"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		got := c.c.GeneratedName()
		if got != c.want {
			t.Errorf("%s user %q client %q: GeneratedName = %q, want %q",
				AuthMethodName(c.c.AuthMethod), c.c.User, c.c.ClientID, got, c.want)
		}
		if seen[got] {
			t.Errorf("GeneratedName %q generated twice", got)
		}
		seen[got] = true
	}

	cfg := &Config{}
	for _, c := range cases {
		cfg.AddOrUpdate(c.c)
	}
	if len(cfg.Connections) != len(cases) {
		t.Errorf("%d distinct connections saved as %d entries", len(cases), len(cfg.Connections))
	}
}

// The auth table's labels are the Connect dialog's dropdown items, SSMS's
// names for the methods; a relabel is free (the method is stored as its
// number) but the order and the numbers are not.
func TestAuthMethodTable(t *testing.T) {
	want := []struct {
		m     AuthMethod
		n     int
		label string
		entra bool
	}{
		{AuthSQLServer, 0, "SQL Server Authentication", false},
		{AuthWindows, 1, "Windows Authentication", false},
		{AuthEntraDefault, 2, "Microsoft Entra Default", true},
		{AuthEntraPassword, 3, "Microsoft Entra Password", true},
		{AuthEntraMSI, 4, "Microsoft Entra Managed Identity", true},
		{AuthEntraServicePrincipal, 5, "Microsoft Entra Service Principal", true},
		{AuthEntraInteractive, 9, "Microsoft Entra MFA", true},
		{AuthEntraDeviceCode, 10, "Microsoft Entra Device Code", true},
		{AuthEntraAzCLI, 11, "Microsoft Entra Azure CLI", true},
	}
	all := AllAuthMethods()
	if len(all) != len(want) {
		t.Fatalf("AllAuthMethods has %d methods, want %d", len(all), len(want))
	}
	for i, w := range want {
		if all[i] != w.m || int(w.m) != w.n {
			t.Errorf("AllAuthMethods()[%d] = %d, want %d", i, all[i], w.n)
		}
		if got := AuthMethodName(w.m); got != w.label {
			t.Errorf("AuthMethodName(%d) = %q, want %q", w.n, got, w.label)
		}
		if got := IsEntraMethod(w.m); got != w.entra {
			t.Errorf("IsEntraMethod(%d) = %v, want %v", w.n, got, w.entra)
		}
	}
	// A method only a hand-edited config.json has dials as SQL Server
	// Authentication (db.toGosmoAuth), so it reads that one's fields.
	if AuthMethodName(7) != "Unknown" || IsEntraMethod(7) || FieldsFor(7) != FieldsFor(AuthSQLServer) {
		t.Errorf("unknown method 7: %q, entra %v, fields %+v", AuthMethodName(7), IsEntraMethod(7), FieldsFor(7))
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
		Encrypt:                EncryptStrict,
		HostNameInCertificate:  "sql.example.com",
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
		got.User != want.User || got.ExtraProperties != want.ExtraProperties ||
		got.Encrypt != want.Encrypt || got.HostNameInCertificate != want.HostNameInCertificate ||
		got.TrustServerCertificate != want.TrustServerCertificate {
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

// TestSaveCarriesUnnamedFields confirms Save copies the
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

// PasswordUnreadable separates "no password saved" from "a password is saved
// and this session cannot open it" — two states Password alone cannot tell
// apart, since Load blanks it for both. A caller that would sign in with the
// entry needs the difference: the second is a guaranteed login failure, and
// the sealed ciphertext behind it is still on its way back to disk untouched.
func TestPasswordUnreadableDistinguishesNoPasswordFromASealedOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		conn Connection
		want bool
	}{
		{"no password at all", Connection{Server: "a"}, false},
		{"password available", Connection{Server: "a", Password: "pw"}, false},
		{"sealed and not opened", Connection{Server: "a", sealed: "gAAA-nope"}, true},
		// Both set cannot happen out of Load, but the password is what a
		// caller would use, so it wins.
		{"opened, ciphertext retained", Connection{Server: "a", Password: "pw", sealed: "gAAA"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.conn.PasswordUnreadable(); got != tc.want {
				t.Errorf("PasswordUnreadable() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The state above is one Load actually produces: a ciphertext the key cannot
// open comes back with an empty Password and the ciphertext stashed.
func TestLoadMarksAnUndecryptablePasswordUnreadable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "gossms")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"connections": [
		{"server": "ubusql2", "user": "sa", "password": "not-a-ciphertext"},
		{"server": "ubusql3", "auth_method": 1}
	]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if len(cfg.Connections) != 2 {
		t.Fatalf("len(Connections) = %d, want 2", len(cfg.Connections))
	}
	if !cfg.Connections[0].PasswordUnreadable() {
		t.Error("an undecryptable password did not report as unreadable")
	}
	if cfg.Connections[1].PasswordUnreadable() {
		t.Error("a connection that never had a password reports as unreadable")
	}
}

// The boolean "encrypt" earlier releases wrote maps onto the two modes its
// checkbox could express, and "encrypt_mode" wins once present.
func TestLoadMapsTheLegacyEncryptBoolean(t *testing.T) {
	cases := []struct {
		json string
		want EncryptMode
	}{
		{`{"server":"s","encrypt":true}`, EncryptMandatory},
		{`{"server":"s","encrypt":false}`, EncryptOptional},
		{`{"server":"s"}`, EncryptOptional},
		{`{"server":"s","encrypt":false,"encrypt_mode":"strict"}`, EncryptStrict},
		{`{"server":"s","encrypt":true,"encrypt_mode":"optional"}`, EncryptOptional},
	}
	for _, c := range cases {
		var conn Connection
		if err := json.Unmarshal([]byte(c.json), &conn); err != nil {
			t.Fatalf("Unmarshal(%s): %v", c.json, err)
		}
		if conn.Encrypt != c.want || conn.Server != "s" {
			t.Errorf("Unmarshal(%s) → Encrypt %q, Server %q; want %q, s", c.json, conn.Encrypt, conn.Server, c.want)
		}
	}
}

// A release before encrypt_mode decodes "encrypt" as a bool; a string there
// fails its whole config.json. So the boolean is still written, true for
// anything but Optional, beside the mode.
func TestSaveKeepsTheLegacyEncryptBooleanForADowngrade(t *testing.T) {
	for mode, legacy := range map[EncryptMode]bool{
		EncryptOptional: false, EncryptMandatory: true, EncryptStrict: true, "": false,
	} {
		data, err := json.Marshal(Connection{Server: "s", Encrypt: mode})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var old struct {
			Server  string `json:"server"`
			Encrypt bool   `json:"encrypt"`
		}
		if err := json.Unmarshal(data, &old); err != nil {
			t.Fatalf("an old release could not read %s: %v", data, err)
		}
		if old.Encrypt != legacy || old.Server != "s" {
			t.Errorf("mode %q wrote encrypt=%v (%s), want %v", mode, old.Encrypt, data, legacy)
		}
	}
}

// A log past the limit is moved to .1 and a fresh one started; a log under it
// is left alone and appended to. Only one generation is kept, so a second
// rotation replaces the first .1 rather than accumulating files.
func TestOpenLogFileRotatesOnlyPastTheLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := LogFilePath()
	if err != nil {
		t.Fatal(err)
	}

	writeLog := func(content string) {
		t.Helper()
		f, err := OpenLogFile()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(content); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	writeLog("small\n")
	writeLog("more\n")
	if got := read(path); got != "small\nmore\n" {
		t.Fatalf("a log under the limit was not appended to: %q", got)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("a log under the limit was rotated (stat .1: %v)", err)
	}

	big := strings.Repeat("x", MaxLogSize+1)
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLog("fresh\n")
	if got := read(path); got != "fresh\n" {
		t.Errorf("after rotation the log holds %d bytes, want only the new line", len(got))
	}
	if got := read(path + ".1"); got != big {
		t.Errorf(".1 holds %d bytes, want the %d-byte old log", len(got), len(big))
	}

	if err := os.WriteFile(path, []byte(big+"second"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLog("third\n")
	if got := read(path + ".1"); got != big+"second" {
		t.Errorf("the second rotation did not replace .1 (%d bytes)", len(got))
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("fresh log mode = %v, want 0600", fi.Mode().Perm())
	}
}
