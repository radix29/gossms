package cmd

import (
	"context"
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

// runSecurity runs `security <argv…>` against s.
func runSecurity(t *testing.T, s *secServer, argv ...string) (*output.Result, error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
			return s.conn(ctx)
		},
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
		res, err := runSecurity(t, newSecServer(t), append([]string{"logins", "list"}, tc.argv...)...)
		if err != nil {
			t.Fatal(err)
		}
		if got := column(res.Sets[0], "name"); !slices.Equal(got, tc.want) {
			t.Errorf("%q: names %q, want %q", tc.argv, got, tc.want)
		}
	}
	res, _ := runSecurity(t, newSecServer(t), "logins", "list", "--include-disabled")
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
	res, err := runSecurity(t, newSecServer(t), "users", "list")
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
	srv := newSecServer(t)
	srv.Info.EngineEdition = int(gosmo.EngineAzureSQLDatabase)
	res, _ = runSecurity(t, srv, "users", "list")
	if got := column(res.Sets[0], "orphaned"); slices.ContainsFunc(got, func(s string) bool { return s != "NULL" }) {
		t.Errorf("Azure orphaned %q, want all NULL", got)
	}
}

func TestSecurityRolesList(t *testing.T) {
	res, err := runSecurity(t, newSecServer(t), "roles", "list")
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
	res, err = runSecurity(t, newSecServer(t), "roles", "list", "--scope", "server")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowsOf(res.Sets[0]), []string{
		"monitoring|server|0|sa|1|etl",
		"public|server|1|sa|0|∅",
		`sysadmin|server|1|sa|2|CORP\dba, sa`, // members by name
	}; !slices.Equal(got, want) {
		t.Errorf("server roles %q, want %q", got, want)
	}
}

func TestSecurityRoleMembers(t *testing.T) {
	res, err := runSecurity(t, newSecServer(t), "role-members", "reporting")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowsOf(res.Sets[0]), []string{"etl_user|SQL_USER", "readers|DATABASE_ROLE"}; !slices.Equal(got, want) {
		t.Errorf("members %q, want %q", got, want)
	}
	if f := fieldOf(t, res, "scope"); f.Value != "database" {
		t.Errorf("scope %q", f.Value)
	}
	res, err = runSecurity(t, newSecServer(t), "role-members", "sysadmin", "--scope", "server")
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
		_, err := runSecurity(t, newSecServer(t), append([]string{"role-members"}, tc.argv...)...)
		e := wantCode(t, err, output.CodeNotFound)
		if !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%q: hint %q, want %q", tc.argv, e.Hint, tc.hint)
		}
	}
}

func TestSecurityPermissionsExplicit(t *testing.T) {
	res, err := runSecurity(t, newSecServer(t), "permissions", "etl_user")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"principal": "etl_user", "principalType": "SQL_USER", "scope": "database",
		"mode": "explicit", "memberOf": "readers, reporting"} {
		if f := fieldOf(t, res, key); f.Value != want {
			t.Errorf("%s %q, want %q", key, f.Value, want)
		}
	}
	// class|schema|name|column|permission|state: securables (the server's
	// order: by class_desc, OBJECT_OR_COLUMN before SCHEMA), then columns.
	if got, want := rowsOf(res.Sets[0]), []string{
		"DATABASE|∅|∅|∅|CONNECT|GRANT",
		"TABLE|dbo|Orders|∅|DELETE|DENY",
		"SCHEMA|∅|Sales|∅|SELECT|GRANT_WITH_GRANT_OPTION",
		"TABLE|dbo|Customers|Email|SELECT|DENY",
	}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}

	// A role answers too, explicitly.
	res, err = runSecurity(t, newSecServer(t), "permissions", "readers")
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

	// So does an application role: after users and database roles.
	res, err = runSecurity(t, newSecServer(t), "permissions", "billing_app")
	if err != nil {
		t.Fatal(err)
	}
	if f := fieldOf(t, res, "principalType"); f.Value != "APPLICATION_ROLE" {
		t.Errorf("billing_app type %q", f.Value)
	}
	if got := rowsOf(res.Sets[0]); !slices.Equal(got, []string{"SCHEMA|∅|Sales|∅|EXECUTE|GRANT"}) {
		t.Errorf("billing_app rows %q", got)
	}

	// Server scope: only the login's own entries.
	res, err = runSecurity(t, newSecServer(t), "permissions", "etl", "--scope", "server")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowsOf(res.Sets[0]), []string{
		"SERVER|∅|∅|∅|ALTER ANY LOGIN|DENY", "SERVER|∅|∅|∅|VIEW SERVER STATE|GRANT",
	}; !slices.Equal(got, want) {
		t.Errorf("server rows %q, want %q", got, want)
	}
	if f := fieldOf(t, res, "memberOf"); f.Value != "monitoring" {
		t.Errorf("etl memberOf %q", f.Value)
	}
	// A principal in no role: memberOf NULL.
	res, _ = runSecurity(t, newSecServer(t), "permissions", "ghost")
	if f := fieldOf(t, res, "memberOf"); !f.Null {
		t.Errorf("ghost memberOf %+v, want NULL", f)
	}
}

func TestSecurityPermissionsEffective(t *testing.T) {
	srv := newSecServer(t)
	res, err := runSecurity(t, srv, "permissions", "etl_user", "--effective")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowsOf(res.Sets[0]); !slices.Equal(got, []string{"database|CONNECT", "database|SELECT"}) {
		t.Errorf("rows %q", got)
	}
	if f := fieldOf(t, res, "mode"); f.Value != "effective" {
		t.Errorf("mode %q", f.Value)
	}
	res, err = runSecurity(t, srv, "permissions", "etl", "--scope", "server", "--effective")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "permission"); !slices.Equal(got, []string{"CONNECT SQL", "VIEW SERVER STATE"}) {
		t.Errorf("server rows %q", got)
	}
	if !slices.Equal(srv.effAsked, []string{"db:etl_user", "server:etl"}) {
		t.Errorf("asked %q", srv.effAsked)
	}

	// A role cannot be impersonated: a usage error before any EXECUTE AS.
	for _, argv := range [][]string{{"readers"}, {"billing_app"}, {"sysadmin", "--scope", "server"}} {
		srv := newSecServer(t)
		_, err := runSecurity(t, srv, append(append([]string{"permissions"}, argv...), "--effective")...)
		e := wantCode(t, err, output.CodeUsageError)
		if !strings.Contains(e.Message, "cannot impersonate a role") {
			t.Errorf("%q: %q", argv, e.Message)
		}
		if len(srv.effAsked) != 0 {
			t.Errorf("%q: impersonated %q", argv, srv.effAsked)
		}
		// An application role has no member to suggest.
		if member := strings.Contains(e.Hint, "that is a member"); member != (argv[0] != "billing_app") {
			t.Errorf("%q: hint %q", argv, e.Hint)
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
		srv := newSecServer(t)
		srv.effErr = fmt.Errorf("gosmo: effective: %w", mssql.Error{Number: tc.number, Class: 16,
			Message: "Cannot execute as the principal because it does not exist, cannot be impersonated, or you do not have permission."})
		_, err := runSecurity(t, srv, append(append([]string{"permissions"}, tc.argv...), "--effective")...)
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
		{[]string{"billing_app", "--scope", "server"}, "is an application role of database [app]: drop --scope server"},
		{[]string{"nosuch"}, "security users list and security roles list"},
	} {
		_, err := runSecurity(t, newSecServer(t), append([]string{"permissions"}, tc.argv...)...)
		e := wantCode(t, err, output.CodeNotFound)
		if !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%q: hint %q, want %q", tc.argv, e.Hint, tc.hint)
		}
	}
	// A login with no user here: --scope server.
	srv := newSecServer(t)
	srv.users = slices.DeleteFunc(srv.users, func(u *gosmo.User) bool { return u.LoginName == "etl" })
	_, err := runSecurity(t, srv, "permissions", "etl")
	if e := wantCode(t, err, output.CodeNotFound); !strings.Contains(e.Hint, "[etl] is a login: add --scope server") {
		t.Errorf("hint %q", e.Hint)
	}
}
