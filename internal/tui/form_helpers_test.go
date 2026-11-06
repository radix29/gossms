package tui

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// hostApply is apply as the dialog runs it: the form's commit hook first, as
// PropDialog and newObjectDialog do on the UI goroutine before every run
// (propsheet.Form.SetCommit), so a test that drives the editor fields and calls
// apply sees what an OK would send.
func hostApply(form *propsheet.Form, apply propApply) propApply {
	if apply == nil {
		return nil
	}
	return func(ctx context.Context) error {
		form.Commit()
		return apply(ctx)
	}
}

// textRow finds an editable row by its label, so a test can drive a page the
// way a user does rather than by index into Rows() — an order that changes
// whenever a field is added.
func textRow(t *testing.T, f *propsheet.Form, label string) *propsheet.TextRow {
	t.Helper()
	for _, r := range f.Rows() {
		if tr, ok := r.(*propsheet.TextRow); ok && tr.Label() == sheetLabel(label) {
			return tr
		}
	}
	t.Fatalf("no text row labelled %q on this page", label)
	return nil
}

// sheetLabel is what the sheet actually stores for a label: propsheet pads
// every label to LabelWidth with core.PadRight, which *truncates* anything
// longer without an ellipsis. A row asked for by its intended label therefore
// has to be looked up by the truncated form, or every page with a long label
// is unreachable from a test.
//
// Applying the same rule here rather than writing truncated strings into the
// tests keeps them readable, and keeps them correct if a label is later
// shortened to fit. It does not make the truncation right — see
// docs/open-threads.md.
func sheetLabel(label string) string {
	return strings.TrimRight(core.PadRight(label, propsheet.LabelWidth), " ")
}

// selectRow is textRow for dropdowns.
func selectRow(t *testing.T, f *propsheet.Form, label string) *propsheet.SelectRow {
	t.Helper()
	for _, r := range f.Rows() {
		if sr, ok := r.(*propsheet.SelectRow); ok && sr.Label() == sheetLabel(label) {
			return sr
		}
	}
	t.Fatalf("no select row labelled %q on this page", label)
	return nil
}

// radioRow is textRow for radio groups. Unlike Text and Select, a Radio
// label is drawn on its own line and is not padded, so it is matched as
// written rather than through sheetLabel.
func radioRow(t *testing.T, f *propsheet.Form, label string) *propsheet.RadioRow {
	t.Helper()
	for _, r := range f.Rows() {
		if rr, ok := r.(*propsheet.RadioRow); ok && rr.Label() == label {
			return rr
		}
	}
	t.Fatalf("no radio row labelled %q on this page", label)
	return nil
}

// editRadio picks an option by its text, for the reason editSelect does.
func editRadio(t *testing.T, f *propsheet.Form, label, option string) {
	t.Helper()
	row := radioRow(t, f, label)
	i := slices.Index(row.Options(), option)
	if i < 0 {
		t.Fatalf("row %q offers %q, not %q", label, row.Options(), option)
	}
	row.Edit(i)
	if !row.Dirty() {
		t.Fatalf("row %q is not dirty after selecting %q — apply will skip it", label, option)
	}
}

// clickButton fires the page action labelled label, the way pressing or
// clicking the button does.
func clickButton(t *testing.T, f *propsheet.Form, label string) {
	t.Helper()
	for _, r := range f.Rows() {
		br, ok := r.(*propsheet.ButtonsRow)
		if !ok {
			continue
		}
		for _, b := range br.Buttons() {
			if b.Label() == label {
				if b.OnClick == nil {
					t.Fatalf("button %q has no action wired", label)
				}
				b.OnClick()
				return
			}
		}
	}
	t.Fatalf("no button labelled %q on this page", label)
}

// toggleGrid finds the page's single toggle grid. Every page that has one has
// exactly one, and it has no label to match on.
func toggleGrid(t *testing.T, f *propsheet.Form) *propsheet.ToggleGridRow {
	t.Helper()
	var found *propsheet.ToggleGridRow
	for _, r := range f.Rows() {
		if tg, ok := r.(*propsheet.ToggleGridRow); ok {
			if found != nil {
				t.Fatal("this page has more than one toggle grid; find it by hand")
			}
			found = tg
		}
	}
	if found == nil {
		t.Fatal("no toggle grid on this page")
	}
	return found
}

// toggleByName flips the checkbox on the row whose first text column is name,
// the way clicking it does.
//
// By name and not by index deliberately. Every one of these pages reads its
// grid back index-parallel against the list it built the grid from, so a test
// that also worked in indices would agree with a page that had them
// misaligned — and misalignment is the failure worth catching, since the row
// labelled sysadmin is the one the user meant to tick.
func toggleByName(t *testing.T, tg *propsheet.ToggleGridRow, name string, col int) {
	t.Helper()
	for i, row := range tg.Text() {
		if len(row) > 0 && row[0] == name {
			tg.Toggle(i, col)
			if !tg.Dirty() {
				t.Fatalf("the grid is not dirty after toggling %q — apply will skip it", name)
			}
			return
		}
	}
	t.Fatalf("no grid row named %q", name)
}

// editText and editSelect change a row the way the user does, so it comes out
// dirty.
//
// This is the trap in driving a page from a test, and it fails silently in the
// direction that looks like success: SetValue/SetSelected move the baseline
// with the value, so a row set that way is clean, apply skips it, and a test
// asserting "the right statement was written" fails while a test asserting
// "nothing was written" passes for entirely the wrong reason. Always go
// through these. See propsheet.TextRow.Edit.
func editText(t *testing.T, f *propsheet.Form, label, value string) {
	t.Helper()
	row := textRow(t, f, label)
	row.Edit(value)
	if !row.Dirty() {
		t.Fatalf("row %q is not dirty after editing it to %q — apply will skip it", label, value)
	}
}

// editorRow finds a multi-line editor row by its label, and editEditor drives
// one the way editText drives a text row — through Edit, so the row goes dirty
// and apply doesn't skip it.
func editorRow(t *testing.T, f *propsheet.Form, label string) *propsheet.EditorRow {
	t.Helper()
	for _, r := range f.Rows() {
		if er, ok := r.(*propsheet.EditorRow); ok && er.Label() == label {
			return er
		}
	}
	t.Fatalf("no editor row labelled %q on this page", label)
	return nil
}

func editEditor(t *testing.T, f *propsheet.Form, label, value string) {
	t.Helper()
	row := editorRow(t, f, label)
	row.Edit(value)
	if !row.Dirty() {
		t.Fatalf("row %q is not dirty after editing it to %q — apply will skip it", label, value)
	}
}

// typeInto is editText for a row the page deliberately keeps out of dirty
// tracking — a filter or search box, which drives the view and is never
// written. Those rows stay clean by design (SetDirtyTracked(false)), so
// editText's check would fail on exactly the rows it should not.
func typeInto(t *testing.T, f *propsheet.Form, label, value string) {
	t.Helper()
	row := textRow(t, f, label)
	row.Edit(value)
	if row.Dirty() {
		t.Fatalf("row %q is dirty-tracked; use editText, or the page will try to write a view control", label)
	}
}

// editSelect picks an item by its text rather than its index, so a test says
// which option it chose and cannot silently follow a reordered list.
func editSelect(t *testing.T, f *propsheet.Form, label, value string) {
	t.Helper()
	row := selectRow(t, f, label)
	i := slices.Index(row.Items(), value)
	if i < 0 {
		t.Fatalf("row %q offers %q, not %q", label, row.Items(), value)
	}
	row.Edit(i)
	if !row.Dirty() {
		t.Fatalf("row %q is not dirty after selecting %q — apply will skip it", label, value)
	}
}

// -- plain grids -------------------------------------------------------------
//
// A page whose grid is a controls.DataGrid rather than a propsheet.ToggleGridRow
// (User Mapping's database list, Change Tracking's table list) has no Toggle
// method to call, and SetSelectedRow/SetSelectedCell deliberately do not fire
// OnSelectRow. Driving one therefore means sending the keys the user sends —
// which is also the only way to exercise the OnSelectRow/OnActivateCell wiring
// the page hangs its commit-and-redraw on.

// plainGrid finds the page's single controls.DataGrid-backed row. A
// ToggleGridRow embeds *GridRow but is its own type, so a page with both (User
// Mapping) is unambiguous.
func plainGrid(t *testing.T, f *propsheet.Form) *controls.DataGrid {
	t.Helper()
	var found *controls.DataGrid
	for _, r := range f.Rows() {
		gr, ok := r.(*propsheet.GridRow)
		if !ok {
			continue
		}
		if found != nil {
			t.Fatal("this page has more than one plain grid; find it by hand")
		}
		found = gr.Grid
	}
	if found == nil {
		t.Fatal("no plain grid on this page")
	}
	return found
}

func gridKey(t *testing.T, g *controls.DataGrid, k tcell.Key) {
	t.Helper()
	if !g.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone)) {
		t.Fatalf("DataGrid refused %v", k)
	}
}

// gridRowIndex finds the row whose column col reads name.
func gridRowIndex(t *testing.T, g *controls.DataGrid, col int, name string) int {
	t.Helper()
	for i := 0; ; i++ {
		row := g.Row(i)
		if row == nil {
			break
		}
		if col < len(row) && row[col] == name {
			return i
		}
	}
	t.Fatalf("no grid row whose column %d reads %q", col, name)
	return -1
}

// selectGridRow moves the cursor onto the named row with arrow keys, so the
// page's OnSelectRow — the commit/load/redraw wiring — runs exactly as it does
// under the user's hands.
func selectGridRow(t *testing.T, g *controls.DataGrid, col int, name string) {
	t.Helper()
	want := gridRowIndex(t, g, col, name)
	for g.SelectedRow() != want {
		if g.SelectedRow() < want {
			gridKey(t, g, tcell.KeyDown)
		} else {
			gridKey(t, g, tcell.KeyUp)
		}
	}
}

// activateGridCell presses Space on column col of the named row, the gesture
// that flips a checkbox cell.
func activateGridCell(t *testing.T, g *controls.DataGrid, nameCol int, name string, col int) {
	t.Helper()
	selectGridRow(t, g, nameCol, name)
	for {
		_, c := g.SelectedCell()
		if c == col {
			break
		}
		if c < col {
			gridKey(t, g, tcell.KeyRight)
		} else {
			gridKey(t, g, tcell.KeyLeft)
		}
	}
	if !g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone)) {
		t.Fatal("DataGrid refused Space")
	}
}

// assertOneStatementIn insists exactly one statement ran in database db and
// that it contains want.
//
// Exactly one, not "at least one containing": on a page that writes per
// database, an extra statement in the right database is as much a bug as one
// in the wrong database, and a Contains-only assertion passes on both.
func assertOneStatementIn(t *testing.T, inst statementsInLog, db, want string) {
	t.Helper()
	stmts := inst.StatementsIn(db)
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement in %s, got %d:\n%s", db, len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], want) {
		t.Errorf("statement in %s:\n%s\nwant it to contain: %s", db, stmts[0], want)
	}
}

// assertNoStatementsIn insists nothing at all was written in the named
// databases — the half of a per-database write that a test asserting only the
// intended database cannot see.
func assertNoStatementsIn(t *testing.T, inst statementsInLog, dbs ...string) {
	t.Helper()
	for _, db := range dbs {
		if stmts := inst.StatementsIn(db); len(stmts) != 0 {
			t.Errorf("%s should not have been written to, but got:\n%s", db, strings.Join(stmts, "\n"))
		}
	}
}

// checkRow is textRow for checkboxes.
func checkRow(t *testing.T, f *propsheet.Form, label string) *propsheet.CheckRow {
	t.Helper()
	for _, r := range f.Rows() {
		if cr, ok := r.(*propsheet.CheckRow); ok && cr.Label() == label {
			return cr
		}
	}
	t.Fatalf("no check row labelled %q on this page", label)
	return nil
}

// editCheck ticks or unticks a checkbox the way the user does — see editText
// for why SetChecked cannot stand in for it.
func editCheck(t *testing.T, f *propsheet.Form, label string, v bool) {
	t.Helper()
	row := checkRow(t, f, label)
	row.Edit(v)
	if !row.Dirty() {
		t.Fatalf("row %q is not dirty after setting it to %v — apply will skip it", label, v)
	}
}

// assertOneStatement is assertOneStatementIn for a server-scoped write, which
// runs on whatever connection the pool hands out rather than in a database of
// its own.
func assertOneStatement(t *testing.T, inst statementLog, want string) {
	t.Helper()
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], want) {
		t.Errorf("wrote:\n%s\nwant it to contain: %s", stmts[0], want)
	}
}

// statementLog is what assertOneStatement reads: a gosmotest-backed gFake.
type statementLog interface{ Statements() []string }

// statementsInLog is a log that can be read per database: the bridge's *gFake.
type statementsInLog interface{ StatementsIn(db string) []string }

// chooseSelect picks an item in a dropdown whose value is never written
// directly — a candidate picker a button consumes, rather than a property.
// Unlike editSelect it makes no claim about the row being dirty: the item that
// happens to be first is a legitimate choice, and asserting dirtiness would
// make the test depend on the order the page built the list in.
func chooseSelect(t *testing.T, f *propsheet.Form, label, value string) {
	t.Helper()
	row := selectRow(t, f, label)
	i := slices.Index(row.Items(), value)
	if i < 0 {
		t.Fatalf("row %q offers %q, not %q", label, row.Items(), value)
	}
	row.Edit(i)
}

// -- dialog-backed pages -----------------------------------------------------

// newFakeDialog builds the PropDialog a page constructor takes when the page
// has buttons that fetch on demand — Securables' column-permission editor,
// Job Steps' syntax check — rather than loading everything up front.
//
// Those buttons run through PropDialog.runPageAction/runPageActionOnce, which
// hand the work to App.safego and report back through App.postAndWake, so the
// page needs an App even though nothing here draws. newTestApp's App has no
// screen, which makes wakeEventLoop a no-op and leaves the posted callback
// queued: drainDialog is what runs it, and a test that clicks such a button
// and asserts without draining is asserting on work that has not happened yet.
func newFakeDialog(t *testing.T) (*PropDialog, *App) {
	t.Helper()
	app := newTestApp()
	return &PropDialog{app: app, ctx: context.Background()}, app
}

// drainDialog runs the callbacks a page action posted, until cond holds. It is
// drainUntil under a name that says which goroutine a Properties page test is
// waiting on.
func drainDialog(t *testing.T, app *App, cond func() bool, what string) {
	t.Helper()
	drainUntil(t, app, cond, what)
}

// jsonNames is a list column as gosmo's jsonList reads it back — the FOR JSON
// array [{"v":…},…] — or nil for no names, since the server returns NULL over
// zero rows.
func jsonNames(names ...string) driver.Value {
	if len(names) == 0 {
		return nil
	}
	parts := make([]string, len(names))
	for i, n := range names {
		b, _ := json.Marshal(n)
		parts[i] = `{"v":` + string(b) + `}`
	}
	return "[" + strings.Join(parts, ",") + "]"
}
