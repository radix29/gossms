package tui

import (
	"testing"
	"time"

	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/tuikit/charts"
)

// amBenchWidth keeps the History canvas at its fixed width, as on a normal
// terminal.
const amBenchWidth = 120

// A cache hit must equal a fresh render; any difference is an unnoticeable
// stale dashboard.
func TestActivityMonitorCachedCanvasMatchesAFreshRender(t *testing.T) {
	am := newTestActivityMonitor(amBenchWidth, 40)
	amWithSamples(am, 900)

	cw, ch := am.canvasSize()
	cached := am.dashboardCanvas(cw, ch)
	if am.dashboardCanvas(cw, ch) != cached {
		t.Fatal("a second draw with nothing changed re-rendered the canvas")
	}
	want := cached.Rows()

	am.canvas = nil
	got := am.dashboardCanvas(cw, ch).Rows()
	if len(got) != len(want) {
		t.Fatalf("fresh render has %d rows, cached one has %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d differs\ncached: %q\nfresh:  %q", i, want[i], got[i])
		}
	}
}

// The cache must not outlive its data.
func TestActivityMonitorCanvasCacheInvalidation(t *testing.T) {
	am := newTestActivityMonitor(amBenchWidth, 40)
	amWithSamples(am, 30)
	cw, ch := am.canvasSize()
	before := am.dashboardCanvas(cw, ch).Rows()

	last, _ := am.store.Latest()
	am.store.Append(activity.Sample{At: last.At.Add(2 * time.Second), BatchesSec: 4000})
	am.rebuild()
	after := am.dashboardCanvas(cw, ch).Rows()
	if rowsEqual(before, after) {
		t.Error("a new sample left the cached canvas in place")
	}

	// The header shows collector state, so pausing redraws without a new
	// sample.
	paused := am.dashboardCanvas(cw, ch)
	am.setPaused(true)
	if am.dashboardCanvas(cw, ch) == paused {
		t.Error("pausing left the cached canvas in place")
	}

	// A tab change draws a different dashboard at a different size.
	sample := am.dashboardCanvas(cw, ch)
	am.setTab(amTabSample)
	cw, ch = am.canvasSize()
	if am.dashboardCanvas(cw, ch) == sample {
		t.Error("switching tabs left the cached canvas in place")
	}
}

func rowsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// BenchmarkDashboardCanvas is one full History render — the uncached per-event
// cost.
func BenchmarkDashboardCanvas(b *testing.B) {
	am := newTestActivityMonitor(amBenchWidth, 40)
	amWithSamples(am, 900)
	cw, ch := am.canvasSize()

	for b.Loop() {
		am.canvas = nil
		am.dashboardCanvas(cw, ch)
	}
}

// BenchmarkActivityMonitorDraw is a frame from cache: blit, scrollbars, chrome
// — the per-keystroke cost.
func BenchmarkActivityMonitorDraw(b *testing.B) {
	am := newTestActivityMonitor(amBenchWidth, 40)
	amWithSamples(am, 900)
	s := charts.NewCanvas(amBenchWidth, 40)

	for b.Loop() {
		am.Draw(s)
	}
}
