package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
)

// The Details pane's Delete, driven the way a user drives it: rows selected in
// the grid, the item taken off the cell menu, the confirmation answered, and
// the statements read back off the scripted instance. What these pin is that
// the batch acts on the rows that are selected and on nothing else — a pane
// that deleted the first row, or the whole folder, looks identical from the
// menu.

// opsPaneObjects is three tables in two schemas, so a test can select a middle
// row and a selection can span schemas.
func opsPaneObjects() []nodeData {
	return []nodeData{
		{Type: NodeTable, DBName: "appdb", Schema: "dbo", Name: "Alpha"},
		{Type: NodeTable, DBName: "appdb", Schema: "sales", Name: "Beta"},
		{Type: NodeTable, DBName: "appdb", Schema: "sales", Name: "Gamma"},
	}
}

// newOpsPane builds the Details pane showing objs as a folder's rows, wired to
// sc through a real App so the menu, the confirmation and the drops all run.
func newOpsPane(a *App, sc *db.ServerConn, objs []nodeData) *DetailBrowser {
	pane := a.newDetailBrowser()
	pane.SetBounds(0, 0, 80, 20)
	folder := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, DBName: "appdb", conn: sc}}
	rows := make([][]string, len(objs))
	for i, n := range objs {
		rows[i] = []string{objectDataName(n)}
	}
	pane.currentNode = folder
	pane.grid.SetData([]string{"Name"}, rows)
	pane.setRowObjects(rows, objs)
	return pane
}

// selectPaneRows puts the cell cursor on first and extends the block selection
// down to last with Shift+Down, the way the keyboard does — SetSelectedCell
// alone leaves blockSelecting false, so a test that assigned the bounds would
// pass against a pane that only ever sees one row.
func selectPaneRows(pane *DetailBrowser, first, last int) {
	// Home first, and plain arrows down to the anchor: any unshifted key
	// collapses a block selection, and SetSelectedCell does not — it moves the
	// cursor and leaves the previous selection's anchor where it was, so a
	// second call in one test would silently select from the old anchor.
	pane.grid.HandleKey(tcell.NewEventKey(tcell.KeyHome, "", tcell.ModNone))
	for range first {
		pane.grid.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	for range last - first {
		pane.grid.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))
	}
}

// paneDeleteItem is the pane's Delete entry, as the cell menu would build it.
func paneDeleteItem(t *testing.T, a *App, pane *DetailBrowser) (label string, enabled bool, note string, run func()) {
	t.Helper()
	items := a.detailMenuItems(pane)
	if len(items) != 1 {
		t.Fatalf("the pane contributed %d menu items, want the one Delete", len(items))
	}
	it := items[0]
	on := it.Enabled == nil || it.Enabled()
	return it.Label, on, it.Note, it.Action
}

// TestTheDetailPaneDeletesEverySelectedRowAndNothingElse is the whole feature:
// two of three rows selected, both dropped, the third untouched. The selection
// deliberately starts at row 1 — a pane that ignored it and acted on the first
// row would pass a test that selected row 0.
func TestTheDetailPaneDeletesEverySelectedRowAndNothingElse(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	pane := newOpsPane(a, sc, opsPaneObjects())
	selectPaneRows(pane, 1, 2)

	label, enabled, _, run := paneDeleteItem(t, a, pane)
	if label != "Delete 2 Tables..." {
		t.Errorf("menu label = %q, want it to name the two rows selected", label)
	}
	if !enabled {
		t.Fatal("Delete is withheld on an unprobed connection — unknown must fail open")
	}
	run()
	answerConfirm(t, a, false)
	waitAndDrain(t, a)

	joined := strings.Join(inst.StatementsIn("appdb"), "\n---\n")
	for _, want := range []string{"DROP TABLE [sales].[Beta]", "DROP TABLE [sales].[Gamma]"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s did not reach the server:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "[dbo].[Alpha]") {
		t.Errorf("the unselected row was dropped too:\n%s", joined)
	}
}

// A single row goes through the same path as the tree's Delete, checkbox
// included: the option the tables' op carries must still be offered and must
// still drive the drop.
func TestTheDetailPaneKeepsTheDropOptionOnASingleRow(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	pane := newOpsPane(a, sc, opsPaneObjects())
	selectPaneRows(pane, 2, 2)

	label, _, _, run := paneDeleteItem(t, a, pane)
	if label != "Delete..." {
		t.Errorf("menu label = %q, want the plain verb for one row", label)
	}
	run()
	answerConfirm(t, a, true) // tick "Also drop the foreign keys that reference it"
	waitAndDrain(t, a)

	joined := strings.Join(inst.StatementsIn("appdb"), "\n---\n")
	if !strings.Contains(joined, "DROP TABLE [sales].[Gamma]") {
		t.Errorf("the table was not dropped:\n%s", joined)
	}
	if !strings.Contains(joined, "sys.foreign_keys") {
		t.Errorf("the ticked cascade did not reach the server:\n%s", joined)
	}
}

// The checkbox is one tick for the whole batch, so it is offered only when
// every selected object answers to it — and when it is offered, it drives every
// drop, not just the first.
func TestTheDetailPaneCascadeAppliesToEverySelectedTable(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	pane := newOpsPane(a, sc, opsPaneObjects())
	selectPaneRows(pane, 1, 2)

	_, _, _, run := paneDeleteItem(t, a, pane)
	run()
	answerConfirm(t, a, true)
	waitAndDrain(t, a)

	joined := strings.Join(inst.StatementsIn("appdb"), "\n---\n")
	if n := strings.Count(joined, "sys.foreign_keys"); n != 2 {
		t.Errorf("the cascade ran %d times for two selected tables:\n%s", n, joined)
	}
}

// A typed confirmation asks for one object's name, so it cannot stand for a
// selection. The refusal has to be a stated one — a Delete that silently
// dropped two databases after a Yes on a plain prompt is exactly what the
// typed confirmation exists to prevent.
func TestTheDetailPaneRefusesAMultiRowDeleteOfATypedObject(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	pane := newOpsPane(a, sc, []nodeData{
		{Type: NodeDatabase, DBName: "one", Name: "one"},
		{Type: NodeDatabase, DBName: "two", Name: "two"},
	})
	selectPaneRows(pane, 0, 1)

	_, enabled, note, _ := paneDeleteItem(t, a, pane)
	if enabled {
		t.Fatal("two databases were offered a batch Delete")
	}
	if !strings.Contains(note, "on its own") || !strings.Contains(note, `"one"`) {
		t.Errorf("note = %q, want it to name the row to keep", note)
	}
	if len(inst.Statements()) != 0 {
		t.Fatalf("something was dropped: %v", inst.Statements())
	}
}

// Principals and databases are deleted one at a time; schema-scoped objects are
// deleted in a set. A login's drop orphans users elsewhere, a database's closes
// every connection to it — consequences the batch confirmation's one shared
// warning cannot carry per object, and the reason multi-select stops at the
// schema boundary rather than covering everything with a drop.
func TestOnlySchemaScopedObjectsAreDeletedAsASelection(t *testing.T) {
	solo := []NodeType{NodeDatabase, NodeLogin, NodeServerRole, NodeUser, NodeDatabaseRole,
		NodeColumnEncryptionKey}
	batch := []NodeType{NodeTable, NodeView, NodeStoredProcedure, NodeFunction,
		NodeSequence, NodeSynonym, NodeIndex, NodeTrigger}

	for _, tp := range solo {
		if op := objectOpFor(tp); !deletedAlone(op) {
			t.Errorf("%s takes part in a multi-object delete, want it deleted on its own", op.noun)
		}
	}
	for _, tp := range batch {
		if op := objectOpFor(tp); deletedAlone(op) {
			t.Errorf("%s is refused in a selection, want it deletable as a set", op.noun)
		}
	}
}

// The menu half of that rule, driven through the pane: two logins selected in
// the Logins folder offer a withheld Delete naming one of them.
func TestTheDetailPaneWithholdsABatchDeleteOfPrincipals(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	pane := newOpsPane(a, sc, []nodeData{
		{Type: NodeLogin, Name: "app_reader"},
		{Type: NodeLogin, Name: "app_writer"},
	})
	selectPaneRows(pane, 0, 1)

	label, enabled, note, _ := paneDeleteItem(t, a, pane)
	if label != "Delete 2 Logins..." {
		t.Errorf("menu label = %q, want it to name the two rows selected", label)
	}
	if enabled {
		t.Fatal("two logins were offered a batch Delete")
	}
	if !strings.Contains(note, "app_reader") {
		t.Errorf("note = %q, want it to name the row to keep", note)
	}
	if len(inst.Statements()) != 0 {
		t.Fatalf("something was dropped: %v", inst.Statements())
	}
}

// The rule is about a selection, not about the type: one login selected in the
// pane still deletes, the same way the tree's Delete does.
func TestTheDetailPaneStillDeletesOnePrincipal(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	pane := newOpsPane(a, sc, []nodeData{
		{Type: NodeLogin, Name: "app_reader"},
		{Type: NodeLogin, Name: "app_writer"},
	})
	selectPaneRows(pane, 1, 1)

	label, enabled, _, run := paneDeleteItem(t, a, pane)
	if label != "Delete..." || !enabled {
		t.Fatalf("one login offered label %q enabled=%v, want the plain Delete", label, enabled)
	}
	run()
	answerConfirm(t, a, false) // a login carries no checkbox, only its warning
	waitAndDrain(t, a)

	joined := strings.Join(inst.Statements(), "\n---\n")
	if !strings.Contains(joined, "app_writer") {
		t.Errorf("the selected login was not dropped:\n%s", joined)
	}
	if strings.Contains(joined, "app_reader") {
		t.Errorf("the unselected login was dropped too:\n%s", joined)
	}
}

// The menu is the half a user sees; confirmDeleteObjects is the half that
// issues the DROP, and it refuses the batch on its own — a caller reaching it
// with two logins must not get a plain Yes/No prompt over them.
func TestTheDeletePathItselfRefusesABatchOfPrincipals(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	objs := []nodeData{{Type: NodeLogin, Name: "app_reader"}, {Type: NodeLogin, Name: "app_writer"}}

	a.confirmDeleteObjects(sc, objs, func() {})

	if a.confirmDialog.Visible() {
		t.Fatal("two logins were offered a plain Yes/No confirmation")
	}
	if len(inst.Statements()) != 0 {
		t.Fatalf("something was dropped: %v", inst.Statements())
	}
	if !strings.Contains(a.statusText, "on its own") {
		t.Errorf("status = %q, want it to say the login has to be deleted on its own", a.statusText)
	}
}

// A system object in the selection withholds the whole item, and the note names
// which row is the problem: "needs ALTER" over a forty-row selection says
// nothing about what to deselect.
func TestTheDetailPaneWithholdsDeleteForASystemObjectInTheSelection(t *testing.T) {
	a := newTestApp()
	sc, _ := opTestConn(t)
	objs := opsPaneObjects()
	objs[2].IsSystem = true
	pane := newOpsPane(a, sc, objs)
	selectPaneRows(pane, 1, 2)

	_, enabled, note, _ := paneDeleteItem(t, a, pane)
	if enabled {
		t.Error("Delete is offered over a selection containing a system object")
	}
	if !strings.Contains(note, "sales.Gamma") {
		t.Errorf("note = %q, want it to name the system object", note)
	}

	// The same selection without that row is offered again — the gate is the
	// selection's, not the folder's.
	selectPaneRows(pane, 0, 1)
	if _, enabled, _, _ := paneDeleteItem(t, a, pane); !enabled {
		t.Error("Delete stayed withheld over a selection with no system object in it")
	}
}

// The rights are asked per object, because a schema-scoped right answers about
// one securable: a selection spanning two schemas can be permitted in one and
// refused in the other.
func TestTheDetailPaneWithholdsDeleteWhenOneRowLacksTheRight(t *testing.T) {
	a := newTestApp()
	// ALTER on sales and nothing else: the fixture's rows straddle the grant.
	sc := schemaProbedConn(t, "appdb", []string{"sales"}, []string{"dbo"})
	pane := newOpsPane(a, sc, opsPaneObjects())

	selectPaneRows(pane, 0, 1) // dbo.Alpha (refused) and sales.Beta (allowed)
	_, enabled, note, _ := paneDeleteItem(t, a, pane)
	if enabled {
		t.Error("Delete is offered over a selection holding an object the server refused")
	}
	if !strings.Contains(note, "dbo.Alpha") || !strings.Contains(note, "ALTER") {
		t.Errorf("note = %q, want the refused object and the right it needs", note)
	}

	// The two rows inside the grant are still offered — a single answer for
	// the whole folder would have withheld these too.
	selectPaneRows(pane, 1, 2)
	if _, enabled, _, _ := paneDeleteItem(t, a, pane); !enabled {
		t.Error("Delete was withheld from two objects in a schema the login holds ALTER on")
	}
}

// A view whose rows are not objects offers nothing — a Property/Value grid, a
// Query Store report, an error-log listing. Without this the pane would carry a
// Delete keyed off whatever row index the cursor happened to be on.
func TestTheDetailPaneOffersNothingWhereRowsAreNotObjects(t *testing.T) {
	a := newTestApp()
	sc, _ := opTestConn(t)
	pane := newOpsPane(a, sc, opsPaneObjects())

	pane.grid.SetData([]string{"Property", "Value"}, [][]string{{"Name", "appdb"}})
	pane.setRowObjects([][]string{{"Name", "appdb"}}, nil)
	if items := a.detailMenuItems(pane); items != nil {
		t.Errorf("a Property/Value view offered %v", items)
	}
}

// A mapping that does not line up with the rows is refused outright: the pane
// deletes by row index, so one row short would drop the object the *next* row
// describes. Losing Delete is the safe failure.
func TestRowObjectsThatDoNotLineUpAreDropped(t *testing.T) {
	a := newTestApp()
	sc, _ := opTestConn(t)
	pane := newOpsPane(a, sc, opsPaneObjects())
	if len(pane.rowObjs) != 3 {
		t.Fatalf("the fixture itself did not line up: %d objects for 3 rows", len(pane.rowObjs))
	}

	rows := [][]string{{"a"}, {"b"}, {"c"}}
	pane.grid.SetData([]string{"Name"}, rows)
	pane.setRowObjects(rows, opsPaneObjects()[:2])
	if pane.rowObjs != nil {
		t.Error("a mapping one object short was kept")
	}
	if items := a.detailMenuItems(pane); items != nil {
		t.Error("Delete was offered over a mapping that does not line up with the rows")
	}
}

// A delete has to make the folder read itself again, or the pane goes on
// listing objects that are gone.
func TestTheDetailPaneDeleteRefreshesTheFolder(t *testing.T) {
	a := newTestApp()
	sc, _ := opTestConn(t)
	pane := newOpsPane(a, sc, opsPaneObjects())
	a.detailBrowser = pane
	folder := pane.currentNode
	folder.data.Loaded = true
	pane.cache[folder] = &detailResult{cols: []string{"Name"}, rows: [][]string{{"stale"}}}
	selectPaneRows(pane, 0, 0)

	_, _, _, run := paneDeleteItem(t, a, pane)
	run()
	answerConfirm(t, a, false)
	waitAndDrain(t, a)

	if folder.data.Loaded {
		t.Error("the folder still claims to be loaded after a delete from the pane")
	}
	if _, ok := pane.cache[folder]; ok {
		t.Error("the folder's cached detail rows survived a delete from the pane")
	}
}

// The label says what will go. A mixed selection cannot name one noun and must
// not pick the first row's.
func TestTheDeleteLabelNamesWhatIsSelected(t *testing.T) {
	tbl := nodeData{Type: NodeTable, Schema: "dbo", Name: "T"}
	idx := nodeData{Type: NodeIndex, Schema: "dbo", Name: "IX", TableName: "T"}
	for _, c := range []struct {
		name string
		objs []nodeData
		want string
	}{
		{"one", []nodeData{tbl}, "Delete..."},
		{"two of a kind", []nodeData{tbl, tbl}, "Delete 2 Tables..."},
		{"mixed", []nodeData{tbl, idx}, "Delete 2 Objects..."},
	} {
		if got := deleteItemLabel(c.objs); got != c.want {
			t.Errorf("%s: label = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestEveryObjectOpNounPluralises keeps pluralNoun's two rules honest as the
// op table grows: a new noun ending in -y or -x reaches the menu as
// "Security Policys" or "Indexs" otherwise.
func TestEveryObjectOpNounPluralises(t *testing.T) {
	// Written out rather than derived: a rule checked against itself proves
	// nothing, and "Indexs" is exactly what a derived expectation would allow.
	want := map[string]string{
		"Alert":                      "Alerts",
		"Column":                     "Columns",
		"Column Encryption Key":      "Column Encryption Keys",
		"Column Master Key":          "Column Master Keys",
		"Constraint":                 "Constraints",
		"Credential":                 "Credentials",
		"Backup Device":              "Backup Devices",
		"Database":                   "Databases",
		"Database Role":              "Database Roles",
		"Foreign Key":                "Foreign Keys",
		"Function":                   "Functions",
		"Index":                      "Indexes",
		"Job":                        "Jobs",
		"Key":                        "Keys",
		"Login":                      "Logins",
		"Operator":                   "Operators",
		"Partition Function":         "Partition Functions",
		"Partition Scheme":           "Partition Schemes",
		"Schedule":                   "Schedules",
		"Schema":                     "Schemas",
		"Security Policy":            "Security Policies",
		"Sequence":                   "Sequences",
		"Server Role":                "Server Roles",
		"Endpoint":                   "Endpoints",
		"Server Trigger":             "Server Triggers",
		"Audit":                      "Audits",
		"Server Audit Specification": "Server Audit Specifications",
		"Statistic":                  "Statistics",
		"Stored Procedure":           "Stored Procedures",
		"Synonym":                    "Synonyms",
		"Table":                      "Tables",
		"Trigger":                    "Triggers",
		"User":                       "Users",
		"View":                       "Views",
	}
	for _, op := range objectOps {
		w, ok := want[op.noun]
		if !ok {
			t.Errorf("objectOps has a new noun %q with no plural pinned here", op.noun)
			continue
		}
		if got := pluralNoun(op.noun); got != w {
			t.Errorf("pluralNoun(%q) = %q, want %q", op.noun, got, w)
		}
	}
}

// The confirmation is one flowing sentence, because ConfirmDialog wraps and
// centres what it is given: a list laid out with newlines arrives as prose with
// the names run into it, which is how the first version read live. And a
// warning written about one object ("All of its data is deleted with it") has
// to be introduced before it can stand for a batch.
func TestTheMultiDeleteConfirmationReadsAsOneSentence(t *testing.T) {
	objs := []nodeData{
		{Type: NodeTable, Schema: "sales", Name: "Beta"},
		{Type: NodeTable, Schema: "sales", Name: "Gamma"},
	}
	list := deleteListText(objs)
	if list != "sales.Beta, sales.Gamma" {
		t.Errorf("object list = %q, want the names separated for a wrapped line", list)
	}
	if strings.Contains(list, "\n") {
		t.Error("the object list carries newlines the dialog cannot render")
	}

	// The cap counts the rest rather than listing four hundred names.
	many := make([]nodeData, maxDeleteListNames+3)
	for i := range many {
		many[i] = nodeData{Type: NodeTable, Schema: "dbo", Name: string(rune('A' + i))}
	}
	if got := deleteListText(many); !strings.HasSuffix(got, ", and 3 more") {
		t.Errorf("a long list ends %q, want the remainder counted", got)
	}

	if got := sharedDeleteWarning([]*objectOp{objectOpFor(NodeTable), objectOpFor(NodeTable)}); got == "" {
		t.Error("two tables lost the warning they share")
	}
	if got := sharedDeleteWarning([]*objectOp{objectOpFor(NodeTable), objectOpFor(NodeView)}); got != "" {
		t.Errorf("a mixed selection carried %q, a warning true of only some of it", got)
	}
}

// ctrlClickPaneRow Ctrl+clicks one of the pane's rows, as the mouse does: press
// and release on the row's first cell with Ctrl held.
func ctrlClickPaneRow(pane *DetailBrowser, row int) {
	// The grid sits one row below the pane's own rect (the title bar), and its
	// data rows two below its top (header plus separator).
	x, y := pane.rect.X+1, pane.rect.Y+1+2+row
	pane.grid.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModCtrl))
	pane.grid.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModCtrl))
}

// TestTheDetailPaneDeletesACtrlClickedSelection is the discontiguous case: the
// first and last of three rows, with the middle one left alone. A pane reading
// the grid's selection *bounds* instead of its rows would drop all three, and
// nothing else in the delete path can tell the difference.
func TestTheDetailPaneDeletesACtrlClickedSelection(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	objs := append(opsPaneObjects(),
		nodeData{Type: NodeTable, DBName: "appdb", Schema: "sales", Name: "Delta"})
	pane := newOpsPane(a, sc, objs)

	// Rows 1 and 3 of four: a row skipped between them, and a row before them
	// that was never selected at all. Starting at row 1 also keeps the row
	// folded in with the first Ctrl+click off row 0, where a selection that came
	// out wrong could still look right.
	selectPaneRows(pane, 1, 1)
	ctrlClickPaneRow(pane, 3)

	label, enabled, _, run := paneDeleteItem(t, a, pane)
	if label != "Delete 2 Tables..." {
		t.Errorf("menu label = %q, want it to name the two Ctrl+clicked rows", label)
	}
	if !enabled {
		t.Fatal("Delete is withheld on an unprobed connection — unknown must fail open")
	}
	run()
	answerConfirm(t, a, false)
	waitAndDrain(t, a)

	joined := strings.Join(inst.StatementsIn("appdb"), "\n---\n")
	for _, want := range []string{"DROP TABLE [sales].[Beta]", "DROP TABLE [sales].[Delta]"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s did not reach the server:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"[sales].[Gamma]", "[dbo].[Alpha]"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("%s was dropped, and it was never selected:\n%s", unwanted, joined)
		}
	}
}
