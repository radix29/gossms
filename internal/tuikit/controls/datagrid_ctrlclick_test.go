package controls

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// newMarkSelectGrid returns a 5-row, 2-column read-only cell-cursor grid — the
// fixture for the Ctrl+click discontiguous-selection tests. Five rows rather
// than three so a selection can skip one and still have rows on both sides of
// the gap.
func newMarkSelectGrid() *DataGrid {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B"}, [][]string{
		{"a0", "b0"},
		{"a1", "b1"},
		{"a2", "b2"},
		{"a3", "b3"},
		{"a4", "b4"},
	})
	g.SetCellCursor(true)
	return g
}

// rowXY is the screen position of a cell in column 0 of the given data row.
func rowXY(g *DataGrid, row int) (int, int) { return g.colWidths[0] / 2, g.rect.Y + 2 + row }

// clickGridRow presses and releases Button1 on column 0 of row, with mods held.
func clickGridRow(g *DataGrid, row int, mods tcell.ModMask) {
	x, y := rowXY(g, row)
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, mods))
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, mods))
}

func wantRows(t *testing.T, g *DataGrid, want []int, what string) {
	t.Helper()
	if got := g.SelectedRows(); !slices.Equal(got, want) {
		t.Fatalf("%s: SelectedRows() = %v, want %v", what, got, want)
	}
}

// TestCtrlClickPicksOutRowsOneAtATime is the whole point of the marked set: two
// rows selected with a row between them that is not, which no anchor/cursor
// rectangle can describe.
func TestCtrlClickPicksOutRowsOneAtATime(t *testing.T) {
	g := newMarkSelectGrid()
	clickGridRow(g, 1, tcell.ModNone)
	wantRows(t, g, []int{1}, "plain click")

	clickGridRow(g, 3, tcell.ModCtrl)
	wantRows(t, g, []int{1, 3}, "Ctrl+click on a second row")

	clickGridRow(g, 0, tcell.ModCtrl)
	wantRows(t, g, []int{0, 1, 3}, "Ctrl+click on a third row")
	if r0, _, r1, _ := g.SelectionBounds(); r0 == 0 && r1 == 3 {
		t.Fatal("SelectionBounds() spans the gap — a host reading it would act on row 2 as well")
	}
}

// TestCtrlClickOnAMarkedRowUnpicksIt covers the toggle, including the last row:
// unpicking everything leaves a selection of nothing, not a fall back to the
// row the cursor happens to sit on.
func TestCtrlClickOnAMarkedRowUnpicksIt(t *testing.T) {
	g := newMarkSelectGrid()
	clickGridRow(g, 1, tcell.ModNone)
	clickGridRow(g, 3, tcell.ModCtrl)
	clickGridRow(g, 1, tcell.ModCtrl)
	wantRows(t, g, []int{3}, "Ctrl+click on a marked row")

	clickGridRow(g, 3, tcell.ModCtrl)
	if got := g.SelectedRows(); len(got) != 0 {
		t.Fatalf("SelectedRows() after unpicking the last row = %v, want none", got)
	}
}

// TestCtrlClickTogglesOncePerPress pins the resend guard: tcell repeats Button1
// for as long as the button is held, and a second toggle would undo the first
// before the user let go.
func TestCtrlClickTogglesOncePerPress(t *testing.T) {
	g := newMarkSelectGrid()
	clickGridRow(g, 1, tcell.ModNone)
	x, y := rowXY(g, 3)
	for range 4 {
		g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModCtrl))
	}
	wantRows(t, g, []int{1, 3}, "four resends of one held Ctrl+click")
}

// TestCtrlClickKeepsAShiftSelectedRun confirms the block in force when the first
// Ctrl+click lands is folded into the marked set rather than discarded.
func TestCtrlClickKeepsAShiftSelectedRun(t *testing.T) {
	g := newMarkSelectGrid()
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))
	wantRows(t, g, []int{0, 1, 2}, "Shift+Down twice")

	clickGridRow(g, 4, tcell.ModCtrl)
	wantRows(t, g, []int{0, 1, 2, 4}, "Ctrl+click after a Shift-selected run")
}

// TestAnythingButACtrlClickDropsTheMarkedRows covers the three gestures that
// start a fresh selection. Shift+click is one of them: it extends from the
// anchor, and carrying the marked set along would make the run it selects
// depend on clicks the user has visibly moved on from.
func TestAnythingButACtrlClickDropsTheMarkedRows(t *testing.T) {
	mark := func(g *DataGrid) {
		clickGridRow(g, 0, tcell.ModNone)
		clickGridRow(g, 3, tcell.ModCtrl)
		wantRows(t, g, []int{0, 3}, "setup")
	}

	g := newMarkSelectGrid()
	mark(g)
	clickGridRow(g, 2, tcell.ModNone)
	wantRows(t, g, []int{2}, "plain click")

	g = newMarkSelectGrid()
	mark(g)
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	wantRows(t, g, []int{g.selRow}, "arrow key")

	g = newMarkSelectGrid()
	mark(g)
	clickGridRow(g, 1, tcell.ModNone)
	clickGridRow(g, 4, tcell.ModShift)
	wantRows(t, g, []int{1, 2, 3, 4}, "Shift+click after Ctrl+clicks")
}

// TestCopyOfAMarkedSelectionTakesWholeRows confirms the marked set copies as
// rows: every column, and only the rows picked — not the ones a rectangle drawn
// round them would include.
func TestCopyOfAMarkedSelectionTakesWholeRows(t *testing.T) {
	g := newMarkSelectGrid()
	clickGridRow(g, 1, tcell.ModNone)
	clickGridRow(g, 3, tcell.ModCtrl)
	if got, want := g.SelectedCellsText(), "a1\tb1\na3\tb3"; got != want {
		t.Fatalf("SelectedCellsText() = %q, want %q", got, want)
	}
}

// TestRightClickInsideAMarkedSelectionKeepsIt confirms the context menu acts on
// the marked rows rather than collapsing them to the clicked cell — and that a
// right-click on an unmarked row still collapses.
func TestRightClickInsideAMarkedSelectionKeepsIt(t *testing.T) {
	g := newMarkSelectGrid()
	g.OnCopyRequest = func(string) {}
	clickGridRow(g, 1, tcell.ModNone)
	clickGridRow(g, 3, tcell.ModCtrl)

	x, y := rowXY(g, 3)
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button2, tcell.ModNone))
	wantRows(t, g, []int{1, 3}, "right-click on a marked row")
	for _, it := range g.cellContextMenuItems() {
		if it.Label == showValueMenuItem {
			t.Fatal("Show Value offered on a multi-row selection, which has no single value to show")
		}
	}

	g.ctxMenu.Hide()
	x, y = rowXY(g, 4)
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button2, tcell.ModNone))
	wantRows(t, g, []int{4}, "right-click on an unmarked row")
}

// TestAltClickExtendsLikeShiftClick pins the fallback gesture: a VTE terminal
// keeps Shift+click for its own text selection and never forwards it, so the
// grid answers to Alt too. Both must reach the same selection — a fallback that
// behaves differently from the gesture it stands in for is a second feature to
// learn, not a way round the terminal.
func TestAltClickExtendsLikeShiftClick(t *testing.T) {
	for _, mod := range []struct {
		name string
		mask tcell.ModMask
	}{
		{"Shift", tcell.ModShift},
		{"Alt", tcell.ModAlt},
	} {
		t.Run(mod.name, func(t *testing.T) {
			g := newMarkSelectGrid()
			clickGridRow(g, 1, tcell.ModNone)
			clickGridRow(g, 3, mod.mask)
			wantRows(t, g, []int{1, 2, 3}, mod.name+"+click after a plain click")
		})
	}
}
