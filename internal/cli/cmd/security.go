package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The security commands' options. --scope, not --server: --server is the
// connection option.
const (
	optIncludeDisabled = "include-disabled"
	optScope           = "scope"
	optEffective       = "effective"
)

const (
	scopeDatabase = "database"
	scopeServer   = "server"
)

// scopeOption is --scope: whether a role or principal is the connection
// database's or the server's.
func scopeOption(help string) spec.Option {
	return spec.Option{Name: optScope, Type: spec.TypeEnum, Enum: []string{scopeDatabase, scopeServer},
		Default: scopeDatabase, Help: help}
}

// loginType is one of logins list --type's values and the type_desc it
// keeps.
type loginType struct {
	name string
	typ  gosmo.PrincipalType
}

// loginTypes is logins list --type's enum.
var loginTypes = []loginType{
	{"sql", gosmo.PrincipalSQLLogin},
	{"windows", gosmo.PrincipalWindowsLogin},
	{"windows-group", gosmo.PrincipalWindowsGroup},
	{"external", gosmo.PrincipalExternalLogin},
	{"external-group", gosmo.PrincipalExternalGroup},
	{"certificate", gosmo.PrincipalCertificateMappedLogin},
	{"asymmetric-key", gosmo.PrincipalAsymmetricKeyMappedLogin},
}

func loginTypeNames() []string {
	out := make([]string, len(loginTypes))
	for i, t := range loginTypes {
		out[i] = t.name
	}
	return out
}

// SecurityCommands is the security group: its node and its commands. Its
// summary names only what exists.
func SecurityCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"security"},
			Summary: "List logins, users and roles, a role's members, and a principal's permissions",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"security", "logins"},
			Summary: "List the server's logins",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"security", "logins", "list"},
			Summary: "List the server's logins: type, disabled, default database, password policy",
			Description: "One row per server login (sys.server_principals), by name: SQL, Windows and Windows-group, " +
				"Microsoft Entra, and certificate- or asymmetric-key-mapped logins. A disabled login is left out unless " +
				"--include-disabled. system marks the ##…## logins SQL Server creates for itself. policy_checked and " +
				"expiration_checked are a SQL login's CHECK_POLICY and CHECK_EXPIRATION, 0 for every other type. " +
				"A login sees itself and the fixed roles only, unless it has VIEW ANY DEFINITION, ALTER ANY LOGIN " +
				"or is sysadmin: the list is then short, not refused.",
			Options: append(ConnectOptions(),
				spec.Option{Name: optIncludeDisabled, Type: spec.TypeBool, Help: "Also list disabled logins"},
				spec.Option{Name: optType, Type: spec.TypeEnum, Enum: loginTypeNames(), Help: "Only logins of this type"},
			),
			Class: spec.ClassRead,
			Run:   d.securityLogins,
			Examples: []string{
				"gossms security logins list -p prod-ro",
				"gossms security logins list -p prod-ro --type sql --include-disabled -o json",
			},
		},
		{
			Path:    []string{"security", "users"},
			Summary: "List a database's users",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"security", "users", "list"},
			Summary: "List the connection database's users: type, login, default schema, orphaned",
			Description: "One row per user of the connection's database (the profile's, or -d), by name — dbo, guest, " +
				"INFORMATION_SCHEMA and sys included. login is the server login whose SID the user's matches, NULL " +
				"for none. orphaned is 1 for a SQL user created for a login that no longer matches (authentication " +
				"INSTANCE, no login), 0 for a user with a login or one that needs none (WITHOUT LOGIN, contained), " +
				"and NULL where it cannot be told: a Windows user with no login of its own may enter through a " +
				"Windows group, and Azure SQL Database hides the server's logins from a user database. Without VIEW " +
				"DEFINITION on the database a user sees only itself, the fixed roles and the built-in users: the list " +
				"is then short, not refused.",
			Options: ConnectOptions(),
			Class:   spec.ClassRead,
			Run:     d.securityUsers,
			Examples: []string{
				"gossms security users list -p dev -d AdventureWorks",
				"gossms security users list -p dev -o json",
			},
		},
		{
			Path:    []string{"security", "roles"},
			Summary: "List a database's or the server's roles",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"security", "roles", "list"},
			Summary: "List the roles of the connection database, or of the server: fixed, owner, members",
			Description: "One row per role, by name: the connection database's roles (the profile's database, or -d), " +
				"or with --scope server the server roles. members names the role's direct members — a member that is " +
				"itself a role is not expanded (security role-members shows each member's type). Without VIEW DEFINITION " +
				"(VIEW ANY DEFINITION for server roles) the login sees only the fixed roles and its own.",
			Options: append(ConnectOptions(), scopeOption("database: the connection database's roles; server: the server roles")),
			Class:   spec.ClassRead,
			Run:     d.securityRoles,
			Examples: []string{
				"gossms security roles list -p dev -d AdventureWorks",
				"gossms security roles list -p prod-ro --scope server -o json",
			},
		},
		{
			Path:    []string{"security", "role-members"},
			Summary: "List a role's direct members and their types",
			Description: "The role's direct members, by name, with each member's principal type: a database role of " +
				"the connection's database, or with --scope server a server role. A member that is itself a role " +
				"(type DATABASE_ROLE or SERVER_ROLE) is not expanded: ask for its members in turn. The public " +
				"role lists none, since every principal belongs to it. A role not found in the scope asked is " +
				"NOT_FOUND, with a hint when the other scope has a role of that name.",
			Args:    []spec.Arg{{Name: "role", Help: "Role name", Required: true}},
			Options: append(ConnectOptions(), scopeOption("database: a role of the connection database; server: a server role")),
			Class:   spec.ClassRead,
			Run:     d.securityRoleMembers,
			Examples: []string{
				"gossms security role-members sysadmin --scope server -p prod-ro",
				"gossms security role-members db_owner -p dev -d AdventureWorks -o json",
			},
		},
		{
			Path:    []string{"security", "permissions"},
			Summary: "Show a principal's explicit grants and denies, or what it can effectively do",
			Description: "A database user or role of the connection's database, or with --scope server a login or " +
				"server role. By default the explicit GRANT and DENY entries recorded for the principal itself: on " +
				"the database, its schemas, objects, columns, types and certificates (database scope), or on the " +
				"server (server scope). What it holds through a role is not among them — memberOf names its roles, " +
				"whose own entries a call per role shows. --effective asks the server which permissions the principal " +
				"holds on the database itself (or the server) once roles, ownership and DENY are resolved, by " +
				"impersonating it (EXECUTE AS) around fn_my_permissions: database-level permissions such as CONNECT " +
				"or CREATE TABLE, not a grant on a schema or object. A role cannot be impersonated, so --effective on " +
				"one is a usage error. Impersonating needs IMPERSONATE on the user (or db_owner) or on the login (or " +
				"CONTROL SERVER): without it, exit 9. A principal the login cannot see — without VIEW DEFINITION " +
				"(VIEW ANY DEFINITION on the server) it sees only itself and the fixed roles — is NOT_FOUND.",
			Args: []spec.Arg{{Name: "principal", Help: "User or database role; with --scope server, login or server role", Required: true}},
			Options: append(ConnectOptions(),
				scopeOption("database: a user or role of the connection database; server: a login or server role"),
				spec.Option{Name: optEffective, Type: spec.TypeBool,
					Help: "What the principal can do after role membership and DENY, instead of its explicit entries (a user or login only)"},
			),
			Class: spec.ClassRead,
			Run:   d.securityPermissions,
			Examples: []string{
				"gossms security permissions app_reader -p prod-ro -d AdventureWorks",
				"gossms security permissions app_reader -p prod-ro -d AdventureWorks --effective",
				"gossms security permissions etl_login --scope server -p prod-ro -o json",
			},
		},
	}
}

// --- logins list ------------------------------------------------------------

func (d Deps) securityLogins(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	logins, err := cat.Logins(ctx)
	if err != nil {
		return res, readError(err)
	}
	all, _ := inv.Values[optIncludeDisabled].(bool)
	var only gosmo.PrincipalType
	if inv.Given[optType] {
		i := slices.IndexFunc(loginTypes, func(t loginType) bool { return t.name == inv.Values[optType].(string) })
		only = loginTypes[i].typ
	}
	logins = slices.DeleteFunc(logins, func(l *gosmo.Login) bool {
		return (l.IsDisabled && !all) || (only != "" && l.LoginType != only)
	})

	set := output.Set{Name: "logins", Columns: []output.Column{
		{Name: "name", Type: "nvarchar(128)"}, {Name: "type", Type: "nvarchar(60)"},
		{Name: "disabled", Type: "bit"}, {Name: "system", Type: "bit"},
		{Name: "default_database", Type: "nvarchar(128)", Nullable: true},
		{Name: "default_language", Type: "nvarchar(128)", Nullable: true},
		{Name: "policy_checked", Type: "bit"}, {Name: "expiration_checked", Type: "bit"},
		{Name: "created", Type: "datetime"}, {Name: "modified", Type: "datetime"},
	}}
	for _, l := range logins {
		set.Rows = append(set.Rows, []*string{
			ptr(l.Name), ptr(string(l.LoginType)), bitCell(l.IsDisabled), bitCell(l.IsSystem()),
			textOrNull(l.DefaultDatabase), textOrNull(l.DefaultLanguage),
			bitCell(l.IsPolicyChecked), bitCell(l.IsExpirationChecked),
			ptr(gridTime(l.CreateDate)), ptr(gridTime(l.ModifyDate)),
		})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// --- users list -------------------------------------------------------------

func (d Deps) securityUsers(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, dbase, err := d.connectDatabase(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	users, err := cat.Users(ctx, dbase)
	if err != nil {
		return res, readError(err)
	}
	// A user database on Azure SQL Database cannot see the server's logins,
	// so a missing login says nothing there.
	loginsHidden := false
	if info := cat.ServerInfo(); info != nil && gosmo.EngineEdition(info.EngineEdition) == gosmo.EngineAzureSQLDatabase {
		loginsHidden = true
	}

	set := output.Set{Name: "users", Columns: []output.Column{
		{Name: "name", Type: "nvarchar(128)"}, {Name: "type", Type: "nvarchar(60)"},
		{Name: "login", Type: "nvarchar(128)", Nullable: true}, {Name: "login_disabled", Type: "bit", Nullable: true},
		{Name: "orphaned", Type: "bit", Nullable: true},
		{Name: "default_schema", Type: "nvarchar(128)", Nullable: true},
		{Name: "authentication", Type: "nvarchar(60)", Nullable: true},
		{Name: "mapped_to", Type: "nvarchar(128)", Nullable: true},
		{Name: "created", Type: "datetime"}, {Name: "modified", Type: "datetime"},
	}}
	for _, u := range users {
		var disabled *string
		if u.LoginName != "" {
			disabled = bitCell(u.LoginDisabled)
		}
		set.Rows = append(set.Rows, []*string{
			ptr(u.Name), ptr(string(u.UserType)), textOrNull(u.LoginName), disabled,
			userOrphaned(u, loginsHidden), textOrNull(u.DefaultSchema), textOrNull(u.AuthType),
			textOrNull(u.MappedObject), ptr(gridTime(u.CreateDate)), ptr(gridTime(u.ModifyDate)),
		})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// userOrphaned is users list's orphaned cell: 1 for a SQL user created for a
// login that no longer matches, 0 for a user with a login or one that needs
// none, NULL when it cannot be told (see the command's description).
func userOrphaned(u *gosmo.User, loginsHidden bool) *string {
	switch {
	case loginsHidden:
		return nil
	case u.LoginName != "":
		return bitCell(false)
	case u.AuthType != "INSTANCE":
		// NONE (WITHOUT LOGIN, mapped to a certificate or key), DATABASE
		// (contained), EXTERNAL (Entra): no login to lose.
		return bitCell(false)
	case u.UserType == gosmo.PrincipalSQLUser:
		return bitCell(true)
	}
	return nil
}

// --- roles list -------------------------------------------------------------

// roleRow is one role of either scope, as roles list shows it.
type roleRow struct {
	name, owner string
	fixed       bool
	members     []string
}

func (d Deps) securityRoles(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	scope := inv.Values[optScope].(string)
	// Rows in the server's order: by name, in its collation.
	var rows []roleRow
	if scope == scopeServer {
		sc, cat, err := d.connectCatalog(ctx, inv, res)
		if err != nil {
			return res, err
		}
		defer sc.Close()
		roles, err := cat.ServerRoles(ctx)
		if err != nil {
			return res, readError(err)
		}
		for _, r := range roles {
			rows = append(rows, roleRow{r.Name, r.Owner, r.IsFixedRole, r.Members})
		}
	} else {
		sc, cat, dbase, err := d.connectDatabase(ctx, inv, res)
		if err != nil {
			return res, err
		}
		defer sc.Close()
		roles, err := cat.DatabaseRoles(ctx, dbase)
		if err != nil {
			return res, readError(err)
		}
		for _, r := range roles {
			rows = append(rows, roleRow{r.Name, r.Owner, r.IsFixedRole, r.Members})
		}
	}

	set := output.Set{Name: "roles", Columns: []output.Column{
		{Name: "name", Type: "nvarchar(128)"}, {Name: "scope", Type: "varchar(8)"},
		{Name: "fixed", Type: "bit"}, {Name: "owner", Type: "nvarchar(128)", Nullable: true},
		{Name: "member_count", Type: "int"}, {Name: "members", Type: "nvarchar(max)", Nullable: true},
	}}
	for _, r := range rows {
		set.Rows = append(set.Rows, []*string{
			ptr(r.name), ptr(scope), bitCell(r.fixed), textOrNull(r.owner),
			ptr(strconv.Itoa(len(r.members))), listCell(r.members),
		})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// --- role-members -----------------------------------------------------------

func (d Deps) securityRoleMembers(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	scope := inv.Values[optScope].(string)
	role := inv.Args[0]
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var (
		name    string
		fixed   bool
		members []*gosmo.RoleMember
	)
	if scope == scopeServer {
		r, err := cat.ServerRoleByName(ctx, role)
		if errors.Is(err, gosmo.ErrNotFound) {
			return res, roleNotFound(ctx, cat, res, role, scope)
		}
		if err != nil {
			return res, readError(err)
		}
		name, fixed = r.Name, r.IsFixedRole
		if members, err = cat.ServerRoleMembers(ctx, r.Name); err != nil {
			return res, readError(err)
		}
	} else {
		dbase, err := currentDatabase(ctx, cat, res)
		if err != nil {
			return res, err
		}
		r, err := cat.DatabaseRoleByName(ctx, dbase, role)
		if errors.Is(err, gosmo.ErrNotFound) {
			return res, roleNotFound(ctx, cat, res, role, scope)
		}
		if err != nil {
			return res, readError(err)
		}
		name, fixed = r.Name, r.IsFixedRole
		if members, err = cat.DatabaseRoleMembers(ctx, dbase, r.Name); err != nil {
			return res, readError(err)
		}
	}

	res.Fields = []output.Field{
		{Key: "role", Value: name}, {Key: "scope", Value: scope}, {Key: "fixedRole", Value: boolText(fixed)},
	}
	set := output.Set{Name: "members", Columns: []output.Column{
		{Name: "name", Type: "nvarchar(128)"}, {Name: "type", Type: "nvarchar(60)"},
	}}
	for _, m := range members {
		set.Rows = append(set.Rows, []*string{ptr(m.Name), ptr(string(m.Type))})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// roleNotFound is NOT_FOUND for a role missing from scope, hinting --scope
// when the other scope has a role of that name. A failed hint read leaves
// the hint generic rather than replacing the answer.
func roleNotFound(ctx context.Context, cat Catalog, res *output.Result, role, scope string) *output.Error {
	if scope == scopeServer {
		hint := "Check the name: security roles list --scope server lists the server roles. " +
			"A login without VIEW ANY DEFINITION sees only the fixed roles and its own."
		if dbase, err := currentDatabase(ctx, cat, res); err == nil {
			if _, err := cat.DatabaseRoleByName(ctx, dbase, role); err == nil {
				hint = fmt.Sprintf("%s is a role of database %s: drop --scope server.", gosmo.QuoteName(role), gosmo.QuoteName(dbase.Name))
			}
		}
		return output.NewError(output.CodeNotFound, fmt.Sprintf("no server role %s", gosmo.QuoteName(role)), hint)
	}
	hint := "Check the name, or pass -d for another database: security roles list lists the roles. " +
		"A login without VIEW DEFINITION on the database sees only the fixed roles and its own."
	if _, err := cat.ServerRoleByName(ctx, role); err == nil {
		hint = fmt.Sprintf("%s is a server role: add --scope server.", gosmo.QuoteName(role))
	}
	return output.NewError(output.CodeNotFound,
		fmt.Sprintf("no role %s in database %s", gosmo.QuoteName(role), gosmo.QuoteName(res.Database)), hint)
}

// --- permissions ------------------------------------------------------------

// principal is the one security permissions resolved: its catalog spelling,
// its type_desc, and whether it is a role (which cannot be impersonated).
type principal struct {
	name string
	typ  gosmo.PrincipalType
	role bool
}

func (d Deps) securityPermissions(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	scope := inv.Values[optScope].(string)
	effective, _ := inv.Values[optEffective].(bool)
	asked := inv.Args[0]
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var dbase *gosmo.Database
	if scope == scopeDatabase {
		if dbase, err = currentDatabase(ctx, cat, res); err != nil {
			return res, err
		}
	}
	p, err := resolvePrincipal(ctx, cat, res, dbase, asked)
	if err != nil {
		return res, err
	}
	memberOf, err := principalRoles(ctx, cat, dbase, p.name)
	if err != nil {
		return res, readError(err)
	}
	mode := "explicit"
	if effective {
		mode = "effective"
	}
	res.Fields = []output.Field{
		{Key: "principal", Value: p.name}, {Key: "principalType", Value: string(p.typ)},
		{Key: "scope", Value: scope}, {Key: "mode", Value: mode},
	}
	if len(memberOf) == 0 {
		res.Fields = append(res.Fields, output.Field{Key: "memberOf", Null: true})
	} else {
		res.Fields = append(res.Fields, output.Field{Key: "memberOf", Value: strings.Join(memberOf, ", ")})
	}

	if effective {
		set, err := effectivePermissionsSet(ctx, cat, dbase, p)
		if err != nil {
			return res, err
		}
		res.Sets = []output.Set{set}
		return res, nil
	}
	set, err := explicitPermissionsSet(ctx, cat, dbase, p)
	if err != nil {
		return res, readError(err)
	}
	res.Sets = []output.Set{set}
	return res, nil
}

// resolvePrincipal finds name in dbase (a user, then a database role) or,
// with dbase nil, on the server (a login, then a server role). Nothing found
// is NOT_FOUND, hinting the other scope or the user a login maps to.
func resolvePrincipal(ctx context.Context, cat Catalog, res *output.Result, dbase *gosmo.Database, name string) (principal, error) {
	if dbase == nil {
		l, err := cat.LoginByName(ctx, name)
		if err == nil {
			return principal{name: l.Name, typ: l.LoginType}, nil
		}
		if !errors.Is(err, gosmo.ErrNotFound) {
			return principal{}, readError(err)
		}
		r, err := cat.ServerRoleByName(ctx, name)
		if err == nil {
			return principal{name: r.Name, typ: gosmo.PrincipalServerRole, role: true}, nil
		}
		if !errors.Is(err, gosmo.ErrNotFound) {
			return principal{}, readError(err)
		}
		hint := "Check the name: security logins list and security roles list --scope server list them. " +
			"A login without VIEW ANY DEFINITION sees only itself and the fixed roles."
		if dbase, err := currentDatabase(ctx, cat, res); err == nil {
			if _, err := cat.UserByName(ctx, dbase, name); err == nil {
				hint = fmt.Sprintf("%s is a user of database %s: drop --scope server.", gosmo.QuoteName(name), gosmo.QuoteName(dbase.Name))
			} else if _, err := cat.DatabaseRoleByName(ctx, dbase, name); err == nil {
				hint = fmt.Sprintf("%s is a role of database %s: drop --scope server.", gosmo.QuoteName(name), gosmo.QuoteName(dbase.Name))
			}
		}
		return principal{}, output.NewError(output.CodeNotFound, fmt.Sprintf("no login or server role %s", gosmo.QuoteName(name)), hint)
	}

	u, err := cat.UserByName(ctx, dbase, name)
	if err == nil {
		return principal{name: u.Name, typ: u.UserType}, nil
	}
	if !errors.Is(err, gosmo.ErrNotFound) {
		return principal{}, readError(err)
	}
	r, err := cat.DatabaseRoleByName(ctx, dbase, name)
	if err == nil {
		return principal{name: r.Name, typ: gosmo.PrincipalDatabaseRole, role: true}, nil
	}
	if !errors.Is(err, gosmo.ErrNotFound) {
		return principal{}, readError(err)
	}
	return principal{}, principalNotFound(ctx, cat, dbase, name)
}

// principalNotFound is NOT_FOUND for name in dbase. A login of that name gets
// a hint naming the user it maps to here, or --scope server.
func principalNotFound(ctx context.Context, cat Catalog, dbase *gosmo.Database, name string) *output.Error {
	msg := fmt.Sprintf("no user or role %s in database %s", gosmo.QuoteName(name), gosmo.QuoteName(dbase.Name))
	hint := "Check the name, or pass -d for another database: security users list and security roles list list them. " +
		"A login without VIEW DEFINITION on the database sees only its own user and the fixed roles."
	if _, err := cat.LoginByName(ctx, name); err == nil {
		hint = fmt.Sprintf("%s is a login: add --scope server for its server permissions.", gosmo.QuoteName(name))
		if users, err := cat.Users(ctx, dbase); err == nil {
			for _, u := range users {
				if u.LoginName != "" && strings.EqualFold(u.LoginName, name) {
					hint = fmt.Sprintf("Login %s is user %s in this database: ask for %s, or add --scope server for the login's server permissions.",
						gosmo.QuoteName(name), gosmo.QuoteName(u.Name), u.Name)
					break
				}
			}
		}
	} else if _, err := cat.ServerRoleByName(ctx, name); err == nil {
		hint = fmt.Sprintf("%s is a server role: add --scope server.", gosmo.QuoteName(name))
	}
	return output.NewError(output.CodeNotFound, msg, hint)
}

// principalRoles is the roles name is a direct member of: dbase's roles, or
// the server roles with dbase nil.
func principalRoles(ctx context.Context, cat Catalog, dbase *gosmo.Database, name string) ([]string, error) {
	var out []string
	if dbase == nil {
		roles, err := cat.ServerRoles(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range roles {
			if slices.Contains(r.Members, name) {
				out = append(out, r.Name)
			}
		}
	} else {
		roles, err := cat.DatabaseRoles(ctx, dbase)
		if err != nil {
			return nil, err
		}
		for _, r := range roles {
			if slices.Contains(r.Members, name) {
				out = append(out, r.Name)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// explicitPermissionsSet is the explicit form's permissions set: p's entries
// in dbase (securables, then columns), or its server entries with dbase nil.
func explicitPermissionsSet(ctx context.Context, cat Catalog, dbase *gosmo.Database, p principal) (output.Set, error) {
	set := output.Set{Name: "permissions", Columns: []output.Column{
		{Name: "class", Type: "nvarchar(60)"},
		{Name: "schema", Type: "nvarchar(128)", Nullable: true}, {Name: "name", Type: "nvarchar(128)", Nullable: true},
		{Name: "column", Type: "nvarchar(128)", Nullable: true},
		{Name: "permission", Type: "nvarchar(128)"}, {Name: "state", Type: "nvarchar(60)"},
	}}
	if dbase == nil {
		entries, err := cat.ServerPermissions(ctx)
		if err != nil {
			return set, err
		}
		for _, e := range entries {
			if e.Principal == p.name {
				set.Rows = append(set.Rows, []*string{ptr(string(gosmo.SecurableServer)), nil, nil, nil,
					ptr(string(e.Permission)), ptr(string(e.State))})
			}
		}
	} else {
		entries, err := cat.PermissionsForPrincipal(ctx, dbase, p.name)
		if err != nil {
			return set, err
		}
		for _, e := range entries {
			set.Rows = append(set.Rows, []*string{ptr(string(e.SecurableType)), textOrNull(e.Schema), textOrNull(e.Name), nil,
				ptr(e.Permission), ptr(string(e.State))})
		}
		cols, err := cat.ColumnPermissionsForPrincipal(ctx, dbase, p.name)
		if err != nil {
			return set, err
		}
		for _, e := range cols {
			set.Rows = append(set.Rows, []*string{ptr(string(e.ObjectType)), textOrNull(e.Schema), textOrNull(e.Object),
				textOrNull(e.Column), ptr(string(e.Permission)), ptr(string(e.State))})
		}
	}
	set.TotalRowCount = len(set.Rows)
	return set, nil
}

// effectivePermissionsSet is --effective's permissions set: what p can do on
// dbase, or on the server with dbase nil.
func effectivePermissionsSet(ctx context.Context, cat Catalog, dbase *gosmo.Database, p principal) (output.Set, error) {
	set := output.Set{Name: "permissions", Columns: []output.Column{
		{Name: "entity", Type: "nvarchar(128)"}, {Name: "permission", Type: "nvarchar(128)"},
	}}
	if p.role {
		return set, usageError(fmt.Sprintf("--effective: %s is a role, and SQL Server cannot impersonate a role", gosmo.QuoteName(p.name)),
			"Drop --effective for the role's explicit entries, or ask --effective for a user or login that is a member.")
	}
	var (
		perms []*gosmo.EffectivePermission
		err   error
	)
	if dbase == nil {
		perms, err = cat.EffectiveServerPermissions(ctx, p.name)
	} else {
		perms, err = cat.EffectivePermissions(ctx, dbase, p.name)
	}
	if err != nil {
		return set, impersonationError(err, dbase == nil)
	}
	for _, e := range perms {
		set.Rows = append(set.Rows, []*string{ptr(e.Entity), ptr(e.Permission)})
	}
	set.TotalRowCount = len(set.Rows)
	return set, nil
}

// impersonationError maps an --effective read's failure. The principal was
// found first, so the server's "does not exist, cannot be impersonated, or
// you do not have permission" (15517 for a user, 15406 for a login) means
// the last: the caller may not impersonate it.
func impersonationError(err error, server bool) *output.Error {
	if se, ok := gosmo.AsSQLError(err); ok && (se.Number == 15517 || se.Number == 15406) {
		hint := "--effective impersonates the user: the login needs IMPERSONATE on it, or db_owner. Without it, drop --effective for the explicit entries."
		if server {
			hint = "--effective impersonates the login: the caller needs IMPERSONATE on it, or CONTROL SERVER. Without it, drop --effective for the explicit entries."
		}
		e := sqlError(se)
		e.Code, e.Hint = output.CodePermissionDenied, hint
		return e
	}
	return readError(err)
}
