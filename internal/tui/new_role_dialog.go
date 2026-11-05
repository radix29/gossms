package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_role_dialog.go is the New Database Role dialog (a database's Security >
// Roles folder), built on newObjectDialog. Five pages, applied in order: General
// (the name and the AUTHORIZATION owner) creates the role, then Owned Schemas,
// Members, Securables and Extended Properties target it by the typed name —
// Database Role Properties' pages, less the ones that only describe an existing
// role.

// nroleDefaultOwner is the Owner picker's "leave AUTHORIZATION off" entry; the
// server then makes the creating user the owner (dbo for a db_owner member).
const nroleDefaultOwner = "(Default)"

// nroleSpecial are the principals SQL Server refuses both as a role's
// AUTHORIZATION and as a member, Msg 15405 "Cannot use the special principal"
// (sa, major 17, 2026-11-03). guest is accepted as either.
var nroleSpecial = []string{"sys", "INFORMATION_SCHEMA", "public"}

// roleMemberEligible reports whether ADD MEMBER accepts the principal name: not
// one of nroleSpecial, not dbo and not a fixed role — all three are Msg 15405.
// special is newNameSet(collation, nroleSpecial...). New Database Role and Role
// Properties > Members both filter their pickers through it.
func roleMemberEligible(special *nameSet, name string, isFixedRole bool) bool {
	return !special.Has(name) && name != "dbo" && !isFixedRole
}

// nrolePrefetch is everything the dialog reads before it opens.
type nrolePrefetch struct {
	// existingNames holds every database principal's name — users, database
	// roles and application roles share one namespace (Msg 15023).
	existingNames *nameSet
	// owners are the users and roles AUTHORIZATION may name.
	owners []string
	// members are the principals ADD MEMBER accepts: users other than dbo, and
	// user-defined roles — dbo and every fixed role are refused Msg 15405.
	members       []string
	principalType map[string]gosmo.PrincipalType
	schemas       []*gosmo.Schema
	// collation is the database's, for name comparisons on every page.
	collation string
	// database is read by the Securables page (its Add search, a table's
	// columns); securables seeds that search before anything is typed.
	database   *gosmo.Database
	securables []securable
}

func fetchNewRolePrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nrolePrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	users, err := d.Users(ctx)
	if err != nil {
		return nil, err
	}
	roles, err := d.Roles(ctx)
	if err != nil {
		return nil, err
	}
	appRoles, err := d.ApplicationRoles(ctx)
	if err != nil {
		return nil, err
	}
	schemas, err := d.Schemas(ctx)
	if err != nil {
		return nil, err
	}
	find := securableFinder(d)
	securables, err := find(ctx, "")
	if err != nil {
		return nil, err
	}
	pf := newRolePrefetchFrom(databaseCollation(d), users, roles, appRoles, schemas)
	pf.database, pf.securables = d, securables
	return pf, nil
}

// newRolePrefetchFrom sorts the database's principals into the pickers.
// Application roles only take a name: no picker offers them.
func newRolePrefetchFrom(collation string, users []*gosmo.User, roles []*gosmo.DatabaseRole,
	appRoles []*gosmo.ApplicationRole, schemas []*gosmo.Schema) *nrolePrefetch {
	pf := &nrolePrefetch{existingNames: newNameSet(collation), collation: collation,
		schemas: schemas, principalType: map[string]gosmo.PrincipalType{}}
	special := newNameSet(collation, nroleSpecial...)
	for _, n := range principalNames(users, roles) {
		pf.existingNames.Add(n)
		if !special.Has(n) {
			pf.owners = append(pf.owners, n)
		}
	}
	for _, r := range appRoles {
		pf.existingNames.Add(r.Name)
	}
	for _, u := range users {
		if roleMemberEligible(special, u.Name, false) {
			pf.members = append(pf.members, u.Name)
			pf.principalType[u.Name] = u.UserType
		}
	}
	for _, r := range roles {
		if roleMemberEligible(special, r.Name, r.IsFixedRole) {
			pf.members = append(pf.members, r.Name)
			pf.principalType[r.Name] = gosmo.PrincipalDatabaseRole
		}
	}
	return pf
}

// NewRoleDialog is the New Database Role dialog.
type NewRoleDialog struct {
	dbFolderDialog[nrolePrefetch]
}

// NewNewRoleDialog creates the dialog and wires its callbacks.
func NewNewRoleDialog(app *App) *NewRoleDialog {
	d := &NewRoleDialog{}
	d.initInFolder(app, newObjectConfig[nrolePrefetch]{
		title: "New Database Role",
		noun:  "Database role",
		pages: []string{"General", "Owned Schemas", "Members", "Securables", "Extended Properties"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nrolePrefetch, error) {
			return fetchNewRolePrefetch(ctx, sc, d.dbName)
		},
		build: d.buildPages,
	})
	return d
}

func (d *NewRoleDialog) buildPages(pf *nrolePrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Role name", "", 30)
	ownerField := propsheet.Select("Owner", append([]string{nroleDefaultOwner}, pf.owners...), 0)
	// Principal names run long (##MS_PerformanceDefinitionReader##, DOMAIN\group):
	// the control widens to the widest rather than cutting them alike.
	ownerField.SetFitItems(true)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Database role"),
		nameField,
		propsheet.Static("Database", dbName),
		ownerField,
		propsheet.Note("Left at "+nroleDefaultOwner+", the role is owned by the user creating it. "+
			"Naming another principal as owner takes more than the right to create a role: "+
			"db_securityadmin or CONTROL on the database."),
	)

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("role name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a user or role named %q already exists in %s", name, dbName)
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		req := gosmo.CreateRoleRequest{Name: d.objectName()}
		if o := ownerField.Value(); o != nroleDefaultOwner {
			req.Owner = o
		}
		// DatabaseRef, not DatabaseByName: the CREATE addresses the database by name,
		// and the by-name read would not work under Script Changes.
		_, err := sc.Server.DatabaseRef(dbName).CreateRole(ctx, req)
		return err
	}

	d.forms[1], d.applyFns[1] = buildNewPrincipalOwnedSchemasPage(pf.schemas, "role", d.objectName)

	// The role is addressed by name when apply runs: the name is only known once
	// typed. RoleRef through DatabaseRef keeps it scriptable, as the CREATE is.
	d.forms[2], d.applyFns[2] = buildMembershipForm(membershipConfig{
		collation:     pf.collation,
		candidates:    pf.members,
		principalType: pf.principalType,
		note: "Add a user or another user-defined role from the dropdown. " +
			"dbo and the fixed roles cannot be members of a role.",
		add: func(ctx context.Context, name string) error {
			return sc.Server.DatabaseRef(dbName).RoleRef(d.objectName()).AddMember(ctx, name)
		},
		remove: func(ctx context.Context, name string) error {
			// Nothing is a member before the CREATE, so only a pending add can be removed,
			// and that never reaches apply.
			return nil
		},
	})

	// Role Properties' Securables page, starting from no grants: the role does
	// not exist yet. Reads (the Add search, a table's columns) go through the
	// prefetched database; the GRANTs address it by DatabaseRef, as the CREATE
	// does, and the role by its typed name.
	d.forms[3], d.applyFns[3] = buildSecurablesMatrix(d, pf.database, nil, nil, nil, pf.securables,
		securableFinder(pf.database), 8, 12,
		func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission string) error {
			return applySecurable(ctx, sc.Server.DatabaseRef(dbName), verb, opts, s, permission, d.objectName())
		},
		func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission, column string) error {
			return applyColumnSecurable(ctx, sc.Server.DatabaseRef(dbName), verb, opts, s, permission, column, d.objectName())
		},
	)

	// A database role is classed as USER in sp_addextendedproperty's level names,
	// as on Database Role Properties.
	d.forms[4], d.applyFns[4] = buildExtendedPropertiesForm(sc, dbName, pf.collation, func() gosmo.ExtendedPropertyLevel {
		return gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: d.objectName()}
	}, nil)
}

// showNewRoleDialog is the Roles folder's entry point.
func (a *App) showNewRoleDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newRoleDialog.show(sc, node)
}
