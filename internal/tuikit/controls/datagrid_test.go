package controls

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// countingRowSource wraps a RowSource and counts calls to Row, so tests can
// pin how many rows computeColWidths actually inspects.
type countingRowSource struct {
	RowSource
	calls int
}

func (c *countingRowSource) Row(i int) []string {
	c.calls++
	return c.RowSource.Row(i)
}

func newTestDataGrid() *DataGrid {
	g := NewDataGrid()
	g.SetBounds(0, 0, 40, 10)
	return g
}

// TestDataGridEmptyBeforeSetData confirms a freshly constructed grid (rows
// still the zero-value SliceRowSource(nil) set in NewDataGrid) never panics
// on navigation or draw before SetData/SetSource is ever called.
func TestDataGridEmptyBeforeSetData(t *testing.T) {
	g := newTestDataGrid()
	if n := g.rows.Len(); n != 0 {
		t.Fatalf("Len() before any data = %d, want 0", n)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(1, 3, tcell.Button1, tcell.ModNone))
}

// TestDataGridRowJumpKeysOnEmptyGridKeepIndicesValid asserts the indices
// themselves, not merely that HandleKey returned. End and PgDn used to set
// selRow to rows.Len()-1 == -1, ensureVisible copied it into scrollRow, and
// the next Draw indexed rows.Row(-1) — a panic on the UI goroutine, which has
// no recover, so pressing End on a zero-row query result exited the app.
// HandleKey never panicked itself, which is why asserting "no panic" here
// missed it for as long as it did.
func TestDataGridRowJumpKeysOnEmptyGridKeepIndicesValid(t *testing.T) {
	keys := map[string]tcell.Key{
		"End":  tcell.KeyEnd,
		"PgDn": tcell.KeyPgDn,
		"PgUp": tcell.KeyPgUp,
		"Home": tcell.KeyHome,
	}
	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			g := newTestDataGrid()
			g.SetData([]string{"A", "B"}, nil)
			g.HandleKey(tcell.NewEventKey(key, "", tcell.ModNone))
			if g.selRow < 0 {
				t.Errorf("selRow after %s on an empty grid = %d, want >= 0", name, g.selRow)
			}
			if g.scrollRow < 0 {
				t.Errorf("scrollRow after %s on an empty grid = %d, want >= 0", name, g.scrollRow)
			}
			// What Draw's row loop does for its first line: with scrollRow
			// negative this is the call that panicked.
			if g.scrollRow < g.rows.Len() {
				g.rows.Row(g.scrollRow)
			}
		})
	}
}

// TestDataGridSetDataWrapsSlice confirms SetData (the shape every existing
// caller uses) reaches the grid through the same SetSource/RowSource path
// as a custom source would.
func TestDataGridSetDataWrapsSlice(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B"}, [][]string{{"1", "2"}, {"3", "4"}})
	if n := g.rows.Len(); n != 2 {
		t.Fatalf("Len() = %d, want 2", n)
	}
	if got := g.rows.Row(1); got[0] != "3" || got[1] != "4" {
		t.Errorf("Row(1) = %v, want [3 4]", got)
	}
	if !strings.Contains(g.status, "2 rows") {
		t.Errorf("status = %q, want it to mention 2 rows", g.status)
	}
}

// TestDataGridSetSourceCustomImplementation verifies a RowSource that isn't
// SliceRowSource works end to end — the point of the interface.
func TestDataGridSetSourceCustomImplementation(t *testing.T) {
	src := SliceRowSource{{"x"}, {"y"}, {"z"}}
	g := newTestDataGrid()
	g.SetSource([]string{"Col"}, src)
	if n := g.rows.Len(); n != 3 {
		t.Fatalf("Len() = %d, want 3", n)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModNone))
	if g.selRow != 2 {
		t.Errorf("selRow after End = %d, want 2 (last row)", g.selRow)
	}
}

// TestComputeColWidthsSamplesOnlyFirstRows pins the sampling cap: a source
// with far more than colWidthSampleRows rows must not have every row
// inspected for column width, and a wide cell beyond the sample window must
// not affect the computed width.
func TestComputeColWidthsSamplesOnlyFirstRows(t *testing.T) {
	rows := make([][]string, colWidthSampleRows+50)
	for i := range rows {
		rows[i] = []string{"short"}
	}
	// A very wide cell placed well past the sample window; if it were
	// inspected, colWidths[0] would grow to reflect it (clamped at 40).
	rows[colWidthSampleRows+10] = []string{strings.Repeat("w", 100)}

	counting := &countingRowSource{RowSource: SliceRowSource(rows)}
	g := newTestDataGrid()
	g.SetSource([]string{"Col"}, counting)

	if counting.calls > colWidthSampleRows {
		t.Errorf("computeColWidths called Row() %d times, want at most %d", counting.calls, colWidthSampleRows)
	}
	// "short" (5) + 2 padding = 7, clamped to [6,40] -> 7. If the wide row
	// beyond the sample had been read, this would be 40 instead.
	if g.colWidths[0] != 7 {
		t.Errorf("colWidths[0] = %d, want 7 (sampling must not reach the wide row)", g.colWidths[0])
	}
}

// TestRefreshColumnWidthsRecomputesWithoutResettingSelection confirms
// RefreshColumnWidths picks up cells mutated in place (the pattern a
// progressive background loader uses — see internal/tui's DetailBrowser)
// without resetting scroll position or selection the way calling SetData
// again would. The recompute lands on the next Draw, not on the call
// itself — see RefreshColumnWidths.
func TestRefreshColumnWidthsRecomputesWithoutResettingSelection(t *testing.T) {
	g := newTestDataGrid()
	rows := [][]string{{"a"}, {"b"}, {"c"}}
	g.SetData([]string{"Col"}, rows)
	g.SetSelectedRow(2)

	if g.colWidths[0] != 6 {
		t.Fatalf("colWidths[0] before mutation = %d, want 6 (clamped minimum)", g.colWidths[0])
	}

	rows[1][0] = strings.Repeat("w", 100)
	g.RefreshColumnWidths()
	g.Draw(&fakeMenuScreen{w: 40, h: 10})

	if g.colWidths[0] != 40 {
		t.Errorf("colWidths[0] after RefreshColumnWidths+Draw = %d, want 40 (clamped maximum)", g.colWidths[0])
	}
	if g.selRow != 2 {
		t.Errorf("selRow after RefreshColumnWidths = %d, want 2 (unchanged)", g.selRow)
	}
}

// TestRefreshColumnWidthsCoalescesUntilDraw pins the deferral itself: a
// burst of RefreshColumnWidths calls between two frames (one per row, the
// way a progressive backfill issues them) must cost one recompute, not one
// per call, and a Draw with nothing pending must not recompute at all.
func TestRefreshColumnWidthsCoalescesUntilDraw(t *testing.T) {
	g := newTestDataGrid()
	rows := [][]string{{"a"}, {"b"}, {"c"}}
	g.SetData([]string{"Col"}, rows)
	screen := &fakeMenuScreen{w: 40, h: 10}

	rows[1][0] = strings.Repeat("w", 100)
	for range 10 {
		g.RefreshColumnWidths()
	}
	if g.colWidths[0] != 6 {
		t.Errorf("colWidths[0] before Draw = %d, want 6 (recompute deferred)", g.colWidths[0])
	}
	if !g.widthsDirty {
		t.Errorf("widthsDirty before Draw = false, want true")
	}

	g.Draw(screen)
	if g.widthsDirty {
		t.Errorf("widthsDirty after Draw = true, want false (one recompute drained the burst)")
	}

	// A second frame with nothing newly dirtied must leave the flag clear —
	// i.e. Draw isn't recomputing unconditionally.
	g.Draw(screen)
	if g.widthsDirty {
		t.Errorf("widthsDirty after idle Draw = true, want false")
	}
}

// TestSetFillLastColumnGrowsToFillRemainingWidth confirms a two-column
// Property/Value-shaped grid stretches its narrow, content-clamped Value
// column out to the rect's edge once fillLastColumn is enabled, and shrinks
// back to its content width when disabled again.
func TestSetFillLastColumnGrowsToFillRemainingWidth(t *testing.T) {
	g := newTestDataGrid() // 40 wide
	g.SetData([]string{"Property", "Value"}, [][]string{{"Name", "x"}})

	contentWidth := g.colWidths[1]
	if contentWidth >= 30 {
		t.Fatalf("colWidths[1] = %d before fill, want small enough to leave room to grow", contentWidth)
	}

	g.SetFillLastColumn(true)
	want := g.rect.W - g.colWidths[0]
	if g.colWidths[1] != want {
		t.Errorf("colWidths[1] after SetFillLastColumn(true) = %d, want %d (rect width minus col 0)", g.colWidths[1], want)
	}

	g.SetFillLastColumn(false)
	if g.colWidths[1] != contentWidth {
		t.Errorf("colWidths[1] after SetFillLastColumn(false) = %d, want back to %d", g.colWidths[1], contentWidth)
	}
}

// TestSetFillLastColumnNoOpWhenContentAlreadyWider confirms fillLastColumn
// never shrinks a column whose sampled content already exceeds the rect's
// remaining width — it only grows, never clamps down.
func TestSetFillLastColumnNoOpWhenContentAlreadyWider(t *testing.T) {
	g := newTestDataGrid() // 40 wide
	g.SetData([]string{"Property", "Value"}, [][]string{{"Name", strings.Repeat("w", 100)}})
	contentWidth := g.colWidths[1] // clamped to defaultMaxCellWidth (40)

	g.SetFillLastColumn(true)
	if g.colWidths[1] != contentWidth {
		t.Errorf("colWidths[1] = %d, want unchanged %d (already wider than available space)", g.colWidths[1], contentWidth)
	}
}

// TestDataGridSetErrorUsesRowSource confirms SetError's single error row
// goes through the same RowSource plumbing rather than a raw slice field.
func TestDataGridSetErrorUsesRowSource(t *testing.T) {
	g := newTestDataGrid()
	g.SetError(errTest{"boom"})
	if n := g.rows.Len(); n != 1 {
		t.Fatalf("Len() after SetError = %d, want 1", n)
	}
	if got := g.rows.Row(0); got[0] != "boom" {
		t.Errorf("Row(0) = %v, want [boom]", got)
	}
}

type errTest struct{ msg string }

func (e errTest) Error() string { return e.msg }

// wideTestDataGrid returns a grid whose columns are collectively far wider
// than its rect, so horizontal scrolling — and therefore the horizontal
// scrollbar — is in play.
func wideTestDataGrid() *DataGrid {
	g := newTestDataGrid() // 40 wide, 10 high
	cols := []string{"aaaaaaaa", "bbbbbbbb", "cccccccc", "dddddddd", "eeeeeeee", "ffffffff"}
	g.SetData(cols, [][]string{{"1", "2", "3", "4", "5", "6"}})
	g.SetStatus("") // leave the whole status row to the bar
	return g
}

// TestHScrollbarOnlyWhenColumnsOverflow confirms the horizontal scrollbar
// appears exactly when there's something to scroll to — a grid whose
// columns already fit gets none, which is what stops every two-column
// property grid in the app from growing a pointless bar.
func TestHScrollbarOnlyWhenColumnsOverflow(t *testing.T) {
	narrow := newTestDataGrid()
	narrow.SetData([]string{"Property", "Value"}, [][]string{{"Name", "x"}})
	narrow.SetStatus("")
	if _, _, _, _, _, _, ok := narrow.hScrollbar(); ok {
		t.Errorf("hScrollbar ok = true for a grid whose columns fit, want false")
	}

	g := wideTestDataGrid()
	x, y, w, total, visible, offset, ok := g.hScrollbar()
	if !ok {
		t.Fatalf("hScrollbar ok = false for an overflowing grid, want true")
	}
	if y != g.rect.Y+g.rect.H-1 {
		t.Errorf("bar y = %d, want %d (the status row)", y, g.rect.Y+g.rect.H-1)
	}
	if x != g.rect.X || w < hScrollbarMinWidth {
		t.Errorf("bar x/w = %d/%d, want x=%d and w >= %d", x, w, g.rect.X, hScrollbarMinWidth)
	}
	if total <= visible {
		t.Errorf("total/visible = %d/%d, want total greater", total, visible)
	}
	if offset != 0 {
		t.Errorf("offset at scrollCol 0 = %d, want 0", offset)
	}
}

// TestHScrollbarDragScrollsColumns confirms dragging the thumb moves
// scrollCol, keeps control once the pointer leaves the bar's own row, and
// releases the latch on the mouse-up.
func TestHScrollbarDragScrollsColumns(t *testing.T) {
	g := wideTestDataGrid()
	_, y, w, _, _, _, _ := g.hScrollbar()

	// A press away from the bar's row must not start a drag.
	if g.hScrollbarDrag(tcell.NewEventMouse(g.rect.X+2, y-1, tcell.Button1, tcell.ModNone)) {
		t.Fatalf("press off the bar's row started a horizontal scrollbar drag")
	}

	// A press at the far right of the track scrolls as far as it goes.
	if !g.hScrollbarDrag(tcell.NewEventMouse(g.rect.X+w-1, y, tcell.Button1, tcell.ModNone)) {
		t.Fatalf("press on the bar not consumed")
	}
	far := g.scrollCol
	if far == 0 {
		t.Fatalf("scrollCol after dragging to the end = 0, want a scrolled position")
	}

	// Still latched: an event off the bar's row keeps controlling scroll.
	if !g.hScrollbarDrag(tcell.NewEventMouse(g.rect.X, y-3, tcell.Button1, tcell.ModNone)) {
		t.Fatalf("drag off the bar's row not consumed while latched")
	}
	if g.scrollCol != 0 {
		t.Errorf("scrollCol after dragging back to the left = %d, want 0", g.scrollCol)
	}

	g.HandleMouse(tcell.NewEventMouse(g.rect.X, y, tcell.ButtonNone, tcell.ModNone))
	if g.sbDraggingH {
		t.Errorf("sbDraggingH still set after the release")
	}
}

// TestClickOnStatusRowDoesNotSelectOffscreenRow pins down that the grid's
// bottom row belongs to the status bar (and the horizontal scrollbar
// sharing it), not to the data: rect covers the header, its separator and
// that row too, so an unbounded hit-test resolves a click there to
// scrollRow+dataH — the first row below the view, selected invisibly.
func TestClickOnStatusRowDoesNotSelectOffscreenRow(t *testing.T) {
	g := newTestDataGrid() // 40x10 -> 7 data rows
	rows := make([][]string, 50)
	for i := range rows {
		rows[i] = []string{strconv.Itoa(i)}
	}
	g.SetData([]string{"n"}, rows)

	statusY := g.rect.Y + g.rect.H - 1
	g.HandleMouse(tcell.NewEventMouse(g.rect.X+1, statusY, tcell.Button1, tcell.ModNone))
	if g.selRow != 0 {
		t.Errorf("selRow after clicking the status row = %d, want 0 (unchanged)", g.selRow)
	}

	// The last real data row still selects, so the bound isn't off by one.
	lastDataY := g.rect.Y + 2 + (g.rect.H - 3) - 1
	g.HandleMouse(tcell.NewEventMouse(g.rect.X, lastDataY, tcell.ButtonNone, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(g.rect.X+1, lastDataY, tcell.Button1, tcell.ModNone))
	if want := g.rect.H - 3 - 1; g.selRow != want {
		t.Errorf("selRow after clicking the last data row = %d, want %d", g.selRow, want)
	}
}
