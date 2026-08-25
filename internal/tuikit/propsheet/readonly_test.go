package propsheet

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestAReadOnlyFormCannotBeEdited. The point is not that editing is
// discouraged: nothing on the form can take focus, so no row can be typed
// into or toggled, so the form can never become dirty — which is what makes
// Apply and Script Changes have nothing to send.
func TestAReadOnlyFormCannotBeEdited(t *testing.T) {
	text := Text("Name", "srv01", 20)
	check := Check("Enabled", false)
	f := NewForm(Section("Settings"), text, check)
	f.SetBounds(0, 0, 60, 10)

	f.SetReadOnly(true)
	f.Focus(true)

	if f.Focused() != nil {
		t.Error("a row took focus on a read-only form")
	}
	if f.FocusNext() || f.FocusPrev() {
		t.Error("focus moved into a read-only form")
	}
	// Tab must be refused rather than swallowed, so the sheet can move focus
	// on to the button row — the keyboard-trap rule.
	if f.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone)) {
		t.Error("a read-only form claimed Tab, trapping focus on itself")
	}
	if f.Dirty() {
		t.Error("a read-only form reported itself dirty")
	}

	// And the values are still there to read.
	if text.Value() != "srv01" {
		t.Errorf("value = %q, want the form still readable", text.Value())
	}

	f.SetReadOnly(false)
	if !f.FocusNext() {
		t.Error("clearing read-only did not give the rows their focus back")
	}
}

// TestAReadOnlyPageDropsOKAndApply. A greyed OK reads as "not yet"; there is
// no OK to press at all here, and Script Changes is exactly what a login
// without the rights to run the statements wants.
func TestAReadOnlyPageDropsOKAndApply(t *testing.T) {
	p := newTestSheet("General", "Memory")
	var page, seq int
	p.OnLoadPage = func(pg, sq int) { page, seq = pg, sq }
	p.Show()

	// Page 0 is writable.
	p.SetPageForm(page, seq, NewForm(Static("Name", "srv01")))
	if got := strings.Join(p.buttonLabels(), ","); got != "OK,Cancel,Apply,Script Changes" {
		t.Fatalf("writable page buttons = %q", got)
	}

	p.SelectPage(1)
	f := NewForm(Text("Maximum server memory", "2048", 12))
	p.SetPageReadOnly(page, seq, "Read-only: requires ALTER SETTINGS (serveradmin).")
	p.SetPageForm(page, seq, f)

	got := strings.Join(p.buttonLabels(), ",")
	if got != "Close,Script Changes" {
		t.Errorf("read-only page buttons = %q, want %q", got, "Close,Script Changes")
	}
	if !f.ReadOnly() {
		t.Error("the form on a read-only page is still editable")
	}
	if note, ok := f.Rows()[0].(interface{ Text() string }); !ok ||
		!strings.Contains(note.Text(), "ALTER SETTINGS") {
		t.Error("the read-only reason is not the first row of the page")
	}

	// Back on the writable page the full row returns, and the button focus
	// index carried over from the two-button row cannot point past its end.
	p.SelectPage(0)
	if got := strings.Join(p.buttonLabels(), ","); got != "OK,Cancel,Apply,Script Changes" {
		t.Errorf("buttons = %q after returning to a writable page", got)
	}
}

// TestReadOnlyButtonsRunTheActionTheyAreLabelled. The two rows are different
// lengths, so an index-based dispatch runs OK when the user pressed Close —
// which on a Properties dialog means writing.
func TestReadOnlyButtonsRunTheActionTheyAreLabelled(t *testing.T) {
	p := newTestSheet("Memory")
	var page, seq int
	p.OnLoadPage = func(pg, sq int) { page, seq = pg, sq }
	okCalls, applyCalls, scriptCalls := 0, 0, 0
	p.OnOK = func() { okCalls++ }
	p.OnApply = func() { applyCalls++ }
	p.OnScript = func() { scriptCalls++ }
	p.Show()

	p.SetPageReadOnly(page, seq, "Read-only: requires ALTER SETTINGS (serveradmin).")
	p.SetPageForm(page, seq, NewForm(Static("Physical memory (MB)", "16384")))

	p.activateButton(0) // Close
	if okCalls != 0 || applyCalls != 0 {
		t.Errorf("Close wrote: OnOK ran %d times, OnApply %d", okCalls, applyCalls)
	}
	if p.Visible() {
		t.Error("Close did not close the sheet")
	}

	p.Show()
	p.SetPageReadOnly(page, seq+1, "Read-only.")
	p.SetPageForm(page, seq+1, NewForm(Static("Physical memory (MB)", "16384")))
	p.activateButton(1) // Script Changes
	if scriptCalls != 1 {
		t.Errorf("Script Changes ran OnScript %d times, want 1", scriptCalls)
	}
}
