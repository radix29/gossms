package dialogs

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// dragToButtonRow presses inside f, drags two rows down (the direction the
// button row is in), then sweeps the whole button row still holding Button1.
// It reports the selection the off-row motion produced, and fails the moment
// the dialog stops being visible — a button pressed by a gesture that started
// in a text field.
func dragToButtonRow(t *testing.T, d interface {
	HandleMouse(*tcell.EventMouse) bool
	Visible() bool
	ButtonRowY() int
	Rect() core.Rect
}, f *widgets.InputField) string {
	t.Helper()
	ix, y := f.InputX(), f.RectY()
	if !d.HandleMouse(tcell.NewEventMouse(ix+1, y, tcell.Button1, tcell.ModNone)) {
		t.Fatal("the press inside the field was refused — test premise is wrong")
	}
	d.HandleMouse(tcell.NewEventMouse(ix+5, y+2, tcell.Button1, tcell.ModNone))
	got := f.SelectedText()

	by, r := d.ButtonRowY(), d.Rect()
	if by <= y {
		t.Fatalf("button row at y=%d is not below the field at y=%d — test premise is wrong", by, y)
	}
	for mx := r.X; mx < r.X+r.W; mx++ {
		d.HandleMouse(tcell.NewEventMouse(mx, by, tcell.Button1, tcell.ModNone))
		if !d.Visible() {
			t.Fatalf("a button at x=%d fired during a text-selection drag", mx)
		}
	}
	return got
}

// Both dialogs below hit-tested every Button1 against the field's own rect and
// asked ButtonClicked first, so a selection drag froze the moment it left the
// field's row and, on reaching the button row, pressed the button under the
// pointer. On PromptDialog that is OK, which accepts the value — the rename it
// was opened for. On TypedConfirmDialog it is Confirm, which answers a
// destructive confirmation the retyping exists to slow down.

func TestPromptDialogDragOutOfTheFieldNeitherFreezesNorPressesAButton(t *testing.T) {
	d := NewPromptDialog(&sizedScreen{w: 120, h: 40})
	accepted := 0
	d.ShowPrompt("Rename", "New name:", "Name:", "abcdefgh", func(string) { accepted++ })
	inner := d.InnerRect()
	d.input.SetBounds(inner.X+1, inner.Y+2)

	if got := dragToButtonRow(t, d, d.input); got != "abcd" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the field's row", got, "abcd")
	}
	if accepted != 0 {
		t.Errorf("OnAccept fired %d times during a text-selection drag", accepted)
	}
}

func TestTypedConfirmDialogDragOutOfTheFieldNeitherFreezesNorPressesAButton(t *testing.T) {
	d := NewTypedConfirmDialog(&sizedScreen{w: 120, h: 40})
	answers := 0
	d.ShowTypedConfirm("Confirm Drop", `Type "Orders" to confirm.`, "Orders",
		func(bool) { answers++ })
	inner := d.InnerRect()
	d.input.SetBounds(inner.X+1, inner.Y+3)
	// The matching text, so a button pressed mid-drag really would confirm.
	d.input.SetValue("Orders")

	if got := dragToButtonRow(t, d, d.input); got != "Orde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the field's row", got, "Orde")
	}
	if answers != 0 {
		t.Errorf("the confirmation was answered %d times during a text-selection drag", answers)
	}
}

// Each dialog rebuilds its input on every showing, so a gesture held from the
// last one points at a widget that is no longer on screen.
func TestPromptAndTypedConfirmClearTheGestureOnEveryShowing(t *testing.T) {
	p := NewPromptDialog(&sizedScreen{w: 120, h: 40})
	p.ShowPrompt("Rename", "New name:", "Name:", "abcdefgh", nil)
	inner := p.InnerRect()
	p.input.SetBounds(inner.X+1, inner.Y+2)
	p.HandleMouse(tcell.NewEventMouse(p.input.InputX()+1, p.input.RectY(), tcell.Button1, tcell.ModNone))
	if p.drag.Field() == nil {
		t.Fatal("the press did not arm a latch — test premise is wrong")
	}
	p.ShowPrompt("Rename", "New name:", "Name:", "ijklmnop", nil)
	if p.drag.Field() != nil {
		t.Error("ShowPrompt left a gesture armed from the previous showing")
	}

	c := NewTypedConfirmDialog(&sizedScreen{w: 120, h: 40})
	c.ShowTypedConfirm("Confirm", "msg", "Orders", nil)
	inner = c.InnerRect()
	c.input.SetBounds(inner.X+1, inner.Y+3)
	c.HandleMouse(tcell.NewEventMouse(c.input.InputX()+1, c.input.RectY(), tcell.Button1, tcell.ModNone))
	if c.drag.Field() == nil {
		t.Fatal("the press did not arm a latch — test premise is wrong")
	}
	c.ShowTypedConfirm("Confirm", "msg", "Orders", nil)
	if c.drag.Field() != nil {
		t.Error("ShowTypedConfirm left a gesture armed from the previous showing")
	}
}
