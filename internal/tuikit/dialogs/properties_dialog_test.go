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
// scrollbar scrolls its row list instead of being silently ignored, the
// same as every sibling scrollable dialog in this package.
func TestPropertiesDialogScrollbarDragScrolls(t *testing.T) {
	d := newTestPropertiesDialog(40)
	inner := d.InnerRect()
	dataH := propsDataH(inner)
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

// TestPropertiesDialogRowsClearButtonRow pins the row capacity against the
// chrome below it: the last drawable row must sit above the separator, or
// DrawSeparator/DrawButtons paint over it and the scroll clamp never brings
// it into view.
func TestPropertiesDialogRowsClearButtonRow(t *testing.T) {
	d := newTestPropertiesDialog(40)
	inner := d.InnerRect()
	lastY := inner.Y + 2 + propsDataH(inner) - 1
	sepY := d.ButtonRowY() - 1
	if lastY >= sepY {
		t.Errorf("last row y = %d overlaps separator y = %d", lastY, sepY)
	}
}

// TestPropertiesDialogScrollReachesLastRow confirms the scroll clamp lets
// every row become visible, including the last.
func TestPropertiesDialogScrollReachesLastRow(t *testing.T) {
	d := newTestPropertiesDialog(40)
	dataH := propsDataH(d.InnerRect())
	for range len(d.rows) {
		d.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	if d.scroll+dataH != len(d.rows) {
		t.Errorf("scroll = %d with dataH %d over %d rows; last row never reaches the viewport", d.scroll, dataH, len(d.rows))
	}
}

// TestPropertiesDialogSizedWidensKeyColumn confirms ShowPropertiesSized
// widens the Property column to fit the longest key, and that a plain
// ShowProperties afterwards returns the dialog to its default size.
func TestPropertiesDialogSizedWidensKeyColumn(t *testing.T) {
	scr := &sizedScreen{w: 120, h: 50}
	d := NewPropertiesDialog(scr)
	medium := "A Key Of Twenty-Eight Chars." // 28 columns
	d.ShowPropertiesSized("Big", []PropertyRow{
		PropertySection("A Section Caption Longer Than Any Key Here"),
		{Key: medium, Value: "v"},
	}, 90, 20)
	if d.keyW != len(medium) {
		t.Errorf("keyW = %d, want %d (widened to the longest key, sections excluded)", d.keyW, len(medium))
	}
	if got := d.Rect().W; got != 90 {
		t.Errorf("dialog width = %d, want 90", got)
	}

	d.ShowProperties("Small", []PropertyRow{{Key: "K", Value: "V"}})
	if got := d.Rect().W; got != propsDefaultW {
		t.Errorf("dialog width = %d after ShowProperties, want the default %d", got, propsDefaultW)
	}
	if d.keyW != propsKeyW {
		t.Errorf("keyW = %d after ShowProperties, want the default %d", d.keyW, propsKeyW)
	}
}

// TestPropertiesDialogWheelScrolls confirms the mouse wheel scrolls the row
// list, matching every sibling dialog (HelpDialog, KeyDiagnosticsDialog,
// QueryListDialog, TasksDialog) — previously only Up/Down keys scrolled it.
func TestPropertiesDialogWheelScrolls(t *testing.T) {
	d := newTestPropertiesDialog(40)
	inner := d.InnerRect()
	dataH := propsDataH(inner)
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
