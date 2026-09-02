package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func trackedPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tracked_queries.json")
}

// TestTrackedQueriesRoundTripThroughTheFile: the whole point of the file is
// that the list is there at the next start, so the second read is a fresh one
// off disk rather than the same object.
func TestTrackedQueriesRoundTripThroughTheFile(t *testing.T) {
	path := trackedPath(t)
	tq := LoadTrackedQueriesFrom(path)
	for _, id := range []int64{9, 3} {
		if _, err := tq.Toggle("HOST\\SQL2022", "appdb", id); err != nil {
			t.Fatalf("Toggle: %v", err)
		}
	}
	// A second database on the same server is its own set.
	if _, err := tq.Toggle("HOST\\SQL2022", "otherdb", 100); err != nil {
		t.Fatalf("Toggle: %v", err)
	}

	back := LoadTrackedQueriesFrom(path)
	if got := back.IDs("HOST\\SQL2022", "appdb"); len(got) != 2 || got[0] != 3 || got[1] != 9 {
		t.Errorf("IDs after reload = %v, want [3 9] — sorted, both kept", got)
	}
	if got := back.IDs("HOST\\SQL2022", "otherdb"); len(got) != 1 || got[0] != 100 {
		t.Errorf("the second database's set = %v, want [100]", got)
	}
	if got := back.IDs("HOST\\SQL2022", "nosuchdb"); got != nil {
		t.Errorf("an untracked database answered %v, want nothing", got)
	}
	// The address is case-insensitive: the same instance reached by another
	// spelling must not come back with an empty list.
	if got := back.IDs("host\\sql2022", "appdb"); len(got) != 2 {
		t.Errorf("a differently-cased server answered %v, want the same two ids", got)
	}
}

// TestTrackedQueriesToggleRemovesAndForgetsTheDatabase. An emptied set is
// deleted rather than written as [], which would grow the file by one entry per
// database ever visited.
func TestTrackedQueriesToggleRemovesAndForgetsTheDatabase(t *testing.T) {
	path := trackedPath(t)
	tq := LoadTrackedQueriesFrom(path)
	if tracked, _ := tq.Toggle("srv", "appdb", 7); !tracked {
		t.Error("the first Toggle reported the query untracked")
	}
	if !tq.IsTracked("srv", "appdb", 7) {
		t.Error("IsTracked says no right after tracking")
	}
	if tracked, _ := tq.Toggle("srv", "appdb", 7); tracked {
		t.Error("the second Toggle reported the query tracked")
	}
	if tq.IsTracked("srv", "appdb", 7) {
		t.Error("IsTracked says yes after untracking")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(data), "appdb") {
		t.Errorf("the emptied database is still in the file:\n%s", data)
	}
	if got := LoadTrackedQueriesFrom(path).IDs("srv", "appdb"); got != nil {
		t.Errorf("after reload the emptied set is %v, want nothing", got)
	}
}

// TestTrackedQueriesKeepsAFileItCannotRead. Same rule Config follows: a file
// that exists and could not be read is not the same as no file, and saving over
// it would replace a list that is very likely still intact with an empty one.
// The file here is deliberately one a save *could* write — a read failure alone
// has to stop it.
func TestTrackedQueriesKeepsAFileItCannotRead(t *testing.T) {
	path := trackedPath(t)
	const held = `{"tracked":{"srv":{"appdb":[1,2,3]}}}`
	if err := os.WriteFile(path, []byte(held), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o200); err != nil { // write-only
		t.Fatalf("chmod: %v", err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this user can read a mode-0200 file (running as root?)")
	}

	tq := LoadTrackedQueriesFrom(path)
	if got := tq.IDs("srv", "appdb"); got != nil {
		t.Fatalf("an unreadable file loaded as %v", got)
	}
	if _, err := tq.Toggle("srv", "appdb", 7); err == nil {
		t.Error("Toggle saved over a file that could not be read")
	}
	// And the toggle still applied in memory, so the session is usable.
	if !tq.IsTracked("srv", "appdb", 7) {
		t.Error("the refused save also lost the toggle")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != held {
		t.Errorf("the file was rewritten as %q, want it untouched", data)
	}
}

// TestTrackedQueriesKeepsACorruptFileAside, so a hand-edit that breaks the JSON
// does not silently vanish.
func TestTrackedQueriesKeepsACorruptFileAside(t *testing.T) {
	path := trackedPath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tq := LoadTrackedQueriesFrom(path)
	if got := tq.IDs("srv", "appdb"); got != nil {
		t.Errorf("a corrupt file loaded as %v", got)
	}
	if data, err := os.ReadFile(path + ".corrupt"); err != nil || string(data) != "{not json" {
		t.Errorf("the corrupt file was not kept aside: %v / %q", err, data)
	}
	// It is not write-protected: the bytes are safe, so the next toggle saves.
	if _, err := tq.Toggle("srv", "appdb", 7); err != nil {
		t.Errorf("Toggle after a corrupt load: %v", err)
	}
}

// The two halves of a set's key are treated differently on purpose: the server
// address is folded, the database name is compared exactly. Both directions
// matter, so both are asserted here — folding the database would merge Sales
// and sales, which are two databases on a case-sensitive server collation, and
// not folding the server would split a set the moment the user typed the
// instance in a different case in Connect.
func TestServerIsFoldedAndDatabaseIsNot(t *testing.T) {
	tq := LoadTrackedQueriesFrom(filepath.Join(t.TempDir(), trackedFileName))
	if _, err := tq.Toggle(`HOST\SQL2022`, "Sales", 7); err != nil {
		t.Fatalf("Toggle: %v", err)
	}

	if got := tq.IDs(`host\sql2022`, "Sales"); len(got) != 1 || got[0] != 7 {
		t.Errorf("IDs(host\\sql2022, Sales) = %v, want [7] — the server address is meant to fold", got)
	}
	if got := tq.IDs(`HOST\SQL2022`, "sales"); len(got) != 0 {
		t.Errorf("IDs(HOST\\SQL2022, sales) = %v, want none — a case-sensitive server has both Sales and sales", got)
	}
}

// A nil set answers the readers rather than panicking, and must refuse the
// writers rather than reporting a pin it never recorded.
func TestNilTrackedQueriesRefusesToWrite(t *testing.T) {
	var tq *TrackedQueries
	if tq.IsTracked("s", "d", 1) || tq.IDs("s", "d") != nil {
		t.Error("a nil set should read as empty")
	}
	if tracked, err := tq.Toggle("s", "d", 1); err == nil || tracked {
		t.Errorf("Toggle on a nil set = (%v, %v), want (false, an error)", tracked, err)
	}
	if err := tq.Save(); err == nil {
		t.Error("Save on a nil set should report that there is nothing to save")
	}
}
