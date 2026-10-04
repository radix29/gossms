package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// New Session and Session Properties (xevent_session_*.go), driven through
// fakedb_test.go. The session under test is zz_trace, the third in
// xeSessionResponses' list, with two events (rpc_completed collecting
// sql_text, and sql_batch_completed) — every edit lands on the second event,
// so a page ignoring the selection fails.

// xeObjectRow is one row of gosmo's XEObjects read.
func xeObjectRow(pkg, name, kind, desc, channel, keyword, typeName string) []driver.Value {
	return []driver.Value{pkg, name, kind, desc, "", channel, keyword, typeName}
}

// xeCatalogResponses scripts the four catalog reads, each scoped by its kind.
func xeCatalogResponses() []fakeResponse {
	const q = "FROM   sys.dm_xe_objects o"
	return []fakeResponse{
		{match: q, arg: gosmo.XEObjectEvent, cols: 8, rows: [][]driver.Value{
			xeObjectRow("sqlserver", "attention", "event", "Client attention", "Analytic", "execution", ""),
			xeObjectRow("sqlserver", "rpc_completed", "event", "RPC done", "Analytic", "execution", ""),
			xeObjectRow("sqlserver", "sql_batch_completed", "event", "Batch done", "Analytic", "execution", ""),
			xeObjectRow("sqlserver", "xml_deadlock_report", "event", "Deadlock graph", "Admin", "deadlock_monitor", ""),
		}},
		{match: q, arg: gosmo.XEObjectAction, cols: 8, rows: [][]driver.Value{
			xeObjectRow("package0", "event_sequence", "action", "Sequence", "", "", ""),
			xeObjectRow("sqlserver", "client_app_name", "action", "App", "", "", ""),
			xeObjectRow("sqlserver", "sql_text", "action", "Text", "", "", ""),
		}},
		{match: q, arg: gosmo.XEObjectPredSource, cols: 8, rows: [][]driver.Value{
			xeObjectRow("sqlserver", "database_name", "pred_source", "", "", "", "unicode_string"),
			xeObjectRow("sqlserver", "session_id", "pred_source", "", "", "", "uint16"),
		}},
		{match: q, arg: gosmo.XEObjectTarget, cols: 8, rows: [][]driver.Value{
			xeObjectRow("package0", "event_file", "target", "", "", "", ""),
			xeObjectRow("package0", "histogram", "target", "", "", "", ""),
			xeObjectRow("package0", "ring_buffer", "target", "", "", "", ""),
		}},
		{match: q, arg: gosmo.XEObjectMap, cols: 8, rows: [][]driver.Value{
			xeObjectRow("sqlos", "wait_types", "map", "", "", "", ""),
		}},
	}
}

// xeColumnsResponse scripts XEObjectColumns for one object: name, id, type,
// column type, default, description, mandatory.
func xeColumnsResponse(object string, rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "FROM   sys.dm_xe_object_columns c", arg: object, cols: 7, rows: rows}
}

func xeColumn(name, typeName, columnType, def string, mandatory bool) []driver.Value {
	return []driver.Value{name, int64(0), typeName, columnType, def, "", mandatory}
}

func xeEventColumnResponses() []fakeResponse {
	return []fakeResponse{
		xeColumnsResponse("rpc_completed",
			xeColumn("duration", "uint64", gosmo.XEColumnData, "", false),
			xeColumn("collect_statement", "boolean", gosmo.XEColumnCustomizable, "true", false)),
		xeColumnsResponse("sql_batch_completed",
			xeColumn("duration", "uint64", gosmo.XEColumnData, "", false),
			xeColumn("batch_text", "unicode_string", gosmo.XEColumnData, "", false),
			xeColumn("collect_batch_text", "boolean", gosmo.XEColumnCustomizable, "true", false)),
		xeColumnsResponse("attention",
			xeColumn("duration", "uint64", gosmo.XEColumnData, "", false)),
		xeColumnsResponse("event_file",
			xeColumn("filename", "unicode_string_ptr", gosmo.XEColumnCustomizable, "", true),
			xeColumn("max_file_size", "uint64", gosmo.XEColumnCustomizable, "1024", false),
			xeColumn("lazy_create_blob", "boolean", gosmo.XEColumnCustomizable, "true", false)),
		xeColumnsResponse("ring_buffer",
			xeColumn("max_memory", "uint32", gosmo.XEColumnCustomizable, "0", false)),
	}
}

// xePropsConn is a connection answering the session name's by-name read, the
// catalog and the columns.
func xePropsConn(t *testing.T, name string) (*db.ServerConn, *fakeInstance) {
	t.Helper()
	responses := xeSessionByName(name)
	responses = append(responses, xeCatalogResponses()...)
	responses = append(responses, xeEventColumnResponses()...)
	return newFakeConn(t, responses...)
}

func xeTestHost(sc *db.ServerConn) xeHost {
	return xeHost{app: newTestApp(), sc: sc, ctx: context.Background}
}

// formGrids returns a form's grids in page order.
func formGrids(f *propsheet.Form) []*controls.DataGrid {
	var out []*controls.DataGrid
	for _, r := range f.Rows() {
		if g, ok := r.(*propsheet.GridRow); ok {
			out = append(out, g.Grid)
		}
	}
	return out
}

func statementsContaining(inst *fakeInstance, s string) []string {
	var out []string
	for _, st := range inst.Statements() {
		if strings.Contains(st, s) {
			out = append(out, st)
		}
	}
	return out
}

func TestXERetentionAndPartitionLabelsAndValuesArePaired(t *testing.T) {
	for _, tc := range []struct {
		items, values []string
		want          map[string]string
	}{
		{xeRetentionItems, xeRetentionValues, map[string]string{
			"Single event loss":   gosmo.XERetentionAllowSingleEventLoss,
			"Multiple event loss": gosmo.XERetentionAllowMultipleEventLoss,
			"No event loss":       gosmo.XERetentionNoEventLoss,
		}},
		{xePartitionItems, xePartitionValues, map[string]string{
			"None":     gosmo.XEPartitionNone,
			"Per node": gosmo.XEPartitionPerNode,
			"Per CPU":  gosmo.XEPartitionPerCPU,
		}},
	} {
		if len(tc.items) != len(tc.values) || len(tc.items) != len(tc.want) {
			t.Fatalf("%d labels, %d values, %d pinned", len(tc.items), len(tc.values), len(tc.want))
		}
		for label, want := range tc.want {
			i := slices.Index(tc.items, label)
			if i < 0 || tc.values[i] != want {
				t.Errorf("%q writes %v, want %q", label, i, want)
			}
		}
	}
}

// The builder quotes by the source's type, uses the ansi LIKE for an ansi
// field, and takes a value already quoted as typed.
func TestXEClauseQuotesByType(t *testing.T) {
	str := xePredSource{ref: "[sqlserver].[database_name]", typeName: "unicode_string"}
	num := xePredSource{ref: "[duration]", typeName: "uint64"}
	ansi := xePredSource{ref: "[file]", typeName: "ansi_string"}
	for _, tc := range []struct {
		src       xePredSource
		op, value string
		want      string
	}{
		{str, "=", "O'Brien", "[sqlserver].[database_name]=N'O''Brien'"},
		{num, ">=", " 1000 ", "[duration]>=(1000)"},
		{str, "like", "%x%", "[sqlserver].[like_i_sql_unicode_string]([sqlserver].[database_name],N'%x%')"},
		{ansi, "not like", "a%", "NOT [sqlserver].[like_i_sql_ansi_string]([file],'a%')"},
		{num, "=", "N'already'", "[duration]=N'already'"},
	} {
		got, err := xeClause(tc.src, tc.op, tc.value)
		if err != nil || got != tc.want {
			t.Errorf("xeClause(%s %s %q) = %q, %v; want %q", tc.src.ref, tc.op, tc.value, got, err, tc.want)
		}
	}
	if _, err := xeClause(num, "=", "  "); err == nil {
		t.Error("an empty value made a clause")
	}
	if got := xeJoinPredicate("  ", "AND", "[a]=(1)"); got != "[a]=(1)" {
		t.Errorf("first clause = %q", got)
	}
	if got := xeJoinPredicate("([a]=(1))", "OR", "[b]=(2)"); got != "([a]=(1)) OR [b]=(2)" {
		t.Errorf("joined = %q", got)
	}
}

// Collecting one more global field and filtering the second event drops and
// re-adds that event only; the first is not named at all.
func TestXEEventsPageAltersOnlyTheEditedEvent(t *testing.T) {
	sc, inst := xePropsConn(t, "zz_trace")
	f, apply := loadPage(t, pageXESessionEvents(xeTestHost(sc), "zz_trace"), inst)
	grids := formGrids(f)
	lib, sel, acts, fields := grids[0], grids[1], grids[2], grids[3]

	if got := lib.Row(gridRowIndex(t, lib, 1, "rpc_completed"))[0]; got != "Yes" {
		t.Errorf("the library does not mark rpc_completed as in the session: %q", got)
	}
	selectGridRow(t, sel, 0, "sqlserver.sql_batch_completed")
	activateGridCell(t, acts, 1, "sqlserver.client_app_name", 0)
	textRow(t, f, "Filter (predicate)").Edit("[duration]>(5)")
	activateGridCell(t, fields, 0, "collect_batch_text", 0)

	if !f.Dirty() {
		t.Fatal("the page does not report its edits")
	}
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	drops := statementsContaining(inst, "DROP EVENT")
	adds := statementsContaining(inst, "ADD EVENT")
	if len(drops) != 1 || !strings.Contains(drops[0], "DROP EVENT sqlserver.sql_batch_completed") {
		t.Errorf("drops = %q", drops)
	}
	want := "ADD EVENT sqlserver.sql_batch_completed(SET collect_batch_text=(0)\n    ACTION(sqlserver.client_app_name)\n    WHERE [duration]>(5))"
	if len(adds) != 1 || !strings.Contains(adds[0], want) {
		t.Errorf("adds = %q, want one holding %q", adds, want)
	}
	for _, st := range inst.Statements() {
		if strings.Contains(st, "rpc_completed") {
			t.Errorf("the untouched event was written: %q", st)
		}
	}
}

// Adding an event from a filtered library adds it; removing every event is
// refused before anything is sent.
func TestXEEventsPageLibraryAddsAndKeepsOneEvent(t *testing.T) {
	sc, inst := xePropsConn(t, "zz_trace")
	f, apply := loadPage(t, pageXESessionEvents(xeTestHost(sc), "zz_trace"), inst)
	grids := formGrids(f)
	lib, sel := grids[0], grids[1]

	textRow(t, f, "Search events").Edit("atten")
	if lib.Row(1) != nil || lib.Row(0)[1] != "attention" {
		t.Fatalf("search left %v", lib.Row(0))
	}
	activateGridCell(t, lib, 1, "attention", 1)
	if gridRowIndex(t, sel, 0, "sqlserver.attention") < 0 {
		t.Fatal("not added")
	}
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if adds := statementsContaining(inst, "ADD EVENT sqlserver.attention"); len(adds) != 1 {
		t.Errorf("statements = %q", inst.Statements())
	}

	for sel.Row(0) != nil {
		clickFormButton(t, f, "Remove Event")
	}
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "at least one event") {
		t.Errorf("Validate = %v", err)
	}
}

// Adding a ring_buffer adds that target alone; an event_file without its
// filename is refused, and Revert puts the list back; a numeric parameter
// that is not a number is refused.
func TestXEStoragePageAddsATargetAndRequiresAFilename(t *testing.T) {
	sc, inst := xePropsConn(t, "zz_trace")
	f, apply := loadPage(t, pageXESessionStorage(xeTestHost(sc), "zz_trace"), inst)
	tgt, params := formGrids(f)[0], formGrids(f)[1]

	// zz_trace already has both kinds; take the ring_buffer off and put a
	// histogram on.
	selectGridRow(t, tgt, 0, "package0.ring_buffer")
	clickFormButton(t, f, "Remove Target")
	sr := selectRow(t, f, "Target type")
	sr.Edit(slices.Index(sr.Items(), "package0.histogram"))
	clickFormButton(t, f, "Add Target")
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := statementsContaining(inst, "TARGET"); len(st) != 2 ||
		!strings.Contains(st[0], "DROP TARGET package0.ring_buffer") || !strings.Contains(st[1], "ADD TARGET package0.histogram") {
		t.Errorf("statements = %q", inst.Statements())
	}

	selectGridRow(t, tgt, 0, "package0.event_file")
	selectGridRow(t, params, 0, "filename")
	textRow(t, f, "Selected parameter's value").Edit("")
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "filename") {
		t.Errorf("Validate = %v", err)
	}
	f.Revert()
	if f.Dirty() || tgt.Row(1) == nil || tgt.Row(1)[0] != "package0.ring_buffer" {
		t.Errorf("after Revert: dirty %v, rows %v %v", f.Dirty(), tgt.Row(0), tgt.Row(1))
	}

	// A numeric parameter is written into the DDL as (value), so a value
	// that is not a number is refused here, not after the dialog closes.
	selectGridRow(t, tgt, 0, "package0.event_file")
	selectGridRow(t, params, 0, "max_file_size")
	textRow(t, f, "Selected parameter's value").Edit("5),max_rollover_files=(0")
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "package0.event_file: field max_file_size") {
		t.Errorf("Validate = %v", err)
	}
}

// On a running session an option change asks first, and gosmo's stop window
// wraps the ALTER.
func TestXEAdvancedPageOnARunningSessionAsksAndRestarts(t *testing.T) {
	sc, inst := xePropsConn(t, "system_health")
	f, apply := loadPage(t, pageXESessionAdvanced(sc, xeScope{}, "system_health"), inst)
	if f.ApplyConfirm() != "" {
		t.Error("an untouched page asks")
	}
	sr := selectRow(t, f, "Event retention mode")
	sr.Edit(slices.Index(xeRetentionItems, "No event loss"))
	if f.ApplyConfirm() != xeRunningAlterWarning {
		t.Errorf("ApplyConfirm = %q", f.ApplyConfirm())
	}
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := inst.Statements()
	if len(st) != 3 || !strings.HasSuffix(st[0], "STATE = STOP") ||
		!strings.Contains(st[1], "WITH (EVENT_RETENTION_MODE=NO_EVENT_LOSS)") || !strings.HasSuffix(st[2], "STATE = START") {
		t.Errorf("statements = %q", st)
	}
}

// Startup state alone is one ALTER, with no stop window.
func TestXEGeneralPageWritesStartupStateAlone(t *testing.T) {
	sc, inst := xePropsConn(t, "system_health")
	f, apply := loadPage(t, pageXESessionGeneral(sc, xeScope{}, "system_health"), inst)
	for _, r := range f.Rows() {
		if c, ok := r.(*propsheet.CheckRow); ok && c.Label() == "Start at server startup" {
			c.Edit(true)
		}
	}
	if f.ApplyConfirm() != "" {
		t.Error("a startup-state change asks, though the server takes it on a running session")
	}
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := inst.Statements(); len(st) != 1 || !strings.HasSuffix(st[0], "WITH (STARTUP_STATE=ON)") {
		t.Errorf("statements = %q", st)
	}
}

// A template fills the events, targets and options, leaving out what the
// server's catalog lacks; the create writes it all, then starts the session.
func TestNewXESessionTemplateCreatesAndStarts(t *testing.T) {
	// The create reads its session back; an empty answer is "not found",
	// which gosmo takes as the create having worked.
	sc, inst := newFakeConn(t, append(xeCatalogResponses(),
		fakeResponse{match: "s.event_retention_mode_desc", arg: "gossms_new", cols: 12})...)
	cat, err := loadXECatalog(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	app := newTestApp()
	d := NewNewXESessionDialog(app)
	d.sc = sc
	d.ctx = context.Background()
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	d.buildPages(&nxeSessionPrefetch{existing: newNameSet("", "taken"), cat: cat, mayStart: true, major: 17})

	gen := d.forms[nxePageGeneral]
	if err := d.preflight(); err == nil {
		t.Error("an unnamed session passed preflight")
	}
	textRow(t, gen, "Session name").Edit("taken")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("preflight = %v", err)
	}
	textRow(t, gen, "Session name").Edit("gossms_new")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "at least one event") {
		t.Errorf("preflight = %v", err)
	}

	tr := selectRow(t, gen, "Template")
	tr.Edit(slices.Index(tr.Items(), "Query Batch Tracking"))
	// Four events, less error_reported, which the fake catalog lacks.
	if got := formGrids(d.forms[nxePageEvents])[1]; got.Row(2) == nil || got.Row(3) != nil {
		t.Fatalf("the template's three available events did not arrive: %v", got.Row(0))
	}
	var hint string
	for _, r := range gen.Rows() {
		if n, ok := r.(*propsheet.NoteRow); ok && strings.Contains(n.Text(), "left out") {
			hint = n.Text()
		}
	}
	// The fake catalog lacks error_reported and most of the template's
	// actions: each is named once.
	if !strings.Contains(hint, "sqlserver.error_reported") || strings.Count(hint, "sqlserver.query_hash") != 1 {
		t.Errorf("hint = %q", hint)
	}
	for _, r := range gen.Rows() {
		if c, ok := r.(*propsheet.CheckRow); ok && c.Label() == "Start the session after creation" {
			c.Edit(true)
		}
	}
	if err := d.preflight(); err != nil {
		t.Fatal(err)
	}
	if err := d.applyFns[nxePageGeneral](context.Background()); err != nil {
		t.Fatal(err)
	}
	st := inst.Statements()
	if len(st) != 2 || !strings.HasPrefix(st[0], "CREATE EVENT SESSION [gossms_new] ON SERVER") ||
		!strings.Contains(st[0], "ADD EVENT sqlserver.attention(ACTION(package0.event_sequence,sqlserver.client_app_name)") ||
		strings.Contains(st[0], "error_reported") ||
		!strings.Contains(st[0], "MAX_DISPATCH_LATENCY=30 SECONDS") || !strings.HasSuffix(st[1], "STATE = START") {
		t.Errorf("statements = %q", st)
	}
}

// A login that may create but not start a session gets neither box.
func TestNewXESessionWithoutStartRightNeitherStartsNorWatches(t *testing.T) {
	g := newXENewGeneral(&nxeSessionPrefetch{cat: &xeCatalog{}, mayStart: false},
		newXESessionModel(gosmo.EventSessionSpec{}), newXEOptionRows(xeNewSessionDefaults(), 17, false))
	g.start.SetChecked(true)
	g.watch.SetChecked(true)
	if g.startNow() || g.watchNow() {
		t.Error("the create would start a session the login may not start")
	}
	if !g.start.ReadOnly() || !g.watch.ReadOnly() {
		t.Error("the boxes are offered")
	}
}

// The type hint tells a map from the other non-numeric types — xml read as a
// map once, live.
func TestXETypeHintTellsAMapFromXML(t *testing.T) {
	for _, tc := range []struct {
		typ   string
		isMap bool
		want  string
	}{
		{"unicode_string", false, "N'…'"},
		{"wait_types", true, "a map"},
		{"xml", false, "as typed"},
		{"uint64", false, "a number"},
		{"boolean", false, "0 or 1"},
	} {
		if got := xeTypeHint(tc.typ, tc.isMap); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q", tc.typ, got)
		}
	}
}

// Selecting an event lists the global fields it collects first; toggling one
// leaves every row where it was.
func TestXEEventsPageListsCollectedGlobalFieldsFirst(t *testing.T) {
	sc, inst := xePropsConn(t, "zz_trace")
	f, _ := loadPage(t, pageXESessionEvents(xeTestHost(sc), "zz_trace"), inst)
	sel, acts := formGrids(f)[1], formGrids(f)[2]
	selectGridRow(t, sel, 0, "sqlserver.rpc_completed")
	if got := acts.Row(0); got[0] != "Yes" || got[1] != "sqlserver.sql_text" {
		t.Fatalf("first global field = %v, want the collected sql_text", got)
	}
	before := gridRowIndex(t, acts, 1, "sqlserver.client_app_name")
	activateGridCell(t, acts, 1, "sqlserver.client_app_name", 0)
	if after := gridRowIndex(t, acts, 1, "sqlserver.client_app_name"); after != before {
		t.Errorf("the toggled row moved from %d to %d", before, after)
	}
}

// A login that can read a session but not alter it gets the Events page
// read-only. Its grids still browse — Tab reaches the selected-events grid and
// Down moves to the second event, whose fields then fill the page — and no
// press on any grid adds an event, collects an action or flips a field, so the
// page has nothing to apply.
func TestXEEventsPageBrowsesReadOnly(t *testing.T) {
	sc, inst := xePropsConn(t, "zz_trace")
	f, _ := loadPage(t, pageXESessionEvents(xeTestHost(sc), "zz_trace"), inst)
	grids := formGrids(f)
	lib, sel, acts, fields := grids[0], grids[1], grids[2], grids[3]
	f.SetReadOnly(true) // what PropertySheet.SetPageForm does on a gated page
	f.Focus(true)

	press := func(k tcell.Key) { f.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone)) }
	space := func() { f.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone)) }

	// Focus lands on the library; Enter and Space there would add an event.
	press(tcell.KeyEnter)
	space()
	press(tcell.KeyTab)
	if g, ok := f.Focused().(*propsheet.GridRow); !ok || g.Grid != sel {
		t.Fatalf("Tab went to %T, want the selected-events grid past the read-only filter rows", f.Focused())
	}
	if sel.Row(0)[0] != "sqlserver.rpc_completed" {
		t.Fatalf("first selected event = %v", sel.Row(0))
	}
	press(tcell.KeyDown)
	press(tcell.KeyEnter)
	if !strings.Contains(sectionTitles(f), "sqlserver.sql_batch_completed") {
		t.Errorf("sections = %q, want the second event's Configure heading", sectionTitles(f))
	}
	if gridRowIndex(t, fields, 0, "collect_batch_text") < 0 {
		t.Fatal("the second event's fields did not load")
	}
	for _, g := range []*controls.DataGrid{lib, acts, fields} {
		g.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
		g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
	}

	if sel.Row(1) == nil || sel.Row(2) != nil {
		t.Error("browsing changed the session's events")
	}
	for _, r := range f.Rows() {
		if e, ok := r.(propsheet.Editable); ok && e.Dirty() {
			t.Errorf("row %T went dirty on a read-only page", r)
		}
	}
	if f.Dirty() {
		t.Error("the read-only page reports a change")
	}
}

func sectionTitles(f *propsheet.Form) string {
	var out []string
	for _, r := range f.Rows() {
		if s, ok := r.(*propsheet.SectionRow); ok {
			out = append(out, s.Title())
		}
	}
	return strings.Join(out, " | ")
}
