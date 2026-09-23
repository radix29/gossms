package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// S12: a panic on the UI goroutine ends the process, and cmd/gossms's run
// recovers it only to restore the terminal and exit. This drives the same shape
// — a posted callback panics while the loop drains it, a deferred recover calls
// the save — and asserts the unsaved text reached disk.
func TestUIPanicInAPostedCallbackRecoversUnsavedQueries(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 'unsaved work'")
	clean := NewQueryPanel(a, "Query 2")
	a.panels.AddPanel(clean)
	dir := t.TempDir()
	now := time.Date(2026, 9, 23, 14, 5, 6, 0, time.UTC)

	var paths []string
	var errs []error
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("the posted callback did not panic")
			}
			paths, errs = a.emergencySaveTo(dir, now)
		}()
		a.postEvent(func() { panic("ui boom") })
		a.drainPending()
	}()

	if len(errs) != 0 {
		t.Fatalf("emergency save errors: %v", errs)
	}
	if len(paths) != 1 {
		t.Fatalf("saved %d files, want 1 — only the dirty panel: %v", len(paths), paths)
	}
	want := filepath.Join(dir, "20260923-140506-Query_1.sql")
	if paths[0] != want {
		t.Errorf("saved to %s, want %s", paths[0], want)
	}
	got, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "SELECT 'unsaved work'" {
		t.Errorf("recovered text = %q", got)
	}
}

// Two panels with one title, or a second crash in the same second, must not
// overwrite each other's recovered text.
func TestEmergencySaveNeverOverwrites(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "report.sql", "first")
	dirtyPanel(a, "report.sql", "second")
	dir := t.TempDir()
	now := time.Now()

	paths, errs := a.emergencySaveTo(dir, now)
	if len(errs) != 0 || len(paths) != 2 {
		t.Fatalf("paths %v, errs %v; want two files, no errors", paths, errs)
	}
	again, errs := a.emergencySaveTo(dir, now)
	if len(errs) != 0 || len(again) != 2 {
		t.Fatalf("second save: paths %v, errs %v", again, errs)
	}
	seen := map[string]bool{}
	for _, p := range append(paths, again...) {
		if seen[p] {
			t.Errorf("%s written twice", p)
		}
		seen[p] = true
	}
	if got, _ := os.ReadFile(paths[0]); string(got) != "first" {
		t.Errorf("first file = %q after the second save, want it untouched", got)
	}
}

// run calls it whatever state the panic left, including before buildUI.
func TestEmergencySaveOnAnUnbuiltApp(t *testing.T) {
	var nilApp *App
	if paths, errs := nilApp.EmergencySave(); paths != nil || errs != nil {
		t.Errorf("nil App: paths %v, errs %v", paths, errs)
	}
	if paths, errs := new(App).EmergencySave(); paths != nil || errs != nil {
		t.Errorf("App with no panels: paths %v, errs %v", paths, errs)
	}
}

func TestRecoveredFileStem(t *testing.T) {
	for in, want := range map[string]string{
		"Query 1":     "Query_1",
		"orders.sql":  "orders",
		`a/b\c:d*e`:   "a_b_c_d_e",
		"..":          "query",
		"":            "query",
		"Données.sql": "Données",
		"x.sql.sql":   "x.sql",
	} {
		if got := recoveredFileStem(in); got != want {
			t.Errorf("recoveredFileStem(%q) = %q, want %q", in, got, want)
		}
	}
}
