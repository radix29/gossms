package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_database_scoped_credential_dialog.go is the New Database Scoped
// Credential dialog (a database's Security > Database Scoped Credentials
// folder), built on newObjectDialog like every other New-X so
// OK/Cancel/Apply/Script Changes behave the same.
//
// It is the database-scope twin of new_credential_dialog.go, minus the
// encryption-provider section: there is no FOR CRYPTOGRAPHIC PROVIDER form of
// a database-scoped credential.

// ndbScopedCredPrefetch holds what the dialog needs before it opens: the
// existing credential names in this database, for the uniqueness preflight.
type ndbScopedCredPrefetch struct {
	existingNames map[string]bool
}

func fetchNewDBScopedCredPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*ndbScopedCredPrefetch, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	creds, err := dbObj.DatabaseScopedCredentialsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(creds))
	for _, c := range creds {
		existing[strings.ToLower(c.Name)] = true
	}
	return &ndbScopedCredPrefetch{existingNames: existing}, nil
}

// NewDatabaseScopedCredentialDialog is the New Database Scoped Credential
// dialog.
type NewDatabaseScopedCredentialDialog struct {
	newObjectDialog[ndbScopedCredPrefetch]

	// dbName is the database the credential is created in, and node the
	// folder to refresh afterwards. Both are set by show, before the embedded
	// dialog's own show runs the prefetch that reads them.
	dbName string
	node   *explorerNode
}

// NewNewDatabaseScopedCredentialDialog creates the dialog and wires its
// callbacks.
func NewNewDatabaseScopedCredentialDialog(app *App) *NewDatabaseScopedCredentialDialog {
	d := &NewDatabaseScopedCredentialDialog{}
	d.init(app, newObjectConfig[ndbScopedCredPrefetch]{
		title: "New Database Scoped Credential",
		noun:  "Database Scoped Credential",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*ndbScopedCredPrefetch, error) {
			return fetchNewDBScopedCredPrefetch(ctx, sc, d.dbName)
		},
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	return d
}

// show opens the dialog for one database's Database Scoped Credentials folder.
func (d *NewDatabaseScopedCredentialDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	// Script Changes opens its query window in the database the statement runs
	// in, not the connection's default.
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *NewDatabaseScopedCredentialDialog) buildPages(pf *ndbScopedCredPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Credential name", "", 30)
	identityField := propsheet.Text("Identity", "", 40)
	passwordField := propsheet.Password("Password", 20)
	confirmField := propsheet.Password("Confirm password", 20)
	passwordField.SetValidate(func(v string) error {
		if v != confirmField.Value() {
			return fmt.Errorf("passwords do not match")
		}
		return nil
	})

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Credential identity"),
		nameField,
		propsheet.Static("Database", dbName),
		identityField,
		propsheet.Section("Secret"),
		passwordField, confirmField,
		propsheet.Note("The secret is optional — an identity that needs no password, such as a managed identity, can be left blank. It can never be read back afterwards."),
	)

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("credential name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a database scoped credential named %q already exists in %s", name, dbName)
		}
		// CREATE DATABASE SCOPED CREDENTIAL has no form without IDENTITY, and
		// the server's own error for the omission is a syntax error naming
		// nothing useful.
		if strings.TrimSpace(identityField.Value()) == "" {
			return fmt.Errorf("identity is required")
		}
		if passwordField.Value() != confirmField.Value() {
			return fmt.Errorf("passwords do not match")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		spec := gosmo.DatabaseScopedCredentialSpec{
			Name: d.objectName(),
			// Trimmed to match what the preflight validated: SQL Server stores
			// IDENTITY verbatim, so a pasted trailing space becomes part of
			// the account name and the credential then fails to authenticate
			// with nothing on the page saying why.
			Identity: strings.TrimSpace(identityField.Value()),
			Secret:   passwordField.Value(),
		}
		// Server.Database, not DatabaseByName: the CREATE addresses the
		// database by name, and the by-name read would not work under Script
		// Changes.
		_, err := sc.Server.Database(dbName).CreateDatabaseScopedCredentialContext(ctx, spec)
		return err
	}
}
