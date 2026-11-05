package cmd

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
)

// secServer is the security group's test server: real gosmo over canned rows
// (gosmotest), answering from its fields at the time of each query — the
// server's logins, roles and entries, and database app's users, roles,
// entries and the securables the writes resolve: objects, schemas, types, a
// certificate and the columns of dbo.Customers and the view dbo.vOrders.
// The writes gosmo sends — GRANT, DENY, REVOKE, ALTER [SERVER] ROLE — are
// applied to the same fields as the server would (applyWrite), so a read
// after a write sees it.
//
// Every role's members come from members, the one source both the role
// reads (their FOR JSON member list) and the role-members reads answer
// from. Lists come back in the server's ORDER BY, compared ignoring case as
// a case-insensitive collation does; a by-name read matches exactly.
type secServer struct {
	*catalogServer
	logins      []*gosmo.Login
	serverRoles []*gosmo.ServerRole // Members unused: see members
	serverPerms []*gosmo.ServerPermissionEntry
	users       []*gosmo.User
	dbRoles     []*gosmo.DatabaseRole // Members unused: see members
	appRoles    []*gosmo.ApplicationRole
	members     map[string][]*gosmo.RoleMember // "server:<role>" or "db:<role>"
	dbPerms     map[string][]*gosmo.PrincipalSecurable
	colPerms    map[string][]*gosmo.ColumnPermissionEntry
	effective   map[string][]*gosmo.EffectivePermission // "db:<user>" or "server:<login>"
	effErr      error                                   // fails --effective's read
	schemas     []string
	types       []ObjectName // user-defined data types
	tt          []ObjectName // user-defined table types
	certs       []string

	effAsked []string // each --effective read, "db:<user>" or "server:<login>"

	// writes is each write statement the server was sent, as sent; the
	// failAt'th (1-based) fails with writeErr. ignore takes every write
	// and changes nothing, as for a grant to oneself.
	writes   []string
	failAt   int
	writeErr error
	ignore   bool
}

func newSecServer(t *testing.T) *secServer {
	at := catalogAt
	s := &secServer{
		logins: []*gosmo.Login{
			{Name: "sa", LoginType: gosmo.PrincipalSQLLogin, IsDisabled: true, DefaultDatabase: "master",
				DefaultLanguage: "us_english", IsPolicyChecked: true, CreateDate: at, ModifyDate: at},
			{Name: "etl", LoginType: gosmo.PrincipalSQLLogin, DefaultDatabase: "app", DefaultLanguage: "us_english",
				IsPolicyChecked: true, IsExpirationChecked: true, CreateDate: at, ModifyDate: at},
			{Name: `CORP\dba`, LoginType: gosmo.PrincipalWindowsGroup, DefaultDatabase: "master", CreateDate: at, ModifyDate: at},
			{Name: "##MS_AgentSigningCertificate##", LoginType: gosmo.PrincipalCertificateMappedLogin, CreateDate: at, ModifyDate: at},
		},
		serverRoles: []*gosmo.ServerRole{
			{Name: "sysadmin", IsFixedRole: true, Owner: "sa"},
			{Name: "monitoring", Owner: "sa"},
			{Name: "public", IsFixedRole: true, Owner: "sa"},
		},
		serverPerms: []*gosmo.ServerPermissionEntry{
			{Principal: "etl", Permission: "VIEW SERVER STATE", State: gosmo.PermissionGrant},
			{Principal: "monitoring", Permission: "VIEW ANY DEFINITION", State: gosmo.PermissionGrant},
			{Principal: "etl", Permission: "ALTER ANY LOGIN", State: gosmo.PermissionDeny},
		},
		users: []*gosmo.User{
			{Name: "etl_user", UserType: gosmo.PrincipalSQLUser, LoginName: "etl", DefaultSchema: "dbo", AuthType: "INSTANCE", CreateDate: at, ModifyDate: at},
			{Name: "dbo", UserType: gosmo.PrincipalSQLUser, LoginName: "sa", LoginDisabled: true, DefaultSchema: "dbo", AuthType: "INSTANCE", CreateDate: at, ModifyDate: at},
			{Name: "ghost", UserType: gosmo.PrincipalSQLUser, DefaultSchema: "dbo", AuthType: "INSTANCE", CreateDate: at, ModifyDate: at},
			{Name: "nologin", UserType: gosmo.PrincipalSQLUser, DefaultSchema: "dbo", AuthType: "NONE", CreateDate: at, ModifyDate: at},
			{Name: `CORP\ann`, UserType: gosmo.PrincipalWindowsUser, AuthType: "INSTANCE", CreateDate: at, ModifyDate: at},
		},
		dbRoles: []*gosmo.DatabaseRole{
			{Name: "db_owner", IsFixedRole: true, Owner: "dbo"},
			{Name: "reporting", Owner: "dbo"},
			{Name: "readers", Owner: "dbo"},
		},
		appRoles: []*gosmo.ApplicationRole{{Name: "billing_app", DefaultSchema: "dbo", CreateDate: at, ModifyDate: at}},
		members: map[string][]*gosmo.RoleMember{
			"server:sysadmin":   {{Name: "sa", Type: gosmo.PrincipalSQLLogin}, {Name: `CORP\dba`, Type: gosmo.PrincipalWindowsGroup}},
			"server:monitoring": {{Name: "etl", Type: gosmo.PrincipalSQLLogin}},
			"db:db_owner":       {{Name: "dbo", Type: gosmo.PrincipalSQLUser}},
			"db:reporting":      {{Name: "etl_user", Type: gosmo.PrincipalSQLUser}, {Name: "readers", Type: gosmo.PrincipalDatabaseRole}},
			"db:readers":        {{Name: "etl_user", Type: gosmo.PrincipalSQLUser}},
		},
		dbPerms: map[string][]*gosmo.PrincipalSecurable{
			"etl_user": {
				{SecurableType: gosmo.SecurableDatabase, Permission: "CONNECT", State: gosmo.PermissionGrant},
				{SecurableType: gosmo.SecurableSchema, Name: "Sales", Permission: "SELECT", State: gosmo.PermissionGrantWithGrantOption},
				{SecurableType: gosmo.SecurableTable, Schema: "dbo", Name: "Orders", Permission: "DELETE", State: gosmo.PermissionDeny},
			},
			"readers":     {{SecurableType: gosmo.SecurableSchema, Name: "dbo", Permission: "SELECT", State: gosmo.PermissionGrant}},
			"billing_app": {{SecurableType: gosmo.SecurableSchema, Name: "Sales", Permission: "EXECUTE", State: gosmo.PermissionGrant}},
		},
		colPerms: map[string][]*gosmo.ColumnPermissionEntry{
			"etl_user": {{Principal: "etl_user", PrincipalType: gosmo.PrincipalSQLUser, Grantor: "dbo", Schema: "dbo",
				Object: "Customers", ObjectType: gosmo.SecurableTable, Column: "Email", Permission: "SELECT", State: gosmo.PermissionDeny}},
		},
		effective: map[string][]*gosmo.EffectivePermission{
			"db:etl_user": {{Entity: "database", Permission: "CONNECT"}, {Entity: "database", Permission: "SELECT"}},
			"server:etl":  {{Entity: "server", Permission: "CONNECT SQL"}, {Entity: "server", Permission: "VIEW SERVER STATE"}},
		},
		schemas: []string{"dbo", "Sales", "sys"},
		// Sales.phone differs from dbo.Phone by case only: an exact match wins.
		types: []ObjectName{{"dbo", "Phone"}, {"Sales", "phone"}, {"Sales", "Money2"}, {"dbo", "Money2"}},
		tt:    []ObjectName{{"Sales", "OrderList"}},
		certs: []string{"SignCert"},
	}
	obj := func(id int, schema, name, typ, desc string) *gosmo.ObjectInfo {
		return &gosmo.ObjectInfo{ObjectID: id, Schema: schema, Name: name, Type: typ, TypeDesc: desc, Created: at, Modified: at}
	}
	s.catalogServer = newCatalogServerWith(t, "app", func(c *catalogServer) {
		s.catalogServer = c
		s.rules()
	},
		obj(1, "dbo", "Customers", "U", "USER_TABLE"),
		obj(2, "dbo", "Orders", "U", "USER_TABLE"),
		obj(3, "dbo", "vOrders", "V", "VIEW"),
		obj(4, "Sales", "usp_Load", "P", "SQL_STORED_PROCEDURE"),
		obj(5, "Sales", "fn_Total", "FN", "SQL_SCALAR_FUNCTION"),
		obj(6, "Sales", "tvf_Rows", "IF", "SQL_INLINE_TABLE_VALUED_FUNCTION"),
		obj(7, "Sales", "mtvf_Rows", "TF", "SQL_TABLE_VALUED_FUNCTION"),
		obj(8, "dbo", "seq_Id", "SO", "SEQUENCE_OBJECT"),
		obj(9, "dbo", "syn_Cust", "SN", "SYNONYM"),
		obj(10, "dbo", "PK_Orders", "PK", "PRIMARY_KEY_CONSTRAINT"),
		obj(11, "Sales", "Orders", "U", "USER_TABLE"),
		obj(12, "dbo", "Server", "U", "USER_TABLE"),
	)
	col := func(name string, ordinal int) *gosmo.Column {
		return &gosmo.Column{Name: name, OrdinalPosition: ordinal, DataType: "int", TypeSchema: "sys"}
	}
	for _, id := range []int{2, 11, 12} {
		s.columns[id] = []*gosmo.Column{col("id", 1)}
	}
	s.columns[1] = []*gosmo.Column{col("id", 1), col("Email", 2), col("Phone", 3)}
	s.columns[3] = []*gosmo.Column{col("OrderID", 1), col("Total", 2)}
	return s
}

// sortedCI is list ordered by the keys key returns, each compared ignoring
// case, as an ORDER BY under a case-insensitive collation.
func sortedCI[T any](list []T, key func(T) []string) []T {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b T) int {
		ka, kb := key(a), key(b)
		for i := range ka {
			if c := cmp.Compare(strings.ToLower(ka[i]), strings.ToLower(kb[i])); c != 0 {
				return c
			}
		}
		return 0
	})
	return out
}

// memberNames is role's members' names, ordered by name.
func (s *secServer) memberNames(key string) []string {
	var out []string
	for _, m := range sortedCI(s.members[key], func(m *gosmo.RoleMember) []string { return []string{m.Name} }) {
		out = append(out, m.Name)
	}
	return out
}

// principalType is the type_desc of server principal name.
func (s *secServer) principalType(name string) gosmo.PrincipalType {
	for _, l := range s.logins {
		if l.Name == name {
			return l.LoginType
		}
	}
	return gosmo.PrincipalServerRole
}

var executeAs = regexp.MustCompile(`EXECUTE AS (USER|LOGIN) = N'((?:[^']|'')*)'`)

var (
	permissionStatement = regexp.MustCompile(`^(?:USE master; )?(GRANT|DENY|REVOKE) (GRANT OPTION FOR )?(.+?)` +
		`(?: \((.+?)\))?(?: ON (?:(SCHEMA|TYPE|CERTIFICATE)::)?(.+?))? (?:TO|FROM) (\[.+\])( WITH GRANT OPTION)?( CASCADE)?$`)
	memberStatement = regexp.MustCompile(`^ALTER (SERVER )?ROLE (\[.+\]) (ADD|DROP) MEMBER (\[.+\])$`)
	bracketed       = regexp.MustCompile(`\[((?:[^\]]|\]\])*)\]`)
)

// bracketNames is the bracketed identifiers in s, unquoted: "[dbo].[t]" is dbo, t.
func bracketNames(s string) []string {
	var out []string
	for _, m := range bracketed.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.ReplaceAll(m[1], "]]", "]"))
	}
	return out
}

// applyWrite is the server taking q, a write: recorded, failed as failAt
// says, else the grant or membership it states applied to s.
func (s *secServer) applyWrite(q gosmotest.Query) gosmotest.Result {
	s.writes = append(s.writes, q.Text)
	if s.failAt == len(s.writes) {
		return gosmotest.Result{Err: s.writeErr}
	}
	if s.ignore {
		return gosmotest.Result{}
	}
	if m := memberStatement.FindStringSubmatch(q.Text); m != nil {
		role, member := bracketNames(m[2])[0], bracketNames(m[4])[0]
		key, typ := "db:"+role, gosmo.PrincipalSQLUser
		if m[1] != "" {
			key, typ = "server:"+role, gosmo.PrincipalSQLLogin
		}
		s.members[key] = slices.DeleteFunc(s.members[key], func(m *gosmo.RoleMember) bool { return m.Name == member })
		if m[3] == "ADD" {
			s.members[key] = append(s.members[key], &gosmo.RoleMember{Name: member, Type: typ})
		}
		return gosmotest.Result{}
	}
	m := permissionStatement.FindStringSubmatch(q.Text)
	if m == nil {
		s.t.Errorf("secServer: a write it does not know: %s", q)
		return gosmotest.Result{}
	}
	verb, optionOnly, perm, cols, class, on, principal := m[1], m[2] != "", m[3], bracketNames(m[4]), m[5], bracketNames(m[6]), bracketNames(m[7])[0]
	state := gosmo.PermissionGrant
	switch {
	case verb == "DENY":
		state = gosmo.PermissionDeny
	case m[8] != "":
		state = gosmo.PermissionGrantWithGrantOption
	case optionOnly:
		// REVOKE GRANT OPTION FOR leaves the grant itself.
		verb, state = "GRANT", gosmo.PermissionGrant
	}
	var schema, name string
	switch len(on) {
	case 1:
		name = on[0]
	case 2:
		schema, name = on[0], on[1]
	}
	switch {
	case strings.HasPrefix(q.Text, "USE master; "):
		s.serverPerms = slices.DeleteFunc(s.serverPerms, func(e *gosmo.ServerPermissionEntry) bool {
			return e.Principal == principal && string(e.Permission) == perm
		})
		if verb != "REVOKE" {
			s.serverPerms = append(s.serverPerms, &gosmo.ServerPermissionEntry{Principal: principal, Permission: gosmo.ServerPermission(perm), State: state})
		}
		return gosmotest.Result{}
	case len(on) == 0:
		class = string(gosmo.SecurableDatabase)
	case class == "":
		o := s.object(schema, name)
		if o == nil {
			s.t.Errorf("secServer: a permission on an unknown object: %s", q)
			return gosmotest.Result{}
		}
		class = string(objectClasses[o.Type])
	}
	sc := gosmo.SecurableClass(class)
	if cols != nil {
		for _, col := range cols {
			s.colPerms[principal] = slices.DeleteFunc(s.colPerms[principal], func(e *gosmo.ColumnPermissionEntry) bool {
				return e.Object == name && e.Column == col && string(e.Permission) == perm
			})
			if verb != "REVOKE" {
				s.colPerms[principal] = append(s.colPerms[principal], &gosmo.ColumnPermissionEntry{Principal: principal,
					PrincipalType: gosmo.PrincipalSQLUser, Grantor: "dbo", Schema: schema, Object: name,
					ObjectType: sc, Column: col, Permission: gosmo.ObjectPermission(perm), State: state})
			}
		}
		return gosmotest.Result{}
	}
	s.dbPerms[principal] = slices.DeleteFunc(s.dbPerms[principal], func(e *gosmo.PrincipalSecurable) bool {
		return e.SecurableType == sc && e.Schema == schema && e.Name == name && e.Permission == perm
	})
	if verb != "REVOKE" {
		s.dbPerms[principal] = append(s.dbPerms[principal], &gosmo.PrincipalSecurable{SecurableType: sc,
			Schema: schema, Name: name, Permission: perm, State: state})
	}
	return gosmotest.Result{}
}

func (s *secServer) rules() {
	byName := func(q gosmotest.Query) string { return argString(q, 0) }

	s.OnFunc(func(q gosmotest.Query) bool { return q.Exec }, s.applyWrite)

	// Logins.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, l := range sortedCI(s.logins, func(l *gosmo.Login) []string { return []string{l.Name} }) {
			if !has(q, "@p1") || l.Name == byName(q) {
				rows = append(rows, clitest.LoginRow(l))
			}
		}
		return rows
	}, "FROM sys.server_principals sp", "LEFT JOIN sys.sql_logins sl")

	// Server roles: the list, one by name, a role's members.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for i, r := range sortedCI(s.serverRoles, func(r *gosmo.ServerRole) []string { return []string{r.Name} }) {
			members := s.memberNames("server:" + r.Name)
			switch {
			case !has(q, "@p1"):
				rows = append(rows, clitest.RoleRow(r.Name, 100+i, r.IsFixedRole, r.Owner, members))
			case r.Name == byName(q):
				rows = append(rows, clitest.RoleByNameRow(r.Name, 100+i, r.IsFixedRole, r.Owner, members, catalogAt))
			}
		}
		return rows
	}, "FROM sys.server_principals r", "WHERE r.type = 'R'")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, m := range sortedCI(s.members["server:"+byName(q)], func(m *gosmo.RoleMember) []string { return []string{m.Name} }) {
			rows = append(rows, clitest.RoleMemberRow(m))
		}
		return rows
	}, "SELECT m.name, m.type_desc", "FROM   sys.server_role_members rm")

	// Database roles, likewise.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for i, r := range sortedCI(s.dbRoles, func(r *gosmo.DatabaseRole) []string { return []string{r.Name} }) {
			members := s.memberNames("db:" + r.Name)
			switch {
			case !has(q, "@p1"):
				rows = append(rows, clitest.RoleRow(r.Name, 16384+i, r.IsFixedRole, r.Owner, members))
			case r.Name == byName(q):
				rows = append(rows, clitest.RoleByNameRow(r.Name, 16384+i, r.IsFixedRole, r.Owner, members, catalogAt))
			}
		}
		return rows
	}, "FROM   sys.database_principals r", "WHERE  r.type = 'R'")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, m := range sortedCI(s.members["db:"+byName(q)], func(m *gosmo.RoleMember) []string { return []string{m.Name} }) {
			rows = append(rows, clitest.RoleMemberRow(m))
		}
		return rows
	}, "SELECT m.name, m.type_desc", "FROM   sys.database_role_members rm")

	// Users; the login columns are NULL without the server_principals join
	// (Azure SQL Database).
	s.on(func(q gosmotest.Query) [][]any {
		join := has(q, "JOIN sys.server_principals sp")
		var rows [][]any
		for i, u := range sortedCI(s.users, func(u *gosmo.User) []string { return []string{u.Name} }) {
			if !has(q, "@p1") || u.Name == byName(q) {
				rows = append(rows, clitest.UserRow(u, 5+i, join))
			}
		}
		return rows
	}, "FROM   sys.database_principals dp", "dp.type IN ('S'")

	// Application roles.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for i, r := range s.appRoles {
			if !has(q, "@p1") || r.Name == byName(q) {
				rows = append(rows, clitest.ApplicationRoleRow(r, 200+i))
			}
		}
		return rows
	}, "FROM   sys.database_principals", "WHERE  type = 'A'")

	// A principal's explicit entries: securables, columns, the server's.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, e := range s.dbPerms[byName(q)] {
			rows = append(rows, clitest.PermissionRow(e))
		}
		return sortedCI(rows, func(r []any) []string { // class_desc, schema_name, object_name, permission
			return []string{r[0].(string), r[3].(string), r[4].(string), r[1].(string)}
		})
	}, "FROM   sys.database_permissions dp", "LEFT   JOIN sys.schemas sch")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, e := range sortedCI(s.colPerms[byName(q)], func(e *gosmo.ColumnPermissionEntry) []string {
			return []string{e.Schema, e.Object, e.Column, string(e.Permission)}
		}) {
			rows = append(rows, clitest.ColumnPermissionRow(e))
		}
		return rows
	}, "JOIN   sys.columns col", "AND    pr.name = @p1")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, e := range sortedCI(s.serverPerms, func(e *gosmo.ServerPermissionEntry) []string {
			return []string{e.Principal, string(e.Permission)}
		}) {
			e := *e
			e.PrincipalType, e.Grantor = s.principalType(e.Principal), "sa"
			rows = append(rows, clitest.ServerPermissionRow(&e))
		}
		return rows
	}, "FROM   sys.server_permissions sp")

	// --effective: fn_my_permissions under EXECUTE AS USER or LOGIN.
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "fn_my_permissions") },
		func(q gosmotest.Query) gosmotest.Result {
			m := executeAs.FindStringSubmatch(q.Text)
			key := "db:"
			if m[1] == "LOGIN" {
				key = "server:"
			}
			key += strings.ReplaceAll(m[2], "''", "'")
			s.effAsked = append(s.effAsked, key)
			if s.effErr != nil {
				return gosmotest.Result{Err: s.effErr}
			}
			var rows [][]any
			for _, e := range s.effective[key] {
				rows = append(rows, clitest.EffectivePermissionRow(e))
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}}
		})

	// The securables grant/deny/revoke resolves besides objects.
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for i, n := range sortedCI(s.schemas, func(n string) []string { return []string{n} }) {
			rows = append(rows, clitest.SchemaRow(n, 1+i))
		}
		return rows
	}, "SELECT s.name, s.schema_id, p.name AS owner", "ORDER  BY s.name")
	s.on(func(q gosmotest.Query) [][]any {
		for i, c := range s.certs {
			if c == byName(q) {
				return [][]any{clitest.CertificateRow(c, 256+i, catalogAt)}
			}
		}
		return nil
	}, "FROM   sys.certificates c", "WHERE  c.name = @p1")
	typeKey := func(n ObjectName) []string { return []string{n.Schema, n.Name} }
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for i, n := range sortedCI(s.types, typeKey) {
			rows = append(rows, clitest.DataTypeRow(n.Schema, n.Name, 257+i))
		}
		return rows
	}, "FROM   sys.types t", "t.is_table_type = 0")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for i, n := range sortedCI(s.tt, typeKey) {
			rows = append(rows, clitest.TableTypeRow(n.Schema, n.Name, 300+i))
		}
		return rows
	}, "FROM   sys.table_types tt")
	s.on(func(gosmotest.Query) [][]any { return nil }, "WHERE  t.is_assembly_type = 1")
}
