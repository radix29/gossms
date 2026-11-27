package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
)

func newSecurityCompletionPanel(t *testing.T) *QueryPanel {
	t.Helper()
	p := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	p.app.completion.security = make(map[string]*securityInventory)
	for _, class := range securityClasses {
		database := p.database
		if class == "LOGIN" || class == "SERVER ROLE" || class == "ENDPOINT" || class == "AVAILABILITY GROUP" {
			database = ""
		}
		p.app.completion.security[securityInventoryKey(p.conn, database, class)] = &securityInventory{
			serverKey: sysCompletionInventoryKey(p.conn.Opts), database: database, kind: class,
		}
	}
	seed := func(database, kind string, names ...securityName) {
		p.app.completion.security[securityInventoryKey(p.conn, database, kind)] = &securityInventory{
			serverKey: sysCompletionInventoryKey(p.conn.Opts), database: database, kind: kind, names: names,
		}
	}
	seed(p.database, "OBJECT",
		securityName{class: "OBJECT", schema: "dbo", name: "Customers", objectClass: gosmo.SecurableTable},
		securityName{class: "OBJECT", schema: "dbo", name: "Orders", objectClass: gosmo.SecurableTable},
		securityName{class: "OBJECT", schema: "dbo", name: "MessageQueue", objectClass: securityQueueClass},
		securityName{class: "OBJECT", schema: "dbo", name: "RunReport", objectClass: gosmo.SecurableProcedure})
	seed(p.database, "database-principals",
		securityName{class: "USER", name: "reader"}, securityName{class: "ROLE", name: "report role"},
		securityName{class: "USER", name: "odd] user"},
		securityName{class: "APPLICATION ROLE", name: "app"})
	seed("", "server-principals", securityName{class: "LOGIN", name: "login"}, securityName{class: "SERVER ROLE", name: "server_role"})
	seed("", "server-logins", securityName{class: "LOGIN", name: "login"})
	return p
}

func TestSecurityCompletionGrammar(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want []string
		not  []string
	}{
		{"GR|", []string{"GRANT"}, nil},
		{"DE|", []string{"DENY", "DELETE"}, nil},
		{"RE|", []string{"REVOKE"}, nil},
		{"GRANT |", []string{"SELECT", "CREATE TABLE", "CONTROL SERVER", "ALTER ANY LOGIN"}, []string{"COUNT", "Customers"}},
		{"CREATE PROCEDURE p AS GRANT |", []string{"SELECT", "CREATE TABLE", "CONTROL SERVER"}, []string{"dbo.Customers"}},
		{"CREATE OR ALTER PROCEDURE p AS GRANT |", []string{"SELECT", "CREATE TABLE"}, []string{"dbo.Customers"}},
		{"BEGIN\nGRANT |\nEND", []string{"SELECT", "CREATE TABLE"}, []string{"dbo.Customers"}},
		{"BEGIN GRANT |", []string{"SELECT", "CREATE TABLE"}, []string{"dbo.Customers"}},
		{"IF 1=1 GRANT |", []string{"SELECT", "CREATE TABLE"}, []string{"dbo.Customers"}},
		{"IF 1=1 SELECT 1 ELSE GRANT |", []string{"SELECT", "CREATE TABLE"}, []string{"dbo.Customers"}},
		{"SELECT CASE WHEN 1=1 THEN\n grant\n ELSE\n deny\n END FROM dbo.Orders WHERE |", []string{"Id", "CustomerId", "Total"}, []string{"TO", "FROM"}},
		{"IF EXISTS (SELECT 1) DENY |", []string{"SELECT", "CREATE TABLE"}, []string{"dbo.Customers"}},
		{"WHILE 1=1 REVOKE |", []string{"SELECT", "GRANT OPTION FOR"}, []string{"dbo.Customers"}},
		{"GRANT |\nCREATE INDEX ix ON dbo.Customers(Id)", []string{"SELECT", "CREATE TABLE", "CONTROL SERVER"}, nil},
		{"GRANT |\nCREATE TABLE ON DATABASE::testdb TO reader", []string{"SELECT", "CREATE TABLE"}, []string{"CONTROL SERVER", "ALTER ANY LOGIN"}},
		{"GRANT SEL|\nCREATE INDEX ix ON dbo.Customers(Id)", []string{"SELECT"}, nil},
		{"DENY |", []string{"SELECT", "EXECUTE", "ALL"}, []string{"GRANT OPTION FOR"}},
		{"REVOKE |", []string{"SELECT", "GRANT OPTION FOR"}, nil},
		{"GRANT ALTER |", []string{"ANY LOGIN", "ANY USER"}, nil},
		{"GRANT ALTER AN|", []string{"ANY LOGIN", "ANY USER"}, nil},
		{"GRANT VIEW SERVER |", []string{"STATE"}, nil},
		{"GRANT SELECT, |", []string{"UPDATE", "INSERT"}, []string{"SELECT", "COUNT", "FROM"}},
		{"GRANT SELECT |", []string{"ON", "TO"}, []string{"FROM", "COUNT"}},
		{"REVOKE SELECT |", []string{"ON", "TO", "FROM"}, nil},
		{"GRANT SELECT ON |", []string{"OBJECT::", "SCHEMA::", "DATABASE::", "dbo.Customers"}, []string{"COUNT", "SERVER::"}},
		{"GRANT CONTROL ON SERVER |", []string{"ROLE::"}, nil},
		{"GRANT SELECT ON OBJECT::dbo.Cus|", []string{"Customers"}, []string{"COUNT"}},
		{"GRANT SELECT ON SCHEMA::|", []string{"dbo", "sales"}, nil},
		{"GRANT SELECT ON DATABASE::|", []string{"testdb"}, nil},
		{"GRANT SELECT ON dbo.Customers |", []string{"TO"}, []string{"FROM", "WHERE"}},
		{"REVOKE SELECT ON dbo.Customers |", []string{"TO", "FROM"}, nil},
		{"GRANT SELECT (|) ON dbo.Customers TO reader", []string{"Id", "Name", "Email"}, []string{"COUNT"}},
		{"GRANT SELECT (Id, |) ON dbo.Customers TO reader", []string{"Name", "Email"}, []string{"Id"}},
		{"GRANT SELECT ON dbo.Customers (|)", []string{"Id", "Name", "Email"}, nil},
		{"GRANT SELECT ON dbo.Customers TO |", []string{"reader", "report role", "app"}, []string{"login"}},
		{"GRANT CONTROL SERVER TO |", []string{"login", "server_role"}, []string{"reader"}},
		{"GRANT SELECT ON dbo.Customers TO reader, |", []string{"report role", "app"}, []string{"reader"}},
		{"GRANT SELECT ON dbo.Customers TO reader |", []string{"WITH GRANT OPTION", "AS"}, []string{"CASCADE"}},
		{"GRANT SELECT ON dbo.Customers TO reader WITH |", []string{"GRANT OPTION"}, nil},
		{"GRANT SELECT ON dbo.Customers TO reader WITH GRANT |", []string{"OPTION"}, nil},
		{"GRANT SELECT ON dbo.Customers TO reader WITH GRANT OPTION |", []string{"AS"}, []string{"WITH GRANT OPTION"}},
		{"GRANT SELECT ON dbo.Customers TO reader WITH GRANT OPTION, |", nil, []string{"reader", "report role", "app", "AS"}},
		{"DENY SELECT ON dbo.Customers TO reader CASCADE, |", nil, []string{"reader", "report role", "app", "AS"}},
		{"REVOKE SELECT ON dbo.Customers FROM reader AS login, |", nil, []string{"reader", "report role", "app", "AS"}},
		{"GRANT SELECT ON dbo.Customers TO reader AS |", []string{"reader", "report role", "app"}, []string{"login"}},
		{"GRANT CONTROL SERVER TO login AS |", []string{"login"}, []string{"server_role", "reader"}},
		{"DENY SELECT ON dbo.Customers TO reader |", []string{"CASCADE", "AS"}, []string{"WITH GRANT OPTION"}},
		{"DENY SELECT ON dbo.Customers TO reader CASCADE |", []string{"AS"}, []string{"CASCADE"}},
		{"REVOKE GRANT |", []string{"OPTION FOR"}, nil},
		{"REVOKE GRANT OPTION |", []string{"FOR"}, nil},
		{"REVOKE GRANT OPTION FOR |", []string{"SELECT"}, []string{"GRANT OPTION FOR"}},
		{"REVOKE GRANT OPTION FOR SELECT FROM reader |", []string{"CASCADE", "AS"}, nil},
		{"SELECT * FROM dbo.Orders;\nGRANT SELECT ON dbo.Customers TO |", []string{"reader"}, []string{"CustomerId"}},
		{"GRANT SELECT ON dbo.Customers TO reader;\nSELECT | FROM dbo.Orders", []string{"Id", "CustomerId", "Total"}, []string{"CASCADE"}},
		{"GRANT SELECT ON dbo.Customers TO reader\nSELECT | FROM dbo.Orders", []string{"Id", "CustomerId", "Total"}, []string{"CASCADE"}},
		{"GRANT /* comment */ SELECT,\n UPDATE ON dbo.Customers TO |", []string{"reader"}, []string{"COUNT"}},
		{"GRANT SELECT (Id) | ON dbo.Customers", []string{"ON"}, []string{"UPDATE", "INSERT"}},
		{"GRANT SELECT (Id) |", []string{"ON"}, []string{"TO", "FROM"}},
		{"GRANT ALTER ANY LOGIN |", []string{"TO"}, []string{"ON"}},
		{"GRANT CONTROL SERVER |", []string{"TO"}, []string{"ON"}},
		{"REVOKE SELECT ON dbo.Customers FROM reader |", []string{"CASCADE", "AS"}, nil},
		{"REVOKE ALL FROM reader |", []string{"CASCADE", "AS"}, nil},
		{"REVOKE SELECT ON dbo.Customers TO reader |", []string{"CASCADE", "AS"}, nil},
		{"SELECT deny, | FROM dbo.Orders o", []string{"Id", "CustomerId", "Total"}, []string{"CREATE TABLE"}},
		{"SELECT o.Total AS deny, | FROM dbo.Orders o", []string{"Id", "CustomerId", "Total"}, []string{"CREATE TABLE"}},
		{"UPDATE dbo.Orders SET deny = 1 WHERE |", []string{"Id", "CustomerId", "Total"}, []string{"CREATE TABLE"}},
		{"SELECT Id AS grant FROM dbo.Orders WHERE |", []string{"Id", "CustomerId", "Total"}, []string{"WITH GRANT OPTION"}},
		{"SELECT revoke FROM dbo.Orders WHERE |", []string{"Id", "CustomerId", "Total"}, []string{"AS"}},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders grant ON grant.CustomerId = c.|", []string{"Id", "Name", "Email"}, nil},
		{"SELECT 1 AS revoke;\nGRANT SELECT ON dbo.Customers TO |", []string{"reader"}, nil},
		{"SELECT * FROM dbo.Orders\nGRANT |", []string{"SELECT", "CREATE TABLE"}, []string{"WHERE"}},
		{"UPDATE dbo.Orders SET Total = 1\nDENY |", []string{"SELECT", "UPDATE"}, []string{"WHERE"}},
		{"GRANT SELECT ON dbo.Customers TO reader\nGRANT |", []string{"SELECT", "CREATE TABLE"}, nil},
		{"GRANT SELECT ON dbo.Customers TO reader\nREVOKE |", []string{"SELECT", "GRANT OPTION FOR"}, nil},
		{"SELECT * FROM dbo.Orders\nGRANT SEL|", []string{"SELECT"}, nil},
		{"USE testdb\nGRANT |", []string{"SELECT"}, []string{"dbo.Customers"}},
		{"GRANT SELECT ON dbo.RunReport TO reader\nSELECT 1\nGRANT |", []string{"SELECT"}, []string{"FROM"}},
		{"GRANT SELECT ON dbo.Customers\nSELECT | FROM dbo.Orders", []string{"CustomerId", "Total"}, []string{"Email"}},
		{"SELECT * FROM dbo.Orders\nGRANT EXTERNAL ACCESS ASSEMBLY TO |", []string{"login"}, []string{"reader"}},
		{"SELECT * FROM dbo.Orders\nGRANT UNSAFE ASSEMBLY TO |", []string{"login"}, []string{"reader"}},
		{"GRANT SELECT ON testdb.dbo.|", nil, []string{"Customers", "Orders"}},
		{"GRANT SELECT ON testdb.dbo.Customers (|)", nil, []string{"Id", "Name"}},
		{"GRANT SELECT ON FOO::|", nil, []string{"Customers"}},
		{"GRANT SELECT ON SERVER::|", nil, []string{"Customers"}},
		{"SELECT Id,\n  Deny,\n  | FROM dbo.Orders", []string{"CustomerId", "Total"}, []string{"CREATE TABLE"}},
		{"SELECT * FROM dbo.Orders\nORDER BY\n  deny DESC,\n  |", []string{"CustomerId", "Total"}, []string{"CREATE TABLE"}},
		{"SELECT *\nFROM dbo.Orders o\nWHERE o.Id = 1 AND\n  revoke = 0 AND |", []string{"CustomerId", "Total"}, []string{"CREATE TABLE"}},
		{"UPDATE dbo.Orders SET\n  Total = 1,\n  grant = 2,\n  | = 3", []string{"CustomerId"}, []string{"CREATE TABLE"}},
		{"SELECT Id\n  FROM dbo.Orders\n  revoke\n  WHERE |", []string{"CustomerId", "Total"}, []string{"CREATE TABLE"}},
		{"GRANT |\nSELECT * FROM dbo.Orders o JOIN dbo.Customers c ON o.CustomerId = c.Id", []string{"CREATE TABLE", "ALTER ANY LOGIN"}, nil},
		{"GRANT SELECT, |\nUPDATE dbo.Orders SET Total = 1 FROM dbo.Orders o JOIN dbo.Customers c ON o.CustomerId = c.Id", []string{"CREATE TABLE", "ALTER ANY USER"}, nil},
		{"GRANT SELECT ON dbo.Customers |", []string{"TO"}, []string{"DATABASE::", "OBJECT::", "SCHEMA::"}},
		{"GRANT SELECT ON dbo.Customers (Id) |", []string{"TO"}, []string{"DATABASE::", "OBJECT::", "SCHEMA::"}},
		{"GRANT RECEIVE ON dbo.|", []string{"MessageQueue"}, []string{"Customers", "Orders", "RunReport"}},
		{"GRANT | ON dbo.Customers", []string{"SELECT"}, []string{"RECEIVE"}},
		{"GRANT | ON dbo.RunReport", []string{"EXECUTE"}, []string{"RECEIVE"}},
		{"GRANT | ON dbo.MessageQueue", []string{"RECEIVE"}, []string{"SELECT", "EXECUTE"}},
		{"SELECT * FROM dbo.Orders\nrevoke\nINNER JOIN dbo.Customers c ON c.Id = revoke.CustomerId WHERE |", []string{"Total", "Email"}, []string{"TO", "FROM"}},
		{"SELECT [revoke].| FROM dbo.Orders\n[revoke]\nINNER JOIN dbo.Customers c ON c.Id = [revoke].CustomerId", []string{"CustomerId", "Total"}, nil},
		{"SELECT * FROM dbo.Orders\ndeny\nORDER BY |", []string{"CustomerId", "Total"}, nil},
		{"SELECT * FROM dbo.Orders o\ngrant\nGROUP BY |", []string{"CustomerId", "Total"}, nil},
		{"GRANT CONTROL SERVER ON |", nil, []string{"dbo.Customers", "dbo", "sales"}},
		{"GRANT CREATE TABLE ON |", []string{"DATABASE::"}, []string{"dbo.Customers", "dbo", "sales"}},
		{"GRANT IMPERSONATE ON |", []string{"LOGIN::", "USER::"}, []string{"dbo.Customers", "dbo", "sales"}},
		{"GRANT CREATE TABLE ON SCHEMA::|", nil, []string{"dbo", "sales"}},
		{"GRANT CONTROL SERVER, |", []string{"ALTER ANY LOGIN"}, []string{"SELECT", "CREATE TABLE"}},
		{"GRANT SELECT, ALTER ANY |", []string{"USER", "SCHEMA"}, []string{"LOGIN", "SERVER ROLE"}},
		{"GRANT ALTER ANY LOGIN, |", []string{"CONTROL SERVER"}, []string{"SELECT", "CREATE TABLE"}},
		{"GRANT ALL ON dbo.|", []string{"Customers"}, []string{"MessageQueue"}},
		{"GRANT ALL PRIVILEGES ON dbo.|", []string{"Customers"}, []string{"MessageQueue"}},
		{"GRANT ALL |", []string{"PRIVILEGES", "ON", "TO"}, nil},
		{"GRANT ALL PRIVILEGES |", []string{"ON", "TO"}, nil},
		{"GRANT SELECT TO public |", []string{"WITH GRANT OPTION", "AS"}, nil},
		{"DENY SELECT TO reader, app CASCADE |", []string{"AS"}, []string{"WITH GRANT OPTION"}},
		{"REVOKE GRANT OPTION FOR SELECT ON dbo.Customers FROM |", []string{"reader", "app"}, nil},
		{"GRANT SELECT (Id), | ON dbo.Customers", []string{"UPDATE"}, []string{"SELECT"}},
		{"GRANT CONTROL (|) ON dbo.Customers", nil, []string{"Id", "COUNT"}},
		{"GRANT EXECUTE ON dbo.RunReport (|)", nil, []string{"Id"}},
		{"GRANT | ON dbo.RunReport", []string{"EXECUTE", "REFERENCES"}, []string{"SELECT", "UPDATE"}},
		{"GRANT | ON dbo.Customers", []string{"SELECT", "UPDATE"}, []string{"EXECUTE"}},
		{"GRANT EXECUTE ON dbo.|", []string{"RunReport"}, []string{"Customers", "Orders"}},
		{"GRANT SELECT ON d|", []string{"dbo"}, nil},
		{"GRANT SELECT ON OBJECT::dbo.[Cus|]", []string{"Customers"}, nil},
		{"GRANT SELECT ON dbo.Customers TO [rea|]", []string{"reader"}, nil},
		{"GRANT SELECT ON dbo.Customers TO reader WITH GRANT OPTION\nWITH c AS (SELECT Id FROM dbo.Orders) SELECT | FROM c", []string{"Id"}, []string{"Email"}},
		{"GRANT SELECT ON dbo.Customers TO reader\nGO\nDENY |", []string{"UPDATE"}, []string{"COUNT"}},
		{"GRANT SELECT ON dbo.Customers TO 'rea|'", nil, []string{"reader"}},
		{"GRANT SELECT ON dbo.Customers TO /* rea| */ reader", nil, []string{"reader"}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			p := newSecurityCompletionPanel(t)
			lines, row, col := linesAndCursor(t, tc.sql)
			items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, want := range tc.want {
				if !containsLabel(items, want) {
					t.Errorf("missing %q in %v", want, labels(items))
				}
			}
			for _, unwanted := range tc.not {
				if containsLabel(items, unwanted) {
					t.Errorf("unexpected %q in %v", unwanted, labels(items))
				}
			}
		})
	}
}
func TestSecurityCompletionEditor(t *testing.T) {
	for _, tc := range []struct{ sql, typed, after, want string }{
		{"GRANT ALTER ANY ", "log", "", "GRANT ALTER ANY LOGIN |"},
		{"GRANT SELECT ON OBJECT::dbo.", "cus", "", "GRANT SELECT ON OBJECT::dbo.Customers |"},
		{"GRANT SELECT ON dbo.Customers TO ", "report", "", "GRANT SELECT ON dbo.Customers TO [report role] |"},
		{"GRANT SELECT ON dbo.Customers TO reader ", "with", "", "GRANT SELECT ON dbo.Customers TO reader WITH GRANT OPTION |"},
		{"REVOKE ", "grant", "", "REVOKE GRANT OPTION FOR |"},
		{"REVOKE ", "gra", "", "REVOKE GRANT OPTION FOR |"},
		{"GRANT SELECT ON dbo.Customers TO reader ", "wit", "", "GRANT SELECT ON dbo.Customers TO reader WITH GRANT OPTION |"},
		{"GRANT SELECT ON dbo.Customers (", "I", ") TO reader", "GRANT SELECT ON dbo.Customers (Id|) TO reader"},
		{"GRANT SELECT ON dbo.Customers (", "odd", ") TO reader", "GRANT SELECT ON dbo.Customers ([odd]] col]|) TO reader"},
		{"GRANT SELECT ON dbo.Customers TO [", "report", "", "GRANT SELECT ON dbo.Customers TO [report role] |"},
		{"GRANT SELECT ON dbo.", "cus", " TO reader", "GRANT SELECT ON dbo.Customers |TO reader"},
		{"GRANT SELECT ON dbo.Customers TO [", "rea", "] WITH GRANT OPTION", "GRANT SELECT ON dbo.Customers TO reader |WITH GRANT OPTION"},
		{"GRANT SELECT ON dbo.Customers TO ", "rea", " -- keep", "GRANT SELECT ON dbo.Customers TO reader |-- keep"},
		{"GRANT SELECT ON dbo.Customers TO ", "rea", "\nSELECT 1", "GRANT SELECT ON dbo.Customers TO reader |\nSELECT 1"},
		{"GRANT SELECT ON dbo.Customers TO ", "odd", "", "GRANT SELECT ON dbo.Customers TO [odd]] user] |"},
	} {
		for _, accept := range []struct {
			name string
			key  tcell.Key
		}{{"Tab", tcell.KeyTab}, {"Enter", tcell.KeyEnter}} {
			t.Run(tc.sql+tc.typed+"/"+accept.name, func(t *testing.T) {
				p := newSecurityCompletionPanel(t)
				inv := p.app.completion.inventories[completionInventoryKey(p.conn.Opts, p.database)]
				inv.catalog.Objects[0].Columns = append(inv.catalog.Objects[0].Columns, gosmo.CatalogColumn{Name: "odd] col", DataType: "int"})
				p.editor.SetText(tc.sql + tc.after)
				p.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
				for range []rune(tc.after) {
					p.editor.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
				}
				for _, r := range tc.typed {
					p.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
				}
				p.editor.HandleKey(tcell.NewEventKey(accept.key, "", tcell.ModNone))
				_, wantRow, wantCol := linesAndCursor(t, tc.want)
				want := strings.ReplaceAll(tc.want, "|", "")
				if got := p.editor.Text(); got != want {
					t.Fatalf("accepting produced %q, want %q", got, want)
				}
				row, col := p.editor.CursorPos()
				if row != wantRow || col != wantCol {
					t.Fatalf("caret %d,%d, want %d,%d", row, col, wantRow, wantCol)
				}
			})
		}
	}
}

func TestSecurityCompletionEmptyPrefixEnter(t *testing.T) {
	p := newSecurityCompletionPanel(t)
	for _, r := range "GRANT SELECT ON dbo.Customers TO reader " {
		p.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
	p.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if got := p.editor.Text(); got != "GRANT SELECT ON dbo.Customers TO reader \n" {
		t.Fatalf("Enter accepted an untyped suggestion instead of a newline: %q", got)
	}
	row, col := p.editor.CursorPos()
	if row != 1 || col != 0 {
		t.Fatalf("newline caret = %d,%d", row, col)
	}
}

func TestSecurityCompletionFabric(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want []string
		not  []string
	}{
		{"GRANT |", []string{"SELECT", "CREATE TABLE", "ALTER ANY USER"}, []string{"ALL", "CONTROL SERVER", "ALTER ANY LOGIN", "CREATE ASSEMBLY"}},
		{"REVOKE |", []string{"SELECT"}, []string{"GRANT OPTION FOR"}},
		{"GRANT CONTROL ON |", []string{"OBJECT::", "DATABASE::", "SCHEMA::", "ROLE::", "USER::"}, []string{"LOGIN::", "SERVER ROLE::", "ASSEMBLY::"}},
		{"GRANT SELECT ON dbo.Customers TO reader |", []string{"WITH GRANT OPTION"}, []string{"AS", "CASCADE"}},
		{"DENY SELECT ON dbo.Customers TO reader |", []string{"CASCADE"}, []string{"AS"}},
		{"REVOKE SELECT ON dbo.Customers FROM reader |", []string{"CASCADE"}, []string{"AS"}},
		{"GRANT SELECT ON testdb.dbo.Cus|", []string{"Customers"}, nil},
		{"GRANT SELECT ON testdb.dbo.Customers (|)", []string{"Id", "Name"}, nil},
		{"GRANT SELECT ON otherdb.dbo.Cus|", nil, []string{"Customers"}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			p := newSecurityCompletionPanel(t)
			p.conn.Server = fabricConn(t).Server
			lines, row, col := linesAndCursor(t, tc.sql)
			items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, want := range tc.want {
				if !containsLabel(items, want) {
					t.Errorf("missing %q in %v", want, labels(items))
				}
			}
			for _, not := range tc.not {
				if containsLabel(items, not) {
					t.Errorf("unexpected %q in %v", not, labels(items))
				}
			}
		})
	}
}

func TestSecurityCompletionPermissionCatalogLifecycle(t *testing.T) {
	f := gosmotest.New(t)
	server := f.Server()
	f.OnFunc(func(q gosmotest.Query) bool {
		return strings.Contains(q.Text, "sys.fn_builtin_permissions")
	}, gosmotest.Rows([]any{"SERVER", "VIEW SERVER PERFORMANCE STATE"}, []any{"OBJECT", "SELECT"}))
	f.OnFunc(func(gosmotest.Query) bool { return true }, gosmotest.Fail(errors.New("optional metadata unavailable")))
	p := newSecurityCompletionPanel(t)
	p.conn.Server = server
	inv := p.app.ensureSecurityInventory(p.conn, "", "permissions")
	drainUntil(t, p.app, func() bool { return !inv.loading }, "security permission vocabulary")
	lines, row, col := linesAndCursor(t, "GRANT VIEW SERVER |")
	items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "PERFORMANCE STATE") {
		t.Fatalf("engine's newer permission was lost: %v", labels(items))
	}
	lines, row, col = linesAndCursor(t, "GRANT | ON dbo.Customers")
	items, _ = p.sqlCompletionCandidates(completionReq(lines, row, col))
	if containsLabel(items, "VIEW SERVER PERFORMANCE STATE") || !containsLabel(items, "SELECT") {
		t.Fatalf("catalog not filtered by target scope: %v", labels(items))
	}
	p.app.refreshSecurityCompletion(p.conn)
	drainUntil(t, p.app, func() bool { return !inv.loading }, "refreshed security vocabulary")
	reads := 0
	for _, query := range f.Queries() {
		if strings.Contains(query.Text, "sys.fn_builtin_permissions") {
			reads++
		}
	}
	if reads != 2 {
		t.Fatalf("expected one lazy read and one refresh, got %d reads", reads)
	}
	ctx, seq := inv.load.Begin(context.Background())
	p.app.purgeSecurityCompletion(sysCompletionInventoryKey(p.conn.Opts))
	if inv.load.Done(seq) || ctx.Err() == nil || len(p.app.completion.security) != 0 {
		t.Fatal("disconnect did not abandon security loads and remove cached metadata")
	}
}

func TestSecurityCompletionPermissionCatalogFailure(t *testing.T) {
	f := gosmotest.New(t)
	server := f.Server()
	f.OnFunc(func(q gosmotest.Query) bool { return strings.Contains(q.Text, "sys.fn_builtin_permissions") },
		gosmotest.Fail(errors.New("metadata denied")))
	p := newSecurityCompletionPanel(t)
	p.conn.Server = server
	inv := p.app.ensureSecurityInventory(p.conn, "", "permissions")
	drainUntil(t, p.app, func() bool { return !inv.loading }, "failed security vocabulary read")
	for _, marked := range []string{"GRANT |", "SELECT | FROM dbo.Customers"} {
		lines, row, col := linesAndCursor(t, marked)
		items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
		want := "SELECT"
		if strings.HasPrefix(marked, "SELECT") {
			want = "Name"
		}
		if !containsLabel(items, want) {
			t.Fatalf("optional permission read broke %q: %v", marked, labels(items))
		}
	}
	if inv.err == nil || !slices.ContainsFunc(f.Queries(), func(q gosmotest.Query) bool { return strings.Contains(q.Text, "builtin_permissions") }) {
		t.Fatal("failure fixture did not exercise the permission read")
	}
}

func TestSecurityCompletionObjectMetadataFailure(t *testing.T) {
	p := newSecurityCompletionPanel(t)
	definitions := p.securityPermissionDefinitions()
	f := gosmotest.New(t)
	p.conn.Server = f.Server()
	f.OnFunc(func(q gosmotest.Query) bool { return strings.Contains(q.Text, "x.kind") },
		gosmotest.Fail(errors.New("securable metadata denied")))
	p.app.completion.security[securityInventoryKey(p.conn, "", "permissions")] = &securityInventory{permissions: definitions}
	key := securityInventoryKey(p.conn, p.database, "OBJECT")
	delete(p.app.completion.security, key)
	lines, row, col := linesAndCursor(t, "GRANT SELECT ON dbo.|")
	items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "Customers") {
		t.Fatal("catalog names were unavailable during the optional securable read")
	}
	inv := p.app.completion.security[key]
	drainUntil(t, p.app, func() bool { return !inv.loading }, "failed object metadata read")
	items, _ = p.sqlCompletionCandidates(completionReq(lines, row, col))
	if inv.err == nil || !containsLabel(items, "Customers") {
		t.Fatalf("securable read failure removed catalog completion: %v, %v", inv.err, labels(items))
	}
	cacheSize := len(p.app.completion.security)
	for _, marked := range []string{"GRANT SELECT ON FOO::|", "GRANT SELECT ON SERVER::|"} {
		lines, row, col := linesAndCursor(t, marked)
		p.sqlCompletionCandidates(completionReq(lines, row, col))
	}
	if len(p.app.completion.security) != cacheSize {
		t.Fatal("unsupported class names created metadata loads/cache entries")
	}
}

func TestSecurityCompletionRoutineFallback(t *testing.T) {
	p := newSecurityCompletionPanel(t)
	key := completionInventoryKey(p.conn.Opts, p.database)
	cat := p.app.completion.inventories[key].catalog
	cat.Functions = []gosmo.CatalogObject{{Schema: "dbo", Name: "Totals", Type: gosmo.CatalogFunction,
		Columns: []gosmo.CatalogColumn{{Name: "Total", DataType: "int"}}}}
	inv := newCompletionInventory(cat)
	inv.procedures = []*gosmo.StoredProcedure{{Schema: "dbo", Name: "RunReport"}}
	p.app.completion.inventories[key] = inv
	p.app.completion.security[securityInventoryKey(p.conn, p.database, "OBJECT")] = &securityInventory{err: errors.New("metadata denied")}
	for _, tc := range []struct {
		sql, want, not string
	}{
		{"GRANT EXECUTE ON dbo.|", "RunReport", "Totals"},
		{"GRANT | ON dbo.RunReport", "EXECUTE", "SELECT"},
		{"GRANT SELECT ON dbo.|", "Totals", "RunReport"},
		{"GRANT | ON dbo.Totals", "SELECT", "EXECUTE"},
		{"GRANT SELECT ON dbo.Totals (|)", "Total", "EXECUTE"},
	} {
		lines, row, col := linesAndCursor(t, tc.sql)
		items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
		if !containsLabel(items, tc.want) || containsLabel(items, tc.not) {
			t.Errorf("%q: missing %q or unexpected %q in %v", tc.sql, tc.want, tc.not, labels(items))
		}
	}
}

func TestSecurityCompletionTypedCatalogs(t *testing.T) {
	now := time.Unix(0, 0)
	principal := func(name, kind string) []any {
		return []any{name, int64(10), kind, "dbo", now, now, "DATABASE", []byte{1}, nil, nil, nil, false, "dbo", "[]"}
	}
	for _, tc := range []struct {
		class, sql string
		want, not  []string
	}{
		{"OBJECT", "GRANT CONTROL ON OBJECT::dbo.Tar|", []string{"TargetTable", "TargetQueue"}, []string{"TargetType", "TargetCert"}},
		{"TYPE", "GRANT CONTROL ON TYPE::dbo.Tar|", []string{"TargetType"}, []string{"TargetTable", "TargetCert"}},
		{"CERTIFICATE", "GRANT CONTROL ON CERTIFICATE::Tar|", []string{"TargetCert"}, []string{"TargetTable"}},
		{"USER", "GRANT CONTROL ON USER::Tar|", []string{"TargetUser"}, []string{"TargetRole", "TargetApp"}},
		{"ROLE", "GRANT CONTROL ON ROLE::Tar|", []string{"TargetRole"}, []string{"TargetUser", "TargetApp"}},
		{"APPLICATION ROLE", "GRANT CONTROL ON APPLICATION ROLE::Tar|", []string{"TargetApp"}, []string{"TargetRole", "TargetUser"}},
		{"database-principals", "GRANT SELECT TO Tar|", []string{"TargetUser", "TargetRole", "TargetApp"}, []string{"TargetLogin"}},
		{"LOGIN", "GRANT CONTROL ON LOGIN::Tar|", []string{"TargetLogin"}, []string{"TargetServerRole"}},
		{"SERVER ROLE", "GRANT CONTROL ON SERVER ROLE::Tar|", []string{"TargetServerRole"}, []string{"TargetLogin"}},
		{"server-principals", "GRANT CONTROL SERVER TO Tar|", []string{"TargetLogin", "TargetServerRole"}, []string{"TargetUser"}},
		{"server-logins", "GRANT CONTROL SERVER TO login AS Tar|", []string{"TargetLogin"}, []string{"TargetServerRole"}},
		{"ENDPOINT", "GRANT CONTROL ON ENDPOINT::Tar|", []string{"TargetEndpoint"}, nil},
		{"AVAILABILITY GROUP", "GRANT CONTROL ON AVAILABILITY GROUP::Tar|", []string{"TargetGroup"}, nil},
		{"ASSEMBLY", "GRANT CONTROL ON ASSEMBLY::Tar|", []string{"TargetAssembly"}, nil},
		{"ASYMMETRIC KEY", "GRANT CONTROL ON ASYMMETRIC KEY::Tar|", []string{"TargetAsymmetric"}, nil},
		{"SYMMETRIC KEY", "GRANT CONTROL ON SYMMETRIC KEY::Tar|", []string{"TargetSymmetric"}, nil},
		{"DATABASE SCOPED CREDENTIAL", "GRANT CONTROL ON DATABASE SCOPED CREDENTIAL::Tar|", []string{"TargetCredential"}, nil},
		{"XML SCHEMA COLLECTION", "GRANT CONTROL ON XML SCHEMA COLLECTION::dbo.Tar|", []string{"TargetXML"}, nil},
		{"FULLTEXT CATALOG", "GRANT CONTROL ON FULLTEXT CATALOG::Tar|", []string{"TargetFulltext"}, nil},
		{"FULLTEXT STOPLIST", "GRANT CONTROL ON FULLTEXT STOPLIST::Tar|", []string{"TargetStoplist"}, nil},
		{"SEARCH PROPERTY LIST", "GRANT CONTROL ON SEARCH PROPERTY LIST::Tar|", []string{"TargetProperties"}, nil},
		{"CONTRACT", "GRANT CONTROL ON CONTRACT::Tar|", []string{"TargetContract"}, nil},
		{"MESSAGE TYPE", "GRANT CONTROL ON MESSAGE TYPE::Tar|", []string{"TargetMessage"}, nil},
		{"REMOTE SERVICE BINDING", "GRANT CONTROL ON REMOTE SERVICE BINDING::Tar|", []string{"TargetBinding"}, nil},
		{"ROUTE", "GRANT CONTROL ON ROUTE::Tar|", []string{"TargetRoute"}, nil},
		{"SERVICE", "GRANT CONTROL ON SERVICE::Tar|", []string{"TargetService"}, nil},
	} {
		t.Run(tc.class, func(t *testing.T) {
			p := newSecurityCompletionPanel(t)
			definitions := p.securityPermissionDefinitions()
			f := gosmotest.New(t)
			p.conn.Server = f.Server()
			empty := gosmotest.Sets(gosmotest.Set{Columns: []string{"unused"}})
			f.On("x.kind", gosmotest.Rows([]any{"TABLE", "dbo", "TargetTable"},
				[]any{"TYPE", "dbo", "TargetType"}, []any{"CERTIFICATE", "", "TargetCert"}, []any{"SCHEMA", "", "TargetSchema"}))
			f.On("FROM   sys.service_queues", gosmotest.Rows([]any{int64(10), "TargetQueue", "dbo", "dbo",
				false, true, true, false, true, false, "", "", "", int64(1), "dbo", "PRIMARY", now, now}))
			f.On("FROM   sys.database_principals dp", gosmotest.Rows(principal("TargetUser", "SQL_USER"),
				principal("TargetRole", "DATABASE_ROLE"), principal("TargetApp", "APPLICATION_ROLE")))
			f.On("LEFT JOIN sys.sql_logins", gosmotest.Rows([]any{"TargetLogin", []byte{1}, "SQL_LOGIN", false, "testdb", now, now, "English", false, false}))
			f.On("WHERE r.type = 'R'", gosmotest.Rows([]any{"TargetServerRole", int64(10), false, "dbo", "[]"}))
			f.On("FROM   sys.endpoints", gosmotest.Rows([]any{int64(10), "TargetEndpoint", "dbo", "TCP", "DATABASE_MIRRORING", "STARTED", false, int64(5022)}))
			f.On("FROM sys.availability_groups", gosmotest.Rows([]any{"guid", "TargetGroup", "", "", "WSFC", "PRIMARY",
				int64(1), int64(1), int64(1), false, false, false, false, int64(0), false, "", "", ""}))
			f.On("FROM   sys.assemblies", gosmotest.Rows([]any{"TargetAssembly", int64(10), "dbo", "", "SAFE_ACCESS", true, true, now, now}))
			f.On("FROM   sys.asymmetric_keys", gosmotest.Rows([]any{"TargetAsymmetric", int64(10), int64(1), "RSA_2048", int64(2048), "", []byte{1}, "dbo", "", ""}))
			f.On("FROM   sys.symmetric_keys", gosmotest.Rows([]any{"TargetSymmetric", int64(10), int64(1), "dbo", "AES_256", int64(256), "guid", now, now, "", nil, nil, nil}))
			f.On("FROM   sys.database_scoped_credentials", gosmotest.Rows([]any{int64(10), "TargetCredential", "identity", now, now}))
			f.On("FROM   sys.xml_schema_collections", gosmotest.Rows([]any{"TargetXML", "dbo", int64(10), now, now}))
			f.On("FROM   sys.fulltext_catalogs", gosmotest.Rows([]any{int64(10), "TargetFulltext", false, true, "dbo", false,
				int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0)}))
			f.On("FROM   sys.fulltext_stoplists", gosmotest.Rows([]any{int64(10), "TargetStoplist", "dbo", now, now}))
			f.On("FROM   sys.registered_search_property_lists", gosmotest.Rows([]any{int64(10), "TargetProperties", "dbo", now, now}))
			f.On("FROM   sys.service_contracts c", gosmotest.Rows([]any{int64(10), "TargetContract", "dbo"}))
			f.On("FROM   sys.service_contract_message_usages", empty)
			f.On("FROM   sys.service_message_types mt", gosmotest.Rows([]any{int64(10), "TargetMessage", "dbo", "NONE", "", ""}))
			f.On("FROM   sys.remote_service_bindings", gosmotest.Rows([]any{int64(10), "TargetBinding", "dbo", "", "", false, ""}))
			f.On("FROM   sys.routes", gosmotest.Rows([]any{int64(10), "TargetRoute", "dbo", "", "", "", "", nil, nil}))
			f.On("FROM   sys.services s", gosmotest.Rows([]any{int64(10), "TargetService", "dbo", "dbo", "TargetQueue"}))
			f.On("FROM   sys.service_contract_usages", empty)
			p.app.completion.security[securityInventoryKey(p.conn, "", "permissions")] = &securityInventory{permissions: definitions}
			database := p.database
			if slices.Contains([]string{"LOGIN", "SERVER ROLE", "server-principals", "server-logins", "ENDPOINT", "AVAILABILITY GROUP"}, tc.class) {
				database = ""
			}
			key := securityInventoryKey(p.conn, database, tc.class)
			delete(p.app.completion.security, key)
			lines, row, col := linesAndCursor(t, tc.sql)
			p.sqlCompletionCandidates(completionReq(lines, row, col))
			inv := p.app.completion.security[key]
			if inv == nil {
				t.Fatal("the documented securable class did not request its metadata")
			}
			drainUntil(t, p.app, func() bool { return !inv.loading }, "typed security names")
			if inv.err != nil {
				t.Fatal(inv.err)
			}
			items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, want := range tc.want {
				if !containsLabel(items, want) {
					t.Errorf("missing %q in %v", want, labels(items))
				}
			}
			for _, not := range tc.not {
				if containsLabel(items, not) {
					t.Errorf("wrong securable/principal family %q in %v", not, labels(items))
				}
			}
		})
	}
}

func TestSecurityCompletionEditionGates(t *testing.T) {
	for _, edition := range []gosmo.EngineEdition{gosmo.EngineAzureSQLDatabase, gosmo.EngineFabricSQLDatabase} {
		t.Run(edition.String(), func(t *testing.T) {
			p := newSecurityCompletionPanel(t)
			definitions := p.securityPermissionDefinitions()
			f := gosmotest.New(t)
			f.Info.EngineEdition = edition
			p.conn.Server = f.Server()
			p.app.completion.security[securityInventoryKey(p.conn, "", "permissions")] = &securityInventory{permissions: definitions}
			for _, marked := range []string{"GRANT |", "GRANT CONTROL ON |", "GRANT CONTROL SERVER TO |"} {
				lines, row, col := linesAndCursor(t, marked)
				items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
				for _, forbidden := range []string{"CONTROL SERVER", "ALTER ANY LOGIN", "LOGIN::", "SERVER ROLE::", "ENDPOINT::",
					"AVAILABILITY GROUP::", "CONTRACT::", "MESSAGE TYPE::", "REMOTE SERVICE BINDING::", "ROUTE::", "SERVICE::", "login"} {
					if containsLabel(items, forbidden) {
						t.Errorf("%q offers unsupported %q", marked, forbidden)
					}
				}
				if marked == "GRANT |" && !containsLabel(items, "CREATE TABLE") {
					t.Fatal("database permissions disappeared on the database-scoped edition")
				}
			}
			for _, query := range f.Queries() {
				if strings.Contains(query.Text, "sp.type IN") {
					t.Fatal("unsupported server principal completion queried logins")
				}
			}
		})
	}
}

func TestSecurityCompletionWarehouseReads(t *testing.T) {
	p := newSecurityCompletionPanel(t)
	f := gosmotest.New(t)
	f.Info.EngineEdition = gosmo.EngineAzureSynapseSrvls
	f.Info.Name = "test.datawarehouse.fabric.microsoft.com"
	p.conn.Server = f.Server()
	if !p.conn.IsFabricDW() {
		t.Fatal("fixture is not a Fabric Warehouse")
	}
	f.On("x.kind", gosmotest.Rows([]any{"TABLE", "dbo", "TargetTable"}))
	f.OnFunc(func(gosmotest.Query) bool { return true }, gosmotest.Fail(errors.New("unexpected Warehouse metadata read")))
	delete(p.app.completion.security, securityInventoryKey(p.conn, p.database, "OBJECT"))
	lines, row, col := linesAndCursor(t, "GRANT SELECT ON dbo.Tar|")
	p.sqlCompletionCandidates(completionReq(lines, row, col))
	inv := p.app.completion.security[securityInventoryKey(p.conn, p.database, "OBJECT")]
	drainUntil(t, p.app, func() bool { return !inv.loading }, "Warehouse securable names")
	items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
	if inv.err != nil || !containsLabel(items, "TargetTable") {
		t.Fatalf("Warehouse object metadata = %v, %v", inv.err, labels(items))
	}
	for _, query := range f.Queries() {
		if strings.Contains(query.Text, "fn_builtin_permissions") || strings.Contains(query.Text, "FROM   sys.service_queues") {
			t.Fatalf("Warehouse queried unsupported catalog: %s", query.Text)
		}
	}
}

func TestSecurityCompletionNameLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newSecurityCompletionPanel(t)
		f := gosmotest.New(t)
		p.conn.Server = f.Server()
		p.app.completion.security = nil
		var calls atomic.Int32
		release := make(chan struct{})
		staleStarted, purgeStarted := make(chan struct{}), make(chan struct{})
		role := func(name string) gosmotest.Responder {
			return gosmotest.Rows([]any{name, int64(10), false, "dbo", "[]"})
		}
		f.On("WHERE r.type = 'R'", func(q gosmotest.Query) gosmotest.Result {
			switch calls.Add(1) {
			case 1:
				return role("OldRole")(q)
			case 2:
				return gosmotest.Fail(errors.New("refresh denied"))(q)
			case 3:
				return role("NewRole")(q)
			case 4:
				close(staleStarted)
				return gosmotest.Hold(release, role("StaleRole"))(q)
			case 5:
				return role("LatestRole")(q)
			case 6:
				close(purgeStarted)
				return gosmotest.Hold(release, role("PurgedRole"))(q)
			}
			return gosmotest.Fail(errors.New("unexpected role read"))(q)
		})
		inv := p.app.ensureSecurityInventory(p.conn, "", "SERVER ROLE")
		waitAndDrain(t, p.app)
		if inv.err != nil || len(inv.names) != 1 || inv.names[0].name != "OldRole" {
			t.Fatal("initial name inventory did not load")
		}
		p.app.refreshSecurityCompletion(p.conn)
		waitAndDrain(t, p.app)
		if inv.err == nil || len(inv.names) != 1 || inv.names[0].name != "OldRole" {
			t.Fatal("a failed refresh discarded the last known names")
		}
		p.app.refreshSecurityCompletion(p.conn)
		waitAndDrain(t, p.app)
		if inv.err != nil || inv.names[0].name != "NewRole" {
			t.Fatal("refresh did not recover after a name-read failure")
		}
		p.app.refreshSecurityCompletion(p.conn)
		select {
		case <-staleStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("stale load did not start")
		}
		p.app.refreshSecurityCompletion(p.conn)
		synctest.Wait()
		p.app.drainPending()
		if inv.err != nil || inv.loading || inv.names[0].name != "LatestRole" {
			t.Fatal("a stale refresh result replaced the latest name inventory")
		}
		p.app.refreshSecurityCompletion(p.conn)
		select {
		case <-purgeStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("purged load did not start")
		}
		p.app.purgeSecurityCompletion(sysCompletionInventoryKey(p.conn.Opts))
		synctest.Wait()
		p.app.drainPending()
		if len(p.app.completion.security) != 0 || inv.names[0].name != "LatestRole" || inv.err != nil {
			t.Fatal("a disconnected name load applied after purge")
		}
	})
}

func TestSecurityCompletionPartialServerPrincipals(t *testing.T) {
	p := newSecurityCompletionPanel(t)
	definitions := p.securityPermissionDefinitions()
	f := gosmotest.New(t)
	p.conn.Server = f.Server()
	now := time.Unix(0, 0)
	f.On("LEFT JOIN sys.sql_logins", gosmotest.Rows([]any{"TargetLogin", []byte{1}, "SQL_LOGIN", false, "testdb", now, now, "English", false, false}))
	f.On("WHERE r.type = 'R'", gosmotest.Fail(errors.New("server roles denied")))
	delete(p.app.completion.security, securityInventoryKey(p.conn, "", "server-principals"))
	p.app.completion.security[securityInventoryKey(p.conn, "", "permissions")] = &securityInventory{permissions: definitions}
	lines, row, col := linesAndCursor(t, "GRANT CONTROL SERVER TO Tar|")
	p.sqlCompletionCandidates(completionReq(lines, row, col))
	inv := p.app.completion.security[securityInventoryKey(p.conn, "", "server-principals")]
	drainUntil(t, p.app, func() bool { return !inv.loading }, "partial server principal metadata")
	items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
	if inv.err == nil || !containsLabel(items, "TargetLogin") {
		t.Fatalf("a refused role read discarded visible logins: %v, %v", inv.err, labels(items))
	}
	p.app.completion.security = map[string]*securityInventory{
		securityInventoryKey(p.conn, "", "server-principals"): inv,
		securityInventoryKey(p.conn, "", "permissions"):       {permissions: definitions},
	}
	for range 3 {
		p.app.refreshSecurityCompletion(p.conn)
		drainUntil(t, p.app, func() bool { return !inv.loading }, "refreshed partial principals")
		if inv.err == nil || len(inv.names) != 1 || inv.names[0].name != "TargetLogin" {
			t.Fatalf("partial refresh grew or lost the principal inventory: %v, %v", inv.names, inv.err)
		}
	}
}

func TestSecurityCompletionPermissionFallbackAndPreservation(t *testing.T) {
	p := newSecurityCompletionPanel(t)
	f := gosmotest.New(t)
	p.conn.Server = f.Server()
	var reads atomic.Int32
	f.On("sys.fn_builtin_permissions", func(q gosmotest.Query) gosmotest.Result {
		if reads.Add(1) == 1 {
			return gosmotest.Rows()(q)
		}
		return gosmotest.Fail(errors.New("refresh denied"))(q)
	})
	inv := p.app.ensureSecurityInventory(p.conn, "", "permissions")
	drainUntil(t, p.app, func() bool { return !inv.loading }, "empty vocabulary")
	if !slices.ContainsFunc(p.securityPermissionDefinitions(), func(def gosmo.BuiltinPermission) bool {
		return def.Class == "DATABASE" && def.Name == "CREATE TABLE"
	}) {
		t.Fatal("empty runtime vocabulary suppressed documented fallback")
	}
	for _, class := range []string{"ASSEMBLY", "ASYMMETRIC KEY", "CERTIFICATE", "SYMMETRIC KEY", "DATABASE SCOPED CREDENTIAL",
		"FULLTEXT CATALOG", "FULLTEXT STOPLIST", "SEARCH PROPERTY LIST", "CONTRACT", "MESSAGE TYPE"} {
		if !p.securityClassAllows(class, []string{"REFERENCES"}) {
			t.Errorf("documented REFERENCES permission missing from %s fallback", class)
		}
	}
	inv.permissions = []gosmo.BuiltinPermission{{Class: "DATABASE", Name: "VIEW NEW ENGINE STATE"}}
	p.app.loadSecurityInventory(p.conn, securityInventoryKey(p.conn, "", "permissions"), inv)
	inv.loading = true
	drainUntil(t, p.app, func() bool { return !inv.loading }, "refused vocabulary refresh")
	if inv.err == nil || !slices.Equal(p.securityPermissionDefinitions(), inv.permissions) ||
		len(inv.permissions) != 1 || inv.permissions[0].Name != "VIEW NEW ENGINE STATE" {
		t.Fatal("failed refresh discarded the previously loaded engine vocabulary")
	}
}

func TestSecurityCompletionExactOptions(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want []string
	}{
		{"GRANT SELECT |", []string{"ALL USER SECURABLES", "ON", "TO"}},
		{"REVOKE SELECT |", []string{"ALL USER SECURABLES", "FROM", "ON", "TO"}},
		{"GRANT SELECT ON dbo.Customers TO reader |", []string{"AS", "WITH GRANT OPTION"}},
		{"DENY SELECT ON dbo.Customers TO reader |", []string{"AS", "CASCADE"}},
		{"REVOKE SELECT ON dbo.Customers FROM reader CASCADE |", []string{"AS"}},
		{"GRANT SELECT ON dbo.Customers TO reader WITH GRANT OPTION, |", nil},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			p := newSecurityCompletionPanel(t)
			lines, row, col := linesAndCursor(t, tc.sql)
			items, _ := p.sqlCompletionCandidates(completionReq(lines, row, col))
			got := labels(items)
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("options = %v, want exactly %v", got, tc.want)
			}
		})
	}
}
