package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func profileOf(t *testing.T, cfg *Config, server string) Connection {
	t.Helper()
	for _, c := range cfg.Connections {
		if c.Server == server {
			return c
		}
	}
	t.Fatalf("no saved connection for %s in %v", server, serversOf(cfg.Connections))
	return Connection{}
}

func TestValidateProfileName(t *testing.T) {
	for _, ok := range []string{"", "dev", "prod-ro", "a.b_c", "9lives", strings.Repeat("x", MaxProfileNameLength)} {
		if err := ValidateProfileName(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	// A leading '-' would parse as an option: `gossms profile show -dev`.
	for _, bad := range []string{"-dev", ".dev", "_dev", "has space", "srv,1433", "dév", "a/b", strings.Repeat("x", MaxProfileNameLength+1)} {
		if ValidateProfileName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSetProfileNamesAndClears(t *testing.T) {
	cfg := &Config{}
	cfg.AddOrUpdate(Connection{Server: "a", User: "sa"})
	cfg.AddOrUpdate(Connection{Server: "b", User: "sa"})
	keyA := Connection{Server: "a", User: "sa"}.GeneratedName()
	keyB := Connection{Server: "b", User: "sa"}.GeneratedName()

	if err := cfg.SetProfile(keyA, "Dev", AllowWrite); err != nil {
		t.Fatal(err)
	}
	if p := profileOf(t, cfg, "a"); p.Profile != "Dev" || p.MaxAllow != AllowWrite {
		t.Fatalf("a = %q/%q, want Dev/write", p.Profile, p.MaxAllow)
	}
	// Unique ignoring case, against every other entry.
	if err := cfg.SetProfile(keyB, "dev", AllowRead); err == nil {
		t.Error("a second entry took the name dev")
	}
	// Renaming the same entry, case and all, is fine.
	if err := cfg.SetProfile(keyA, "dev", AllowRead); err != nil {
		t.Errorf("renaming a to dev: %v", err)
	}
	for _, bad := range []struct {
		key, name string
		allow     AllowClass
	}{
		{keyB, "-x", AllowRead},
		{keyB, "x", "superuser"},
		{"nope,1433,,", "x", AllowRead},
	} {
		if err := cfg.SetProfile(bad.key, bad.name, bad.allow); err == nil {
			t.Errorf("SetProfile(%q, %q, %q) accepted", bad.key, bad.name, bad.allow)
		}
	}
	// Clearing the name clears the ceiling with it.
	if err := cfg.SetProfile(keyA, "", AllowRead); err != nil {
		t.Fatal(err)
	}
	if p := profileOf(t, cfg, "a"); p.Profile != "" || p.MaxAllow != "" {
		t.Errorf("cleared a = %q/%q, want both empty", p.Profile, p.MaxAllow)
	}
}

// A reconnect through the Connect dialog has no profile in its form; the name
// and ceiling must carry over from the entry it replaces.
func TestAddOrUpdateKeepsTheProfileOnReconnect(t *testing.T) {
	cfg := &Config{}
	conn := Connection{Server: "a", User: "sa", Database: "db"}
	cfg.AddOrUpdate(conn)
	if err := cfg.SetProfile(conn.GeneratedName(), "dev", AllowRead); err != nil {
		t.Fatal(err)
	}
	cfg.AddOrUpdate(Connection{Server: "b", User: "sa"})
	cfg.AddOrUpdate(conn)
	if p := profileOf(t, cfg, "a"); p.Profile != "dev" || p.MaxAllow != AllowRead {
		t.Errorf("after reconnect a = %q/%q, want dev/read", p.Profile, p.MaxAllow)
	}
	if last := cfg.Connections[len(cfg.Connections)-1]; last.Server != "a" {
		t.Errorf("reconnected entry is not the most recent: %v", serversOf(cfg.Connections))
	}
}

func TestNamedConnectionsAreNotTrimmed(t *testing.T) {
	cfg := &Config{}
	named := Connection{Server: "named", User: "sa"}
	cfg.AddOrUpdate(named)
	if err := cfg.SetProfile(named.GeneratedName(), "keep", AllowAdmin); err != nil {
		t.Fatal(err)
	}
	for i := range MaxSavedConnections + 5 {
		cfg.AddOrUpdate(Connection{Server: "srv", Port: i + 1, User: "sa"})
	}
	if len(cfg.Connections) != MaxSavedConnections+1 {
		t.Fatalf("len = %d, want %d unnamed + 1 named", len(cfg.Connections), MaxSavedConnections+1)
	}
	if p := profileOf(t, cfg, "named"); p.Profile != "keep" {
		t.Errorf("named entry lost its name: %+v", p)
	}
	// The oldest unnamed ones went: ports 1..5.
	for _, c := range cfg.Connections {
		if c.Server == "srv" && c.Port <= 5 {
			t.Errorf("port %d survived the trim", c.Port)
		}
	}
}

// The done-when of phase 1: a sealed password still opens after the
// connection is named, and the name survives a save and reload.
func TestProfileRoundTripsAndKeepsThePassword(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := Load()
	conn := Connection{Server: "a", User: "sa", Password: "secret", RememberPassword: true}
	cfg.AddOrUpdate(conn)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	cfg = Load()
	if err := cfg.SetProfile(conn.GeneratedName(), "dev", AllowDDL); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	for _, load := range []func() *Config{Load, func() *Config {
		c, err := LoadReadOnly()
		if err != nil {
			t.Fatal(err)
		}
		return c
	}} {
		got, err := load().ProfileByName("DEV")
		if err != nil {
			t.Fatal(err)
		}
		if got.Password != "secret" || got.MaxAllow != AllowDDL || got.PasswordUnreadable() {
			t.Errorf("reloaded profile = password %q, max %q, unreadable %v", got.Password, got.MaxAllow, got.PasswordUnreadable())
		}
	}
}

// Another instance reconnecting the same connection (its op holds no profile)
// must not wipe a name this one saved, and vice versa.
func TestAProfileSurvivesAnotherInstancesReconnect(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	conn := Connection{Server: "a", User: "sa"}
	seed := Load()
	seed.AddOrUpdate(conn)
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	a, b := Load(), Load()
	if err := a.SetProfile(conn.GeneratedName(), "dev", AllowRead); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b.AddOrUpdate(conn) // b loaded before a named it
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if p := profileOf(t, Load(), "a"); p.Profile != "dev" || p.MaxAllow != AllowRead {
		t.Errorf("after b's reconnect a = %q/%q, want dev/read", p.Profile, p.MaxAllow)
	}
	if p := profileOf(t, b, "a"); p.Profile != "dev" {
		t.Errorf("b's merged list lost the name: %q", p.Profile)
	}
}

// Two instances giving one name to different entries: the later save keeps
// it, so names stay unique on disk.
func TestALaterProfileNameMovesOffAnotherInstancesEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ca, cb := Connection{Server: "a", User: "sa"}, Connection{Server: "b", User: "sa"}
	seed := Load()
	seed.AddOrUpdate(ca)
	seed.AddOrUpdate(cb)
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}
	a, b := Load(), Load()
	if err := a.SetProfile(ca.GeneratedName(), "dev", AllowRead); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.SetProfile(cb.GeneratedName(), "dev", AllowWrite); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if pa, pb := profileOf(t, got, "a"), profileOf(t, got, "b"); pa.Profile != "" || pb.Profile != "dev" {
		t.Errorf("a=%q b=%q, want the name on b only", pa.Profile, pb.Profile)
	}
}

func TestProfileByName(t *testing.T) {
	cfg := &Config{}
	conn := Connection{Server: "a", Port: 1500, Database: "db", User: "sa"}
	cfg.AddOrUpdate(conn)
	cfg.AddOrUpdate(Connection{Server: "b", User: "sa"})
	if err := cfg.SetProfile(conn.GeneratedName(), "Dev", AllowRead); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Dev", "dev", "a,1500,db,sa", "b,1433,,sa"} {
		if _, err := cfg.ProfileByName(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := cfg.ProfileByName("prod"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("prod: %v, want ErrProfileNotFound", err)
	}
	if ps := cfg.Profiles(); len(ps) != 1 || ps[0].Server != "a" {
		t.Errorf("Profiles() = %v", serversOf(ps))
	}
}

func TestCeiling(t *testing.T) {
	for allow, want := range map[AllowClass]AllowClass{
		"": AllowAdmin, AllowRead: AllowRead, AllowWrite: AllowWrite, AllowDDL: AllowDDL, AllowAdmin: AllowAdmin,
		// Unknown fails closed.
		"Admin": AllowRead, "root": AllowRead,
	} {
		if got := (Connection{MaxAllow: allow}).Ceiling(); got != want {
			t.Errorf("Ceiling(%q) = %q, want %q", allow, got, want)
		}
	}
}

// LoadReadOnly writes nothing: no key, no sweep, no corrupt copy, no save.
func TestLoadReadOnlyWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "gossms")

	// Missing config: empty, no directory created.
	cfg, err := LoadReadOnly()
	if err != nil || len(cfg.Connections) != 0 {
		t.Fatalf("missing config: %v, %d connections", err, len(cfg.Connections))
	}
	if _, err := os.Stat(cfgDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadReadOnly created %s", cfgDir)
	}
	if err := cfg.Save(); err == nil {
		t.Error("Save on a read-only config succeeded")
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "config.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("Save wrote config.json")
	}

	// A saved password, then the key removed: sealed, and no new key made.
	rw := Load()
	rw.AddOrUpdate(Connection{Server: "a", User: "sa", Password: "pw", RememberPassword: true})
	if err := rw.Save(); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(cfgDir, keyFileName)
	key, _ := os.ReadFile(keyPath)
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(cfgDir, "config.json.tmp1234567")
	if err := os.WriteFile(stale, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(stale, old, old)

	cfg, err = LoadReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.Connections[0]; c.Password != "" || !c.PasswordUnreadable() {
		t.Errorf("without a key: password %q, unreadable %v", c.Password, c.PasswordUnreadable())
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, fs.ErrNotExist) {
		t.Error("LoadReadOnly created a key file")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Error("LoadReadOnly swept a temp file")
	}

	// With the key back the password opens.
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = LoadReadOnly(); cfg.Connections[0].Password != "pw" {
		t.Errorf("with the key: password %q, want pw", cfg.Connections[0].Password)
	}

	// Corrupt: an error, and no .corrupt copy.
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReadOnly(); err == nil {
		t.Error("a corrupt config loaded without an error")
	}
	entries, _ := os.ReadDir(cfgDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "corrupt") {
			t.Errorf("LoadReadOnly kept a copy: %s", e.Name())
		}
	}
}
