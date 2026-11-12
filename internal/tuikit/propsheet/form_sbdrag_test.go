package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// newGridForm builds a form taller than its rect with a focused GridRow, so
// a release can land on the grid rather than on the form's own bands.
func newGridForm() *Form {
	g := controls.NewDataGrid()
	g.SetData([]string{"A"}, [][]string{{"1"}, {"2"}, {"3"}, {"4"}, {"5"}, {"6"}})
	f := NewForm(Section("S1"), NewGridRow(g, 10), Section("S2"), Note("x"), Section("S3"), Note("y"))
	f.SetBounds(0, 0, 60, 12)
	f.Focus(true)
	f.setFocus(1)
	f.Draw(tuikittest.NewScreen(100, 40))
	return f
}

// A scrollbar drag released over the focused GridRow must still drop the
// latch. DataGrid.HandleMouse returns true for any release inside its rect,
// so with the reset below the focused row's dispatch it never ran.
func TestFormScrollbarLatchClearsOnReleaseOverGrid(t *testing.T) {
	f := newGridForm()

	f.HandleMouse(tcell.NewEventMouse(f.rect.Right()-1, 3, tcell.Button1, tcell.ModNone))
	if !f.sbDragging {
		t.Fatal("press on the scrollbar column did not arm sbDragging")
	}
	f.HandleMouse(tcell.NewEventMouse(10, 4, tcell.ButtonNone, tcell.ModNone))
	if f.sbDragging {
		t.Error("sbDragging still armed after the release landed on the focused GridRow")
	}
}

// What the stuck latch actually cost: every later click in the form was
// taken as a scrollbar drag and jumped the scroll to the click's own row.
func TestFormClickAfterScrollbarDragDoesNotScroll(t *testing.T) {
	f := newGridForm()

	f.HandleMouse(tcell.NewEventMouse(f.rect.Right()-1, 1, tcell.Button1, tcell.ModNone))
	f.HandleMouse(tcell.NewEventMouse(10, 4, tcell.ButtonNone, tcell.ModNone))
	f.Draw(tuikittest.NewScreen(100, 40))
	before := f.scroll

	// A fresh press well down the form, nowhere near the scrollbar column.
	f.HandleMouse(tcell.NewEventMouse(10, 11, tcell.Button1, tcell.ModNone))
	if f.scroll != before {
		t.Errorf("click at x=10 changed scroll %d -> %d, want it unchanged", before, f.scroll)
	}
}

// Dragging the thumb to the track's last cell must reach maxScroll, as the
// wheel does. With row starts that don't land exactly on total-H (a two-line
// Section ahead of one-line rows), rounding the drag's line down to its row
// parked the form one row short of the bottom.
func TestFormScrollbarDragReachesBottom(t *testing.T) {
	var rows []Row
	for range 8 {
		rows = append(rows, Section("S"), Static("A", "1"))
	}
	f := NewForm(rows...)
	f.SetBounds(0, 0, 60, 8) // 24 lines, maxScroll's row starts at 17, not 16
	f.Draw(tuikittest.NewScreen(100, 40))
	w := f.contentWidth()
	want := f.maxScroll(w)

	f.HandleMouse(tcell.NewEventMouse(f.rect.Right()-1, 0, tcell.Button1, tcell.ModNone))
	f.HandleMouse(tcell.NewEventMouse(f.rect.Right()-1, f.rect.H-1, tcell.Button1, tcell.ModNone))
	if f.scroll != want {
		t.Errorf("drag to the track's end left scroll at %d, want maxScroll %d", f.scroll, want)
	}
	f.HandleMouse(tcell.NewEventMouse(f.rect.Right()-1, 0, tcell.Button1, tcell.ModNone))
	if f.scroll != 0 {
		t.Errorf("drag back to the track's top left scroll at %d, want 0", f.scroll)
	}
}
