package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// security grant/deny/revoke and security role add-member/remove-member
// (phase 8, D6): all admin, as query run classes GRANT, DENY, REVOKE and
// ALTER [SERVER] ROLE (D1). None takes --confirm (D3): each is undone by its
// opposite, and --dry-run is the safeguard.

// The security writes' own options.
const (
	optOn              = "on"
	optTo              = "to"
	optFrom            = "from"
	optWithGrantOption = "with-grant-option"
	optCascade         = "cascade"
	optGrantOptionOnly = "grant-option-only"
)

// SQL Server's refusals of a GRANT/DENY/REVOKE that the commands explain.
const (
	msgGrantableNeedsCascade = 4611 // To revoke or deny grantable privileges, specify the CASCADE option.
	msgGrantorLacksGrant     = 4613 // Grantor does not have GRANT permission.
)

// The --on prefixes: what is left of "::", or a bare SERVER or DATABASE.
const (
	onServer      = "SERVER"
	onDatabase    = "DATABASE"
	onSchema      = "SCHEMA"
	onObject      = "OBJECT"
	onType        = "TYPE"
	onCertificate = "CERTIFICATE"
)

// objectClasses is the securable class of each sys.objects type a
// permission can be granted on — gosmo's own mapping (security.go's
// objectSecurableClasses), which its explicit-permission reads report.
var objectClasses = map[string]gosmo.SecurableClass{
	"U": gosmo.SecurableTable, "V": gosmo.SecurableView,
	"P": gosmo.SecurableProcedure, "PC": gosmo.SecurableProcedure,
	"FN": gosmo.SecurableScalarFunction, "FS": gosmo.SecurableScalarFunction,
	"IF": gosmo.SecurableInlineFunction,
	"TF": gosmo.SecurableTableFunction, "FT": gosmo.SecurableTableFunction,
	"SO": gosmo.SecurableSequence, "SN": gosmo.SecurableSynonym,
}

const onHelp = "What the permission is on: SERVER, DATABASE, SCHEMA::<schema>, OBJECT::<schema.name> " +
	"(or just <schema.name>), TYPE::<schema.name>, CERTIFICATE::<name>"

func securityWriteCommands(d Deps) []*spec.Command {
	permArg := spec.Arg{Name: "permission", Required: true, Repeat: true,
		Help: "Permission name, such as SELECT or VIEW DEFINITION; several separated by commas"}
	common := func(principalOpt, principalHelp string) []spec.Option {
		return slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
			{Name: optOn, Type: spec.TypeString, Help: onHelp},
			{Name: principalOpt, Type: spec.TypeString, Help: principalHelp},
			{Name: optColumns, Type: spec.TypeList, Help: "Only these columns of a table, view or table-valued function"},
		})
	}
	const principalHelp = "the user, database role or application role; with --on SERVER, the login or server role"
	const shared = "--on names the securable: SERVER (a server permission, run in master), DATABASE (the connection's " +
		"database; DATABASE::<name> must be it), SCHEMA::<schema>, OBJECT::<schema.name> or a bare <schema.name> — a " +
		"table, view, procedure, function, sequence or synonym, a one-part name looked up across schemas —, " +
		"TYPE::<schema.name> (a user-defined data, table or CLR type) and CERTIFICATE::<name>; a bare SERVER or " +
		"DATABASE is the class, so an object of that name is OBJECT::<name>. The securable and the principal are " +
		"read first: either missing is exit 8 (a login named in database scope: the hint names its user here). A " +
		"permission the securable's class does not take — EXECUTE on a table, SELECT on a procedure — is exit 2 " +
		"before anything is sent, the hint listing the ones it does. Several permissions, comma-separated, are one " +
		"statement each, in the order given. --columns narrows the permission to columns of a table, view or " +
		"table-valued function (each table or view column read first). The set permissions is the principal's " +
		"explicit entries on the securable (its columns included): after the change, or with --dry-run as they " +
		"stand. A change the set does not show is a warning: SQL Server ignores, with an informational message " +
		"only, a statement naming sa, dbo, the securable's owner or the caller itself. Needs --allow admin, and on the server CONTROL on the securable, the permission itself WITH GRANT " +
		"OPTION, or db_securityadmin in the database (CONTROL SERVER or securityadmin for a server permission); " +
		"without: exit 9."
	return []*spec.Command{
		{
			Path:    []string{"security", "grant"},
			Summary: "Grant a permission on a securable to a user, role or login",
			Description: "GRANT <permission> ON <securable> TO <principal>, through gosmo. " + shared +
				" --with-grant-option lets the principal grant it on to others.",
			Args: []spec.Arg{permArg},
			Options: append(common(optTo, "Grantee: "+principalHelp),
				spec.Option{Name: optWithGrantOption, Type: spec.TypeBool, Help: "WITH GRANT OPTION: the grantee may grant it on"}),
			Class: spec.ClassAdmin,
			Run:   d.securityPermissionWrite(gosmo.VerbGrant),
			Examples: []string{
				"gossms security grant SELECT --on SCHEMA::Sales --to app_reader -p dev --allow admin --dry-run",
				"gossms security grant SELECT,INSERT --on Sales.Customer --to app_writer -p dev -d AdventureWorks --allow admin",
				"gossms security grant VIEW SERVER STATE --on SERVER --to etl_login -p dev --allow admin",
			},
		},
		{
			Path:    []string{"security", "deny"},
			Summary: "Deny a permission on a securable to a user, role or login",
			Description: "DENY <permission> ON <securable> TO <principal>, through gosmo: a DENY wins over any GRANT, " +
				"including one held through a role. " + shared + " A permission the principal holds WITH GRANT OPTION " +
				"needs --cascade (SQL Server's Msg 4611 otherwise, exit 5), which denies it to everyone the principal " +
				"granted it to as well.",
			Args: []spec.Arg{permArg},
			Options: append(common(optTo, "Principal denied: "+principalHelp),
				spec.Option{Name: optCascade, Type: spec.TypeBool, Help: "CASCADE: also deny it to everyone the principal granted it to"}),
			Class: spec.ClassAdmin,
			Run:   d.securityPermissionWrite(gosmo.VerbDeny),
			Examples: []string{
				"gossms security deny DELETE --on dbo.Orders --to app_writer -p dev --allow admin --dry-run",
				"gossms security deny SELECT --on dbo.Customers --columns Email,Phone --to app_reader -p dev --allow admin",
			},
		},
		{
			Path:    []string{"security", "revoke"},
			Summary: "Revoke a granted or denied permission from a user, role or login",
			Description: "REVOKE <permission> ON <securable> FROM <principal>, through gosmo: removes a GRANT or a DENY " +
				"alike, leaving whatever the principal holds through its roles. " + shared + " A permission granted " +
				"WITH GRANT OPTION needs --cascade (Msg 4611 otherwise, exit 5), which revokes it from everyone the " +
				"principal granted it to as well. --grant-option-only (REVOKE GRANT OPTION FOR, always CASCADE) " +
				"keeps the permission and takes away only the right to grant it on.",
			Args: []spec.Arg{permArg},
			Options: append(common(optFrom, "Principal revoked from: "+principalHelp),
				spec.Option{Name: optCascade, Type: spec.TypeBool, Help: "CASCADE: also revoke it from everyone the principal granted it to"},
				spec.Option{Name: optGrantOptionOnly, Type: spec.TypeBool,
					Help: "REVOKE GRANT OPTION FOR: keep the permission, take away the right to grant it on"}),
			Class: spec.ClassAdmin,
			Run:   d.securityPermissionWrite(gosmo.VerbRevoke),
			Examples: []string{
				"gossms security revoke SELECT --on SCHEMA::Sales --from app_reader -p dev --allow admin --dry-run",
				"gossms security revoke VIEW SERVER STATE --on SERVER --from etl_login -p dev --allow admin",
			},
		},
		{
			Path:    []string{"security", "role"},
			Summary: "Add a member to a role, or remove one",
			Class:   spec.ClassRead,
		},
		roleMemberCommand(d, true),
		roleMemberCommand(d, false),
	}
}

func roleMemberCommand(d Deps, add bool) *spec.Command {
	verb, stmt, word := "add-member", "ADD MEMBER", "Add"
	if !add {
		verb, stmt, word = "remove-member", "DROP MEMBER", "Remove"
	}
	return &spec.Command{
		Path:    []string{"security", "role", verb},
		Summary: word + " a member of a database role, or of a server role",
		Description: "ALTER ROLE … " + stmt + " (with --scope server, ALTER SERVER ROLE … " + stmt + ") through gosmo: " +
			"<role> a role of the connection's database, <member> a user or another role of it; with --scope server " +
			"a server role and a login or another server role. Both are read first: either missing is exit 8, the " +
			"hint naming the other scope when it has the name (or, for a login named in database scope, its user " +
			"here). Record: role, scope, member, memberType, wasMember — a member already in (or, removing, not in) " +
			"the role is a warning, the statement then changing nothing — and on a real run isMember, read back. " +
			"Needs --allow admin, and ALTER on the role, ALTER ANY ROLE or db_securityadmin (server: ALTER ANY SERVER " +
			"ROLE or securityadmin; a fixed server role takes members only from one of its own members or a " +
			"sysadmin); without: exit 9.",
		Args: []spec.Arg{
			{Name: "role", Help: "Role name", Required: true},
			{Name: "member", Help: "User or role; with --scope server, login or server role", Required: true},
		},
		Options: slices.Concat(ConnectOptions(), writeOptions(),
			[]spec.Option{scopeOption("database: a role of the connection database; server: a server role")}),
		Class: spec.ClassAdmin,
		Run: func(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
			return d.securityRoleMember(ctx, inv, add)
		},
		Examples: []string{
			"gossms security role " + verb + " db_datareader app_reader -p dev --allow admin --dry-run",
			"gossms security role " + verb + " sysadmin etl_login --scope server -p dev --allow admin",
		},
	}
}

// --- grant / deny / revoke --------------------------------------------------

// onSpec is --on split before connecting: its kind (an on* constant) and the
// name after "::", unparsed ("" for SERVER and a bare DATABASE).
type onSpec struct {
	kind, name string
}

// parseOn splits --on's value. A known prefix before "::" picks the kind,
// case-insensitively; a bare SERVER or DATABASE is that class; anything else
// is an object name.
func parseOn(s string) (onSpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return onSpec{}, usageError("--on: empty", onHelp+".")
	}
	if i := strings.Index(s, "::"); i > 0 {
		kind := strings.ToUpper(strings.TrimSpace(s[:i]))
		name := strings.TrimSpace(s[i+2:])
		switch kind {
		case onDatabase, onSchema, onObject, onType, onCertificate:
			if name == "" {
				return onSpec{}, usageError(fmt.Sprintf("--on %s: no name after %s::", s, kind), onHelp+".")
			}
			return onSpec{kind, name}, nil
		case onServer:
			return onSpec{}, usageError(fmt.Sprintf("--on %s: a server permission names no securable", s), "Write --on SERVER.")
		}
		if !strings.ContainsAny(s[:i], "[]\".") {
			return onSpec{}, usageError(fmt.Sprintf("--on %s: unknown securable class %s", s, kind),
				onHelp+". Logins, endpoints, assemblies, keys and other classes are out of reach here: use query run.")
		}
	}
	switch strings.ToUpper(s) {
	case onServer:
		return onSpec{kind: onServer}, nil
	case onDatabase:
		return onSpec{kind: onDatabase}, nil
	}
	return onSpec{onObject, s}, nil
}

// fixedClass is the class an --on kind has without a catalog read; "" for
// OBJECT, whose class is the object's.
func (o onSpec) fixedClass() gosmo.SecurableClass {
	switch o.kind {
	case onServer:
		return gosmo.SecurableServer
	case onDatabase:
		return gosmo.SecurableDatabase
	case onSchema:
		return gosmo.SecurableSchema
	case onType:
		return gosmo.SecurableUserType
	case onCertificate:
		return gosmo.SecurableCertificate
	}
	return ""
}

// parsePermissions is the permission argument's words as names: joined,
// split at commas, each upper-cased with its spaces collapsed, duplicates
// dropped.
func parsePermissions(words []string) ([]string, error) {
	var out []string
	for part := range strings.SplitSeq(strings.Join(words, " "), ",") {
		name := strings.ToUpper(strings.Join(strings.Fields(part), " "))
		if name == "" {
			return nil, usageError(fmt.Sprintf("permission %q: an empty name", strings.Join(words, " ")),
				"Name the permission, such as SELECT; separate several with commas: SELECT,INSERT.")
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// checkPermissions refuses a permission class does not take — on a column
// when columns — naming the ones it does.
func checkPermissions(class gosmo.SecurableClass, perms []string, columns bool) error {
	valid, what := class.PermissionNames(), "a "+strings.ToLower(string(class))
	if columns {
		valid, what = class.ColumnPermissionNames(), "a column of a "+strings.ToLower(string(class))
	}
	for _, p := range perms {
		if !slices.Contains(valid, p) {
			return usageError(fmt.Sprintf("%s cannot be granted on %s", p, what),
				fmt.Sprintf("Permissions on %s: %s.", what, strings.Join(valid, ", ")))
		}
	}
	return nil
}

// permissionName is name as the gosmo type of class's scope; ApplyPermission
// checks the name, not the type.
func permissionName(class gosmo.SecurableClass, name string) gosmo.PermissionName {
	switch class {
	case gosmo.SecurableServer:
		return gosmo.ServerPermission(name)
	case gosmo.SecurableDatabase:
		return gosmo.DatabasePermission(name)
	}
	return gosmo.ObjectPermission(name)
}

// permissionRequest is grant/deny/revoke's options, checked before
// connecting.
type permissionRequest struct {
	verb      gosmo.PermissionVerb
	perms     []string
	on        onSpec
	principal string
	columns   []string
	opts      gosmo.PermissionOptions
}

func newPermissionRequest(inv *spec.Invocation, verb gosmo.PermissionVerb) (permissionRequest, error) {
	req := permissionRequest{verb: verb}
	var err error
	if req.perms, err = parsePermissions(inv.Args); err != nil {
		return req, err
	}
	if !inv.Given[optOn] {
		return req, usageError("--on is required", onHelp+".")
	}
	if req.on, err = parseOn(inv.Values[optOn].(string)); err != nil {
		return req, err
	}
	principalOpt := optTo
	if verb == gosmo.VerbRevoke {
		principalOpt = optFrom
	}
	req.principal, _ = inv.Values[principalOpt].(string)
	if strings.TrimSpace(req.principal) == "" {
		return req, usageError("--"+principalOpt+" is required", "Name the user or role (with --on SERVER, the login or server role).")
	}
	if inv.Given[optColumns] {
		for _, c := range inv.Values[optColumns].([]string) {
			if c = strings.TrimSpace(gosmo.UnquoteName(strings.TrimSpace(c))); c == "" {
				return req, usageError("--columns: an empty column name", "")
			}
			if !slices.ContainsFunc(req.columns, func(x string) bool { return strings.EqualFold(x, c) }) {
				req.columns = append(req.columns, c)
			}
		}
		if req.on.kind != onObject {
			return req, usageError(fmt.Sprintf("--columns: a %s has no columns", strings.ToLower(req.on.kind)),
				"Columns belong to a table, view or table-valued function: --on schema.name.")
		}
	}
	req.opts.WithGrantOption, _ = inv.Values[optWithGrantOption].(bool)
	req.opts.Cascade, _ = inv.Values[optCascade].(bool)
	req.opts.GrantOptionOnly, _ = inv.Values[optGrantOptionOnly].(bool)
	if class := req.on.fixedClass(); class != "" {
		if err := checkPermissions(class, req.perms, false); err != nil {
			return req, err
		}
	}
	return req, nil
}

func (d Deps) securityPermissionWrite(verb gosmo.PermissionVerb) spec.Handler {
	return func(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
		req, err := newPermissionRequest(inv, verb)
		if err != nil {
			return nil, err
		}
		res := &output.Result{}
		sc, cat, err := d.connectGated(ctx, inv, res)
		if err != nil {
			return res, err
		}
		defer sc.Close()

		// The database a database-scope permission is in; nil for SERVER.
		var dbase *gosmo.Database
		if req.on.kind != onServer {
			if dbase, err = currentDatabase(ctx, cat, res); err != nil {
				return res, err
			}
		}
		sec, err := resolveSecurable(ctx, cat, dbase, req.on)
		if err != nil {
			return res, err
		}
		if req.columns != nil {
			if !sec.Class.HasColumns() {
				return res, usageError(fmt.Sprintf("--columns: %s is a %s, which has no column permissions",
					securableText(sec), strings.ToLower(string(sec.Class))), "Drop --columns.")
			}
			if sec.Columns, err = resolveColumns(ctx, dbase, sec, req.columns); err != nil {
				return res, err
			}
		}
		if req.on.fixedClass() == "" || req.columns != nil {
			if err := checkPermissions(sec.Class, req.perms, req.columns != nil); err != nil {
				return res, err
			}
		}
		p, err := resolvePrincipal(ctx, sc.Server, cat, res, dbase, req.principal, grantScope)
		if err != nil {
			return res, err
		}

		scope := scopeDatabase
		if dbase == nil {
			scope = scopeServer
		}
		res.Fields = permissionFields(req, sec, p, scope)
		done := 0
		write := func(ctx context.Context) error {
			for _, perm := range req.perms {
				if err := applyPermission(ctx, sc.Server, dbase, verb, sec, permissionName(sec.Class, perm), p.name, req.opts); err != nil {
					return permissionWriteError(err, verb, sec.Class)
				}
				done++
			}
			return nil
		}
		werr := applyWrite(ctx, inv, res, write)
		if werr != nil && done > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s went through before the failure, and stays: "+
				"the permissions set shows what holds now.", verb, strings.Join(req.perms[:done], ", ")))
		}
		// The principal's entries on the securable: before the change in a
		// dry run, after it otherwise (whatever landed of a failed one).
		set, err := explicitPermissionsSet(ctx, sc.Server, dbase, p)
		if err != nil {
			res.Warnings = append(res.Warnings, "The principal's permissions were not read back: "+readError(err).Message)
			return res, werr
		}
		set = entriesOn(set, sec)
		res.Sets = []output.Set{set}
		if werr == nil && !dryRun(inv) {
			if missed := unlanded(set, req, sec); len(missed) > 0 {
				res.Warnings = append(res.Warnings, fmt.Sprintf("The server recorded no change for %s: SQL Server ignores, "+
					"with an informational message only, a %s to sa, dbo, the securable's owner or the caller itself.",
					strings.Join(missed, ", "), verb))
			}
		}
		return res, werr
	}
}

// permissionFields is grant/deny/revoke's record, before dryRun.
func permissionFields(req permissionRequest, sec gosmo.Securable, p principal, scope string) []output.Field {
	cols := output.Field{Key: "columns", Null: true}
	if len(sec.Columns) > 0 {
		cols = output.Field{Key: "columns", Value: strings.Join(sec.Columns, ", ")}
	}
	modifier := output.Field{Key: "modifier", Null: true}
	switch o := req.opts; {
	case o.GrantOptionOnly:
		modifier.Null, modifier.Value = false, "GRANT OPTION FOR"
	case o.WithGrantOption:
		modifier.Null, modifier.Value = false, "WITH GRANT OPTION"
	case o.Cascade:
		modifier.Null, modifier.Value = false, "CASCADE"
	}
	return []output.Field{
		{Key: "verb", Value: string(req.verb)},
		{Key: "permission", Value: strings.Join(req.perms, ", ")},
		{Key: "class", Value: string(sec.Class)},
		{Key: "schema", Value: sec.Schema, Null: sec.Schema == ""},
		{Key: "name", Value: sec.Name, Null: sec.Name == ""},
		cols,
		{Key: "principal", Value: p.name},
		{Key: "principalType", Value: string(p.typ)},
		{Key: "scope", Value: scope},
		modifier,
	}
}

// entriesOn is set (explicitPermissionsSet's) narrowed to the rows on sec:
// its class, schema and name, column rows included.
func entriesOn(set output.Set, sec gosmo.Securable) output.Set {
	cell := func(c *string) string {
		if c == nil {
			return ""
		}
		return *c
	}
	set.Rows = slices.DeleteFunc(set.Rows, func(r []*string) bool {
		return cell(r[0]) != string(sec.Class) || !strings.EqualFold(cell(r[1]), sec.Schema) || !strings.EqualFold(cell(r[2]), sec.Name)
	})
	set.TotalRowCount = len(set.Rows)
	return set
}

// unlanded is the permissions of req that set, read back after the change,
// does not show as asked: a GRANT or DENY with no entry in its state, a
// REVOKE whose entry stays (with --grant-option-only, a grant option that
// stays). On columns, every column must show it.
func unlanded(set output.Set, req permissionRequest, sec gosmo.Securable) []string {
	state := func(perm, column string) string {
		for _, r := range set.Rows {
			if *r[4] == perm && (column == "" && r[3] == nil || r[3] != nil && strings.EqualFold(*r[3], column)) {
				return *r[5]
			}
		}
		return ""
	}
	landed := func(got string) bool {
		switch {
		case req.verb == gosmo.VerbDeny:
			return got == string(gosmo.PermissionDeny)
		case req.verb == gosmo.VerbGrant && req.opts.WithGrantOption:
			return got == string(gosmo.PermissionGrantWithGrantOption)
		case req.verb == gosmo.VerbGrant:
			// A grant held WITH GRANT OPTION keeps it.
			return got == string(gosmo.PermissionGrant) || got == string(gosmo.PermissionGrantWithGrantOption)
		case req.opts.GrantOptionOnly:
			return got != string(gosmo.PermissionGrantWithGrantOption)
		}
		return got == ""
	}
	columns := sec.Columns
	if len(columns) == 0 {
		columns = []string{""}
	}
	var out []string
	for _, perm := range req.perms {
		for _, c := range columns {
			if !landed(state(perm, c)) {
				out = append(out, perm)
				break
			}
		}
	}
	return out
}

// securableText is sec as the messages name it.
func securableText(sec gosmo.Securable) string {
	switch {
	case sec.Class == gosmo.SecurableServer:
		return "the server"
	case sec.Class == gosmo.SecurableDatabase:
		return "the database"
	case sec.Schema != "":
		return gosmo.QualifiedName(sec.Schema, sec.Name)
	}
	return gosmo.QuoteName(sec.Name)
}

// resolveSecurable reads what on names in dbase (nil for SERVER), in its
// catalog spelling. cat resolves an object's name (resolveObject).
func resolveSecurable(ctx context.Context, cat Catalog, dbase *gosmo.Database, on onSpec) (gosmo.Securable, error) {
	switch on.kind {
	case onServer:
		return gosmo.Securable{Class: gosmo.SecurableServer}, nil
	case onDatabase:
		if on.name != "" {
			n, err := onePartName(on, dbase)
			if err != nil {
				return gosmo.Securable{}, err
			}
			if !strings.EqualFold(n, dbase.Name) {
				return gosmo.Securable{}, usageError(
					fmt.Sprintf("--on DATABASE::%s: the connection is in database %s", gosmo.QuoteName(n), gosmo.QuoteName(dbase.Name)),
					fmt.Sprintf("Pass -d %s, or write --on DATABASE for the connection's.", gosmo.QuoteName(n)))
			}
		}
		return gosmo.Securable{Class: gosmo.SecurableDatabase}, nil
	case onSchema:
		n, err := onePartName(on, dbase)
		if err != nil {
			return gosmo.Securable{}, err
		}
		schemas, err := dbase.Schemas(ctx)
		if err != nil {
			return gosmo.Securable{}, readError(err)
		}
		names := make([]string, len(schemas))
		for i, s := range schemas {
			names[i] = s.Name
		}
		name, ok := matchName(names, n)
		if !ok {
			return gosmo.Securable{}, output.NewError(output.CodeNotFound,
				fmt.Sprintf("no schema %s in database %s", gosmo.QuoteName(n), gosmo.QuoteName(dbase.Name)),
				"Check the name, or pass -d for another database.")
		}
		return gosmo.Securable{Class: gosmo.SecurableSchema, Name: name}, nil
	case onCertificate:
		n, err := onePartName(on, dbase)
		if err != nil {
			return gosmo.Securable{}, err
		}
		c, err := dbase.CertificateByName(ctx, n)
		if errors.Is(err, gosmo.ErrNotFound) {
			return gosmo.Securable{}, output.NewError(output.CodeNotFound,
				fmt.Sprintf("no certificate %s in database %s", gosmo.QuoteName(n), gosmo.QuoteName(dbase.Name)),
				"Check the name, or pass -d for another database. A login without VIEW DEFINITION sees no certificate.")
		}
		if err != nil {
			return gosmo.Securable{}, readError(err)
		}
		return gosmo.Securable{Class: gosmo.SecurableCertificate, Name: c.Name}, nil
	case onType:
		return resolveType(ctx, dbase, on)
	}
	n, err := parseObjectName(on.name, dbase.Name)
	if err != nil {
		return gosmo.Securable{}, err
	}
	o, err := resolveObject(ctx, cat, dbase, n)
	if err != nil {
		return gosmo.Securable{}, err
	}
	class, ok := objectClasses[o.Type]
	if !ok {
		return gosmo.Securable{}, usageError(fmt.Sprintf("%s is a %s: permissions are not granted on one",
			gosmo.QualifiedName(o.Schema, o.Name), o.TypeDesc),
			"Grant on the table, view, procedure or function it belongs to, or on its schema.")
	}
	return gosmo.Securable{Class: class, Schema: o.Schema, Name: o.Name}, nil
}

// onePartName is on's name as one unquoted part.
func onePartName(on onSpec, dbase *gosmo.Database) (string, error) {
	n, err := parseObjectName(on.name, dbase.Name)
	if err != nil {
		return "", err
	}
	if n.Schema != "" {
		return "", usageError(fmt.Sprintf("--on %s::%s: a %s name has one part", on.kind, on.name, strings.ToLower(on.kind)),
			"Bracket a name holding a dot: "+on.kind+"::[a.b].")
	}
	return n.Name, nil
}

// matchName is the one of names that is name exactly, else the only one
// equal to it ignoring case.
func matchName(names []string, name string) (string, bool) {
	if slices.Contains(names, name) {
		return name, true
	}
	var found []string
	for _, n := range names {
		if strings.EqualFold(n, name) {
			found = append(found, n)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return "", false
}

// resolveType finds TYPE::'s user-defined data, table or CLR type: a
// two-part name as given, a one-part one across schemas.
func resolveType(ctx context.Context, dbase *gosmo.Database, on onSpec) (gosmo.Securable, error) {
	n, err := parseObjectName(on.name, dbase.Name)
	if err != nil {
		return gosmo.Securable{}, err
	}
	var types []ObjectName
	data, err := dbase.UserDefinedDataTypes(ctx)
	if err != nil {
		return gosmo.Securable{}, readError(err)
	}
	for _, t := range data {
		types = append(types, ObjectName{t.Schema, t.Name})
	}
	tables, err := dbase.UserDefinedTableTypes(ctx)
	if err != nil {
		return gosmo.Securable{}, readError(err)
	}
	for _, t := range tables {
		types = append(types, ObjectName{t.Schema, t.Name})
	}
	clr, err := dbase.ClrTypes(ctx)
	if err != nil {
		return gosmo.Securable{}, readError(err)
	}
	for _, t := range clr {
		types = append(types, ObjectName{t.Schema, t.Name})
	}

	match := func(exact bool) []ObjectName {
		var out []ObjectName
		for _, t := range types {
			eq := func(a, b string) bool { return a == b || (!exact && strings.EqualFold(a, b)) }
			if eq(t.Name, n.Name) && (n.Schema == "" || eq(t.Schema, n.Schema)) {
				out = append(out, t)
			}
		}
		return out
	}
	found := match(true)
	if len(found) == 0 {
		found = match(false)
	}
	switch len(found) {
	case 0:
		return gosmo.Securable{}, output.NewError(output.CodeNotFound,
			fmt.Sprintf("no user-defined type %s in database %s", n, gosmo.QuoteName(dbase.Name)),
			"Check the name, or pass -d for another database: object list --type type lists them.")
	case 1:
		return gosmo.Securable{Class: gosmo.SecurableUserType, Schema: found[0].Schema, Name: found[0].Name}, nil
	}
	names := make([]string, len(found))
	for i, t := range found {
		names[i] = t.String()
	}
	return gosmo.Securable{}, usageError(fmt.Sprintf("type %s is in %d schemas: %s", n, len(found), strings.Join(names, ", ")),
		"Name the schema: TYPE::schema.name.")
}

// resolveColumns is asked as sec's column names, each a column of the table
// or view read in its catalog spelling. A table-valued function's columns
// are left to the server.
func resolveColumns(ctx context.Context, dbase *gosmo.Database, sec gosmo.Securable, asked []string) ([]string, error) {
	var cols []*gosmo.Column
	switch sec.Class {
	case gosmo.SecurableTable:
		t, err := dbase.TableByName(ctx, sec.Schema, sec.Name)
		if err != nil {
			return nil, readError(err)
		}
		if cols, err = t.Columns(ctx); err != nil {
			return nil, readError(err)
		}
	case gosmo.SecurableView:
		v, err := dbase.ViewByName(ctx, sec.Schema, sec.Name)
		if err != nil {
			return nil, readError(err)
		}
		if cols, err = v.Columns(ctx); err != nil {
			return nil, readError(err)
		}
	default:
		return asked, nil
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	out := make([]string, 0, len(asked))
	for _, a := range asked {
		name, ok := matchName(names, a)
		if !ok {
			return nil, output.NewError(output.CodeNotFound,
				fmt.Sprintf("no column %s in %s", gosmo.QuoteName(a), securableText(sec)),
				"Its columns: "+strings.Join(names, ", ")+".")
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// permissionWriteError is one GRANT/DENY/REVOKE's failure. The securable and
// the principal were just read, so "missing or denied" means denied.
func permissionWriteError(err error, verb gosmo.PermissionVerb, class gosmo.SecurableClass) error {
	se, isSQL := gosmo.AsSQLError(err)
	switch {
	case gosmo.IsMissingOrDenied(err) || gosmo.IsPermissionDenied(err) || (isSQL && se.Number == msgGrantorLacksGrant):
		hint := "A " + string(verb) + " needs CONTROL on the securable, the permission itself WITH GRANT OPTION, " +
			"or db_securityadmin / db_owner in the database. Use a login that has it, or have it granted."
		if class == gosmo.SecurableServer {
			hint = "A server " + string(verb) + " needs CONTROL SERVER, the permission itself WITH GRANT OPTION, " +
				"or securityadmin. Use a login that has it, or have it granted."
		}
		e := output.NewError(output.CodePermissionDenied, err.Error(), hint)
		if isSQL {
			e.SQL = sqlError(se).SQL
		}
		return e
	case isSQL && se.Number == msgGrantableNeedsCascade:
		e := sqlError(se)
		e.Hint = "The principal holds it WITH GRANT OPTION: add --cascade, which also takes it from everyone the " +
			"principal granted it to. Or revoke only the grant option first: security revoke … --grant-option-only."
		return e
	}
	return err
}

// applyPermission is one GRANT, DENY or REVOKE on sec in d, or with d nil
// on the server (SecurableServer).
func applyPermission(ctx context.Context, srv *gosmo.Server, d *gosmo.Database, verb gosmo.PermissionVerb, sec gosmo.Securable,
	perm gosmo.PermissionName, principal string, opts gosmo.PermissionOptions) error {
	if d == nil {
		return srv.ApplyPermission(ctx, verb, sec, perm, principal, opts)
	}
	return d.ApplyPermission(ctx, verb, sec, perm, principal, opts)
}

// --- role add-member / remove-member ------------------------------------------

func (d Deps) securityRoleMember(ctx context.Context, inv *spec.Invocation, add bool) (*output.Result, error) {
	scope := inv.Values[optScope].(string)
	roleName, memberName := inv.Args[0], inv.Args[1]
	res := &output.Result{}
	sc, cat, err := d.connectGated(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var (
		dbase   *gosmo.Database
		role    string
		members []string
		// alter adds or removes a member through the role just read.
		alter func(ctx context.Context, member string) error
	)
	if scope == scopeServer {
		r, err := sc.Server.ServerRoleByName(ctx, roleName)
		if errors.Is(err, gosmo.ErrNotFound) {
			return res, roleNotFound(ctx, sc.Server, cat, res, roleName, scope)
		}
		if err != nil {
			return res, readError(err)
		}
		role, members, alter = r.Name, r.Members, r.RemoveMember
		if add {
			alter = r.AddMember
		}
	} else {
		if dbase, err = currentDatabase(ctx, cat, res); err != nil {
			return res, err
		}
		r, err := dbase.RoleByName(ctx, roleName)
		if errors.Is(err, gosmo.ErrNotFound) {
			return res, roleNotFound(ctx, sc.Server, cat, res, roleName, scope)
		}
		if err != nil {
			return res, readError(err)
		}
		role, members, alter = r.Name, r.Members, r.RemoveMember
		if add {
			alter = r.AddMember
		}
	}
	p, err := resolvePrincipal(ctx, sc.Server, cat, res, dbase, memberName, roleScope)
	if err != nil {
		return res, err
	}

	was := slices.Contains(members, p.name)
	res.Fields = []output.Field{
		{Key: "role", Value: role}, {Key: "scope", Value: scope},
		{Key: "member", Value: p.name}, {Key: "memberType", Value: string(p.typ)},
		{Key: "wasMember", Value: boolText(was)},
	}
	switch {
	case add && was:
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s is already a member of %s: the statement changes nothing.",
			gosmo.QuoteName(p.name), gosmo.QuoteName(role)))
	case !add && !was:
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s is not a direct member of %s: the statement changes nothing.",
			gosmo.QuoteName(p.name), gosmo.QuoteName(role)))
	}
	write := func(ctx context.Context) error {
		err := alter(ctx, p.name)
		if gosmo.IsMissingOrDenied(err) || gosmo.IsPermissionDenied(err) {
			hint := "Changing a database role's members needs ALTER on the role, ALTER ANY ROLE, or db_securityadmin / db_owner. " +
				"Use a login that has it, or have it granted."
			if dbase == nil {
				hint = "Changing a server role's members needs ALTER ANY SERVER ROLE or securityadmin; a fixed server role " +
					"takes members only from one of its own members or a sysadmin. Use a login that has it, or have it granted."
			}
			e := output.NewError(output.CodePermissionDenied, err.Error(), hint)
			if se, ok := gosmo.AsSQLError(err); ok {
				e.SQL = sqlError(se).SQL
			}
			return e
		}
		return err
	}
	if err := applyWrite(ctx, inv, res, write); err != nil {
		return res, err
	}
	if dryRun(inv) {
		return res, nil
	}
	var now []*gosmo.RoleMember
	if dbase == nil {
		now, err = sc.Server.ServerRoleMembers(ctx, role)
	} else {
		now, err = dbase.RoleMembers(ctx, role)
	}
	if err != nil {
		res.Warnings = append(res.Warnings, "The role's members were not read back: "+readError(err).Message)
		res.Fields = append(res.Fields, output.Field{Key: "isMember", Null: true})
		return res, nil
	}
	is := slices.ContainsFunc(now, func(m *gosmo.RoleMember) bool { return m.Name == p.name })
	res.Fields = append(res.Fields, output.Field{Key: "isMember", Value: boolText(is)})
	switch {
	case add && !is:
		res.Warnings = append(res.Warnings, fmt.Sprintf("Read back, %s is not a member of %s.", gosmo.QuoteName(p.name), gosmo.QuoteName(role)))
	case !add && is:
		res.Warnings = append(res.Warnings, fmt.Sprintf("Read back, %s is still a member of %s.", gosmo.QuoteName(p.name), gosmo.QuoteName(role)))
	}
	return res, nil
}
