package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// permCatalog is secCatalog plus the securables the security writes resolve
// in database "app": objects, schemas, types, a certificate and the
// columns of dbo.Customers and the view dbo.vOrders.
type permCatalog struct {
	*secCatalog
	objs    objectCatalog
	schemas []string
	types   []ObjectName // user-defined data types; tt is a table type
	tt      []ObjectName
	certs   []string
}

func newPermCatalog() *permCatalog {
	return &permCatalog{
		secCatalog: newSecCatalog(),
		objs: objectCatalog{objects: []*gosmo.ObjectInfo{
			{ObjectID: 1, Schema: "dbo", Name: "Customers", Type: "U", TypeDesc: "USER_TABLE"},
			{ObjectID: 2, Schema: "dbo", Name: "Orders", Type: "U", TypeDesc: "USER_TABLE"},
			{ObjectID: 3, Schema: "dbo", Name: "vOrders", Type: "V", TypeDesc: "VIEW"},
			{ObjectID: 4, Schema: "Sales", Name: "usp_Load", Type: "P", TypeDesc: "SQL_STORED_PROCEDURE"},
			{ObjectID: 5, Schema: "Sales", Name: "fn_Total", Type: "FN", TypeDesc: "SQL_SCALAR_FUNCTION"},
			{ObjectID: 6, Schema: "Sales", Name: "tvf_Rows", Type: "IF", TypeDesc: "SQL_INLINE_TABLE_VALUED_FUNCTION"},
			{ObjectID: 7, Schema: "Sales", Name: "mtvf_Rows", Type: "TF", TypeDesc: "SQL_TABLE_VALUED_FUNCTION"},
			{ObjectID: 8, Schema: "dbo", Name: "seq_Id", Type: "SO", TypeDesc: "SEQUENCE_OBJECT"},
			{ObjectID: 9, Schema: "dbo", Name: "syn_Cust", Type: "SN", TypeDesc: "SYNONYM"},
			{ObjectID: 10, Schema: "dbo", Name: "PK_Orders", Type: "PK", TypeDesc: "PRIMARY_KEY_CONSTRAINT"},
			{ObjectID: 11, Schema: "Sales", Name: "Orders", Type: "U", TypeDesc: "USER_TABLE"},
			{ObjectID: 12, Schema: "dbo", Name: "Server", Type: "U", TypeDesc: "USER_TABLE"},
		}},
		schemas: []string{"dbo", "Sales", "sys"},
		// Sales.phone differs from dbo.Phone by case only: an exact match wins.
		types: []ObjectName{{"dbo", "Phone"}, {"Sales", "phone"}, {"Sales", "Money2"}, {"dbo", "Money2"}},
		tt:    []ObjectName{{"Sales", "OrderList"}},
		certs: []string{"SignCert"},
	}
}

func (c *permCatalog) ObjectByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.ObjectInfo, error) {
	return c.objs.ObjectByName(ctx, d, schema, name)
}

func (c *permCatalog) ObjectsNamed(ctx context.Context, d *gosmo.Database, name string) ([]*gosmo.ObjectInfo, error) {
	return c.objs.ObjectsNamed(ctx, d, name)
}

func (c *permCatalog) Search(ctx context.Context, d *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error) {
	return c.objs.Search(ctx, d, pattern)
}

func (c *permCatalog) Schemas(context.Context, *gosmo.Database) ([]*gosmo.Schema, error) {
	var out []*gosmo.Schema
	for _, s := range c.schemas {
		out = append(out, &gosmo.Schema{Name: s})
	}
	return out, nil
}

func (c *permCatalog) UserDefinedDataTypes(context.Context, *gosmo.Database) ([]*gosmo.UserDefinedDataType, error) {
	var out []*gosmo.UserDefinedDataType
	for _, t := range c.types {
		out = append(out, &gosmo.UserDefinedDataType{Schema: t.Schema, Name: t.Name})
	}
	return out, nil
}

func (c *permCatalog) UserDefinedTableTypes(context.Context, *gosmo.Database) ([]*gosmo.UserDefinedTableType, error) {
	var out []*gosmo.UserDefinedTableType
	for _, t := range c.tt {
		out = append(out, &gosmo.UserDefinedTableType{Schema: t.Schema, Name: t.Name})
	}
	return out, nil
}

func (c *permCatalog) ClrTypes(context.Context, *gosmo.Database) ([]*gosmo.ClrType, error) {
	return nil, nil
}

func (c *permCatalog) CertificateByName(_ context.Context, _ *gosmo.Database, name string) (*gosmo.Certificate, error) {
	if slices.Contains(c.certs, name) {
		return &gosmo.Certificate{Name: name}, nil
	}
	return nil, fmt.Errorf("gosmo: certificate %q: %w", name, gosmo.ErrNotFound)
}

func (c *permCatalog) TableByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.Table, error) {
	return (&gosmo.Server{}).DatabaseRef("app").TableRef(schema, name), nil
}

func (c *permCatalog) TableColumns(_ context.Context, t *gosmo.Table) ([]*gosmo.Column, error) {
	if t.Name != "Customers" {
		return []*gosmo.Column{{Name: "id"}}, nil
	}
	return []*gosmo.Column{{Name: "id"}, {Name: "Email"}, {Name: "Phone"}}, nil
}

func (c *permCatalog) ViewByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.View, error) {
	return (&gosmo.Server{}).DatabaseRef("app").ViewRef(schema, name), nil
}

func (c *permCatalog) ViewColumns(context.Context, *gosmo.View) ([]*gosmo.Column, error) {
	return []*gosmo.Column{{Name: "OrderID"}, {Name: "Total"}}, nil
}

// permWriter applies grants and memberships to its catalog as the server
// would, recording each call; under WithScript it is the real serverWriter
// over a never-connected server. failAt fails that call (1-based) with err.
type permWriter struct {
	Writer
	cat    *permCatalog
	calls  []string
	failAt int
	err    error
	ignore bool // record the call and change nothing, as for a grant to oneself
}

func (w *permWriter) fail() error {
	if w.failAt > 0 && len(w.calls) == w.failAt {
		return w.err
	}
	return nil
}

func (w *permWriter) ApplyPermission(ctx context.Context, d *gosmo.Database, verb gosmo.PermissionVerb, sec gosmo.Securable,
	perm gosmo.PermissionName, principal string, opts gosmo.PermissionOptions) error {
	if gosmo.Scripting(ctx) {
		if d != nil {
			d = scriptDB(d)
		}
		return serverWriter{&gosmo.Server{}}.ApplyPermission(ctx, d, verb, sec, perm, principal, opts)
	}
	name := fmt.Sprint(perm)
	w.calls = append(w.calls, fmt.Sprintf("%s %s %s %s.%s %v %s", verb, name, sec.Class, sec.Schema, sec.Name, sec.Columns, principal))
	if err := w.fail(); err != nil || w.ignore {
		return err
	}
	state := gosmo.PermissionGrant
	switch {
	case verb == gosmo.VerbDeny:
		state = gosmo.PermissionDeny
	case opts.WithGrantOption:
		state = gosmo.PermissionGrantWithGrantOption
	}
	c := w.cat.secCatalog
	switch {
	case d == nil:
		c.serverPerms = slices.DeleteFunc(c.serverPerms, func(e *gosmo.ServerPermissionEntry) bool {
			return e.Principal == principal && string(e.Permission) == name
		})
		if verb != gosmo.VerbRevoke {
			c.serverPerms = append(c.serverPerms, &gosmo.ServerPermissionEntry{Principal: principal, Permission: gosmo.ServerPermission(name), State: state})
		}
	case sec.Columns != nil:
		for _, col := range sec.Columns {
			c.colPerms[principal] = slices.DeleteFunc(c.colPerms[principal], func(e *gosmo.ColumnPermissionEntry) bool {
				return e.Object == sec.Name && e.Column == col && string(e.Permission) == name
			})
			if verb != gosmo.VerbRevoke {
				c.colPerms[principal] = append(c.colPerms[principal], &gosmo.ColumnPermissionEntry{Principal: principal,
					Schema: sec.Schema, Object: sec.Name, ObjectType: sec.Class, Column: col, Permission: gosmo.ObjectPermission(name), State: state})
			}
		}
	default:
		c.dbPerms[principal] = slices.DeleteFunc(c.dbPerms[principal], func(e *gosmo.PrincipalSecurable) bool {
			return e.SecurableType == sec.Class && e.Schema == sec.Schema && e.Name == sec.Name && e.Permission == name
		})
		if verb != gosmo.VerbRevoke {
			c.dbPerms[principal] = append(c.dbPerms[principal], &gosmo.PrincipalSecurable{SecurableType: sec.Class,
				Schema: sec.Schema, Name: sec.Name, Permission: name, State: state})
		}
	}
	return nil
}

func (w *permWriter) AlterRoleMember(ctx context.Context, d *gosmo.Database, role, member string, add bool) error {
	if gosmo.Scripting(ctx) {
		if d != nil {
			d = scriptDB(d)
		}
		return serverWriter{&gosmo.Server{}}.AlterRoleMember(ctx, d, role, member, add)
	}
	w.calls = append(w.calls, fmt.Sprintf("member %s %s %v %v", role, member, add, d == nil))
	if err := w.fail(); err != nil {
		return err
	}
	key := "db:" + role
	if d == nil {
		key = "server:" + role
	}
	c := w.cat.secCatalog
	c.members[key] = slices.DeleteFunc(c.members[key], func(m *gosmo.RoleMember) bool { return m.Name == member })
	if add {
		c.members[key] = append(c.members[key], &gosmo.RoleMember{Name: member})
	}
	return nil
}

func permFakes(*testing.T) (Catalog, Writer) {
	cat := newPermCatalog()
	return cat, &permWriter{cat: cat}
}

// runPerm runs `security <argv>` over cat and w, an ad-hoc connection;
// dials counts the connects.
func runPerm(t *testing.T, cat *permCatalog, w *permWriter, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	w.cat = cat
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			dials++
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
		Writer:  func(*db.ServerConn) Writer { return w },
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
		cat, w := newPermCatalog(), &permWriter{}
		res, _, err := runPerm(t, cat, w, argv)
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
		if len(w.calls) != 0 {
			t.Errorf("%s: dry run wrote %q", argv, w.calls)
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
		cat, w := newPermCatalog(), &permWriter{}
		_, dials, err := runPerm(t, cat, w, tc.argv+" --allow admin")
		e := wantCode(t, err, tc.code)
		if !strings.Contains(e.Message, tc.msg) || !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%s: %q / %q, want %q / %q", tc.argv, e.Message, e.Hint, tc.msg, tc.hint)
		}
		if (dials > 0) != tc.dialed {
			t.Errorf("%s: dials %d", tc.argv, dials)
		}
		if len(w.calls) != 0 {
			t.Errorf("%s: wrote %q", tc.argv, w.calls)
		}
	}
}

// TestSecurityGrantRealRun: several permissions are a statement each, in
// order, and the set read back is the principal's entries on the securable
// alone.
func TestSecurityGrantRealRun(t *testing.T) {
	cat, w := newPermCatalog(), &permWriter{}
	res, _, err := runPerm(t, cat, w, "grant select,insert --on dbo.Orders --to etl_user --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{
		"GRANT SELECT TABLE dbo.Orders [] etl_user", "GRANT INSERT TABLE dbo.Orders [] etl_user",
	}; !slices.Equal(w.calls, want) {
		t.Errorf("calls %q, want %q", w.calls, want)
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
	// the database and Sales are not on this securable.
	if got, want := rowsOf(res.Sets[0]), []string{
		"TABLE|dbo|Orders|∅|DELETE|DENY", "TABLE|dbo|Orders|∅|SELECT|GRANT", "TABLE|dbo|Orders|∅|INSERT|GRANT",
	}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}

	// Revoke one; the server-scope form.
	res, _, err = runPerm(t, cat, w, "revoke SELECT --on dbo.Orders --from etl_user --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "permission"); !slices.Equal(got, []string{"DELETE", "INSERT"}) {
		t.Errorf("after revoke %q", got)
	}
	res, _, err = runPerm(t, cat, w, "deny 'alter any login' --on server --to monitoring --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	// monitoring's own grant was there before.
	if got := rowsOf(res.Sets[0]); !slices.Equal(got, []string{
		"SERVER|∅|∅|∅|VIEW ANY DEFINITION|GRANT", "SERVER|∅|∅|∅|ALTER ANY LOGIN|DENY"}) {
		t.Errorf("server rows %q", got)
	}
	if f := fieldValue(res, "scope"); f != "server" {
		t.Errorf("scope %q", f)
	}

	// Column entries are part of the securable's set.
	res, _, err = runPerm(t, cat, w, "grant SELECT --on Customers --columns Phone --to etl_user --allow admin")
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
		res, _, err := runPerm(t, newPermCatalog(), &permWriter{}, tc.argv+" --allow admin --dry-run")
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
	sqlErr := func(n int32) error {
		return fmt.Errorf("gosmo: grant: %w", mssql.Error{Number: n, Class: 16, Message: fmt.Sprintf("msg %d", n)})
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
		cat, w := newPermCatalog(), &permWriter{failAt: 1, err: sqlErr(tc.n)}
		res, _, err := runPerm(t, cat, w, "revoke SELECT --on SCHEMA::Sales --from etl_user --allow admin")
		e := wantCode(t, err, tc.code)
		if !strings.Contains(e.Hint, tc.hint) || e.SQL == nil || e.SQL.Number != tc.n {
			t.Errorf("%d: %+v hint %q", tc.n, e.SQL, e.Hint)
		}
		if len(res.Sets) != 1 {
			t.Errorf("%d: no set read back", tc.n)
		}
	}
	// A server-scope refusal names the server's rights.
	cat, w := newPermCatalog(), &permWriter{failAt: 1, err: sqlErr(15247)}
	_, _, err := runPerm(t, cat, w, "grant 'VIEW SERVER STATE' --on SERVER --to etl --allow admin")
	if e := wantCode(t, err, output.CodePermissionDenied); !strings.Contains(e.Hint, "securityadmin") {
		t.Errorf("server hint %q", e.Hint)
	}
	// The second of two fails: the first stays, and a warning says so.
	cat, w = newPermCatalog(), &permWriter{failAt: 2, err: sqlErr(4611)}
	res, _, err := runPerm(t, cat, w, "grant SELECT,INSERT --on dbo.Orders --to etl_user --allow admin")
	wantCode(t, err, output.CodeSQLError)
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "GRANT SELECT went through") {
		t.Errorf("warnings %q", res.Warnings)
	}
	if got := column(res.Sets[0], "permission"); !slices.Contains(got, "SELECT") {
		t.Errorf("read back %q", got)
	}
}

func TestSecurityRoleMember(t *testing.T) {
	cat, w := newPermCatalog(), &permWriter{}
	res, _, err := runPerm(t, cat, w, "role add-member readers ghost --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{"member readers ghost true false"}) {
		t.Errorf("calls %q", w.calls)
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
	cat, w = newPermCatalog(), &permWriter{}
	res, _, err = runPerm(t, cat, w, "role remove-member sysadmin sa --scope server --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{"member sysadmin sa false true"}) || fieldValue(res, "isMember") != "false" ||
		fieldValue(res, "wasMember") != "true" {
		t.Errorf("calls %q, record %+v", w.calls, res.Fields)
	}
	res, _, err = runPerm(t, newPermCatalog(), &permWriter{}, "role add-member reporting readers --allow admin --dry-run")
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
	res, _, err = runPerm(t, newPermCatalog(), &permWriter{}, "role add-member reporting billing_app --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(fieldValue(res, "tsql")); !strings.Contains(got, "ALTER ROLE [reporting] ADD MEMBER [billing_app]") ||
		fieldValue(res, "memberType") != "APPLICATION_ROLE" {
		t.Errorf("tsql %q, type %q", got, fieldValue(res, "memberType"))
	}
	res, _, err = runPerm(t, newPermCatalog(), &permWriter{}, "role remove-member monitoring sa --scope server --allow admin --dry-run")
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
		cat, w := newPermCatalog(), &permWriter{}
		_, _, err := runPerm(t, cat, w, tc.argv+" --allow admin")
		e := wantCode(t, err, tc.code)
		if !strings.Contains(e.Message, tc.msg) || !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%s: %q / %q, want %q / %q", tc.argv, e.Message, e.Hint, tc.msg, tc.hint)
		}
		if len(w.calls) != 0 {
			t.Errorf("%s: wrote %q", tc.argv, w.calls)
		}
	}
	// Without --allow admin: blocked before connecting.
	_, dials, err := runPerm(t, newPermCatalog(), &permWriter{}, "role add-member readers ghost")
	wantCode(t, err, output.CodeBlocked)
	if dials != 0 {
		t.Errorf("dials %d", dials)
	}
	// Refused by the server: exit 9 with the role's rights.
	w := &permWriter{failAt: 1, err: fmt.Errorf("gosmo: add: %w", mssql.Error{Number: 15151, Class: 16, Message: "Cannot alter the role"})}
	_, _, err = runPerm(t, newPermCatalog(), w, "role add-member readers ghost --allow admin")
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
		res, _, err := runPerm(t, newPermCatalog(), &permWriter{ignore: true}, tc.argv+" --allow admin")
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
	res, _, err := runPerm(t, newPermCatalog(), &permWriter{}, "revoke SELECT --on SCHEMA::Sales --from etl_user --grant-option-only --allow admin")
	if err != nil || len(res.Warnings) != 0 {
		t.Errorf("landed: %v %q", err, res.Warnings)
	}
}
