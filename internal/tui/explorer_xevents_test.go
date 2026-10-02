package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Object Explorer wiring for Management ▸ Extended Events — the
// per-family checklist the other families cover, plus the running/stopped
// state that drives the glyph and the Start/Stop pair, the 2016–2019 versus
// 2022 permission split, and the typed Delete on SQL Server's own sessions.

// xeSessionRow is one row of gosmo's session read: id, name, startup state,
// running, retention, latency ms, max memory, max event size, partition mode,
// causality, max duration, dropped events.
func xeSessionRow(id int64, name string, running bool, dropped int64) []driver.Value {
	return []driver.Value{id, name, false, running, "ALLOW_SINGLE_EVENT_LOSS", int64(30000),
		int64(4096), int64(0), "NONE", false, int64(0), dropped}
}

// xeSessionResponses scripts gosmo's five-read session listing: three
// sessions, the user one ("zz_trace", stopped) neither first nor running, so a
// loader that reads row 0 or ignores the running flag fails. zz_trace has two
// targets; system_health one.
func xeSessionResponses() []fakeResponse {
	return []fakeResponse{
		{match: "s.event_retention_mode_desc", cols: 12, rows: [][]driver.Value{
			xeSessionRow(1, "AlwaysOn_health", false, 0),
			xeSessionRow(2, "system_health", true, 7),
			xeSessionRow(3, "zz_trace", false, 0),
		}},
		{match: "e.predicate", cols: 5, rows: [][]driver.Value{
			{int64(2), int64(1), "sqlserver", "error_reported", "([severity]>=(20))"},
			{int64(3), int64(1), "sqlserver", "rpc_completed", nil},
			{int64(3), int64(2), "sqlserver", "sql_batch_completed", nil},
		}},
		{match: "FROM   sys.server_event_session_actions", cols: 4, rows: [][]driver.Value{
			{int64(3), int64(1), "sqlserver", "sql_text"},
		}},
		{match: "FROM   sys.server_event_session_targets", cols: 4, rows: [][]driver.Value{
			{int64(2), int64(3), "package0", "ring_buffer"},
			{int64(3), int64(3), "package0", "event_file"},
			{int64(3), int64(4), "package0", "ring_buffer"},
		}},
		{match: "FROM   sys.server_event_session_fields", cols: 5, rows: [][]driver.Value{
			{int64(2), int64(3), "max_memory", "4096", "int"},
			{int64(3), int64(3), "filename", "zz_trace", "nvarchar"},
		}},
	}
}

// xeSessionByName is the by-name read of one session, scoped with arg: and
// placed before the list responses (docs/testing.md): the listing's queries
// contain every string the by-name ones do.
func xeSessionByName(name string) []fakeResponse {
	var out []fakeResponse
	for _, r := range xeSessionResponses() {
		// The by-name read filters in SQL; the fake doesn't, so keep only the
		// named session's rows here.
		id := int64(-1)
		for _, row := range xeSessionResponses()[0].rows {
			if row[1] == name {
				id = row[0].(int64)
			}
		}
		r.arg = name
		var rows [][]driver.Value
		for _, row := range r.rows {
			if row[0] == id {
				rows = append(rows, row)
			}
		}
		r.rows = rows
		out = append(out, r)
	}
	return out
}

func TestExtendedEventsHangsUnderManagement(t *testing.T) {
	children, err := loadManagementChildren(loaderCtx{}, &explorerNode{})
	if err != nil {
		t.Fatal(err)
	}
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"Resource Governor", "Extended Events", "SQL Server Logs", "Database Mail"}) {
		t.Errorf("Management's children are %v, want Resource Governor, Extended Events, SQL Server Logs, Database Mail (SSMS's order)", got)
	}
	sub, _ := loadExtendedEventsChildren(loaderCtx{}, children[1])
	if len(sub) != 2 || sub[0].data.Type != NodeEventSessions || sub[0].label != "Sessions" ||
		sub[1].data.Type != NodeXEventProfiler || sub[1].label != "XEvent Profiler" {
		t.Errorf("Extended Events' children are %v", labelsOfNodes(sub))
	}
}

func TestEventSessionsFolderListsEverySessionWithItsState(t *testing.T) {
	sc, _ := newFakeConn(t, xeSessionResponses()...)
	children, err := loadEventSessionsChildren(loaderCtx{ctx: context.Background(), sc: sc},
		&explorerNode{data: nodeData{Type: NodeEventSessions, conn: sc}})
	if err != nil {
		t.Fatalf("loadEventSessionsChildren: %v", err)
	}
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"AlwaysOn_health", "system_health", "zz_trace"}) {
		t.Fatalf("sessions = %v", got)
	}
	for i, want := range []bool{false, true, false} {
		if children[i].data.IsEnabled != want {
			t.Errorf("%s IsEnabled = %v, want %v", children[i].label, children[i].data.IsEnabled, want)
		}
		// Built-in sessions are listed like any other, not hidden as system.
		if children[i].data.IsSystem {
			t.Errorf("%s is marked system — it would lose Script and Delete", children[i].label)
		}
	}
}

// The state is the glyph and nothing else, so each style must draw the two
// differently — or a stopped session is indistinguishable from a running one.
func TestAStoppedSessionDrawsDifferentlyInEveryStyle(t *testing.T) {
	for _, style := range []config.IconStyle{config.IconStyleEmoji, config.IconStyleSymbols, config.IconStylePortable} {
		running := nodeIcon(nodeData{Type: NodeEventSession, IsEnabled: true}, style, false)
		stopped := nodeIcon(nodeData{Type: NodeEventSession}, style, false)
		if running == stopped || running == '•' || stopped == '•' {
			t.Errorf("style %v: running %q, stopped %q", style, running, stopped)
		}
	}
}

func TestEventSessionChildrenAreItsTargets(t *testing.T) {
	sc, _ := newFakeConn(t, append(xeSessionByName("zz_trace"), xeSessionResponses()...)...)
	node := &explorerNode{data: nodeData{Type: NodeEventSession, Name: "zz_trace", conn: sc}}
	children, err := loadEventSessionChildren(loaderCtx{ctx: context.Background(), sc: sc}, node)
	if err != nil {
		t.Fatalf("loadEventSessionChildren: %v", err)
	}
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"package0.event_file", "package0.ring_buffer"}) {
		t.Fatalf("targets = %v", got)
	}
	for _, c := range children {
		if c.data.Type != NodeEventTarget || c.data.XESession != "zz_trace" {
			t.Errorf("target %q carries %+v", c.label, c.data)
		}
	}
	if children[1].data.Name != "ring_buffer" {
		t.Errorf("target Name = %q, want the bare target name", children[1].data.Name)
	}
}

func eventSessionMenuItem(t *testing.T, a *App, node *explorerNode, label string) controls.MenuItem {
	t.Helper()
	items := a.contextMenuItemsForNode(node)
	i := slices.IndexFunc(items, func(it controls.MenuItem) bool { return it.Label == label })
	if i < 0 {
		t.Fatalf("menu %v has no %q", labelsOf(items), label)
	}
	return items[i]
}

func TestStartAndStopAreEachOfferedInTheirOwnState(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	for _, running := range []bool{true, false} {
		node := &explorerNode{data: nodeData{Type: NodeEventSession, Name: "zz_trace", IsEnabled: running, conn: sc}}
		start := eventSessionMenuItem(t, a, node, "Start Session")
		stop := eventSessionMenuItem(t, a, node, "Stop Session")
		if start.Enabled() == running || stop.Enabled() != running {
			t.Errorf("running=%v: Start enabled %v, Stop enabled %v", running, start.Enabled(), stop.Enabled())
		}
	}
}

// Either permission set satisfies: 2016–2019 know only the wide name, and 2022
// adds a granular one per verb. The granular names read unknown on the older
// versions, and that unknown must not stand in for a wide name the server
// refused.
func TestEventSessionVerbsGateOnEitherPermissionSet(t *testing.T) {
	for _, c := range []struct {
		name            string
		granted, denied []string
		start, stop     bool
		del             bool
	}{
		{name: "2019, wide name held", granted: []string{"ALTER ANY EVENT SESSION"},
			start: true, stop: true, del: true},
		{name: "2019, wide name refused", denied: []string{"ALTER ANY EVENT SESSION"}},
		{name: "2022, only ENABLE held",
			granted: []string{"ALTER ANY EVENT SESSION ENABLE"},
			denied:  []string{"ALTER ANY EVENT SESSION", "ALTER ANY EVENT SESSION DISABLE", "DROP ANY EVENT SESSION"},
			start:   true},
		{name: "2022, only DROP held",
			granted: []string{"DROP ANY EVENT SESSION"},
			denied:  []string{"ALTER ANY EVENT SESSION", "ALTER ANY EVENT SESSION ENABLE", "ALTER ANY EVENT SESSION DISABLE"},
			del:     true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, _ := newFakeConn(t, capabilityResponses(true, c.granted, c.denied, nil, nil)...)
			sc.ProbeCapabilities()
			stopped := &explorerNode{data: nodeData{Type: NodeEventSession, Name: "zz_trace", conn: sc}}
			running := &explorerNode{data: nodeData{Type: NodeEventSession, Name: "zz_trace", IsEnabled: true, conn: sc}}
			if got := eventSessionMenuItem(t, a, stopped, "Start Session").Enabled(); got != c.start {
				t.Errorf("Start enabled = %v, want %v", got, c.start)
			}
			if got := eventSessionMenuItem(t, a, running, "Stop Session").Enabled(); got != c.stop {
				t.Errorf("Stop enabled = %v, want %v", got, c.stop)
			}
			if got := eventSessionMenuItem(t, a, stopped, "Delete...").Enabled(); got != c.del {
				t.Errorf("Delete enabled = %v, want %v", got, c.del)
			}
		})
	}
}

func TestStartRunsAtOnceAndStopAsksFirst(t *testing.T) {
	for _, start := range []bool{true, false} {
		a := newTestApp()
		sc, inst := newFakeConn(t)
		node := opTestNode(sc, NodeEventSession, "", "zz_trace", "")
		node.data.DBName = "" // a server-scoped session's node carries none
		node.data.IsEnabled = !start

		a.setEventSessionState(sc, node, start)
		if got := a.confirmDialog.Visible(); got == start {
			t.Fatalf("start=%v: confirmation shown = %v, want only a stop to ask", start, got)
		}
		if !start {
			answerConfirm(t, a, false)
		}
		waitAndDrain(t, a)

		want := "ALTER EVENT SESSION [zz_trace] ON SERVER STATE = STOP"
		if start {
			want = "ALTER EVENT SESSION [zz_trace] ON SERVER STATE = START"
		}
		if stmts := inst.Statements(); !slices.Equal(stmts, []string{want}) {
			t.Errorf("start=%v: statements = %q, want [%s]", start, stmts, want)
		}
		if node.data.IsEnabled != start {
			t.Errorf("start=%v: node IsEnabled = %v after success", start, node.data.IsEnabled)
		}
	}
}

func TestEventSessionScriptsAndDrops(t *testing.T) {
	a := &App{}
	items := a.scriptMenuItems(opNode(NodeEventSession, "", "zz_trace", ""))
	if len(items) == 0 || items[0].Label != "Script Session as" {
		t.Fatalf("script items = %v", labelsOf(items))
	}
	if got, want := labelsOf(items[0].Sub), []string{"CREATE To", "DROP To", "DROP And CREATE To"}; !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}

	op := objectOps[NodeEventSession]
	if op.drop == nil || op.rename != nil {
		t.Fatal("an event session drops and has no rename (ALTER EVENT SESSION has no WITH NAME)")
	}
	sc, inst := newFakeConn(t)
	if err := op.drop(t.Context(), sc, nodeData{Type: NodeEventSession, Name: "zz_trace"}); err != nil {
		t.Fatal(err)
	}
	if stmts := inst.Statements(); !slices.Equal(stmts, []string{"DROP EVENT SESSION [zz_trace] ON SERVER"}) {
		t.Errorf("drop ran %q", stmts)
	}
}

// SQL Server's own sessions take a typed confirmation and are kept out of a
// batch; a user session is neither.
func TestDeletingABuiltInSessionAsksForItsName(t *testing.T) {
	op := objectOpFor(NodeEventSession)
	for name, builtIn := range map[string]bool{
		"system_health": true, "AlwaysOn_health": true, "telemetry_xevents": true,
		"SYSTEM_HEALTH": true, "zz_trace": false, "system_health_copy": false,
	} {
		n := nodeData{Type: NodeEventSession, Name: name}
		if typedDelete(op, n) != builtIn || deletedAlone(op, n) != builtIn {
			t.Errorf("%s: typed %v, alone %v, want both %v", name, typedDelete(op, n), deletedAlone(op, n), builtIn)
		}
	}

	a := newTestApp()
	sc, inst := newFakeConn(t)
	a.confirmDeleteObjects(sc, []nodeData{{Type: NodeEventSession, Name: "system_health"}}, func() {})
	if !a.confirmTypedDialog.Visible() || a.confirmDialog.Visible() {
		t.Fatalf("typed %v, plain %v — want the typed confirmation", a.confirmTypedDialog.Visible(), a.confirmDialog.Visible())
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("ran %q before the name was typed", stmts)
	}

	a = newTestApp()
	a.confirmDeleteObjects(sc, []nodeData{
		{Type: NodeEventSession, Name: "zz_trace"}, {Type: NodeEventSession, Name: "system_health"},
	}, func() {})
	if a.confirmDialog.Visible() || a.confirmTypedDialog.Visible() {
		t.Error("a batch holding system_health was offered for confirmation")
	}
	if !strings.Contains(a.statusText, "system_health") {
		t.Errorf("status = %q, want it to name the session to delete alone", a.statusText)
	}
}

func TestEventSessionsFolderDetail(t *testing.T) {
	sc, _ := newFakeConn(t, xeSessionResponses()...)
	var objs []nodeData
	cols, rows, err := eventSessionsFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeEventSessions}}, &objs)
	if err != nil {
		t.Fatal(err)
	}
	col := func(name string) int {
		i := slices.Index(cols, name)
		if i < 0 {
			t.Fatalf("no %q column in %v", name, cols)
		}
		return i
	}
	if len(rows) != 3 || len(objs) != 3 {
		t.Fatalf("%d rows, %d row objects", len(rows), len(objs))
	}
	sh, zz := rows[1], rows[2]
	if sh[col("State")] != "Running" || sh[col("Dropped")] != "7" || sh[col("Targets")] != "ring_buffer" {
		t.Errorf("system_health row = %v", sh)
	}
	// A stopped session has no counter; "0" would read as "losing nothing".
	if zz[col("State")] != "Stopped" || zz[col("Dropped")] != "" ||
		zz[col("Events")] != "2" || zz[col("Targets")] != "event_file, ring_buffer" {
		t.Errorf("zz_trace row = %v", zz)
	}
	if objs[2] != (nodeData{Type: NodeEventSession, Name: "zz_trace"}) {
		t.Errorf("row object 2 = %+v", objs[2])
	}

	// The folder's filter applies to the pane too, before rows are built.
	objs = nil
	filtered := &explorerNode{data: nodeData{Type: NodeEventSessions, Filter: &nodeFilter{criteria: []filterCriterion{
		{prop: filterProps(NodeEventSessions)[0], op: opContains, value: "zz"},
	}}}}
	_, rows, _ = eventSessionsFolderDetail(context.Background(), sc, filtered, &objs)
	if len(rows) != 1 || rows[0][0] != "zz_trace" || len(objs) != 1 {
		t.Errorf("filtered rows = %v", rows)
	}
}

func TestEventSessionDetailListsEventsThenTargets(t *testing.T) {
	sc, _ := newFakeConn(t, append(xeSessionByName("zz_trace"), xeSessionResponses()...)...)
	cols, rows, err := eventSessionDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeEventSession, Name: "zz_trace"}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r[0]+" "+r[1])
	}
	want := []string{"Event sqlserver.rpc_completed", "Event sqlserver.sql_batch_completed",
		"Target package0.event_file", "Target package0.ring_buffer"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if rows[0][slices.Index(cols, "Actions")] != "sqlserver.sql_text" ||
		rows[2][slices.Index(cols, "Settings")] != "filename=zz_trace" {
		t.Errorf("rows = %v", rows)
	}
}

// xeSavedTree is an App over sc with Sessions ▸ zz_trace expanded and loaded
// — its two targets under it — and system_health listed but never expanded.
// zz_trace is selected.
func xeSavedTree(t *testing.T, sc *db.ServerConn) (a *App, session, idle *explorerNode, targets []*explorerNode) {
	t.Helper()
	a = newTestApp()
	a.explorer.SetBounds(0, 0, 40, 30)
	a.detailBrowser = NewDetailBrowser("Object Explorer Details")
	a.propDialog = NewPropDialog(a)
	a.connections = append(a.connections, sc)
	a.explorer.AddRoot("testsrv", sc)
	root := a.explorer.Selected()
	folder := &explorerNode{label: "Sessions", data: nodeData{Type: NodeEventSessions, conn: sc}}
	session = &explorerNode{label: "zz_trace", data: nodeData{Type: NodeEventSession, Name: "zz_trace", conn: sc}}
	idle = &explorerNode{label: "system_health", data: nodeData{Type: NodeEventSession, Name: "system_health", conn: sc}}
	root.expanded, folder.expanded, session.expanded = true, true, true
	a.explorer.SetChildren(root, []*explorerNode{folder})
	a.explorer.SetChildren(folder, []*explorerNode{idle, session})
	targets = []*explorerNode{
		{label: "package0.event_file", data: nodeData{Type: NodeEventTarget, Name: "event_file", XESession: "zz_trace", conn: sc}},
		{label: "package0.ring_buffer", data: nodeData{Type: NodeEventTarget, Name: "ring_buffer", XESession: "zz_trace", conn: sc}},
	}
	a.explorer.SetChildren(session, targets)
	a.explorer.view.SelectID(session.id)
	return a, session, idle, targets
}

// dropRingBuffer opens Session Properties on zz_trace, waits for Data Storage
// and takes its ring_buffer off.
func dropRingBuffer(t *testing.T, a *App) *PropDialog {
	t.Helper()
	a.showEventSessionPropertiesFor(a.connections[0], xeScope{}, "zz_trace")
	d := a.propDialog
	d.SelectPage(2)
	drainUntil(t, a, func() bool { return d.PageState(2) == propsheet.PageReady }, "Data Storage to load")
	f := d.PageForm(2)
	selectGridRow(t, formGrids(f)[0], 0, "package0.ring_buffer")
	clickFormButton(t, f, "Remove Target")
	return d
}

// A target added or dropped on Data Storage is a child of the session node:
// Apply reloads that node, so the tree shows the change without a Refresh,
// and the selection stays where it was. Another session is left alone, and Script Changes — which wrote nothing — reloads nothing.
func TestSessionPropertiesApplyReloadsTheSessionsTargets(t *testing.T) {
	t.Run("Apply", func(t *testing.T) {
		sc, inst := xePropsConn(t, "zz_trace")
		a, session, idle, targets := xeSavedTree(t, sc)
		d := dropRingBuffer(t, a)

		runAndWait(t, d, func() { d.runApply(false) })
		if len(statementsContaining(inst, "DROP TARGET")) != 1 {
			t.Fatalf("statements = %q", inst.Statements())
		}
		for _, tg := range targets {
			if !tg.retired {
				t.Errorf("target %q survived the Apply — the session node was not reloaded", tg.label)
			}
		}
		if idle.expanded || len(idle.children) > 0 {
			t.Error("another session was touched")
		}
		if got := a.explorer.Selected(); got != session {
			t.Errorf("selection after the reload = %v, want the session node", got)
		}
		// The reload's fetch lands; Apply's page reloads often post first,
		// so one callback is not enough.
		drainUntil(t, a, func() bool { return len(session.children) == 2 }, "the session's targets to reload")
		if got := labelsOfNodes(session.children); !slices.Equal(got, []string{"package0.event_file", "package0.ring_buffer"}) {
			t.Errorf("session's children after the reload = %v", got)
		}
		if got := a.explorer.Selected(); got != session {
			t.Errorf("selection after the fetch = %v, want the session node", got)
		}
	})
	t.Run("Script Changes", func(t *testing.T) {
		sc, _ := xePropsConn(t, "zz_trace")
		a, _, _, targets := xeSavedTree(t, sc)
		d := dropRingBuffer(t, a)

		runAndWait(t, d, d.runScript)
		for _, tg := range targets {
			if tg.retired {
				t.Errorf("Script Changes reloaded the session node (target %q retired)", tg.label)
			}
		}
	})
}

// The hook belongs to the showing that set it: the next Properties dialog,
// for any other object, must not reload the session.
func TestAnotherDialogDoesNotInheritTheSessionsHook(t *testing.T) {
	sc, _ := xePropsConn(t, "zz_trace")
	a, _, _, _ := xeSavedTree(t, sc)
	a.showEventSessionPropertiesFor(sc, xeScope{}, "zz_trace")
	a.propDialog.Dismiss()
	a.propDialog.show(sc, "", "Properties", "", "", func() []propPage { return nil })
	if a.propDialog.onSaved != nil {
		t.Error("the session dialog's onSaved survived into the next showing")
	}
}
