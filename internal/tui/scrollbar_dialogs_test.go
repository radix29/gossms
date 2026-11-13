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
	a.screen = tuikittest.NewScreen(80, 30)

	d := &QueryListDialog{app: a}
	d.InitModal(a.screen, "Query List", 56, 16)
	d.titles = make([]string, 20)
	d.indices = make([]int, 20)
	for i := range d.titles {
		d.titles[i] = "Query " + strconv.Itoa(i)
		d.indices[i] = i
	}
	d.ModalDialog.Show()

	inner := d.InnerRect()
	dataH := d.dataH()
	if len(d.titles) <= dataH {
		t.Fatalf("test needs more titles than dataH (%d) to exercise scrolling, got %d", dataH, len(d.titles))
	}
	selBefore := d.sel

	sbX := d.Rect().Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+dataH, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if d.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if d.sel != selBefore {
		t.Errorf("sel changed to %d, clicking the scrollbar must not activate/select a row", d.sel)
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
	a.screen = tuikittest.NewScreen(80, 30)
	for i := range 20 {
		a.tasks = append(a.tasks, &Task{Label: "Task " + strconv.Itoa(i)})
	}

	d := NewTasksDialog(a)
	d.Show()

	inner := d.InnerRect()
	dataH := d.dataH()
	if len(a.tasks) <= dataH {
		t.Fatalf("test needs more tasks than dataH (%d) to exercise scrolling, got %d", dataH, len(a.tasks))
	}
	selBefore := d.sel

	sbX := d.Rect().Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+dataH, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if d.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if d.sel != selBefore {
		t.Errorf("sel changed to %d, clicking the scrollbar must not select a task", d.sel)
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
// controls/scrollbar_drag_test.go.
func TestHelpDialogScrollbarDragScrolls(t *testing.T) {
	a := newTestApp()
	a.screen = tuikittest.NewScreen(80, 30)
	d := NewHelpDialog(a)
	d.Show()

	inner := d.InnerRect()
	dataH := d.dataH()
	if len(helpLines) <= dataH {
		t.Fatalf("helpLines (%d) must exceed dataH (%d) for this test to mean anything", len(helpLines), dataH)
	}

	sbX := d.Rect().Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+1+dataH-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if d.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
}
