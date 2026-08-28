package propsheet

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/widgets"
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

// drawRow lays a single row out at the top-left of a 60-column screen and
// returns what it painted on its first line.
func drawRow(row Row, readOnly bool) string {
	f := NewForm(row)
	f.SetBounds(0, 0, 60, 10)
	f.SetReadOnly(readOnly)
	s := &fakeScreen{w: 60, h: 10}
	f.Draw(s)
	return s.line(0)
}

// TestAReadOnlyRowDrawsNoControl. Behaviour and appearance are separate halves
// of read-only: SetReadOnly already made the page impossible to edit, and a row
// still drawing its input box or its [ ] reads as a field the terminal is
// refusing to type into. Each case below fails against a row that keeps its
// control.
func TestAReadOnlyRowDrawsNoControl(t *testing.T) {
	cases := []struct {
		name       string
		row        Row
		wantSubstr string   // what the value must still say
		wantAbsent []string // control chrome that must be gone
	}{
		{"text", Text("Name", "srv01", 20), "srv01", []string{"[", "]"}},
		{"int with unit", Int("Maximum server memory", 2048, 0, 9999, "MB"), "2048 MB", []string{"[", "]"}},
		{"password", Password("Password", 20), "Password", []string{"[", "]", "*"}},
		{"check on", Check("Auto shrink", true), "✓ Auto shrink", []string{"[", "]"}},
		{"check off", Check("Auto shrink", false), "✗ Auto shrink", []string{"[", "]"}},
		{"select", Select("Recovery model", []string{"FULL", "SIMPLE"}, 1), "SIMPLE", []string{"[", "]", "▾", "v]"}},
		{"radio", Radio("Restrict access", []string{"MULTI_USER", "SINGLE_USER"}, 1), "SINGLE_USER", []string{"(o)", "( )"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := drawRow(c.row, true)
			if !strings.Contains(got, c.wantSubstr) {
				t.Errorf("read-only draw = %q, want it to say %q", got, c.wantSubstr)
			}
			for _, bad := range c.wantAbsent {
				if strings.Contains(got, bad) {
					t.Errorf("read-only draw = %q, still draws control chrome %q", got, bad)
				}
			}
		})
	}
}

// TestAWritableRowStillDrawsItsControl is the other half: the read-only
// rendering must be a state the row leaves, not a one-way switch, or a page
// shared between a gated and an ungated connection comes up unusable.
func TestAWritableRowStillDrawsItsControl(t *testing.T) {
	text := Text("Name", "srv01", 20)
	f := NewForm(text)
	f.SetBounds(0, 0, 60, 10)

	f.SetReadOnly(true)
	f.SetReadOnly(false)

	s := &fakeScreen{w: 60, h: 10}
	f.Draw(s)
	if got := s.line(0); !strings.Contains(got, "[") {
		t.Errorf("draw after clearing read-only = %q, want the input box back", got)
	}
}

// TestARowAddedToAReadOnlyFormDrawsReadOnly pins the propagation in Add and
// Prepend: a page whose rows are built after the gate has decided would
// otherwise draw them editable.
func TestARowAddedToAReadOnlyFormDrawsReadOnly(t *testing.T) {
	f := NewForm()
	f.SetBounds(0, 0, 60, 10)
	f.SetReadOnly(true)
	f.Add(Text("Name", "srv01", 20))

	s := &fakeScreen{w: 60, h: 10}
	f.Draw(s)
	if got := s.line(0); strings.Contains(got, "[") {
		t.Errorf("row added to a read-only form = %q, want no input box", got)
	}
}

// TestAReadOnlyRadioRowIsOneLine. Collapsing the group to its selected value
// changes the row's height, so Height has to report it — Form lays rows out
// from Height every frame, and a row claiming three lines while drawing one
// leaves two blank lines mid-page.
func TestAReadOnlyRadioRowIsOneLine(t *testing.T) {
	r := Radio("Restrict access", []string{"MULTI_USER", "SINGLE_USER", "RESTRICTED_USER"}, 0)
	before := r.Height(60)
	r.SetDrawReadOnly(true)
	if before <= 1 {
		t.Fatalf("editable radio height = %d, want more than one line", before)
	}
	if got := r.Height(60); got != 1 {
		t.Errorf("read-only radio height = %d, want 1", got)
	}
}

// TestAReadOnlyToggleGridDrawsTicksNotCheckboxes. The toggle columns are the
// last thing on a gated page that looked like a control.
func TestAReadOnlyToggleGridDrawsTicksNotCheckboxes(t *testing.T) {
	g := newTestToggleGrid()
	g.SetDrawReadOnly(true)

	if got := g.Grid.Row(1); got[1] != "✓" || got[2] != "✗" {
		t.Fatalf("read-only toggle cells = %v, want [✓ ✗]", got[1:])
	}

	if got := g.Grid.Row(1); got[1] != "✓" || got[2] != "✗" {
		t.Fatalf("read-only toggle cells = %v, want [✓ ✗]", got[1:])
	}
	if got := g.Grid.Row(1)[0]; got != "Processor 1" {
		t.Errorf("text column = %q, want it untouched", got)
	}

	g.SetDrawReadOnly(false)
	if got := g.Grid.Row(1); got[1] != "[x]" || got[2] != "[ ]" {
		t.Errorf("toggle cells after clearing read-only = %v, want [[x] [ ]]", got[1:])
	}
}

// TestAReadOnlyEditorRowKeepsThePagesOwnReadOnly. A job step whose command is
// not T-SQL is read-only for its own reason; clearing the form's gate must not
// make it editable.
func TestAReadOnlyEditorRowKeepsThePagesOwnReadOnly(t *testing.T) {
	ed := controls.NewEditor(nil)
	ed.SetReadOnly(true) // the page's own guard
	r := NewEditorRow("Command", ed, 8)

	r.SetDrawReadOnly(true)
	r.SetDrawReadOnly(false)
	if !ed.ReadOnly() {
		t.Error("clearing the form's gate cleared the page's own read-only guard")
	}

	plain := controls.NewEditor(nil)
	pr := NewEditorRow("Command", plain, 8)
	pr.SetDrawReadOnly(true)
	if !plain.ReadOnly() {
		t.Error("a read-only form left its editor row editable")
	}
	pr.SetDrawReadOnly(false)
	if plain.ReadOnly() {
		t.Error("clearing read-only left the editor row uneditable")
	}
}

// TestAReadOnlyToggleGridStillDrawsItsRows. Form.SetReadOnly runs before the
// sheet has laid the grid out, and re-rendering through SetDataPreservingView
// there restores a selection against a zero rect: ensureVisible scrolls past
// every row and the grid draws its header over blank lines. Reproduced live on
// Server Properties > Processors, whose affinity grid reported "4 rows" and
// showed none. A grid laid out first hides this, so this test must not lay it
// out — which is exactly how a Properties page builds one.
func TestAReadOnlyToggleGridStillDrawsItsRows(t *testing.T) {
	g := NewToggleGrid([]string{"CPU", "Affinity"}, []int{1}, 10)
	g.SetRows(
		[][]string{{"Processor 0"}, {"Processor 1"}},
		[][]bool{{false}, {true}},
	)

	f := NewForm(g)
	f.SetBounds(0, 0, 60, 20)
	f.SetReadOnly(true)

	s := &fakeScreen{w: 60, h: 20}
	f.Draw(s)
	// Line 0 is the header, line 1 the separator, so the rows start at 2.
	if got := s.line(2); !strings.Contains(got, "Processor 0") {
		t.Errorf("first grid line = %q, want the grid's first row", got)
	}
	if got := s.line(3); !strings.Contains(got, "Processor 1") {
		t.Errorf("second grid line = %q, want the grid's second row", got)
	}
}

// TestFlatValueStartsWhereAnEditableValueDoes. A page that toggles read-only
// draws the same row two ways, and the two have to line up: a flat value one
// column short of the editable text makes every value on the page jog sideways
// when the permission gate closes.
func TestFlatValueStartsWhereAnEditableValueDoes(t *testing.T) {
	const x = 4
	// Built exactly as TextRow builds its field, and positioned as its Layout
	// positions it.
	f := widgets.NewInputField(core.PadRight("Name", LabelWidth), 20, false)
	f.SetBounds(x, 0)

	// InputX is the '[' column; the text starts one past it.
	if got, want := flatValueX(x), f.InputX()+1; got != want {
		t.Errorf("flat value column = %d, editable text column = %d", got, want)
	}
}

// TestAPageReadOnlyRowRefusesEveryEditPath. A page gating one row of an
// otherwise editable form (a job step of a subsystem the page cannot write) has
// to close every way in, not just the drawn one: Edit is the path a test takes,
// HandleKey the path a keystroke takes, and Focusable is what stops the row
// being reachable at all. A row that only *draws* flat still goes dirty and
// still gets written.
func TestAPageReadOnlyRowRefusesEveryEditPath(t *testing.T) {
	text := Text("Step name", "Notify ops", 20)
	sel := Select("Database", []string{"(unchanged)", "appdb", "salesdb"}, 0)

	text.SetReadOnly(true)
	sel.SetReadOnly(true)

	text.Edit("Renamed")
	sel.Edit(2)
	if text.Value() != "Notify ops" || sel.Selected() != 0 {
		t.Errorf("Edit went through the page's gate: %q / %d", text.Value(), sel.Selected())
	}
	// The field is focused by hand: it ignores keys it is not focused for, so
	// a row whose gate is missing would still look inert here.
	text.field.Focus(true)
	sel.dd.Focus(true)
	if text.HandleKey(tcell.NewEventKey(tcell.KeyRune, "X", tcell.ModNone)) {
		t.Error("a page-read-only text row claimed a keystroke")
	}
	// Enter, not Down: a closed dropdown lets Up/Down fall through on purpose,
	// so only the key that opens the list shows the gate holding.
	if sel.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) || sel.OverlayActive() {
		t.Error("a page-read-only select row opened its list")
	}
	if text.Value() != "Notify ops" || sel.Selected() != 0 {
		t.Errorf("a keystroke edited a page-read-only row: %q / %d", text.Value(), sel.Selected())
	}
	if text.Focusable() || sel.Focusable() {
		t.Error("a page-read-only row is still focusable")
	}
	if text.Dirty() || sel.Dirty() {
		t.Error("a page-read-only row went dirty")
	}
	// And it looks the part, on a form with no gate of its own: an input box
	// the terminal refuses to type into reads as a broken terminal.
	for _, c := range []struct {
		name string
		row  Row
	}{{"text", text}, {"select", sel}} {
		if got := drawRow(c.row, false); strings.Contains(got, "[") {
			t.Errorf("page-read-only %s row = %q, still draws its control", c.name, got)
		}
	}

	// A gate applied over an already-open list must close it: the overlay is
	// drawn last and takes every event first, so one left open floats over a
	// row nothing routes to any more.
	open := Select("Database", []string{"appdb", "salesdb"}, 0)
	open.dd.Focus(true)
	open.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if !open.OverlayActive() {
		t.Fatal("the dropdown did not open — the rest of this case proves nothing")
	}
	open.SetReadOnly(true)
	if open.OverlayActive() {
		t.Error("gating a select row left its list open")
	}

	text.SetReadOnly(false)
	sel.SetReadOnly(false)
	text.Edit("Renamed")
	sel.Edit(2)
	if text.Value() != "Renamed" || sel.Selected() != 2 {
		t.Errorf("lifting the page's gate left the row uneditable: %q / %d", text.Value(), sel.Selected())
	}
}

// TestThePagesGateAndTheFormsGateAreIndependent. Whichever is set last must not
// cancel the other out: lifting a permission gate must not make a non-T-SQL job
// step editable, and a page gating one row must not make the whole form
// editable when the permission gate is on. EditorRow's counterpart is above.
func TestThePagesGateAndTheFormsGateAreIndependent(t *testing.T) {
	text := Text("Step name", "Notify ops", 20)
	f := NewForm(text)
	f.SetBounds(0, 0, 60, 10)

	text.SetReadOnly(true)
	f.SetReadOnly(true)
	f.SetReadOnly(false)
	if !text.ReadOnly() || text.Focusable() {
		t.Error("clearing the form's gate cleared the page's own")
	}

	// Asked through the form, not the row: the form's gate is enforced in
	// Form.focusableAt, so a row's own Focusable() knows nothing about it.
	f.SetReadOnly(true)
	text.SetReadOnly(false)
	f.Focus(true)
	if f.Focused() != nil || f.FocusNext() {
		t.Error("clearing the page's gate let a row focus on a read-only form")
	}
}
