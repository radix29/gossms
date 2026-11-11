package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_application_role_dialog.go is the New Application Role dialog (a
// database's Security > Roles > Application Roles folder), built on
// newObjectDialog. Four pages, applied in order: General (name, default schema,
// password) creates the role, then Owned Schemas, Securables and Extended
// Properties target it by the typed name — Application Role Properties' pages.

// narDefaultSchema is the Default schema picker's "leave DEFAULT_SCHEMA off"
// entry; the server then resolves unqualified names in dbo.
const narDefaultSchema = "(Default)"

// narPrefetch is everything the dialog reads before it opens.
type narPrefetch struct {
	// existingNames holds every database principal's name — users, database
	// roles and application roles share one namespace (Msg 15023).
	existingNames *nameSet
	schemas       []*gosmo.Schema
	// collation is the database's, for name comparisons on every page.
	collation string
	// database is read by the Securables page (its Add search, a table's
	// columns); securables seeds that search before anything is typed.
	database   *gosmo.Database
	securables []securable
}

func fetchNewApplicationRolePrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*narPrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	principals, err := loadDatabasePrincipals(ctx, d)
	if err != nil {
		return nil, err
	}
	schemas, err := d.Schemas(ctx)
	if err != nil {
		return nil, err
	}
	securables, err := securableFinder(d)(ctx, "")
	if err != nil {
		return nil, err
	}
	pf := newApplicationRolePrefetchFrom(databaseCollation(d), principals.Users, principals.Roles, principals.ApplicationRoles, schemas)
	pf.database, pf.securables = d, securables
	return pf, nil
}

// newApplicationRolePrefetchFrom fills the name set from every kind of database
// principal.
func newApplicationRolePrefetchFrom(collation string, users []*gosmo.User, roles []*gosmo.DatabaseRole,
	appRoles []*gosmo.ApplicationRole, schemas []*gosmo.Schema) *narPrefetch {
	pf := &narPrefetch{existingNames: newNameSet(collation), collation: collation, schemas: schemas}
	for _, n := range principalNames(users, roles, appRoles) {
		pf.existingNames.Add(n)
	}
	return pf
}

// NewApplicationRoleDialog is the New Application Role dialog.
type NewApplicationRoleDialog struct {
	dbFolderDialog[narPrefetch]
}

// NewNewApplicationRoleDialog creates the dialog and wires its callbacks.
func NewNewApplicationRoleDialog(app *App) *NewApplicationRoleDialog {
	d := &NewApplicationRoleDialog{}
	d.initInFolder(app, newObjectConfig[narPrefetch]{
		title: "New Application Role",
		noun:  "Application role",
		pages: []string{"General", "Owned Schemas", "Securables", "Extended Properties"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*narPrefetch, error) {
			return fetchNewApplicationRolePrefetch(ctx, sc, d.dbName)
		},
		build: d.buildPages,
	})
	return d
}

func (d *NewApplicationRoleDialog) buildPages(pf *narPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Role name", "", 30)
	schemaItems := []string{narDefaultSchema}
	for _, s := range pf.schemas {
		schemaItems = append(schemaItems, s.Name)
	}
	schemaField := propsheet.Select("Default schema", schemaItems, 0)
	passwordField := propsheet.Password("Password", 20)
	confirmField := propsheet.Password("Confirm password", 20)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Application role"),
		nameField,
		propsheet.Static("Database", dbName),
		schemaField,
		propsheet.Section("Password"),
		passwordField, confirmField,
		propsheet.Note("A session takes the role on with sp_setapprole and this password. "+
			"The server applies the Windows password policy to it."),
	)

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		return validateNewApplicationRole(d.objectName(), passwordField.Value(), confirmField.Value(), dbName, pf)
	}
	d.applyFns[0] = func(ctx context.Context) error {
		req := gosmo.CreateApplicationRoleRequest{Name: d.objectName(), Password: passwordField.Value()}
		if s := schemaField.Value(); s != narDefaultSchema {
			req.DefaultSchema = s
		}
		// DatabaseRef, not DatabaseByName: the CREATE addresses the database by name,
		// and the by-name read would not work under Script Changes.
		_, err := sc.Server.DatabaseRef(dbName).CreateApplicationRole(ctx, req)
		return err
	}

	d.forms[1], d.applyFns[1] = buildNewPrincipalOwnedSchemasPage(pf.schemas, "application role", d.objectName)

	// Application Role Properties' Securables page, starting from no grants: the
	// role does not exist yet. Reads go through the prefetched database; the
	// GRANTs address it by DatabaseRef, as the CREATE does, and the role by its
	// typed name.
	d.forms[2], d.applyFns[2] = buildSecurablesMatrix(d, pf.database, nil, nil, nil, pf.securables,
		securableFinder(pf.database), 8, 12,
		func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission string) error {
			return applySecurable(ctx, sc.Server.DatabaseRef(dbName), verb, opts, s, permission, d.objectName())
		},
		func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission, column string) error {
			return applyColumnSecurable(ctx, sc.Server.DatabaseRef(dbName), verb, opts, s, permission, column, d.objectName())
		},
	)

	// Classed as USER in sp_addextendedproperty's level names, as on Application
	// Role Properties.
	d.forms[3], d.applyFns[3] = buildExtendedPropertiesForm(sc, dbName, pf.collation, func() gosmo.ExtendedPropertyLevel {
		return gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: d.objectName()}
	}, nil)
}

// validateNewApplicationRole refuses, naming the field, what the CREATE would
// be refused for: no name, a name another principal holds (Msg 15023), no
// password (CREATE APPLICATION ROLE has no form without one), or a confirmation
// that differs.
func validateNewApplicationRole(name, password, confirm, dbName string, pf *narPrefetch) error {
	switch {
	case name == "":
		return fmt.Errorf("role name is required")
	case pf.existingNames.Has(name):
		return fmt.Errorf("a user or role named %q already exists in %s", name, dbName)
	case password == "":
		return fmt.Errorf("an application role needs a password")
	case password != confirm:
		return fmt.Errorf("passwords do not match")
	}
	return nil
}

// showNewApplicationRoleDialog is the Application Roles folder's entry point.
func (a *App) showNewApplicationRoleDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newApplicationRoleDialog.show(sc, node)
}
