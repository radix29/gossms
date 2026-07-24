package tui

import (
	"strconv"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// fakeSizedScreen is a minimal tcell.Screen fake — Size() is all
// ModalDialog.recentre needs to compute a real, non-zero Rect() for these
// tests, mirroring dialogs.sizedScreen's identical purpose in that package.
type fakeSizedScreen struct {
	tcell.Screen
	w, h int
}

func (s *fakeSizedScreen) Size() (int, int) { return s.w, s.h }

// TestQueryListDialogScrollbarDragScrolls confirms dragging the query list's
// scrollbar scrolls it instead of being read as a click that switches to
// (and closes the dialog on) whatever row sits under it — QueryListDialog's
// row click is a single-click "activate", so misreading a scrollbar click
// this way would be especially disruptive.
func TestQueryListDialogScrollbarDragScrolls(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 80, h: 30}

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
	dataH := inner.H - 2
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
// TasksDialog, whose row click likewise reads position without an
// intervening scrollbar check before this fix.
func TestTasksDialogScrollbarDragScrolls(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 80, h: 30}
	for i := 0; i < 20; i++ {
		a.tasks = append(a.tasks, &Task{Label: "Task " + strconv.Itoa(i)})
	}

	d := NewTasksDialog(a)
	d.Show()

	inner := d.InnerRect()
	dataH := inner.H - 2
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
func TestHelpDialogScrollbarDragScrolls(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 80, h: 30}
	d := NewHelpDialog(a)
	d.Show()

	inner := d.InnerRect()
	dataH := inner.H - 2
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

// TestKeyDiagnosticsDialogScrollbarDragScrolls mirrors
// TestHelpDialogScrollbarDragScrolls for KeyDiagnosticsDialog, which got the
// identical ScrollbarDrag fix but had no test of its own.
func TestKeyDiagnosticsDialogScrollbarDragScrolls(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 80, h: 30}
	d := NewKeyDiagnosticsDialog(a)
	d.Show()
	for i := 0; i < 30; i++ {
		d.lines = append(d.lines, "line")
	}

	inner := d.InnerRect()
	dataH := inner.H - 2
	if len(d.lines) <= dataH {
		t.Fatalf("test needs more lines than dataH (%d) to exercise scrolling, got %d", dataH, len(d.lines))
	}

	sbX := d.Rect().Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+1+dataH-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if d.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
}
