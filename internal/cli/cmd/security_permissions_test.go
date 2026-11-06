package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// permFakes is the dry-run fidelity harness's: real gosmo over a
// secServer, which applies the writes itself.
func permFakes(t *testing.T) Catalog {
	return ServerCatalog(&db.ServerConn{Server: newSecServer(t).Server()})
}

// runPerm runs `security <argv>` over s, an ad-hoc connection; dials
// counts the connects.
func runPerm(t *testing.T, s *secServer, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
			dials++
			return s.conn(ctx)
		},
		Catalog: ServerCatalog,
	}
	words, err := splitArgv(argv)
	if err != nil {
		t.Fatal(err)
	}
	inv, perr := args.Parse(&spec.Tree{Commands: SecurityCommands(d)},
		append(append([]string{"security"}, words...), "--server", "srv", "--auth", "windows"), args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

// splitArgv is strings.Fields with '…' quoting, for permission names and
// object names holding spaces.
func splitArgv(s string) ([]string, error) {
	var out []string
	for i, part := range strings.Split(s, "'") {
		if i%2 == 1 {
			out = append(out, part)
			continue
		}
		out = append(out, strings.Fields(part)...)
	}
	if strings.Count(s, "'")%2 != 0 {
		return nil, errors.New("unbalanced quote")
	}
	return out, nil
}

func TestParseOn(t *testing.T) {
	for _, tc := range []struct {
		in, kind, name string
	}{
		{"SERVER", onServer, ""},
		{"server", onServer, ""},
		{"DATABASE", onDatabase, ""},
		{"Database::app", onDatabase, "app"},
		{"SCHEMA::Sales", onSchema, "Sales"},
		{"schema :: [Sa les]", onSchema, "[Sa les]"},
		{"OBJECT::dbo.t", onObject, "dbo.t"},
		{"dbo.t", onObject, "dbo.t"},
		{"t", onObject, "t"},
		{"[dbo].[a::b]", onObject, "[dbo].[a::b]"}, // "::" inside brackets is a name
		{"TYPE::dbo.Phone", onType, "dbo.Phone"},
		{"CERTIFICATE::SignCert", onCertificate, "SignCert"},
		{"OBJECT::Server", onObject, "Server"},
	} {
		got, err := parseOn(tc.in)
		if err != nil || got.kind != tc.kind || got.name != tc.name {
			t.Errorf("parseOn(%q) = %+v, %v; want %s %q", tc.in, got, err, tc.kind, tc.name)
		}
	}
	for _, in := range []string{"", "  ", "SCHEMA::", "SERVER::x", "LOGIN::sa", "ASSEMBLY::a", "endpoint::e"} {
		if _, err := parseOn(in); err == nil {
			t.Errorf("parseOn(%q): no error", in)
		} else if e := wantCode(t, err, output.CodeUsageError); in == "LOGIN::sa" && !strings.Contains(e.Hint, "query run") {
			t.Errorf("LOGIN hint %q", e.Hint)
		}
	}
}

func TestParsePermissions(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want []string
	}{
		{[]string{"select"}, []string{"SELECT"}},
		{[]string{"VIEW", "DEFINITION"}, []string{"VIEW DEFINITION"}},
		{[]string{"view  definition"}, []string{"VIEW DEFINITION"}},
		{[]string{"SELECT,INSERT"}, []string{"SELECT", "INSERT"}},
		{[]string{"SELECT,", "insert", ",select"}, []string{"SELECT", "INSERT"}},
	} {
		got, err := parsePermissions(tc.in)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%q: %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, in := range [][]string{{"SELECT,,INSERT"}, {","}, {"SELECT,"}} {
		if _, err := parsePermissions(in); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

// TestSecurityGrantPerClass: every --on form resolves to its class and
// catalog spelling, and the statement gosmo renders for it.
func TestSecurityGrantPerClass(t *testing.T) {
	for _, tc := range []struct {
		argv, class, schema, name, tsql string
	}{
		{"SELECT --on DATABASE", "DATABASE", "", "", "GRANT SELECT TO [etl_user]"},
		{"CONNECT --on DATABASE::APP", "DATABASE", "", "", "GRANT CONNECT TO [etl_user]"},
		{"'VIEW SERVER STATE' --on SERVER --to etl", "SERVER", "", "", "USE master; GRANT VIEW SERVER STATE TO [etl]"},
		{"SELECT --on SCHEMA::sales", "SCHEMA", "", "Sales", "GRANT SELECT ON SCHEMA::[Sales] TO [etl_user]"},
		{"SELECT --on dbo.Customers", "TABLE", "dbo", "Customers", "GRANT SELECT ON [dbo].[Customers] TO [etl_user]"},
		{"SELECT --on OBJECT::Customers", "TABLE", "dbo", "Customers", "GRANT SELECT ON [dbo].[Customers] TO [etl_user]"},
		{"SELECT --on OBJECT::Server", "TABLE", "dbo", "Server", "GRANT SELECT ON [dbo].[Server] TO [etl_user]"},
		{"SELECT --on vOrders", "VIEW", "dbo", "vOrders", "GRANT SELECT ON [dbo].[vOrders] TO [etl_user]"},
		{"EXECUTE --on Sales.usp_Load", "PROCEDURE", "Sales", "usp_Load", "GRANT EXECUTE ON [Sales].[usp_Load] TO [etl_user]"},
		{"EXECUTE --on fn_Total", "SCALAR FUNCTION", "Sales", "fn_Total", "GRANT EXECUTE ON [Sales].[fn_Total] TO [etl_user]"},
		{"INSERT --on tvf_Rows", "INLINE FUNCTION", "Sales", "tvf_Rows", "GRANT INSERT ON [Sales].[tvf_Rows] TO [etl_user]"},
		{"SELECT --on mtvf_Rows", "TABLE FUNCTION", "Sales", "mtvf_Rows", "GRANT SELECT ON [Sales].[mtvf_Rows] TO [etl_user]"},
		{"UPDATE --on seq_Id", "SEQUENCE", "dbo", "seq_Id", "GRANT UPDATE ON [dbo].[seq_Id] TO [etl_user]"},
		{"SELECT --on syn_Cust", "SYNONYM", "dbo", "syn_Cust", "GRANT SELECT ON [dbo].[syn_Cust] TO [etl_user]"},
		{"EXECUTE --on TYPE::Phone", "TYPE", "dbo", "Phone", "GRANT EXECUTE ON TYPE::[dbo].[Phone] TO [etl_user]"},
		{"REFERENCES --on TYPE::sales.orderlist", "TYPE", "Sales", "OrderList", "GRANT REFERENCES ON TYPE::[Sales].[OrderList] TO [etl_user]"},
		{"CONTROL --on CERTIFICATE::SignCert", "CERTIFICATE", "", "SignCert", "GRANT CONTROL ON CERTIFICATE::[SignCert] TO [etl_user]"},
		{"SELECT --on Customers --columns email,[Phone]", "TABLE", "dbo", "Customers",
			"GRANT SELECT ([Email], [Phone]) ON [dbo].[Customers] TO [etl_user]"},
		{"SELECT --on vOrders --columns Total", "VIEW", "dbo", "vOrders", "GRANT SELECT ([Total]) ON [dbo].[vOrders] TO [etl_user]"},
		{"SELECT --on SCHEMA::Sales --to readers --with-grant-option", "SCHEMA", "", "Sales",
			"GRANT SELECT ON SCHEMA::[Sales] TO [readers] WITH GRANT OPTION"},
		{"EXECUTE --on SCHEMA::Sales --to billing_app", "SCHEMA", "", "Sales", "GRANT EXECUTE ON SCHEMA::[Sales] TO [billing_app]"},
	} {
		argv := "grant " + tc.argv + " --allow admin --dry-run"
		if !strings.Contains(tc.argv, "--to") {
			argv += " --to etl_user"
		}
		cat := newSecServer(t)
		res, _, err := runPerm(t, cat, argv)
		if err != nil {
			t.Errorf("%s: %v", argv, err)
			continue
		}
		got := fieldValue(res, "class") + "|" + fieldValue(res, "schema") + "|" + fieldValue(res, "name")
		if want := tc.class + "|" + tc.schema + "|" + tc.name; got != want {
			t.Errorf("%s: securable %s, want %s", argv, got, want)
		}
		if got := strings.TrimSpace(fieldValue(res, "tsql")); !strings.Contains(got, tc.tsql) {
			t.Errorf("%s: tsql %q, want %q", argv, got, tc.tsql)
		}
		if len(cat.writes) != 0 {
			t.Errorf("%s: dry run wrote %q", argv, cat.writes)
		}
	}
}

func TestSecurityGrantRefusals(t *testing.T) {
	for _, tc := range []struct {
		argv      string
		code      output.Code
		msg, hint string
		dialed    bool
	}{
		// Before connecting.
		{"grant SELECT --to etl_user", output.CodeUsageError, "--on is required", "", false},
		{"grant SELECT --on dbo.Customers", output.CodeUsageError, "--to is required", "", false},
		{"grant 'VIEW DATABASE STATE' --on SERVER --to etl", output.CodeUsageError, "cannot be granted on a server",
			"VIEW SERVER STATE", false},
		{"grant EXECUTE --on SCHEMA::Sales --to etl_user --columns a", output.CodeUsageError, "a schema has no columns", "", false},
		{"grant FROB --on DATABASE --to etl_user", output.CodeUsageError, "FROB cannot be granted on a database", "", false},
		// After resolving.
		{"grant EXECUTE --on dbo.Customers --to etl_user", output.CodeUsageError, "cannot be granted on a table", "SELECT", true},
		{"grant SELECT --on Sales.usp_Load --to etl_user", output.CodeUsageError, "cannot be granted on a procedure", "EXECUTE", true},
		{"grant DELETE --on Customers --columns Email --to etl_user", output.CodeUsageError,
			"DELETE cannot be granted on a column of a table", "SELECT", true},
		{"grant EXECUTE --on Sales.usp_Load --columns a --to etl_user", output.CodeUsageError, "has no column permissions", "", true},
		{"grant SELECT --on Customers --columns Nope --to etl_user", output.CodeNotFound, "no column [Nope]", "Email", true},
		{"grant SELECT --on Orders --to etl_user", output.CodeUsageError, "is in 2 schemas", "schema.name", true},
		{"grant SELECT --on PK_Orders --to etl_user", output.CodeUsageError, "PRIMARY_KEY_CONSTRAINT", "", true},
		{"grant SELECT --on dbo.Nope --to etl_user", output.CodeNotFound, "no object [dbo].[Nope]", "", true},
		{"grant SELECT --on SCHEMA::Nope --to etl_user", output.CodeNotFound, "no schema [Nope]", "", true},
		{"grant SELECT --on SCHEMA::dbo.x --to etl_user", output.CodeUsageError, "a schema name has one part", "", true},
		{"grant CONTROL --on CERTIFICATE::Nope --to etl_user", output.CodeNotFound, "no certificate [Nope]", "", true},
		{"grant EXECUTE --on TYPE::Money2 --to etl_user", output.CodeUsageError, "type [Money2] is in 2 schemas", "", true},
		{"grant EXECUTE --on TYPE::Nope --to etl_user", output.CodeNotFound, "no user-defined type", "", true},
		{"grant SELECT --on DATABASE::other --to etl_user", output.CodeUsageError, "the connection is in database [app]", "-d [other]", true},
		{"grant SELECT --on other.dbo.t --to etl_user", output.CodeUsageError, "names database [other]", "", true},
		// The principal: a login named in database scope names its user.
		{"grant SELECT --on dbo.Customers --to etl", output.CodeNotFound, "no user or role [etl]", "is user [etl_user]", true},
		{"grant 'VIEW SERVER STATE' --on SERVER --to etl_user", output.CodeNotFound, "no login or server role",
			"is a user of database [app]: name a database securable in --on", true},
		{"grant SELECT --on DATABASE --to monitoring", output.CodeNotFound, "no user or role", "is a server role: use --on SERVER", true},
		// Modifiers the verb has no form for are not options of it at all;
		// GRANT OPTION FOR is revoke's.
	} {
		cat := newSecServer(t)
		_, dials, err := runPerm(t, cat, tc.argv+" --allow admin")
		e := wantCode(t, err, tc.code)
		if !strings.Contains(e.Message, tc.msg) || !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%s: %q / %q, want %q / %q", tc.argv, e.Message, e.Hint, tc.msg, tc.hint)
		}
		if (dials > 0) != tc.dialed {
			t.Errorf("%s: dials %d", tc.argv, dials)
		}
		if len(cat.writes) != 0 {
			t.Errorf("%s: wrote %q", tc.argv, cat.writes)
		}
	}
}

// TestSecurityGrantRealRun: several permissions are a statement each, in
// order, and the set read back is the principal's entries on the securable
// alone.
func TestSecurityGrantRealRun(t *testing.T) {
	cat := newSecServer(t)
	res, _, err := runPerm(t, cat, "grant select,insert --on dbo.Orders --to etl_user --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{
		"GRANT SELECT ON [dbo].[Orders] TO [etl_user]", "GRANT INSERT ON [dbo].[Orders] TO [etl_user]",
	}; !slices.Equal(cat.writes, want) {
		t.Errorf("writes %q, want %q", cat.writes, want)
	}
	for key, want := range map[string]string{"verb": "GRANT", "permission": "SELECT, INSERT", "principal": "etl_user",
		"principalType": "SQL_USER", "scope": "database", "dryRun": "false"} {
		if got := fieldValue(res, key); got != want {
			t.Errorf("%s %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"modifier", "columns", "schema"} {
		if f := fieldOf(t, res, key); f.Null != (key != "schema") {
			t.Errorf("%s %+v", key, f)
		}
	}
	if hasField(res, "tsql") {
		t.Error("real run printed tsql")
	}
	// etl_user's DENY DELETE on dbo.Orders was there before; its grants on
	// the database and Sales are not on this securable. The server orders
	// them by permission.
	if got, want := rowsOf(res.Sets[0]), []string{
		"TABLE|dbo|Orders|∅|DELETE|DENY", "TABLE|dbo|Orders|∅|INSERT|GRANT", "TABLE|dbo|Orders|∅|SELECT|GRANT",
	}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}

	// Revoke one; the server-scope form.
	res, _, err = runPerm(t, cat, "revoke SELECT --on dbo.Orders --from etl_user --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "permission"); !slices.Equal(got, []string{"DELETE", "INSERT"}) {
		t.Errorf("after revoke %q", got)
	}
	res, _, err = runPerm(t, cat, "deny 'alter any login' --on server --to monitoring --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	// monitoring's own grant was there before.
	if got := rowsOf(res.Sets[0]); !slices.Equal(got, []string{
		"SERVER|∅|∅|∅|ALTER ANY LOGIN|DENY", "SERVER|∅|∅|∅|VIEW ANY DEFINITION|GRANT"}) {
		t.Errorf("server rows %q", got)
	}
	if f := fieldValue(res, "scope"); f != "server" {
		t.Errorf("scope %q", f)
	}

	// Column entries are part of the securable's set.
	res, _, err = runPerm(t, cat, "grant SELECT --on Customers --columns Phone --to etl_user --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowsOf(res.Sets[0]), []string{
		"TABLE|dbo|Customers|Email|SELECT|DENY", "TABLE|dbo|Customers|Phone|SELECT|GRANT",
	}; !slices.Equal(got, want) {
		t.Errorf("column rows %q, want %q", got, want)
	}
	if f := fieldValue(res, "columns"); f != "Phone" {
		t.Errorf("columns %q", f)
	}
}

func TestSecurityPermissionModifiers(t *testing.T) {
	for _, tc := range []struct {
		argv, modifier, tsql string
	}{
		{"deny SELECT --on SCHEMA::Sales --to etl_user --cascade", "CASCADE", "DENY SELECT ON SCHEMA::[Sales] TO [etl_user] CASCADE"},
		{"revoke SELECT --on SCHEMA::Sales --from etl_user --cascade", "CASCADE", "REVOKE SELECT ON SCHEMA::[Sales] FROM [etl_user] CASCADE"},
		{"revoke SELECT --on SCHEMA::Sales --from etl_user --grant-option-only", "GRANT OPTION FOR",
			"REVOKE GRANT OPTION FOR SELECT ON SCHEMA::[Sales] FROM [etl_user] CASCADE"},
		{"grant SELECT --on SCHEMA::Sales --to etl_user --with-grant-option", "WITH GRANT OPTION",
			"GRANT SELECT ON SCHEMA::[Sales] TO [etl_user] WITH GRANT OPTION"},
	} {
		res, _, err := runPerm(t, newSecServer(t), tc.argv+" --allow admin --dry-run")
		if err != nil {
			t.Fatalf("%s: %v", tc.argv, err)
		}
		if got := fieldValue(res, "modifier"); got != tc.modifier {
			t.Errorf("%s: modifier %q", tc.argv, got)
		}
		if got := strings.TrimSpace(fieldValue(res, "tsql")); !strings.Contains(got, tc.tsql) {
			t.Errorf("%s: tsql %q, want %q", tc.argv, got, tc.tsql)
		}
		// The dry run's set is the state now.
		if got := rowsOf(res.Sets[0]); !slices.Equal(got, []string{"SCHEMA|∅|Sales|∅|SELECT|GRANT_WITH_GRANT_OPTION"}) {
			t.Errorf("%s: rows %q", tc.argv, got)
		}
	}
	// Each verb takes only its own modifiers.
	for _, argv := range []string{
		"grant SELECT --on DATABASE --to etl_user --cascade",
		"deny SELECT --on DATABASE --to etl_user --with-grant-option",
		"revoke SELECT --on DATABASE --to etl_user",
		"deny SELECT --on DATABASE --to etl_user --grant-option-only",
	} {
		words, _ := splitArgv(argv)
		_, perr := args.Parse(&spec.Tree{Commands: SecurityCommands(Deps{})}, append([]string{"security"}, words...), args.Options{})
		if perr == nil {
			t.Errorf("%s: parsed", argv)
		}
	}
}

func TestSecurityPermissionServerRefusals(t *testing.T) {
	// The driver's error: gosmo wraps it.
	sqlErr := func(n int32) error {
		return mssql.Error{Number: n, Class: 16, Message: fmt.Sprintf("msg %d", n)}
	}
	for _, tc := range []struct {
		n    int32
		code output.Code
		hint string
	}{
		{15151, output.CodePermissionDenied, "db_securityadmin"}, // missing or denied: just read, so denied
		{15247, output.CodePermissionDenied, "db_securityadmin"},
		{4613, output.CodePermissionDenied, "WITH GRANT OPTION"},
		{4611, output.CodeSQLError, "add --cascade"},
	} {
		cat := newSecServer(t)
		cat.failAt, cat.writeErr = 1, sqlErr(tc.n)
		res, _, err := runPerm(t, cat, "revoke SELECT --on SCHEMA::Sales --from etl_user --allow admin")
		e := wantCode(t, err, tc.code)
		if !strings.Contains(e.Hint, tc.hint) || e.SQL == nil || e.SQL.Number != tc.n {
			t.Errorf("%d: %+v hint %q", tc.n, e.SQL, e.Hint)
		}
		if len(res.Sets) != 1 {
			t.Errorf("%d: no set read back", tc.n)
		}
	}
	// A server-scope refusal names the server's rights.
	cat := newSecServer(t)
	cat.failAt, cat.writeErr = 1, sqlErr(15247)
	_, _, err := runPerm(t, cat, "grant 'VIEW SERVER STATE' --on SERVER --to etl --allow admin")
	if e := wantCode(t, err, output.CodePermissionDenied); !strings.Contains(e.Hint, "securityadmin") {
		t.Errorf("server hint %q", e.Hint)
	}
	// The second of two fails: the first stays, and a warning says so.
	cat = newSecServer(t)
	cat.failAt, cat.writeErr = 2, sqlErr(4611)
	res, _, err := runPerm(t, cat, "grant SELECT,INSERT --on dbo.Orders --to etl_user --allow admin")
	wantCode(t, err, output.CodeSQLError)
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "GRANT SELECT went through") {
		t.Errorf("warnings %q", res.Warnings)
	}
	if got := column(res.Sets[0], "permission"); !slices.Contains(got, "SELECT") {
		t.Errorf("read back %q", got)
	}
}

func TestSecurityRoleMember(t *testing.T) {
	cat := newSecServer(t)
	res, _, err := runPerm(t, cat, "role add-member readers ghost --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.writes, []string{"ALTER ROLE [readers] ADD MEMBER [ghost]"}) {
		t.Errorf("writes %q", cat.writes)
	}
	for key, want := range map[string]string{"role": "readers", "scope": "database", "member": "ghost",
		"memberType": "SQL_USER", "wasMember": "false", "isMember": "true", "dryRun": "false"} {
		if got := fieldValue(res, key); got != want {
			t.Errorf("%s %q, want %q", key, got, want)
		}
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}

	// Server scope, removing; a nested role as the member.
	cat = newSecServer(t)
	res, _, err = runPerm(t, cat, "role remove-member sysadmin sa --scope server --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.writes, []string{"ALTER SERVER ROLE [sysadmin] DROP MEMBER [sa]"}) || fieldValue(res, "isMember") != "false" ||
		fieldValue(res, "wasMember") != "true" {
		t.Errorf("writes %q, record %+v", cat.writes, res.Fields)
	}
	res, _, err = runPerm(t, newSecServer(t), "role add-member reporting readers --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(fieldValue(res, "tsql")); !strings.Contains(got, "ALTER ROLE [reporting] ADD MEMBER [readers]") {
		t.Errorf("tsql %q", got)
	}
	// Already a member: said, still sent.
	if fieldValue(res, "memberType") != "DATABASE_ROLE" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "already a member") {
		t.Errorf("type %q, warnings %q", fieldValue(res, "memberType"), res.Warnings)
	}
	// An application role as the member.
	res, _, err = runPerm(t, newSecServer(t), "role add-member reporting billing_app --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(fieldValue(res, "tsql")); !strings.Contains(got, "ALTER ROLE [reporting] ADD MEMBER [billing_app]") ||
		fieldValue(res, "memberType") != "APPLICATION_ROLE" {
		t.Errorf("tsql %q, type %q", got, fieldValue(res, "memberType"))
	}
	res, _, err = runPerm(t, newSecServer(t), "role remove-member monitoring sa --scope server --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(fieldValue(res, "tsql")); !strings.Contains(got, "ALTER SERVER ROLE [monitoring] DROP MEMBER [sa]") {
		t.Errorf("tsql %q", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not a direct member") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestSecurityRoleMemberRefusals(t *testing.T) {
	for _, tc := range []struct {
		argv      string
		code      output.Code
		msg, hint string
	}{
		{"role add-member sysadmin etl_user", output.CodeNotFound, "no role [sysadmin]", "add --scope server"},
		{"role add-member readers etl --scope server", output.CodeNotFound, "no server role [readers]", "drop --scope server"},
		{"role add-member readers etl", output.CodeNotFound, "no user or role [etl]", "name etl_user, or add --scope server."},
		{"role add-member monitoring etl_user --scope server", output.CodeNotFound, "no login or server role", "drop --scope server"},
	} {
		cat := newSecServer(t)
		_, _, err := runPerm(t, cat, tc.argv+" --allow admin")
		e := wantCode(t, err, tc.code)
		if !strings.Contains(e.Message, tc.msg) || !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%s: %q / %q, want %q / %q", tc.argv, e.Message, e.Hint, tc.msg, tc.hint)
		}
		if len(cat.writes) != 0 {
			t.Errorf("%s: wrote %q", tc.argv, cat.writes)
		}
	}
	// Without --allow admin: blocked before connecting.
	_, dials, err := runPerm(t, newSecServer(t), "role add-member readers ghost")
	wantCode(t, err, output.CodeBlocked)
	if dials != 0 {
		t.Errorf("dials %d", dials)
	}
	// Refused by the server: exit 9 with the role's rights.
	s := newSecServer(t)
	s.failAt, s.writeErr = 1, mssql.Error{Number: 15151, Class: 16, Message: "Cannot alter the role"}
	_, _, err = runPerm(t, s, "role add-member readers ghost --allow admin")
	if e := wantCode(t, err, output.CodePermissionDenied); !strings.Contains(e.Hint, "ALTER ANY ROLE") {
		t.Errorf("hint %q", e.Hint)
	}
}

// TestSecurityPermissionIgnored: a statement the server answers with an
// informational message and records nothing for — a grant to oneself, sa,
// dbo or the owner — is a warning naming what did not land.
func TestSecurityPermissionIgnored(t *testing.T) {
	for _, tc := range []struct {
		argv   string
		missed string
	}{
		{"grant SELECT,DELETE --on dbo.Orders --to etl_user", "SELECT, DELETE"}, // DELETE stays DENY
		{"deny DELETE --on dbo.Orders --to etl_user", ""},                       // already denied: nothing to tell
		{"revoke DELETE --on dbo.Orders --from etl_user", "DELETE"},
		{"grant SELECT --on SCHEMA::Sales --to etl_user", ""}, // held WITH GRANT OPTION: still granted
		{"grant SELECT --on SCHEMA::Sales --to etl_user --with-grant-option", ""},
		{"revoke SELECT --on SCHEMA::Sales --from etl_user --grant-option-only", "SELECT"},
		{"grant SELECT --on Customers --columns Email,Phone --to etl_user", "SELECT"},
		{"revoke SELECT --on Customers --columns Email --from etl_user", "SELECT"},
		{"grant 'VIEW SERVER STATE' --on SERVER --to etl", ""},
	} {
		s := newSecServer(t)
		s.ignore = true
		res, _, err := runPerm(t, s, tc.argv+" --allow admin")
		if err != nil {
			t.Fatalf("%s: %v", tc.argv, err)
		}
		got := ""
		if len(res.Warnings) == 1 && strings.HasPrefix(res.Warnings[0], "The server recorded no change for ") {
			got = strings.TrimPrefix(res.Warnings[0], "The server recorded no change for ")
			got = got[:strings.Index(got, ":")]
		} else if len(res.Warnings) > 0 {
			got = "?" + strings.Join(res.Warnings, "|")
		}
		if got != tc.missed {
			t.Errorf("%s: missed %q, want %q", tc.argv, got, tc.missed)
		}
	}
	// Landed: no warning.
	res, _, err := runPerm(t, newSecServer(t), "revoke SELECT --on SCHEMA::Sales --from etl_user --grant-option-only --allow admin")
	if err != nil || len(res.Warnings) != 0 {
		t.Errorf("landed: %v %q", err, res.Warnings)
	}
}
