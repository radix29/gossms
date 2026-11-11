package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_schema_dialog.go is the New Schema dialog (a database's Security >
// Schemas folder), built on newObjectDialog. Three pages, as SSMS has them:
// General (the name and the AUTHORIZATION owner), Permissions (schema-scoped
// GRANT/DENY, Schema Properties' matrix with no existing entries) and Extended
// Properties. The last two run after the CREATE, against the typed name.

// nschemaDefaultOwner is the Owner picker's "leave AUTHORIZATION off" entry;
// the server then makes the creating user the owner.
const nschemaDefaultOwner = "(Default)"

// nschemaUnownable are the principals CREATE SCHEMA ... AUTHORIZATION refuses
// whoever runs it (sa included, major 17): sys and INFORMATION_SCHEMA with Msg
// 15151, public with a syntax error. guest and the fixed roles are accepted.
var nschemaUnownable = []string{"sys", "INFORMATION_SCHEMA", "public"}

// nschemaPrefetch is everything the dialog reads before it opens.
type nschemaPrefetch struct {
	existingNames *nameSet
	// owners are the users, roles and application roles AUTHORIZATION may name.
	owners []string
	// principals are what the Permissions page may grant to: every user and role.
	principals []permPrincipal
	// collation is the database's, for the Extended Properties page's names.
	collation string
}

func fetchNewSchemaPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nschemaPrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	schemas, err := d.Schemas(ctx)
	if err != nil {
		return nil, err
	}
	pf := &nschemaPrefetch{existingNames: newNameSet(databaseCollation(d)), collation: databaseCollation(d)}
	for _, s := range schemas {
		pf.existingNames.Add(s.Name)
	}
	principals, err := loadDatabasePrincipals(ctx, d)
	if err != nil {
		return nil, err
	}
	pf.principals = principals.grantees()
	pf.owners = principals.owners(newNameSet(databaseCollation(d), nschemaUnownable...))
	return pf, nil
}

// NewSchemaDialog is the New Schema dialog.
type NewSchemaDialog struct {
	dbFolderDialog[nschemaPrefetch]
}

// NewNewSchemaDialog creates the dialog and wires its callbacks.
func NewNewSchemaDialog(app *App) *NewSchemaDialog {
	d := &NewSchemaDialog{}
	d.initInFolder(app, newObjectConfig[nschemaPrefetch]{
		title: "New Schema",
		noun:  "Schema",
		pages: []string{"General", "Permissions", "Extended Properties"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nschemaPrefetch, error) {
			return fetchNewSchemaPrefetch(ctx, sc, d.dbName)
		},
		build: d.buildPages,
	})
	return d
}

func (d *NewSchemaDialog) buildPages(pf *nschemaPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Schema name", "", 30)
	ownerField := propsheet.Select("Schema owner", append([]string{nschemaDefaultOwner}, pf.owners...), 0)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Schema"),
		nameField,
		propsheet.Static("Database", dbName),
		ownerField,
		propsheet.Note("Left at "+nschemaDefaultOwner+", the schema is owned by the user creating it. "+
			"Naming another user takes IMPERSONATE on that user; naming a role, ALTER on the role."),
	)

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("schema name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a schema named %q already exists in %s", name, dbName)
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		req := gosmo.CreateSchemaRequest{Name: d.objectName()}
		if o := ownerField.Value(); o != nschemaDefaultOwner {
			req.Owner = o
		}
		// DatabaseRef, not DatabaseByName: the CREATE addresses the database by name,
		// and the by-name read would not work under Script Changes.
		_, err := sc.Server.DatabaseRef(dbName).CreateSchema(ctx, req)
		return err
	}

	// The securable and the level are read when apply runs: the name is only
	// known once typed. Permissions go through DatabaseRef for the same reason
	// as the CREATE; the GRANTs need nothing but the database's name.
	var noEntries []permEntry
	d.forms[1], d.applyFns[1] = buildPermissionsMatrix(pf.principals, gosmo.SecurableSchema.PermissionNames(), noEntries,
		func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, permission, principal string) error {
			sec := gosmo.Securable{Class: gosmo.SecurableSchema, Name: d.objectName()}
			return securablePermApply(sc.Server.DatabaseRef(dbName), sec)(ctx, verb, opts, permission, principal)
		})
	d.forms[2], d.applyFns[2] = buildExtendedPropertiesForm(sc, dbName, pf.collation, func() gosmo.ExtendedPropertyLevel {
		return gosmo.ExtendedPropertyLevel{Level0Type: "SCHEMA", Level0Name: d.objectName()}
	}, nil)
}

// showNewSchemaDialog is the Schemas folder's entry point.
func (a *App) showNewSchemaDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newSchemaDialog.show(sc, node)
}
