package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// secCatalog answers the security reads from its fields: the server's
// logins, roles and entries, and the one database "app"'s users, roles and
// entries. Each read records what it was asked; effErr fails --effective.
type secCatalog struct {
	Catalog
	engine      gosmo.EngineEdition
	logins      []*gosmo.Login
	serverRoles []*gosmo.ServerRole
	serverPerms []*gosmo.ServerPermissionEntry
	users       []*gosmo.User
	dbRoles     []*gosmo.DatabaseRole
	members     map[string][]*gosmo.RoleMember // role name -> members, either scope
	dbPerms     map[string][]*gosmo.PrincipalSecurable
	colPerms    map[string][]*gosmo.ColumnPermissionEntry
	effective   map[string][]*gosmo.EffectivePermission
	effErr      error

	effAsked []string // "db:<name>" or "server:<name>"
}

var secDB = &gosmo.Database{Name: "app", ID: 5}

func (c *secCatalog) ServerInfo() *gosmo.ServerInfo {
	return &gosmo.ServerInfo{EngineEdition: int(c.engine)}
}

func (c *secCatalog) CurrentDatabase(context.Context) (string, error) { return "app", nil }

func (c *secCatalog) DatabaseByName(_ context.Context, name string) (*gosmo.Database, error) {
	if strings.EqualFold(name, "app") {
		return secDB, nil
	}
	return nil, gosmo.ErrNotFound
}

func (c *secCatalog) Logins(context.Context) ([]*gosmo.Login, error) {
	return sortedBy(c.logins, func(l *gosmo.Login) string { return l.Name }), nil
}

// sortedBy is list as gosmo's ORDER BY name returns it under a
// case-insensitive collation; the handlers keep the server's order.
func sortedBy[T any](list []T, nameOf func(T) string) []T {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b T) int {
		return strings.Compare(strings.ToLower(nameOf(a)), strings.ToLower(nameOf(b)))
	})
	return out
}

// byName finds name in list, case-sensitively as a binary collation would.
func byName[T any](list []T, nameOf func(T) string, name, what string) (T, error) {
	for _, x := range list {
		if nameOf(x) == name {
			return x, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("gosmo: %s %q not found: %w", what, name, gosmo.ErrNotFound)
}

func (c *secCatalog) LoginByName(_ context.Context, name string) (*gosmo.Login, error) {
	return byName(c.logins, func(l *gosmo.Login) string { return l.Name }, name, "login")
}

func (c *secCatalog) ServerRoles(context.Context) ([]*gosmo.ServerRole, error) {
	return sortedBy(c.serverRoles, func(r *gosmo.ServerRole) string { return r.Name }), nil
}

func (c *secCatalog) ServerRoleByName(_ context.Context, name string) (*gosmo.ServerRole, error) {
	return byName(c.serverRoles, func(r *gosmo.ServerRole) string { return r.Name }, name, "server role")
}

func (c *secCatalog) ServerRoleMembers(_ context.Context, role string) ([]*gosmo.RoleMember, error) {
	return c.members["server:"+role], nil
}

func (c *secCatalog) Users(_ context.Context, d *gosmo.Database) ([]*gosmo.User, error) {
	return sortedBy(c.users, func(u *gosmo.User) string { return u.Name }), nil
}

func (c *secCatalog) UserByName(_ context.Context, _ *gosmo.Database, name string) (*gosmo.User, error) {
	return byName(c.users, func(u *gosmo.User) string { return u.Name }, name, "user")
}

func (c *secCatalog) DatabaseRoles(context.Context, *gosmo.Database) ([]*gosmo.DatabaseRole, error) {
	return sortedBy(c.dbRoles, func(r *gosmo.DatabaseRole) string { return r.Name }), nil
}

func (c *secCatalog) DatabaseRoleByName(_ context.Context, _ *gosmo.Database, name string) (*gosmo.DatabaseRole, error) {
	return byName(c.dbRoles, func(r *gosmo.DatabaseRole) string { return r.Name }, name, "role")
}

func (c *secCatalog) DatabaseRoleMembers(_ context.Context, _ *gosmo.Database, role string) ([]*gosmo.RoleMember, error) {
	return c.members["db:"+role], nil
}

func (c *secCatalog) PermissionsForPrincipal(_ context.Context, _ *gosmo.Database, p string) ([]*gosmo.PrincipalSecurable, error) {
	return c.dbPerms[p], nil
}

func (c *secCatalog) ColumnPermissionsForPrincipal(_ context.Context, _ *gosmo.Database, p string) ([]*gosmo.ColumnPermissionEntry, error) {
	return c.colPerms[p], nil
}

func (c *secCatalog) ServerPermissions(context.Context) ([]*gosmo.ServerPermissionEntry, error) {
	return c.serverPerms, nil
}

func (c *secCatalog) EffectivePermissions(_ context.Context, _ *gosmo.Database, user string) ([]*gosmo.EffectivePermission, error) {
	c.effAsked = append(c.effAsked, "db:"+user)
	return c.effective["db:"+user], c.effErr
}

func (c *secCatalog) EffectiveServerPermissions(_ context.Context, login string) ([]*gosmo.EffectivePermission, error) {
	c.effAsked = append(c.effAsked, "server:"+login)
	return c.effective["server:"+login], c.effErr
}

func newSecCatalog() *secCatalog {
	t := time.Date(2026, 3, 14, 9, 12, 44, 0, time.UTC)
	return &secCatalog{
		logins: []*gosmo.Login{
			{Name: "sa", LoginType: gosmo.PrincipalSQLLogin, IsDisabled: true, DefaultDatabase: "master",
				DefaultLanguage: "us_english", IsPolicyChecked: true, CreateDate: t, ModifyDate: t},
			{Name: "etl", LoginType: gosmo.PrincipalSQLLogin, DefaultDatabase: "app", DefaultLanguage: "us_english",
				IsPolicyChecked: true, IsExpirationChecked: true, CreateDate: t, ModifyDate: t},
			{Name: `CORP\dba`, LoginType: gosmo.PrincipalWindowsGroup, DefaultDatabase: "master", CreateDate: t, ModifyDate: t},
			{Name: "##MS_AgentSigningCertificate##", LoginType: gosmo.PrincipalCertificateMappedLogin, CreateDate: t, ModifyDate: t},
		},
		serverRoles: []*gosmo.ServerRole{
			{Name: "sysadmin", IsFixedRole: true, Owner: "sa", Members: []string{"sa", `CORP\dba`}},
			{Name: "monitoring", Owner: "sa", Members: []string{"etl"}},
			{Name: "public", IsFixedRole: true, Owner: "sa"},
		},
		serverPerms: []*gosmo.ServerPermissionEntry{
			{Principal: "etl", Permission: "VIEW SERVER STATE", State: gosmo.PermissionGrant},
			{Principal: "monitoring", Permission: "VIEW ANY DEFINITION", State: gosmo.PermissionGrant},
			{Principal: "etl", Permission: "ALTER ANY LOGIN", State: gosmo.PermissionDeny},
		},
		users: []*gosmo.User{
			{Name: "etl_user", UserType: gosmo.PrincipalSQLUser, LoginName: "etl", DefaultSchema: "dbo", AuthType: "INSTANCE", CreateDate: t, ModifyDate: t},
			{Name: "dbo", UserType: gosmo.PrincipalSQLUser, LoginName: "sa", LoginDisabled: true, DefaultSchema: "dbo", AuthType: "INSTANCE", CreateDate: t, ModifyDate: t},
			{Name: "ghost", UserType: gosmo.PrincipalSQLUser, DefaultSchema: "dbo", AuthType: "INSTANCE", CreateDate: t, ModifyDate: t},
			{Name: "nologin", UserType: gosmo.PrincipalSQLUser, DefaultSchema: "dbo", AuthType: "NONE", CreateDate: t, ModifyDate: t},
			{Name: `CORP\ann`, UserType: gosmo.PrincipalWindowsUser, AuthType: "INSTANCE", CreateDate: t, ModifyDate: t},
		},
		dbRoles: []*gosmo.DatabaseRole{
			{Name: "db_owner", IsFixedRole: true, Owner: "dbo", Members: []string{"dbo"}},
			{Name: "reporting", Owner: "dbo", Members: []string{"etl_user", "readers"}},
			{Name: "readers", Owner: "dbo", Members: []string{"etl_user"}},
		},
		members: map[string][]*gosmo.RoleMember{
			"db:reporting":    {{Name: "etl_user", Type: gosmo.PrincipalSQLUser}, {Name: "readers", Type: gosmo.PrincipalDatabaseRole}},
			"server:sysadmin": {{Name: `CORP\dba`, Type: gosmo.PrincipalWindowsGroup}, {Name: "sa", Type: gosmo.PrincipalSQLLogin}},
		},
		dbPerms: map[string][]*gosmo.PrincipalSecurable{
			"etl_user": {
				{SecurableType: gosmo.SecurableDatabase, Permission: "CONNECT", State: gosmo.PermissionGrant},
				{SecurableType: gosmo.SecurableSchema, Name: "Sales", Permission: "SELECT", State: gosmo.PermissionGrantWithGrantOption},
				{SecurableType: gosmo.SecurableTable, Schema: "dbo", Name: "Orders", Permission: "DELETE", State: gosmo.PermissionDeny},
			},
			"readers": {{SecurableType: gosmo.SecurableSchema, Name: "dbo", Permission: "SELECT", State: gosmo.PermissionGrant}},
		},
		colPerms: map[string][]*gosmo.ColumnPermissionEntry{
			"etl_user": {{Principal: "etl_user", Schema: "dbo", Object: "Customers", ObjectType: gosmo.SecurableTable,
				Column: "Email", Permission: "SELECT", State: gosmo.PermissionDeny}},
		},
		effective: map[string][]*gosmo.EffectivePermission{
			"db:etl_user": {{Entity: "database", Permission: "CONNECT"}, {Entity: "database", Permission: "SELECT"}},
			"server:etl":  {{Entity: "server", Permission: "CONNECT SQL"}, {Entity: "server", Permission: "VIEW SERVER STATE"}},
		},
	}
}

// runSecurity runs `security <argv…>` against cat.
func runSecurity(t *testing.T, cat Catalog, argv ...string) (*output.Result, error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
	}
	tree := &spec.Tree{Commands: SecurityCommands(d)}
	inv, perr := args.Parse(tree, append([]string{"security"}, append(argv, "--server", "srv", "--auth", "windows")...), args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	return inv.Command.Run(context.Background(), inv)
}

// rowsOf is set's rows as "a|b|c" strings, NULL as "∅".
func rowsOf(set output.Set) []string {
	var out []string
	for _, r := range set.Rows {
		cells := make([]string, len(r))
		for i, c := range r {
			if c == nil {
				cells[i] = "∅"
			} else {
				cells[i] = *c
			}
		}
		out = append(out, strings.Join(cells, "|"))
	}
	return out
}

func fieldOf(t *testing.T, res *output.Result, key string) output.Field {
	t.Helper()
	i := slices.IndexFunc(res.Fields, func(f output.Field) bool { return f.Key == key })
	if i < 0 {
		t.Fatalf("no field %s in %+v", key, res.Fields)
	}
	return res.Fields[i]
}

func wantCode(t *testing.T, err error, code output.Code) *output.Error {
	t.Helper()
	e, ok := err.(*output.Error)
	if !ok || e.Code != code {
		t.Fatalf("err %v, want %s", err, code)
	}
	return e
}

func TestSecurityLoginsList(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want []string
	}{
		// Disabled sa left out; sorted by name.
		{nil, []string{"##MS_AgentSigningCertificate##", `CORP\dba`, "etl"}},
		{[]string{"--include-disabled"}, []string{"##MS_AgentSigningCertificate##", `CORP\dba`, "etl", "sa"}},
		{[]string{"--type", "sql"}, []string{"etl"}},
		{[]string{"--type", "sql", "--include-disabled"}, []string{"etl", "sa"}},
		{[]string{"--type", "windows-group"}, []string{`CORP\dba`}},
		{[]string{"--type", "windows"}, nil},
	} {
		res, err := runSecurity(t, newSecCatalog(), append([]string{"logins", "list"}, tc.argv...)...)
		if err != nil {
			t.Fatal(err)
		}
		if got := column(res.Sets[0], "name"); !slices.Equal(got, tc.want) {
			t.Errorf("%q: names %q, want %q", tc.argv, got, tc.want)
		}
	}
	res, _ := runSecurity(t, newSecCatalog(), "logins", "list", "--include-disabled")
	rows := rowsOf(res.Sets[0])
	if want := "sa|SQL_LOGIN|1|0|master|us_english|1|0|2026-03-14 09:12:44.000|2026-03-14 09:12:44.000"; rows[3] != want {
		t.Errorf("sa row %s, want %s", rows[3], want)
	}
	// system marks ##…##; a certificate login has no default language.
	if want := "##MS_AgentSigningCertificate##|CERTIFICATE_MAPPED_LOGIN|0|1|∅|∅|0|0"; !strings.HasPrefix(rows[0], want) {
		t.Errorf("certificate row %s, want prefix %s", rows[0], want)
	}
}

func TestSecurityUsersList(t *testing.T) {
	res, err := runSecurity(t, newSecCatalog(), "users", "list")
	if err != nil {
		t.Fatal(err)
	}
	if res.Database != "app" {
		t.Errorf("database %q", res.Database)
	}
	// name|type|login|login_disabled|orphaned|default_schema|authentication|mapped_to
	want := []string{
		`CORP\ann|WINDOWS_USER|∅|∅|∅|∅|INSTANCE|∅`, // may enter through a group: unknown
		"dbo|SQL_USER|sa|1|0|dbo|INSTANCE|∅",
		"etl_user|SQL_USER|etl|0|0|dbo|INSTANCE|∅",
		"ghost|SQL_USER|∅|∅|1|dbo|INSTANCE|∅", // its login is gone
		"nologin|SQL_USER|∅|∅|0|dbo|NONE|∅",   // WITHOUT LOGIN needs none
	}
	var got []string
	for _, r := range rowsOf(res.Sets[0]) {
		got = append(got, strings.Join(strings.Split(r, "|")[:8], "|"))
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Azure SQL Database hides the logins: orphaned cannot be told.
	cat := newSecCatalog()
	cat.engine = gosmo.EngineAzureSQLDatabase
	res, _ = runSecurity(t, cat, "users", "list")
	if got := column(res.Sets[0], "orphaned"); slices.ContainsFunc(got, func(s string) bool { return s != "NULL" }) {
		t.Errorf("Azure orphaned %q, want all NULL", got)
	}
}

func TestSecurityRolesList(t *testing.T) {
	res, err := runSecurity(t, newSecCatalog(), "roles", "list")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowsOf(res.Sets[0]), []string{
		"db_owner|database|1|dbo|1|dbo",
		"readers|database|0|dbo|1|etl_user",
		"reporting|database|0|dbo|2|etl_user, readers",
	}; !slices.Equal(got, want) {
		t.Errorf("database roles %q, want %q", got, want)
	}
	res, err = runSecurity(t, newSecCatalog(), "roles", "list", "--scope", "server")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowsOf(res.Sets[0]), []string{
		"monitoring|server|0|sa|1|etl",
		"public|server|1|sa|0|∅",
		`sysadmin|server|1|sa|2|sa, CORP\dba`,
	}; !slices.Equal(got, want) {
		t.Errorf("server roles %q, want %q", got, want)
	}
}

func TestSecurityRoleMembers(t *testing.T) {
	res, err := runSecurity(t, newSecCatalog(), "role-members", "reporting")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowsOf(res.Sets[0]), []string{"etl_user|SQL_USER", "readers|DATABASE_ROLE"}; !slices.Equal(got, want) {
		t.Errorf("members %q, want %q", got, want)
	}
	if f := fieldOf(t, res, "scope"); f.Value != "database" {
		t.Errorf("scope %q", f.Value)
	}
	res, err = runSecurity(t, newSecCatalog(), "role-members", "sysadmin", "--scope", "server")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "name"); !slices.Equal(got, []string{`CORP\dba`, "sa"}) {
		t.Errorf("sysadmin members %q", got)
	}
	if f := fieldOf(t, res, "fixedRole"); f.Value != "true" {
		t.Errorf("fixedRole %q", f.Value)
	}
}

func TestSecurityRoleMembersNotFound(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		hint string
	}{
		// The other scope has it: say so.
		{[]string{"sysadmin"}, "is a server role: add --scope server"},
		{[]string{"reporting", "--scope", "server"}, `is a role of database [app]: drop --scope server`},
		{[]string{"nosuch"}, "security roles list lists the roles"},
		{[]string{"nosuch", "--scope", "server"}, "security roles list --scope server"},
	} {
		_, err := runSecurity(t, newSecCatalog(), append([]string{"role-members"}, tc.argv...)...)
		e := wantCode(t, err, output.CodeNotFound)
		if !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%q: hint %q, want %q", tc.argv, e.Hint, tc.hint)
		}
	}
}

func TestSecurityPermissionsExplicit(t *testing.T) {
	res, err := runSecurity(t, newSecCatalog(), "permissions", "etl_user")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"principal": "etl_user", "principalType": "SQL_USER", "scope": "database",
		"mode": "explicit", "memberOf": "readers, reporting"} {
		if f := fieldOf(t, res, key); f.Value != want {
			t.Errorf("%s %q, want %q", key, f.Value, want)
		}
	}
	// class|schema|name|column|permission|state: securables, then columns.
	if got, want := rowsOf(res.Sets[0]), []string{
		"DATABASE|∅|∅|∅|CONNECT|GRANT",
		"SCHEMA|∅|Sales|∅|SELECT|GRANT_WITH_GRANT_OPTION",
		"TABLE|dbo|Orders|∅|DELETE|DENY",
		"TABLE|dbo|Customers|Email|SELECT|DENY",
	}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}

	// A role answers too, explicitly.
	res, err = runSecurity(t, newSecCatalog(), "permissions", "readers")
	if err != nil {
		t.Fatal(err)
	}
	if f := fieldOf(t, res, "principalType"); f.Value != "DATABASE_ROLE" {
		t.Errorf("readers type %q", f.Value)
	}
	if got := rowsOf(res.Sets[0]); !slices.Equal(got, []string{"SCHEMA|∅|dbo|∅|SELECT|GRANT"}) {
		t.Errorf("readers rows %q", got)
	}
	if f := fieldOf(t, res, "memberOf"); f.Value != "reporting" {
		t.Errorf("readers memberOf %q", f.Value)
	}

	// Server scope: only the login's own entries.
	res, err = runSecurity(t, newSecCatalog(), "permissions", "etl", "--scope", "server")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowsOf(res.Sets[0]), []string{
		"SERVER|∅|∅|∅|VIEW SERVER STATE|GRANT", "SERVER|∅|∅|∅|ALTER ANY LOGIN|DENY",
	}; !slices.Equal(got, want) {
		t.Errorf("server rows %q, want %q", got, want)
	}
	if f := fieldOf(t, res, "memberOf"); f.Value != "monitoring" {
		t.Errorf("etl memberOf %q", f.Value)
	}
	// A principal in no role: memberOf NULL.
	res, _ = runSecurity(t, newSecCatalog(), "permissions", "ghost")
	if f := fieldOf(t, res, "memberOf"); !f.Null {
		t.Errorf("ghost memberOf %+v, want NULL", f)
	}
}

func TestSecurityPermissionsEffective(t *testing.T) {
	cat := newSecCatalog()
	res, err := runSecurity(t, cat, "permissions", "etl_user", "--effective")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowsOf(res.Sets[0]); !slices.Equal(got, []string{"database|CONNECT", "database|SELECT"}) {
		t.Errorf("rows %q", got)
	}
	if f := fieldOf(t, res, "mode"); f.Value != "effective" {
		t.Errorf("mode %q", f.Value)
	}
	res, err = runSecurity(t, cat, "permissions", "etl", "--scope", "server", "--effective")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "permission"); !slices.Equal(got, []string{"CONNECT SQL", "VIEW SERVER STATE"}) {
		t.Errorf("server rows %q", got)
	}
	if !slices.Equal(cat.effAsked, []string{"db:etl_user", "server:etl"}) {
		t.Errorf("asked %q", cat.effAsked)
	}

	// A role cannot be impersonated: a usage error before any EXECUTE AS.
	for _, argv := range [][]string{{"readers"}, {"sysadmin", "--scope", "server"}} {
		cat := newSecCatalog()
		_, err := runSecurity(t, cat, append(append([]string{"permissions"}, argv...), "--effective")...)
		e := wantCode(t, err, output.CodeUsageError)
		if !strings.Contains(e.Message, "cannot impersonate a role") {
			t.Errorf("%q: %q", argv, e.Message)
		}
		if len(cat.effAsked) != 0 {
			t.Errorf("%q: impersonated %q", argv, cat.effAsked)
		}
	}
}

func TestSecurityPermissionsEffectiveWithoutImpersonate(t *testing.T) {
	for _, tc := range []struct {
		argv   []string
		number int32
		hint   string
	}{
		{[]string{"etl_user"}, 15517, "IMPERSONATE on it, or db_owner"},
		{[]string{"etl", "--scope", "server"}, 15406, "IMPERSONATE on it, or CONTROL SERVER"},
	} {
		cat := newSecCatalog()
		cat.effErr = fmt.Errorf("gosmo: effective: %w", mssql.Error{Number: tc.number, Class: 16,
			Message: "Cannot execute as the principal because it does not exist, cannot be impersonated, or you do not have permission."})
		_, err := runSecurity(t, cat, append(append([]string{"permissions"}, tc.argv...), "--effective")...)
		e := wantCode(t, err, output.CodePermissionDenied)
		if !strings.Contains(e.Hint, tc.hint) || e.SQL == nil || e.SQL.Number != tc.number {
			t.Errorf("%q: %+v hint %q", tc.argv, e.SQL, e.Hint)
		}
	}
}

func TestSecurityPermissionsNotFound(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		hint string
	}{
		// A login asked for in database scope: name its user here.
		{[]string{"etl"}, "Login [etl] is user [etl_user] in this database"},
		{[]string{"monitoring"}, "is a server role: add --scope server"},
		{[]string{"etl_user", "--scope", "server"}, "is a user of database [app]: drop --scope server"},
		{[]string{"readers", "--scope", "server"}, "is a role of database [app]: drop --scope server"},
		{[]string{"nosuch"}, "security users list and security roles list"},
	} {
		_, err := runSecurity(t, newSecCatalog(), append([]string{"permissions"}, tc.argv...)...)
		e := wantCode(t, err, output.CodeNotFound)
		if !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%q: hint %q, want %q", tc.argv, e.Hint, tc.hint)
		}
	}
	// A login with no user here: --scope server.
	cat := newSecCatalog()
	cat.users = slices.DeleteFunc(cat.users, func(u *gosmo.User) bool { return u.LoginName == "etl" })
	_, err := runSecurity(t, cat, "permissions", "etl")
	if e := wantCode(t, err, output.CodeNotFound); !strings.Contains(e.Hint, "[etl] is a login: add --scope server") {
		t.Errorf("hint %q", e.Hint)
	}
}
