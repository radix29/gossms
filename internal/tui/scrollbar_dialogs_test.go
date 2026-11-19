package tui

import (
	"strconv"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/tuikittest"
)

// TestQueryListDialogScrollbarDragScrolls confirms dragging the query list's
// scrollbar scrolls it instead of being read as a click that switches to
// (and closes the dialog on) whatever row sits under it — QueryListDialog's
// row click is a single-click "activate", so misreading a scrollbar click
// this way would be especially disruptive.
func TestQueryListDialogScrollbarDragScrolls(t *testing.T) {
	a := newTestApp()
	a.setScreen(tuikittest.NewScreen(80, 30))

	d := withScreen(NewQueryListDialog(a), a.screen)
	for i := range 20 {
		d.titles = append(d.titles, "Query "+strconv.Itoa(i))
		d.indices = append(d.indices, i)
	}
	d.ListDialog.Show() // the rows above, not a snapshot of a.panels

	inner := d.InnerRect()
	dataH := d.ButtonRowY() - inner.Y - 1
	if len(d.titles) <= dataH {
		t.Fatalf("test needs more titles than dataH (%d) to exercise scrolling, got %d", dataH, len(d.titles))
	}
	selBefore := d.Selected()

	sbX := d.Rect().Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+dataH, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if d.Scroll() == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if d.Selected() != selBefore {
		t.Errorf("sel changed to %d, clicking the scrollbar must not activate/select a row", d.Selected())
	}
	if !d.Visible() {
		t.Error("clicking the scrollbar must not close the dialog")
	}
}

// TestTasksDialogScrollbarDragScrolls mirrors the QueryListDialog case for
// TasksDialog, whose row click likewise reads position and so needs the
// scrollbar checked first.
func TestTasksDialogScrollbarDragScrolls(t *testing.T) {
	a := newTestApp()
	a.setScreen(tuikittest.NewScreen(80, 30))
	for i := range 20 {
		a.tasks = append(a.tasks, &Task{Label: "Task " + strconv.Itoa(i)})
	}

	d := withScreen(NewTasksDialog(a), a.screen)
	d.Show()

	inner := d.InnerRect()
	dataH := d.ButtonRowY() - inner.Y - 1
	if len(a.tasks) <= dataH {
		t.Fatalf("test needs more tasks than dataH (%d) to exercise scrolling, got %d", dataH, len(a.tasks))
	}
	selBefore := d.Selected()

	sbX := d.Rect().Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+dataH, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if d.Scroll() == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if d.Selected() != selBefore {
		t.Errorf("sel changed to %d, clicking the scrollbar must not select a task", d.Selected())
	}
}

// TestHelpDialogScrollbarDragScrolls confirms the simpler (no per-row
// selection) dialogs get the same scrollbar behavior via
// ModalDialog.ScrollbarDrag.
//
// KeyDiagnosticsDialog was tested here too until it moved its log into a
// read-only editor.Editor, which draws and drags its own scrollbar — the
// same reason StatusHistoryDialog, built that way from the start, has never
// had a case here. That half is pinned in tuikit's
// controls/editor/scrollbar_drag_test.go.
func TestHelpDialogScrollbarDragScrolls(t *testing.T) {
	a := newTestApp()
	a.setScreen(tuikittest.NewScreen(80, 30))
	d := withScreen(NewHelpDialog(a), a.screen)
	d.Show()

	inner := d.InnerRect()
	dataH := d.DataH()
	if len(helpLines) <= dataH {
		t.Fatalf("helpLines (%d) must exceed dataH (%d) for this test to mean anything", len(helpLines), dataH)
	}

	sbX := d.Rect().Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+1+dataH-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if d.Scroll() == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
}
