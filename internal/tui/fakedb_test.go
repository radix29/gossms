package tui

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// A scripted stand-in for a SQL Server instance, so a propPage's load and
// apply closures can be driven with no server anywhere.
//
// The Properties pages were the largest untested surface in this package and
// the one where a mistake writes to a production database. Every page opens
// with a by-name read, and gosmo.WithScript intercepts writes only, so the
// script-collector harness the New-object dialogs use cannot reach them: load
// fails on the first query and apply never runs. gosmo.NewServer closes that
// gap by wrapping a caller-supplied *sql.DB, and this is the pool to hand it.
//
// What it is not: a SQL Server. Queries are matched by substring and answered
// with whatever the test scripted, so a test here proves the page asked for
// the right things and wrote the right statement — never that the statement
// is valid T-SQL, and never that the server would accept it. Statement
// correctness is gosmo's own tests (which build the string and assert it) and
// live runs against win10cli. Keep the two jobs separate: an assertion here
// that reaches for server semantics is asserting the fake.

// fakeResponse is one scripted answer: any query containing match is answered
// with these rows, each of which must have exactly cols values.
type fakeResponse struct {
	match string
	cols  int
	rows  [][]driver.Value

	// db, if set, restricts this answer to a connection currently pinned to
	// that database by a USE — the only way to give two databases different
	// answers to the identical per-database query gosmo runs in each of them
	// (a login's mappings, a database's roles, its tables). Without it every
	// database on a page looks the same, which is exactly the misalignment
	// these tests exist to catch.
	db string

	// arg, if set, restricts this answer to a query one of whose parameters
	// is that string. Substring matching alone cannot tell a by-name read
	// from the list read it is a WHERE clause away from — DatabaseByName's
	// query contains "FROM sys.databases" too, so the list answer served it
	// and every database on the page resolved to whichever row sorted first.
	arg string
}

// fakeInstance is the scripted responses plus the record of every statement
// executed against them.
type fakeInstance struct {
	mu        sync.Mutex
	responses []fakeResponse
	execs     []fakeExec
	// unmatched records queries no response covered, so a test that
	// half-scripted a page fails naming the query it missed rather than on
	// some downstream nil.
	unmatched []string
}

func (f *fakeInstance) respond(q, curDB string, args []driver.NamedValue) (*fakeResponse, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.responses {
		r := &f.responses[i]
		if r.db != "" && r.db != curDB {
			continue
		}
		if r.arg != "" && !hasStringArg(args, r.arg) {
			continue
		}
		if strings.Contains(q, r.match) {
			return r, true
		}
	}
	f.unmatched = append(f.unmatched, q)
	return nil, false
}

func hasStringArg(args []driver.NamedValue, want string) bool {
	for _, a := range args {
		if s, ok := a.Value.(string); ok && s == want {
			return true
		}
	}
	return false
}

// fakeExec is one executed statement and the database the connection was
// pinned to when it ran. The database is half the meaning of a write on these
// pages: DROP USER [appuser] is correct in one database and data loss in the
// next, and the USE that decides which is stripped from Statements.
type fakeExec struct {
	db  string
	sql string
	// args are the statement's parameters. A write that goes through a
	// system procedure — sp_rename, and every sp_add_job* the Agent pages
	// use — passes the name it acts on as a parameter, so the statement text
	// alone reads "EXEC sp_rename @objname = @p1" and says nothing about
	// which object was renamed to what.
	args []driver.Value
}

func (f *fakeInstance) recordExec(db, q string, args []driver.NamedValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	f.execs = append(f.execs, fakeExec{db: db, sql: q, args: vals})
}

// bareUSE matches the standalone "USE [db]" gosmo issues on the pinned
// connection ahead of each database-scoped read. It anchors to the end on
// purpose: several server-scoped writes begin a batch with "USE master;"
// because SQL Server only accepts them from master, and dropping those would
// hide the whole statement rather than the plumbing in front of it.
var bareUSE = regexp.MustCompile(`^USE \[([^\]]*)\]$`)

// Statements returns every statement the page executed, with that plumbing
// dropped — it is not a write, and it would otherwise drown the one statement
// a test cares about.
func (f *fakeInstance) Statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.execs {
		if !bareUSE.MatchString(strings.TrimSpace(e.sql)) {
			out = append(out, e.sql)
		}
	}
	return out
}

// StatementsIn returns only the statements that ran while the connection was
// pinned to db, so a test can say *where* a write landed rather than only that
// it happened.
func (f *fakeInstance) StatementsIn(db string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.execs {
		if e.db == db && !bareUSE.MatchString(strings.TrimSpace(e.sql)) {
			out = append(out, e.sql)
		}
	}
	return out
}

// -- database/sql plumbing ---------------------------------------------------

// fakeInstances maps a DSN to the instance serving it, so each test gets its
// own scripted server out of the one globally registered driver name.
var fakeInstances sync.Map // dsn string -> *fakeInstance

var fakeDSNSeq atomic64

type atomic64 struct {
	mu sync.Mutex
	n  int64
}

func (a *atomic64) next() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	return a.n
}

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	v, ok := fakeInstances.Load(dsn)
	if !ok {
		return nil, fmt.Errorf("fakedb: no instance registered for %q", dsn)
	}
	return &fakeConn{inst: v.(*fakeInstance)}, nil
}

// fakeConn is one pooled connection. It tracks the database a USE has pinned
// it to, because gosmo runs a database-scoped read as USE-then-query on a
// connection it has pinned for exactly that reason.
type fakeConn struct {
	inst  *fakeInstance
	curDB string
}

// ResetSession is called by database/sql when this connection is handed back
// out of the pool, and clearing the pinned database there is what stops one
// page's database-scoped read from answering the next caller's server-scoped
// one. A real server would keep the last USE, but gosmo issues its own USE
// ahead of every database-scoped read, so the only thing carrying it over
// could do here is make a test pass on a query that never asked for it.
func (c *fakeConn) ResetSession(context.Context) error {
	c.curDB = ""
	return nil
}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *fakeConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.inst.recordExec(c.curDB, q, args)
	if m := bareUSE.FindStringSubmatch(strings.TrimSpace(q)); m != nil {
		c.curDB = m[1]
	}
	return driver.ResultNoRows, nil
}

func (c *fakeConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	r, ok := c.inst.respond(q, c.curDB, args)
	if !ok {
		return nil, fmt.Errorf("fakedb: no scripted response for query:\n%s", q)
	}
	return &fakeRows{resp: r}, nil
}

type fakeRows struct {
	resp *fakeResponse
	i    int
}

func (r *fakeRows) Columns() []string { return make([]string, r.resp.cols) }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.resp.rows) {
		return io.EOF
	}
	row := r.resp.rows[r.i]
	if len(row) != r.resp.cols {
		return fmt.Errorf("fakedb: response %q row %d has %d values, want %d", r.resp.match, r.i, len(row), r.resp.cols)
	}
	copy(dest, row)
	r.i++
	return nil
}

func init() { sql.Register("fakedb", fakeDriver{}) }

// -- harness -----------------------------------------------------------------

// serverInfoResponse answers the SERVERPROPERTY query gosmo.NewServer runs.
// Every fake instance needs it, so newFakeConn prepends it rather than making
// each test restate fifteen columns it does not care about.
func serverInfoResponse() fakeResponse {
	return fakeResponse{match: "SERVERPROPERTY('ServerName')", cols: 15, rows: [][]driver.Value{{
		"FAKE\\SQL", "Developer Edition (64-bit)", "16.0.4085.2", "RTM", "SQL_Latin1_General_CP1_CI_AS",
		int64(0), int64(0), int64(0), int64(3),
		"Microsoft SQL Server 2022 ... on Windows",
		int64(16384), int64(8), `C:\Data`, `C:\Log`, `C:\Backup`,
	}}}
}

// newFakeConn builds a *db.ServerConn backed by the scripted responses, and
// returns the instance so the test can read back what was executed.
func newFakeConn(t *testing.T, responses ...fakeResponse) (*db.ServerConn, *fakeInstance) {
	t.Helper()
	inst := &fakeInstance{responses: append([]fakeResponse{serverInfoResponse()}, responses...)}
	dsn := "fake" + strconv.FormatInt(fakeDSNSeq.next(), 10)
	fakeInstances.Store(dsn, inst)
	t.Cleanup(func() { fakeInstances.Delete(dsn) })

	pool, err := sql.Open("fakedb", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	srv, err := gosmo.NewServer(context.Background(), pool)
	if err != nil {
		t.Fatalf("gosmo.NewServer: %v", err)
	}
	return &db.ServerConn{Server: srv}, inst
}

// loadPage runs a page's load closure and fails the test with the queries it
// could not answer — the failure a half-scripted fake actually produces.
func loadPage(t *testing.T, page propPage, inst *fakeInstance) (*propsheet.Form, propApply) {
	t.Helper()
	form, apply, err := page.load(context.Background())
	if err != nil {
		inst.mu.Lock()
		missed := strings.Join(inst.unmatched, "\n---\n")
		inst.mu.Unlock()
		if missed != "" {
			t.Fatalf("page %q load: %v\nunscripted queries:\n%s", page.title, err, missed)
		}
		t.Fatalf("page %q load: %v", page.title, err)
	}
	return form, apply
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
func assertOneStatementIn(t *testing.T, inst *fakeInstance, db, want string) {
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
func assertNoStatementsIn(t *testing.T, inst *fakeInstance, dbs ...string) {
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
func assertOneStatement(t *testing.T, inst *fakeInstance, want string) {
	t.Helper()
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], want) {
		t.Errorf("wrote:\n%s\nwant it to contain: %s", stmts[0], want)
	}
}

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

// argsFor returns the parameters of the one recorded statement containing
// want, and fails unless exactly one statement does — a procedure called twice
// with different arguments must not be asserted on as though it ran once.
func argsFor(t *testing.T, inst *fakeInstance, want string) []driver.Value {
	t.Helper()
	inst.mu.Lock()
	defer inst.mu.Unlock()
	var found []driver.Value
	n := 0
	for _, e := range inst.execs {
		if strings.Contains(e.sql, want) {
			n++
			found = e.args
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one statement containing %q, got %d", want, n)
	}
	return found
}

// assertArgs insists the statement containing want was called with exactly
// these parameters.
func assertArgs(t *testing.T, inst *fakeInstance, want string, args ...driver.Value) {
	t.Helper()
	got := argsFor(t, inst, want)
	if len(got) != len(args) {
		t.Fatalf("%q ran with %d parameters %v, want %d %v", want, len(got), got, len(args), args)
	}
	for i := range args {
		if got[i] != args[i] {
			t.Errorf("%q parameter %d = %v, want %v", want, i+1, got[i], args[i])
		}
	}
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
