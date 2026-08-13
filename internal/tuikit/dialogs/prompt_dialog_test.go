package dialogs

import (
	"errors"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func showTestPrompt(onAccept func(string)) *PromptDialog {
	d := NewPromptDialog(nil)
	d.ShowPrompt("Rename Table", "New name for table \"dbo.Orders\":", "Name:", "Orders", onAccept)
	return d
}

// The initial value comes back selected, so typing replaces it — the rename
// case, where the current name is what the user is editing away from.
func TestPromptDialogSeedsAndSelectsInitial(t *testing.T) {
	d := showTestPrompt(nil)
	if got := d.Value(); got != "Orders" {
		t.Errorf("Value() = %q, want Orders", got)
	}
	if !d.input.HasSelection() {
		t.Error("initial value is not selected — the first keystroke would append, not replace")
	}
	d.HandleKey(tcell.NewEventKey(tcell.KeyRune, "X", tcell.ModNone))
	if got := d.Value(); got != "X" {
		t.Errorf("after typing X, Value() = %q, want X", got)
	}
}

func TestPromptDialogAcceptsOnEnter(t *testing.T) {
	var got string
	calls := 0
	d := showTestPrompt(func(v string) { got, calls = v, calls+1 })
	d.input.SetValue("  Invoices  ")
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if calls != 1 {
		t.Fatalf("OnAccept called %d times, want 1", calls)
	}
	if got != "Invoices" {
		t.Errorf("accepted %q, want the trimmed value", got)
	}
	if d.Visible() {
		t.Error("dialog stayed open after accepting")
	}
}

// Cancel and Escape report nothing: a dialog the user backed out of must not
// look like an accepted empty value.
func TestPromptDialogCancelReportsNothing(t *testing.T) {
	calls := 0
	d := showTestPrompt(func(string) { calls++ })
	d.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if calls != 0 {
		t.Errorf("OnAccept called %d times on Escape, want 0", calls)
	}
	if d.Visible() {
		t.Error("Escape left the dialog open")
	}

	d = showTestPrompt(func(string) { calls++ })
	d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone)) // OK
	d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone)) // Cancel
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if calls != 0 {
		t.Errorf("OnAccept called %d times on Cancel, want 0", calls)
	}
}

// A rejected value keeps the dialog open with the reason shown, rather than
// closing and silently doing nothing.
func TestPromptDialogRefusesEmptyAndInvalidValues(t *testing.T) {
	calls := 0
	d := showTestPrompt(func(string) { calls++ })
	d.input.SetValue("   ")
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if calls != 0 || !d.Visible() {
		t.Fatalf("empty value: calls=%d visible=%v, want 0/true", calls, d.Visible())
	}
	if d.status == "" {
		t.Error("empty value gave no reason")
	}

	d.Validate = func(v string) error { return errors.New("already exists") }
	d.input.SetValue("Orders")
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if calls != 0 || !d.Visible() {
		t.Fatalf("rejected value: calls=%d visible=%v, want 0/true", calls, d.Visible())
	}
	if d.status != "already exists" {
		t.Errorf("status = %q, want the validator's message", d.status)
	}

	d.Validate = nil
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if calls != 1 {
		t.Errorf("OnAccept called %d times once valid, want 1", calls)
	}
}

// Each showing starts clean: a Validate left over from the previous one
// would reject values it knows nothing about.
func TestPromptDialogClearsValidateBetweenShowings(t *testing.T) {
	d := showTestPrompt(nil)
	d.Validate = func(string) error { return errors.New("stale") }
	d.ShowPrompt("Rename View", "New name:", "Name:", "vOrders", nil)
	if d.Validate != nil {
		t.Error("Validate survived into the next showing")
	}
	if d.status != "" {
		t.Errorf("status = %q, want it cleared", d.status)
	}
}
