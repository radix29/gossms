package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

// databaseSecurityReads answers the reads Scripter.ScriptDatabaseSecurity
// makes in dbName: its owner, one user role, one user and one grant.
func databaseSecurityReads(dbName string) []fakeResponse {
	return []fakeResponse{
		{match: "SUSER_SNAME(owner_sid)", cols: 2, rows: [][]driver.Value{{"sa", []byte{1}}}},
		dbByNameResp(dbName, 7),
		{match: "WHERE  type = 'A'", cols: 2},
		{match: "OUTER  APPLY", cols: 10, rows: [][]driver.Value{
			{"app", "G", "SELECT", int64(3), "SCHEMA", int64(5), nil, nil, "sales", nil}}},
		{match: "r.is_fixed_role", cols: 5, rows: [][]driver.Value{{"readers", int64(5), false, "dbo", `[{"v":"app"}]`}}},
		{match: "authentication_type_desc", cols: 11, rows: [][]driver.Value{
			{"app", int64(6), "SQL_USER", "dbo", time.Time{}, time.Time{}, "INSTANCE", []byte{2}, "app_login", false, nil}}},
		{match: "SELECT s.name, s.schema_id", cols: 3},
	}
}

// "Script Database Security" opens gosmo's script of the node's database in
// a new query window, starting in that database, and runs nothing: the user
// reviews and executes it.
func TestScriptDatabaseSecurityOpensUnexecutedScript(t *testing.T) {
	const dbName = "odd]db"
	sc, inst := newFakeConn(t, databaseSecurityReads(dbName)...)
	a := newTestApp()
	node := &explorerNode{label: dbName, data: nodeData{Type: NodeDatabase, DBName: dbName, DBState: "ONLINE", conn: sc}}

	item := findMenuItem(a.contextMenuItemsForNode(node), "Script Database Security")
	if item == nil {
		t.Fatal("the database menu has no Script Database Security item")
	}
	if item.Enabled != nil {
		t.Error("Script Database Security is gated; it only reads the catalog and opens a script")
	}
	item.Action()
	drainUntil(t, a, func() bool { return a.panels.Count() == 1 || strings.HasPrefix(a.statusText, "Script error") }, "the query window")
	if a.panels.Count() != 1 {
		t.Fatalf("no query window: %s", a.statusText)
	}

	qp, ok := a.panels.PanelAt(0).(*QueryPanel)
	if !ok {
		t.Fatalf("panel is %T, want a *QueryPanel", a.panels.PanelAt(0))
	}
	text := qp.editor.Text()
	if !strings.HasPrefix(text, "USE [odd]]db];\nGO\n") {
		t.Errorf("script does not start in the node's database, quoted: %q", text[:min(len(text), 40)])
	}
	for _, want := range []string{
		"CREATE ROLE [readers] AUTHORIZATION [dbo];",
		"CREATE USER [app] FOR LOGIN [app_login] WITH DEFAULT_SCHEMA = [dbo];",
		"ALTER ROLE [readers] ADD MEMBER [app];",
		"GRANT SELECT ON SCHEMA::[sales] TO [app];",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("script lacks %q:\n%s", want, text)
		}
	}
	if got := inst.Statements(); len(got) != 0 {
		t.Errorf("opening the script executed %q", got)
	}
}

// A failed read reports itself on the status line and opens no window.
func TestScriptDatabaseSecurityReportsAFailedRead(t *testing.T) {
	sc, _ := newFakeConn(t, dbByNameResp("appdb", 7)) // nothing answers the owner read
	a := newTestApp()
	a.scriptDatabaseSecurity(sc, "appdb")
	drainUntil(t, a, func() bool { return strings.HasPrefix(a.statusText, "Script error:") }, "the error status")
	if a.panels.Count() != 0 {
		t.Errorf("a failed script opened %d window(s)", a.panels.Count())
	}
}
