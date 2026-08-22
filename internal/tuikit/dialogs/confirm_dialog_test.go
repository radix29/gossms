package dialogs

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestConfirmDialog(t *testing.T) *ConfirmDialog {
	t.Helper()
	return NewConfirmDialog(nil)
}

// Escape on a plain Yes/No prompt still answers No, as it always has —
// every ShowConfirm caller asks a question whose No is the harmless answer.
func TestConfirmDialogEscapeAnswersNo(t *testing.T) {
	d := newTestConfirmDialog(t)
	var got *bool
	d.ShowConfirm("Discard Changes", "Discard your changes?", func(c bool) { got = &c })

	d.HandleKey(key(tcell.KeyEscape))

	if d.Visible() {
		t.Fatal("dialog stayed open after Escape")
	}
	if got == nil {
		t.Fatal("callback never fired")
	}
	if *got {
		t.Error("Escape answered Yes, want No")
	}
}

// The three-way prompt exists because "No" can itself be destructive —
// Close Query's No discards unsaved SQL. Escape must pick neither.
func TestConfirmCancelEscapeAnswersCancel(t *testing.T) {
	d := newTestConfirmDialog(t)
	var got *ConfirmAnswer
	d.ShowConfirmCancel("Close Query", "Query 1 has unsaved changes. Save before closing?",
		func(a ConfirmAnswer) { got = &a })

	d.HandleKey(key(tcell.KeyEscape))

	if d.Visible() {
		t.Fatal("dialog stayed open after Escape")
	}
	if got == nil {
		t.Fatal("callback never fired")
	}
	if *got != ConfirmCancel {
		t.Errorf("Escape answered %v, want ConfirmCancel — answering No here discards the query", *got)
	}
}

func TestConfirmCancelEnterAnswersFocusedButton(t *testing.T) {
	answers := []ConfirmAnswer{ConfirmYes, ConfirmNo, ConfirmCancel}
	for i, want := range answers {
		d := newTestConfirmDialog(t)
		var got *ConfirmAnswer
		d.ShowConfirmCancel("Close Query", "Save?", func(a ConfirmAnswer) { got = &a })
		for range i {
			d.HandleKey(key(tcell.KeyTab))
		}
		d.HandleKey(key(tcell.KeyEnter))
		if got == nil {
			t.Fatalf("%d tabs then Enter: callback never fired", i)
		}
		if *got != want {
			t.Errorf("%d tabs then Enter answered %v, want %v", i, *got, want)
		}
	}
}

// Tab has to wrap over three buttons, not the two the dialog used to have.
func TestConfirmCancelTabWrapsOverThreeButtons(t *testing.T) {
	d := newTestConfirmDialog(t)
	d.ShowConfirmCancel("Close Query", "Save?", func(ConfirmAnswer) {})
	for range 3 {
		d.HandleKey(key(tcell.KeyTab))
	}
	if d.btnFocus != 0 {
		t.Errorf("btnFocus after three Tabs = %d, want 0 (wrapped)", d.btnFocus)
	}
	d.HandleKey(key(tcell.KeyLeft))
	if d.btnFocus != 2 {
		t.Errorf("btnFocus after Left from the first button = %d, want 2 (wrapped to Cancel)", d.btnFocus)
	}
}

// finish clears the handler before running it, so a handler that re-shows
// the dialog (the quit chain walks one dirty panel per prompt) installs its
// own rather than being fired twice by the stale one.
func TestConfirmDialogHandlerFiresOncePerShowing(t *testing.T) {
	d := newTestConfirmDialog(t)
	calls := 0
	d.ShowConfirm("Discard Changes", "Discard?", func(bool) { calls++ })

	d.HandleKey(key(tcell.KeyEscape))
	d.HandleKey(key(tcell.KeyEscape)) // dialog is hidden; must be a no-op

	if calls != 1 {
		t.Errorf("handler fired %d times, want 1", calls)
	}
}

// ShowConfirmOption's checkbox is a modifier on the answer, so what the caller
// gets has to be the state the box was actually in — not the initial value it
// was shown with.
func TestConfirmOptionReportsTheCheckboxState(t *testing.T) {
	for _, c := range []struct {
		name    string
		initial bool
		toggle  bool
		want    bool
	}{
		{"left as shown", false, false, false},
		{"ticked by the user", false, true, true},
		{"unticked by the user", true, true, false},
		{"pre-ticked and left alone", true, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := newTestConfirmDialog(t)
			var confirmed, checked bool
			var fired bool
			d.ShowConfirmOption("Delete Table", "Delete dbo.Orders?", "Also drop referencing foreign keys",
				c.initial, func(ok, ch bool) { confirmed, checked, fired = ok, ch, true })

			if c.toggle {
				d.HandleKey(key(tcell.KeyTab)) // No
				d.HandleKey(key(tcell.KeyTab)) // the checkbox
				d.HandleKey(rn(' '))
				d.HandleKey(key(tcell.KeyTab)) // back to Yes
			}
			d.HandleKey(key(tcell.KeyEnter))

			if !fired {
				t.Fatal("callback never fired")
			}
			if !confirmed {
				t.Errorf("confirmed = false, want true — Enter was on Yes")
			}
			if checked != c.want {
				t.Errorf("checked = %v, want %v", checked, c.want)
			}
		})
	}
}

// Enter while the checkbox has focus must toggle it, not answer: the user is
// still setting the modifier, and answering here would commit whatever state
// it happened to be in.
func TestConfirmOptionEnterOnTheCheckboxDoesNotAnswer(t *testing.T) {
	d := newTestConfirmDialog(t)
	fired := false
	d.ShowConfirmOption("Delete Table", "Delete dbo.Orders?", "Cascade", false,
		func(bool, bool) { fired = true })

	d.HandleKey(key(tcell.KeyTab))
	d.HandleKey(key(tcell.KeyTab)) // on the checkbox
	d.HandleKey(key(tcell.KeyEnter))

	if fired {
		t.Fatal("Enter on the checkbox answered the question")
	}
	if !d.Visible() {
		t.Fatal("dialog closed")
	}
	// ...and it toggled, which is what Enter means to a checkbox.
	if !d.option.Checked() {
		t.Error("Enter on the checkbox did not toggle it")
	}
}

// A checkbox must not survive into the next showing of the same dialog — the
// same rule as ModalDialog's drag latches. ConfirmDialog is a single shared
// instance, so a Delete Table showing followed by any ordinary confirm would
// otherwise draw a stray checkbox and put the keyboard on it.
func TestConfirmOptionDoesNotSurviveTheNextShowing(t *testing.T) {
	d := newTestConfirmDialog(t)
	d.ShowConfirmOption("Delete Table", "Delete dbo.Orders?", "Cascade", true, func(bool, bool) {})
	d.HandleKey(key(tcell.KeyEscape))

	var got *bool
	d.ShowConfirm("Discard Changes", "Discard your changes?", func(c bool) { got = &c })
	if d.option != nil {
		t.Fatal("the previous showing's checkbox is still installed")
	}
	// Two Tabs on a two-button prompt with no checkbox come back to Yes.
	d.HandleKey(key(tcell.KeyTab))
	d.HandleKey(key(tcell.KeyTab))
	d.HandleKey(key(tcell.KeyEnter))
	if got == nil || !*got {
		t.Errorf("answer = %v, want Yes — the focus cycle is one step longer than the buttons", got)
	}
}

// A release that lands outside the dialog still has to reach the checkbox.
// ConsumeOutsideClick answers true for every event outside the rect, so the
// early return it drives skips the widget — and CheckBox clears its
// mouseDragging latch only on an event it actually sees. Left set, the latch
// swallows the user's next press as a continuation of the finished gesture:
// press the box, drift off the dialog, release, and the next click on it does
// nothing at all.
func TestConfirmOptionReleaseOutsideDoesNotStrandTheLatch(t *testing.T) {
	d := NewConfirmDialog(&sizedScreen{w: 200, h: 50})
	d.ShowConfirmOption("Delete Table", "Delete dbo.Orders?", "Cascade", false, func(bool, bool) {})
	// Where Draw puts it; laying it out by hand keeps this about the routing.
	inner := d.InnerRect()
	d.option.SetBounds(inner.X+1, inner.Y+2+len(d.msgLines)+1)
	x, y := d.option.RectX()+1, d.option.RectY()

	d.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	if !d.option.Checked() {
		t.Fatal("the press did not toggle the checkbox")
	}
	// Drift off the dialog with the button still down, then release there.
	outX, outY := d.Rect().X-2, d.Rect().Y-2
	d.HandleMouse(tcell.NewEventMouse(outX, outY, tcell.Button1, tcell.ModNone))
	d.HandleMouse(tcell.NewEventMouse(outX, outY, tcell.ButtonNone, tcell.ModNone))

	d.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	if d.option.Checked() {
		t.Error("the next press on the checkbox was swallowed — the drag latch survived a release outside the dialog")
	}
}
