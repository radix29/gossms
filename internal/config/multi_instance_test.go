package config

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// S13: two gossms instances each load config.json at start and save whenever
// they connect. These pin that a save replays only its own process's changes
// onto the file as it is now, instead of writing back the list it loaded.

func serversOf(conns []Connection) []string {
	var out []string
	for _, c := range conns {
		out = append(out, c.Server)
	}
	slices.Sort(out)
	return out
}

func TestSaveKeepsAConnectionAnotherInstanceSaved(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, b := Load(), Load()

	a.AddOrUpdate(Connection{Server: "from-a", User: "sa", Password: "pa", RememberPassword: true})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b.AddOrUpdate(Connection{Server: "from-b", User: "sa", Password: "pb", RememberPassword: true})
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	got := Load()
	if s := serversOf(got.Connections); !slices.Equal(s, []string{"from-a", "from-b"}) {
		t.Fatalf("saved connections = %v, want both instances'", s)
	}
	for _, c := range got.Connections {
		if want := map[string]string{"from-a": "pa", "from-b": "pb"}[c.Server]; c.Password != want {
			t.Errorf("%s password = %q, want %q", c.Server, c.Password, want)
		}
	}
	// b's own list picks the other instance's entry up, password opened.
	if s := serversOf(b.Connections); !slices.Equal(s, []string{"from-a", "from-b"}) {
		t.Errorf("b's in-memory list after Save = %v, want both", s)
	}
	for _, c := range b.Connections {
		if c.Server == "from-a" && c.Password != "pa" {
			t.Errorf("from-a password in b = %q, want pa", c.Password)
		}
	}
}

func TestSaveDoesNotResurrectAConnectionAnotherInstanceRemoved(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seed := Load()
	seed.AddOrUpdate(Connection{Server: "doomed", User: "sa"})
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	a, b := Load(), Load()
	if !a.RemoveConnection(a.Connections[0].Name) {
		t.Fatal("setup: nothing removed")
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b.AddOrUpdate(Connection{Server: "kept", User: "sa"})
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	if s := serversOf(Load().Connections); !slices.Equal(s, []string{"kept"}) {
		t.Errorf("saved connections = %v, want [kept] — b wrote back the entry a removed", s)
	}
}

func TestSaveKeepsASettingAnotherInstanceChanged(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, b := Load(), Load()

	a.IconStyle = IconStylePortable
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b.IndentWidth = 2
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	got := Load()
	if got.IconStyle != IconStylePortable || got.IndentWidth != 2 {
		t.Errorf("IconStyle %v, IndentWidth %d; want both instances' changes", got.IconStyle, got.IndentWidth)
	}
	// A later save by a, who never touched IndentWidth, keeps b's value.
	a.AddOrUpdate(Connection{Server: "x"})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got.IndentWidth != 2 {
		t.Errorf("IndentWidth = %d after a's next save, want b's 2", got.IndentWidth)
	}
}

// A failed save keeps the pending operations for the next one.
func TestSaveFailureKeepsPendingChanges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	a := Load()
	a.AddOrUpdate(Connection{Server: "pending"})
	// A directory where config.json should be makes the write fail.
	if err := os.MkdirAll(filepath.Join(dir, "gossms", "config.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err == nil {
		t.Fatal("Save over a directory succeeded")
	}
	if err := os.Remove(filepath.Join(dir, "gossms", "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if s := serversOf(Load().Connections); !slices.Equal(s, []string{"pending"}) {
		t.Errorf("saved connections = %v, want [pending]", s)
	}
}

// First run with two instances: both find no key and generate one. Exactly one
// key may end up in use, or the loser's passwords are sealed with a key that
// is no longer on disk.
func TestConcurrentFirstRunsAgreeOnOneKey(t *testing.T) {
	for range 20 {
		dir := t.TempDir()
		const n = 8
		keys := make([][]byte, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() { keys[i], errs[i] = loadOrCreateKey(dir) })
		}
		wg.Wait()
		onDisk, err := os.ReadFile(filepath.Join(dir, keyFileName))
		if err != nil {
			t.Fatal(err)
		}
		for i := range n {
			if errs[i] != nil {
				t.Fatalf("loadOrCreateKey #%d: %v", i, errs[i])
			}
			if !bytes.Equal(keys[i], onDisk) {
				t.Fatalf("caller #%d got a key that is not the one on disk", i)
			}
		}
	}
}

func TestTrackedSaveKeepsAnotherInstancesPins(t *testing.T) {
	path := trackedPath(t)
	a, b := LoadTrackedQueriesFrom(path), LoadTrackedQueriesFrom(path)

	if _, err := a.Toggle("srv", "db", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Toggle("srv", "db", 2); err != nil {
		t.Fatal(err)
	}
	if got := LoadTrackedQueriesFrom(path).IDs("srv", "db"); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("tracked on disk = %v, want [1 2]", got)
	}
	if got := b.IDs("srv", "db"); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("b's set after its save = %v, want [1 2] — a's pin adopted", got)
	}

	// a unpins its own 1; b's next save must not bring it back.
	if tracked, err := a.Toggle("srv", "db", 1); err != nil || tracked {
		t.Fatalf("a's unpin: tracked %v, err %v", tracked, err)
	}
	if _, err := b.Toggle("srv", "db", 3); err != nil {
		t.Fatal(err)
	}
	if got := LoadTrackedQueriesFrom(path).IDs("srv", "db"); !slices.Equal(got, []int64{2, 3}) {
		t.Errorf("tracked on disk = %v, want [2 3]", got)
	}
}
