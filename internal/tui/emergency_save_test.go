package tui

import (
	"os"
	"path/filepath"
	"syscall"
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

// U6: SIGHUP/SIGTERM runs the save on the UI goroutine, which then quits.
// newTestApp has no screen, so the "event loop" is this test draining pending.
func TestSaveOnSignalSavesOnTheUIGoroutineAndQuits(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")
	dirtyPanel(a, "Query 2", "SELECT 2")
	dir := t.TempDir()
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	saves := 0
	save := func() ([]string, []error) { saves++; return a.emergencySaveTo(dir, now) }

	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		for !a.hasPending() {
			time.Sleep(time.Millisecond)
		}
		a.drainPending()
	}()
	paths, errs := a.saveOnSignal(syscall.SIGHUP, 10*time.Second, save)
	<-loopDone // the callback quits after it signals the save is done

	assertRecovered(t, dir, paths, errs)
	if saves != 1 {
		t.Errorf("saved %d times, want 1", saves)
	}
	if !a.quitting {
		t.Error("the UI callback did not quit, so Run would not return")
	}
}

// A wedged event loop must not cost the text: after the wait the signal
// goroutine saves itself, and a loop that wakes up later only quits.
func TestSaveOnSignalSavesWhenTheEventLoopIsStuck(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")
	dirtyPanel(a, "Query 2", "SELECT 2")
	dir := t.TempDir()
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	saves := 0
	save := func() ([]string, []error) { saves++; return a.emergencySaveTo(dir, now) }

	paths, errs := a.saveOnSignal(syscall.SIGTERM, 20*time.Millisecond, save)
	assertRecovered(t, dir, paths, errs)

	a.drainPending() // the loop comes back
	if saves != 1 {
		t.Errorf("saved %d times, want 1 — a late loop must not save again", saves)
	}
	if !a.quitting {
		t.Error("the late UI callback did not quit")
	}
}

func assertRecovered(t *testing.T, dir string, paths []string, errs []error) {
	t.Helper()
	if len(errs) != 0 {
		t.Fatalf("save errors: %v", errs)
	}
	want := map[string]string{
		filepath.Join(dir, "20260924-100000-Query_1.sql"): "SELECT 1",
		filepath.Join(dir, "20260924-100000-Query_2.sql"): "SELECT 2",
	}
	if len(paths) != len(want) {
		t.Fatalf("saved %v, want both dirty panels", paths)
	}
	for path, text := range want {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != text {
			t.Errorf("%s = %q, want %q", path, got, text)
		}
	}
}

func (a *App) hasPending() bool {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	return len(a.pending) > 0
}
