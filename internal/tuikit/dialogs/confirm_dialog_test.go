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
