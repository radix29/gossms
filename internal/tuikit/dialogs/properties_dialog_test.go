package dialogs

import (
	"strconv"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestPropertiesDialog(n int) *PropertiesDialog {
	scr := &sizedScreen{w: 80, h: 30}
	d := NewPropertiesDialog(scr)
	rows := make([]PropertyRow, n)
	for i := range rows {
		rows[i] = PropertyRow{Key: "Key" + strconv.Itoa(i), Value: "Value" + strconv.Itoa(i)}
	}
	d.ShowProperties("Test", rows)
	return d
}

// TestPropertiesDialogScrollbarDragScrolls confirms dragging the dialog's
// scrollbar scrolls its row list instead of being silently ignored — the
// dialog previously had no scrollbar or drag handling at all, unlike every
// sibling scrollable dialog in this package.
func TestPropertiesDialogScrollbarDragScrolls(t *testing.T) {
	d := newTestPropertiesDialog(40)
	inner := d.InnerRect()
	dataH := inner.H - 3
	if len(d.rows) <= dataH {
		t.Fatalf("test needs more rows than dataH (%d) to exercise scrolling, got %d", dataH, len(d.rows))
	}

	sbX := d.Rect().Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+2+dataH-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if d.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if !d.Visible() {
		t.Error("clicking the scrollbar must not close the dialog")
	}

	d.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+2, tcell.ButtonNone, tcell.ModNone))
	if d.sbDragging {
		t.Error("sbDragging should reset on release")
	}
}

// TestPropertiesDialogWheelScrolls confirms the mouse wheel scrolls the row
// list, matching every sibling dialog (HelpDialog, KeyDiagnosticsDialog,
// QueryListDialog, TasksDialog) — previously only Up/Down keys scrolled it.
func TestPropertiesDialogWheelScrolls(t *testing.T) {
	d := newTestPropertiesDialog(40)
	inner := d.InnerRect()
	dataH := inner.H - 3
	if len(d.rows) <= dataH {
		t.Fatalf("test needs more rows than dataH (%d) to exercise scrolling, got %d", dataH, len(d.rows))
	}

	if !d.HandleMouse(tcell.NewEventMouse(inner.X+1, inner.Y+3, tcell.WheelDown, tcell.ModNone)) {
		t.Fatal("HandleMouse with WheelDown should be handled")
	}
	if d.scroll != 1 {
		t.Errorf("scroll = %d after one WheelDown, want 1", d.scroll)
	}

	d.HandleMouse(tcell.NewEventMouse(inner.X+1, inner.Y+3, tcell.WheelUp, tcell.ModNone))
	if d.scroll != 0 {
		t.Errorf("scroll = %d after WheelUp, want 0", d.scroll)
	}

	// WheelUp at scroll 0 must not go negative.
	d.HandleMouse(tcell.NewEventMouse(inner.X+1, inner.Y+3, tcell.WheelUp, tcell.ModNone))
	if d.scroll != 0 {
		t.Errorf("scroll = %d after WheelUp at 0, want 0 (clamped)", d.scroll)
	}
}

// TestPropertiesDialogWheelDoesNotScrollPastEnd confirms WheelDown stops
// advancing once the last row is already visible.
func TestPropertiesDialogWheelDoesNotScrollPastEnd(t *testing.T) {
	d := newTestPropertiesDialog(5) // fewer rows than dataH: nothing to scroll
	inner := d.InnerRect()

	d.HandleMouse(tcell.NewEventMouse(inner.X+1, inner.Y+3, tcell.WheelDown, tcell.ModNone))
	if d.scroll != 0 {
		t.Errorf("scroll = %d, want 0 (all rows already fit, nothing to scroll)", d.scroll)
	}
}
