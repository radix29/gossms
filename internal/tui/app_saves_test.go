package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
)

// waitForSaves plays the event loop until every background config and
// tracked-query save has finished and reported.
func waitForSaves(t *testing.T, a *App) {
	t.Helper()
	savesInFlight.Wait()
	drainUntil(t, a, func() bool { return a.saves.job == nil && len(a.saves.waiting) == 0 }, "background saves")
}

// holdConfigLock creates config.json's lock file as another gossms instance
// mid-save would, and returns its release. A save waits on it for up to two
// seconds.
func holdConfigLock(t *testing.T) (release func()) {
	t.Helper()
	scratchConfigHome(t)
	dir := os.Getenv("XDG_CONFIG_HOME")
	if err := os.MkdirAll(filepath.Join(dir, "gossms"), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, "gossms", "config.json.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Remove(lock); err != nil {
			t.Fatal(err)
		}
	}
}

// T58: a config save must not run on the UI goroutine. Another instance
// holding the file lock made Options' OK, a connect and every XEvent column
// toggle freeze the screen for up to the two-second lock wait.
func TestConfigSaveDoesNotBlockTheCaller(t *testing.T) {
	release := holdConfigLock(t)
	a := newTestApp()
	a.cfg.IndentWidth = 2

	var got []error
	start := time.Now()
	a.saveConfig(func(err error) { got = append(got, err) })
	if waited := time.Since(start); waited > 500*time.Millisecond {
		t.Fatalf("saveConfig blocked its caller for %v", waited)
	}
	release()
	waitForSaves(t, a)
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("done called with %v, want one nil", got)
	}
	if n := config.Load().IndentWidth; n != 2 {
		t.Errorf("IndentWidth on disk = %d, want 2", n)
	}
}

// Saves asked for while one is out run as one more save, after it, with every
// change made meanwhile, and each caller still hears back. Overlapping jobs
// would make EndSave drop the wrong pending ops.
func TestConfigSavesAskedForMeanwhileRunAfterIt(t *testing.T) {
	release := holdConfigLock(t)
	a := newTestApp()

	calls := 0
	done := func(err error) {
		if err != nil {
			t.Errorf("save: %v", err)
		}
		calls++
	}
	a.cfg.AddOrUpdate(config.Connection{Server: "first"})
	a.saveConfig(done)
	first := a.saves.job
	a.cfg.AddOrUpdate(config.Connection{Server: "second"})
	a.saveConfig(done)
	a.cfg.IndentWidth = 3
	a.saveConfig(done)
	if a.saves.job != first || len(a.saves.waiting) != 2 {
		t.Fatalf("a second job started while the first was out (waiting %d)", len(a.saves.waiting))
	}
	release()
	waitForSaves(t, a)
	if calls != 3 {
		t.Errorf("%d callbacks ran, want 3", calls)
	}
	disk := config.Load()
	if len(disk.Connections) != 2 || disk.IndentWidth != 3 {
		t.Errorf("on disk: %d connections, indent %d — want 2 and 3", len(disk.Connections), disk.IndentWidth)
	}
	if len(a.cfg.Connections) != 2 {
		t.Errorf("in memory: %d connections, want 2", len(a.cfg.Connections))
	}
}

// Quitting right after a change must not lose it: Run returns with a save in
// flight and another asked for, and FlushSaves writes both without the event
// loop.
func TestFlushSavesWritesWhatTheLoopLeftOwing(t *testing.T) {
	release := holdConfigLock(t)
	a := newTestApp()
	a.cfg.AddOrUpdate(config.Connection{Server: "first"})
	a.saveConfig(nil)
	a.cfg.IndentWidth = 5
	a.saveConfig(nil)
	release()

	a.FlushSaves(5 * time.Second)
	disk := config.Load()
	if len(disk.Connections) != 1 || disk.IndentWidth != 5 {
		t.Errorf("on disk after the flush: %d connections, indent %d — want 1 and 5", len(disk.Connections), disk.IndentWidth)
	}
	if a.saves.job != nil || len(a.saves.waiting) != 0 {
		t.Error("FlushSaves left a save owing")
	}
}

// Tracking a query must not wait for tracked_queries.json either, and the
// set's readers — the action row's label, drawn every frame — must not wait
// on the save.
func TestTrackedSaveDoesNotBlockTheUI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracked_queries.json")
	waitForSavesAtCleanup(t)
	tq := config.LoadTrackedQueriesFrom(path)
	config.UseTrackedQueries(tq)
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestApp()
	if _, err := tq.Toggle("srv", "db", 7); err != nil {
		t.Fatal(err)
	}
	var saveErr error
	reported := false
	start := time.Now()
	a.saveTracked(func(err error) { saveErr, reported = err, true })
	time.Sleep(20 * time.Millisecond)
	tracked := tq.IsTracked("srv", "db", 7)
	if waited := time.Since(start); waited > 500*time.Millisecond {
		t.Fatalf("saveTracked and a read blocked for %v", waited)
	}
	if !tracked {
		t.Error("the toggle is not visible while it saves")
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	waitForSaves(t, a)
	drainUntil(t, a, func() bool { return reported }, "the tracked save's report")
	if saveErr != nil {
		t.Fatalf("save: %v", saveErr)
	}
	if !config.LoadTrackedQueriesFrom(path).IsTracked("srv", "db", 7) {
		t.Error("the pin did not reach the file")
	}
}
