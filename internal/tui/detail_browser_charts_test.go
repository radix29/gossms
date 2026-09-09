package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// stripCharts is a two-panel strip like the disk-usage one, without needing
// a server to produce it.
func stripCharts() []detailChart {
	return diskUsageCharts(gosmo.DiskUsage{
		DataFilesMB: 72, LogFilesMB: 72,
		DataMB: 13, IndexMB: 3.5, UnusedMB: 2.2, UnallocatedMB: 53.25,
		LogUsedMB: 64, LogUnusedMB: 8,
	})
}

// The strip and the grid are one split: whatever rows the charts take, the
// grid must give up, and neither may reach into the other. A grid drawn over
// the strip is invisible in a unit test and obvious on screen.
func TestDetailChartStripAndGridDoNotOverlap(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.setCharts(stripCharts())

	strip := db.chartsRect()
	if strip.IsZero() {
		t.Fatal("no chart strip in a 100x30 panel, want one")
	}
	if strip.Bottom() != 30 {
		t.Errorf("strip bottom = %d, want the panel's own bottom (30)", strip.Bottom())
	}
	if strip.H != detailChartsH {
		t.Errorf("strip height = %d, want %d", strip.H, detailChartsH)
	}
	grid := db.grid.Bounds()
	if grid.Bottom() != strip.Y {
		t.Errorf("grid bottom = %d, strip top = %d — want them to meet exactly", grid.Bottom(), strip.Y)
	}
	if grid.Y != 1 {
		t.Errorf("grid top = %d, want row 1, under the title bar", grid.Y)
	}
}

// Without charts the grid keeps the whole body, and dropping the charts must
// give the rows back — a node selected after a database is the common path.
func TestDetailChartStripReleasesItsRows(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	full := db.grid.Bounds()

	db.setCharts(stripCharts())
	if db.grid.Bounds().H >= full.H {
		t.Fatalf("grid height with charts = %d, want less than %d", db.grid.Bounds().H, full.H)
	}

	db.setCharts(nil)
	if got := db.grid.Bounds(); got != full {
		t.Errorf("grid bounds after dropping the charts = %+v, want %+v", got, full)
	}
	if !db.chartsRect().IsZero() {
		t.Error("chartsRect is non-zero with no charts")
	}
}

// A panel too short for both, or too narrow for one readable bar, drops the
// strip rather than crowding the properties out.
func TestDetailChartStripDroppedWhenItWouldCrowdTheGrid(t *testing.T) {
	cases := []struct {
		name string
		w, h int
	}{
		{"too short", 100, detailChartsH + detailChartMinGridH}, // one row short of the body it needs
		{"too narrow", detailChartMinPanelW - 1, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := NewDetailBrowser("test")
			db.SetBounds(0, 0, c.w, c.h)
			db.setCharts(stripCharts())
			if !db.chartsRect().IsZero() {
				t.Errorf("chartsRect = %+v in a %dx%d panel, want none", db.chartsRect(), c.w, c.h)
			}
			if got := db.grid.Bounds().H; got != c.h-1 {
				t.Errorf("grid height = %d, want the whole body (%d)", got, c.h-1)
			}
		})
	}
}

// Two panels share the width when both fit and the strip drops the second
// rather than halving itself into two unreadable slivers.
func TestDetailChartPanelsDropWhatCannotFit(t *testing.T) {
	body := core.Rect{X: 0, Y: 1, W: 2*detailChartMinPanelW + detailChartGutter, H: 4}
	if got := detailChartPanels(body, 2); len(got) != 2 {
		t.Errorf("panels at exactly two minimum widths = %d, want 2", len(got))
	}
	body.W--
	got := detailChartPanels(body, 2)
	if len(got) != 1 {
		t.Fatalf("panels one column short = %d, want 1", len(got))
	}
	if got[0].W < detailChartMinPanelW {
		t.Errorf("the surviving panel is %d wide, want at least %d", got[0].W, detailChartMinPanelW)
	}
}

// The data-file bar's segments are the four SSMS reports, in its order, and
// the log bar's two — pinned by name so a reordered or relabelled segment
// can't pass as the one it replaced.
func TestDiskUsageChartSegments(t *testing.T) {
	cs := stripCharts()
	if len(cs) != 2 {
		t.Fatalf("charts = %d, want 2", len(cs))
	}
	want := [][]string{
		{"Data", "Index", "Unused", "Unallocated"},
		{"Used", "Unused"},
	}
	for i, w := range want {
		if len(cs[i].Series) != len(w) {
			t.Errorf("chart %q has %d segments, want %d", cs[i].Title, len(cs[i].Series), len(w))
			continue
		}
		for j, label := range w {
			if cs[i].Series[j].Label != label {
				t.Errorf("chart %q segment %d = %q, want %q", cs[i].Title, j, cs[i].Series[j].Label, label)
			}
		}
	}
	if got := cs[0].Series[3].Values[0]; got != 53.25 {
		t.Errorf("unallocated value = %v, want the DiskUsage figure (53.25)", got)
	}
	if got := cs[1].Series[0].Values[0]; got != 64 {
		t.Errorf("log used value = %v, want 64", got)
	}
}

// clickAt sends one whole press-release gesture at (x, y), the way a real
// click arrives — a press alone leaves the mouseDragging latch armed and the
// next press does nothing.
func clickAt(db *DetailBrowser, x, y int) bool {
	handled := db.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	return handled
}

// chartPoint is a point inside the first chart panel of the strip.
func chartPoint(db *DetailBrowser) (int, int) {
	r := db.chartsRect()
	return r.X + 2, r.Y + detailChartSectionH + 1
}

// A click on a bar pins the readout the legend has no room for: every
// segment named, valued and shared.
func TestDetailChartClickPinsTheSegmentValues(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.setCharts(stripCharts())

	x, y := chartPoint(db)
	if !clickAt(db, x, y) {
		t.Fatal("a click on the chart strip was not handled")
	}
	tip := db.tooltip
	if tip == nil {
		t.Fatal("no tooltip pinned by a click on the data-files bar")
	}
	if tip.title != "DATA FILES SPACE USAGE" {
		t.Errorf("tooltip title = %q, want the panel clicked", tip.title)
	}
	if len(tip.rows) != 4 {
		t.Fatalf("tooltip rows = %d, want one per segment (4)", len(tip.rows))
	}
	// Unallocated is 53.25 of 71.95 MB — the megabytes the bar cannot show
	// and the share it can, so the box and the bar can be read against
	// each other.
	last := tip.rows[3]
	if last.label != "Unallocated" {
		t.Errorf("last row = %q, want Unallocated", last.label)
	}
	if want := "53 MB  74.0%"; last.value != want {
		t.Errorf("Unallocated value = %q, want %q", last.value, want)
	}
	if last.color != db.charts[0].Series[3].Color {
		t.Error("row colour differs from the segment's, so box and bar can't be matched by eye")
	}
}

// The second panel answers for itself — a strip that pinned the first
// chart's numbers wherever it was clicked would look right until read.
func TestDetailChartClickPinsThePanelClicked(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.setCharts(stripCharts())

	r := db.chartsRect()
	clickAt(db, r.Right()-3, r.Y+detailChartSectionH+1)
	if db.tooltip == nil {
		t.Fatal("no tooltip pinned by a click on the log bar")
	}
	if db.tooltip.title != "TRANSACTION LOG SPACE USAGE" {
		t.Errorf("tooltip title = %q, want the log panel", db.tooltip.title)
	}
}

// One click never both closes a box and opens another, and Escape closes
// one without reaching the grid.
func TestDetailChartTooltipDismissal(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.setCharts(stripCharts())
	x, y := chartPoint(db)

	clickAt(db, x, y)
	clickAt(db, x, y)
	if db.tooltip != nil {
		t.Error("a second click on the strip left a box showing, want it dismissed")
	}

	clickAt(db, x, y)
	if !clickAt(db, db.rect.X+2, db.rect.Y+3) {
		t.Error("a click on the grid with a box showing was not claimed to dismiss it")
	}
	if db.tooltip != nil {
		t.Error("a click away from the strip left the box showing")
	}

	clickAt(db, x, y)
	if !db.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone)) {
		t.Error("Escape with a box showing was not claimed")
	}
	if db.tooltip != nil {
		t.Error("Escape left the box showing")
	}
	// With no box, Escape is not the panel's to take.
	if db.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone)) {
		t.Error("Escape with no box was claimed, want it left to the panel's container")
	}
}

// A pin reports numbers that are on screen: anything that replaces them, or
// moves them, drops it.
func TestDetailChartTooltipDroppedWhenItsChartsGo(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.setCharts(stripCharts())
	x, y := chartPoint(db)

	clickAt(db, x, y)
	db.setCharts(nil)
	if db.tooltip != nil {
		t.Error("the box outlived the charts it reports")
	}

	db.setCharts(stripCharts())
	clickAt(db, x, y)
	db.SetBounds(0, 0, 80, 30)
	if db.tooltip != nil {
		t.Error("the box outlived a resize that moved the strip under it")
	}
}

// A segment the bar never drew must not appear in the box naming a colour
// that is nowhere in the picture — an empty log, say, whose used half is 0.
func TestDetailChartTooltipSkipsEmptySegments(t *testing.T) {
	c := detailChart{Title: "LOG", Format: formatMB, Series: []charts.Series{
		{Label: "Used", Values: []float64{0}},
		{Label: "Unused", Values: []float64{8}},
	}}
	rows := c.tooltipRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want only the segment with a value", len(rows))
	}
	if rows[0].label != "Unused" || rows[0].value != "8 MB  100.0%" {
		t.Errorf("row = %q %q, want Unused at the whole bar", rows[0].label, rows[0].value)
	}
}
