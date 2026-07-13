package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestDataGridHorizontalScrollFollowsCellCursor confirms moving the cell
// cursor past the right edge of the grid scrolls just enough columns into
// view to keep the selected cell visible and fully drawable — the fix for
// scrollCol previously being tracked but never applied in Draw.
func TestDataGridHorizontalScrollFollowsCellCursor(t *testing.T) {
	g := newTestDataGrid() // 40 columns wide
	cols := []string{"C0", "C1", "C2", "C3", "C4", "C5", "C6", "C7", "C8", "C9"}
	rows := [][]string{make([]string, len(cols))}
	for i := range rows[0] {
		rows[0][i] = "x"
	}
	g.SetData(cols, rows)
	g.SetCellCursor(true)

	for range cols[1:] {
		g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	}
	if _, col := g.SelectedCell(); col != len(cols)-1 {
		t.Fatalf("selCol = %d, want %d (last column)", col, len(cols)-1)
	}
	if g.scrollCol == 0 {
		t.Fatal("scrollCol should have advanced once the selected column scrolled past the visible width")
	}
	w := 0
	for i := g.scrollCol; i <= g.selCol; i++ {
		w += g.colWidths[i]
	}
	if w > g.rect.W {
		t.Errorf("visible width for scrollCol..selCol = %d, exceeds grid width %d", w, g.rect.W)
	}

	// Left back to the first column must scroll back to 0.
	for range cols[1:] {
		g.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	}
	if g.scrollCol != 0 {
		t.Errorf("scrollCol = %d after returning to column 0, want 0", g.scrollCol)
	}
}

// TestDataGridRowSelectModeScrollsByColumn pins the non-cell-cursor
// (whole-row-select) Left/Right behavior: it shifts scrollCol by one column
// per key, clamped to [0, columns-1].
func TestDataGridRowSelectModeScrollsByColumn(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B", "C"}, [][]string{{"1", "2", "3"}})

	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	if g.scrollCol != 1 {
		t.Fatalf("scrollCol after Right = %d, want 1", g.scrollCol)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone)) // must clamp
	if g.scrollCol != 2 {
		t.Fatalf("scrollCol after repeated Right = %d, want 2 (clamped to last column)", g.scrollCol)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	if g.scrollCol != 1 {
		t.Fatalf("scrollCol after Left = %d, want 1", g.scrollCol)
	}
}

// TestDataGridHorizontalWheelScroll confirms WheelLeft/WheelRight, and
// Shift+WheelUp/WheelDown as the common desktop-convention alias for them,
// all move scrollCol — the mouse-driven equivalent of the Left/Right key
// scrolling already covered by TestDataGridRowSelectModeScrollsByColumn.
func TestDataGridHorizontalWheelScroll(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B", "C", "D", "E"}, [][]string{{"1", "2", "3", "4", "5"}})

	g.HandleMouse(tcell.NewEventMouse(5, 5, tcell.WheelRight, tcell.ModNone))
	if g.scrollCol != horizontalWheelCols {
		t.Fatalf("scrollCol after WheelRight = %d, want %d", g.scrollCol, horizontalWheelCols)
	}
	// Enough further ticks to overshoot the last column regardless of step size.
	for range len(g.columns) {
		g.HandleMouse(tcell.NewEventMouse(5, 5, tcell.WheelRight, tcell.ModNone))
	}
	if g.scrollCol != len(g.columns)-1 {
		t.Fatalf("scrollCol after repeated WheelRight = %d, want %d (clamped to last column)", g.scrollCol, len(g.columns)-1)
	}
	g.HandleMouse(tcell.NewEventMouse(5, 5, tcell.WheelLeft, tcell.ModNone))
	if g.scrollCol != len(g.columns)-1-horizontalWheelCols {
		t.Fatalf("scrollCol after WheelLeft = %d, want %d", g.scrollCol, len(g.columns)-1-horizontalWheelCols)
	}

	g.scrollCol = 0
	g.HandleMouse(tcell.NewEventMouse(5, 5, tcell.WheelDown, tcell.ModShift))
	if g.scrollCol != horizontalWheelCols {
		t.Fatalf("scrollCol after Shift+WheelDown = %d, want %d", g.scrollCol, horizontalWheelCols)
	}
	g.HandleMouse(tcell.NewEventMouse(5, 5, tcell.WheelUp, tcell.ModShift))
	if g.scrollCol != 0 {
		t.Fatalf("scrollCol after Shift+WheelUp = %d, want 0", g.scrollCol)
	}

	// Plain WheelUp/WheelDown (no Shift) still scroll rows, not columns.
	g.HandleMouse(tcell.NewEventMouse(5, 5, tcell.WheelDown, tcell.ModNone))
	if g.scrollCol != 0 {
		t.Fatalf("plain WheelDown must not touch scrollCol, got %d", g.scrollCol)
	}
}

// TestDataGridRowNumbersGutter confirms the row-number column reserves
// screen space that isn't part of any data column: clicks inside it never
// resolve to a cell, and clicks just past it resolve to column 0.
func TestDataGridRowNumbersGutter(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"Grant", "Deny"}, [][]string{{"[ ]", "[ ]"}})
	g.SetCellCursor(true)

	if gw := g.gutterWidth(); gw != 0 {
		t.Fatalf("gutterWidth() before SetRowNumbers = %d, want 0", gw)
	}
	g.SetRowNumbers(true)
	gw := g.gutterWidth()
	if gw == 0 {
		t.Fatal("gutterWidth() should be nonzero once SetRowNumbers(true)")
	}
	if _, ok := g.colAt(g.rect.X); ok {
		t.Error("colAt within the row-number gutter should not resolve to a data column")
	}
	if col, ok := g.colAt(g.rect.X + gw); !ok || col != 0 {
		t.Errorf("colAt just past the gutter = (%d,%v), want (0,true)", col, ok)
	}
}

// TestDataGridEnterAndClickNoLongerOpenViewer confirms Enter and a plain
// left-click only ever call OnActivateCell (or do nothing, for a read-only
// grid) — the content viewer is reached exclusively via right-click's
// "Show Value" (see TestDataGridRightClickShowsContextMenu below), not as
// a side effect of ordinary navigation/activation.
func TestDataGridEnterAndClickNoLongerOpenViewer(t *testing.T) {
	g := newCellCursorGrid() // 2x2 grid, Grant/Deny columns, no OnActivateCell
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if g.viewOpen {
		t.Fatal("Enter should no longer open the built-in content viewer")
	}
	x := g.colWidths[0] + 2 // inside column 1
	y := g.rect.Y + 2 + 1   // data row index 1
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	if g.viewOpen {
		t.Fatal("a left-click should no longer open the built-in content viewer")
	}
	if row, col := g.SelectedCell(); row != 1 || col != 1 {
		t.Fatalf("left-click should still select the cell = (%d,%d), want (1,1)", row, col)
	}
}

// TestDataGridOnActivateCellStillFiresOnEnterAndClick confirms editable
// grids (togglegrid, permission-state cycling, …) keep their existing
// Enter/click-activates-the-cell behavior unchanged.
func TestDataGridOnActivateCellStillFiresOnEnterAndClick(t *testing.T) {
	g := newCellCursorGrid()
	fired := 0
	g.OnActivateCell = func(row, col int) { fired++ }
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	x := g.colWidths[0] + 2
	y := g.rect.Y + 2
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	if fired != 2 {
		t.Errorf("OnActivateCell fired %d times, want 2 (Enter + click)", fired)
	}
}

// TestDataGridRightClickShowsContextMenu confirms right-click on a
// read-only cell (OnActivateCell nil) selects it and opens a context menu
// offering "Show Value", whose action opens the content viewer loaded with
// that cell's full text — and that an editable grid (OnActivateCell set)
// gets no such menu.
func TestDataGridRightClickShowsContextMenu(t *testing.T) {
	g := newCellCursorGrid()
	x := g.colWidths[0] + 2 // inside column 1 ("Deny")
	y := g.rect.Y + 2 + 1   // data row index 1
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button2, tcell.ModNone))
	if !g.ctxMenu.Visible() {
		t.Fatal("right-click on a read-only cell should show a context menu")
	}
	if row, col := g.SelectedCell(); row != 1 || col != 1 {
		t.Fatalf("right-click should select the cell = (%d,%d), want (1,1)", row, col)
	}
	if len(g.ctxMenu.items) != 1 || g.ctxMenu.items[0].Label != showValueMenuItem {
		t.Fatalf("context menu items = %+v, want a single %q item", g.ctxMenu.items, showValueMenuItem)
	}

	g.ctxMenu.items[0].Action()
	if !g.viewOpen {
		t.Fatal("activating \"Show Value\" should open the content viewer")
	}
	if g.viewHeader != "Deny" {
		t.Errorf("viewHeader = %q, want %q", g.viewHeader, "Deny")
	}
	if got := g.viewEditor.Text(); got != "[ ]" {
		t.Errorf("viewer text = %q, want the full cell value %q", got, "[ ]")
	}

	// An editable grid (OnActivateCell set) offers no "Show Value" menu —
	// its cells are toggles, not values to view.
	g2 := newCellCursorGrid()
	g2.OnActivateCell = func(row, col int) {}
	g2.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button2, tcell.ModNone))
	if g2.ctxMenu.Visible() {
		t.Error("an editable grid should not show a \"Show Value\" context menu")
	}
}

// TestDataGridCtrlSpaceShowsContextMenu confirms Ctrl+Space is a keyboard
// equivalent of TestDataGridRightClickShowsContextMenu's mouse right-click —
// same menu, positioned at the keyboard-selected cell instead — and that,
// like the mouse path, an editable grid gets no menu from it either.
func TestDataGridCtrlSpaceShowsContextMenu(t *testing.T) {
	g := newCellCursorGrid()
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone)) // col 1
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))  // row 1

	if !g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl)) {
		t.Fatal("Ctrl+Space should be consumed")
	}
	if !g.ctxMenu.Visible() {
		t.Fatal("Ctrl+Space on a read-only cell should show a context menu")
	}
	if len(g.ctxMenu.items) != 1 || g.ctxMenu.items[0].Label != showValueMenuItem {
		t.Fatalf("context menu items = %+v, want a single %q item", g.ctxMenu.items, showValueMenuItem)
	}

	g2 := newCellCursorGrid()
	g2.OnActivateCell = func(row, col int) {}
	g2.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl))
	if g2.ctxMenu.Visible() {
		t.Error("an editable grid should not show a \"Show Value\" context menu from Ctrl+Space")
	}
}

// TestDataGridViewerIsReadOnlyAndSupportsSelection confirms the content
// viewer's embedded Editor rejects typed input (read-only) but still
// supports Select All and reports a selection — satisfying "allows
// selection and copy to clipboard" without allowing edits.
func TestDataGridViewerIsReadOnlyAndSupportsSelection(t *testing.T) {
	g := newCellCursorGrid()
	g.selRow, g.selCol = 1, 0 // "[x]"
	g.openViewer()
	if !g.viewOpen {
		t.Fatal("openViewer should open the viewer")
	}
	before := g.viewEditor.Text()

	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, "z", tcell.ModNone))
	if got := g.viewEditor.Text(); got != before {
		t.Errorf("typing into the viewer mutated its text: got %q, want unchanged %q", got, before)
	}

	if g.HasSelection() {
		t.Fatal("HasSelection should be false before any selection is made")
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyCtrlA, "", tcell.ModNone))
	if !g.HasSelection() {
		t.Fatal("Ctrl+A (Select All) should select the viewer's text")
	}
	if got := g.SelectedText(); got != before {
		t.Errorf("SelectedText() = %q, want the full cell value %q", got, before)
	}

	g.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if g.viewOpen {
		t.Error("Escape should close the content viewer")
	}
	if g.HasSelection() {
		t.Error("HasSelection should be false once the viewer is closed")
	}
}

// newBlockSelectGrid returns a 3-row, 2-column read-only cell-cursor grid
// (no OnActivateCell) — the fixture for the multi-cell block-selection and
// copy tests below.
func newBlockSelectGrid() *DataGrid {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B"}, [][]string{
		{"a0", "b0"},
		{"a1", "b1"},
		{"a2", "b2"},
	})
	g.SetCellCursor(true)
	return g
}

// TestDataGridShiftArrowExtendsBlockSelection confirms Shift+Right then
// Shift+Down grows a block selection from the anchor cell, and that a
// plain (non-Shift) arrow afterward collapses it back to a single cell.
func TestDataGridShiftArrowExtendsBlockSelection(t *testing.T) {
	g := newBlockSelectGrid()
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModShift))
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))
	if !g.blockSelecting {
		t.Fatal("Shift+Arrow should start a block selection")
	}
	if r0, c0, r1, c1 := g.selectionBounds(); r0 != 0 || c0 != 0 || r1 != 1 || c1 != 1 {
		t.Fatalf("selectionBounds() = (%d,%d,%d,%d), want (0,0,1,1)", r0, c0, r1, c1)
	}

	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if g.blockSelecting {
		t.Fatal("a plain arrow key should collapse the block selection")
	}
	if row, col := g.SelectedCell(); row != 2 || col != 1 {
		t.Fatalf("SelectedCell() after plain Down = (%d,%d), want (2,1)", row, col)
	}
}

// TestDataGridMouseDragSelectsBlock confirms a Button1 press followed by a
// continued-drag move (same Buttons() mask, no release in between) grows a
// block selection, that a release resets the drag tracker, and that a
// subsequent fresh single-cell click collapses the selection again.
func TestDataGridMouseDragSelectsBlock(t *testing.T) {
	g := newBlockSelectGrid()
	x0, y0 := g.colWidths[0]/2, g.rect.Y+2   // (row 0, col 0)
	x1, y1 := g.colWidths[0]+2, g.rect.Y+2+1 // (row 1, col 1)

	g.HandleMouse(tcell.NewEventMouse(x0, y0, tcell.Button1, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(x1, y1, tcell.Button1, tcell.ModNone))
	if !g.blockSelecting {
		t.Fatal("dragging to a different cell should start a block selection")
	}
	if r0, c0, r1, c1 := g.selectionBounds(); r0 != 0 || c0 != 0 || r1 != 1 || c1 != 1 {
		t.Fatalf("selectionBounds() = (%d,%d,%d,%d), want (0,0,1,1)", r0, c0, r1, c1)
	}

	g.HandleMouse(tcell.NewEventMouse(x1, y1, tcell.ButtonNone, tcell.ModNone))
	if g.mouseDragging {
		t.Fatal("mouseDragging should reset on release")
	}

	g.HandleMouse(tcell.NewEventMouse(x0, y0, tcell.Button1, tcell.ModNone))
	if g.blockSelecting {
		t.Fatal("a fresh single-cell click should collapse the block selection")
	}
}

// TestDataGridRightClickBlockSelectionCopy confirms right-clicking inside an
// existing block selection preserves it and offers only "Copy" (no "Show
// Value" — a block has no single cell's value to show), whose action hands
// the whole block's tab/newline-joined text to OnCopyRequest; right-
// clicking outside the block collapses it to the clicked cell and restores
// the usual "Copy"+"Show Value" pair.
func TestDataGridRightClickBlockSelectionCopy(t *testing.T) {
	g := newBlockSelectGrid()
	var copied string
	g.OnCopyRequest = func(text string) { copied = text }

	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModShift))
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))

	x, y := g.colWidths[0]/2, g.rect.Y+2 // inside the (0,0)-(1,1) block
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button2, tcell.ModNone))
	if !g.blockSelecting {
		t.Fatal("right-clicking inside an existing block selection should preserve it")
	}
	items := g.ctxMenu.items
	if len(items) != 1 || items[0].Label != "Copy" {
		t.Fatalf("context menu items = %+v, want a single \"Copy\" item", items)
	}
	items[0].Action()
	if want := "a0\tb0\na1\tb1"; copied != want {
		t.Errorf("copied text = %q, want %q", copied, want)
	}

	// A right-click doesn't reposition an already-open context menu (see
	// ContextMenu.HandleMouse: only Button1 dismisses it) — dismiss it
	// explicitly first, as Escape would in real use, so this second
	// right-click is a fresh interaction rather than being swallowed by
	// the still-open menu from the first one.
	g.ctxMenu.Hide()
	x2, y2 := g.colWidths[0]+2, g.rect.Y+2+2 // row 2, col 1 — outside the block
	g.HandleMouse(tcell.NewEventMouse(x2, y2, tcell.Button2, tcell.ModNone))
	if g.blockSelecting {
		t.Fatal("right-clicking outside the block should collapse it")
	}
	if row, col := g.SelectedCell(); row != 2 || col != 1 {
		t.Fatalf("SelectedCell() after outside right-click = (%d,%d), want (2,1)", row, col)
	}
	items = g.ctxMenu.items
	if len(items) != 2 || items[0].Label != "Copy" || items[1].Label != showValueMenuItem {
		t.Fatalf("context menu items = %+v, want [Copy, %s]", items, showValueMenuItem)
	}
}

// TestDataGridGutterHeaderRightClickOffersCopyAll confirms right-clicking
// the row-number gutter's blank header cell offers "Copy All"/"Copy All
// with Headers" only once OnCopyRequest is wired — a grid that hasn't
// opted in shows no menu there at all, unchanged from before this feature.
func TestDataGridGutterHeaderRightClickOffersCopyAll(t *testing.T) {
	g := newBlockSelectGrid()
	g.SetRowNumbers(true)
	gw := g.gutterWidth()

	g.HandleMouse(tcell.NewEventMouse(g.rect.X+gw/2, g.rect.Y, tcell.Button2, tcell.ModNone))
	if g.ctxMenu.Visible() {
		t.Fatal("gutter header right-click should show no menu without OnCopyRequest wired")
	}

	var copied string
	g.OnCopyRequest = func(text string) { copied = text }
	g.HandleMouse(tcell.NewEventMouse(g.rect.X+gw/2, g.rect.Y, tcell.Button2, tcell.ModNone))
	if !g.ctxMenu.Visible() {
		t.Fatal("gutter header right-click should show a menu once OnCopyRequest is wired")
	}
	items := g.ctxMenu.items
	if len(items) != 2 || items[0].Label != "Copy All" || items[1].Label != "Copy All with Headers" {
		t.Fatalf("context menu items = %+v, want [Copy All, Copy All with Headers]", items)
	}

	items[0].Action()
	if want := "a0\tb0\na1\tb1\na2\tb2"; copied != want {
		t.Errorf("Copy All text = %q, want %q", copied, want)
	}
	items[1].Action()
	if want := "A\tB\na0\tb0\na1\tb1\na2\tb2"; copied != want {
		t.Errorf("Copy All with Headers text = %q, want %q", copied, want)
	}
}

// TestSetMaxCellWidthOverridesClamp confirms a grid that opts into a
// configurable max column width (the query-results grid, via the Options
// dialog's max-cell-length setting) actually gets clamped to it instead of
// the package default.
func TestSetMaxCellWidthOverridesClamp(t *testing.T) {
	g := newTestDataGrid()
	g.SetMaxCellWidth(10)
	g.SetData([]string{"Col"}, [][]string{{strings.Repeat("x", 100)}})
	if g.colWidths[0] != 10 {
		t.Errorf("colWidths[0] = %d, want 10 (SetMaxCellWidth clamp)", g.colWidths[0])
	}
}
