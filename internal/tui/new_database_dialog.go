package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ndbPrefetch holds the one-time fetch every New Database page is built from:
// existing database names (name-uniqueness preflight), server logins (Owner
// row), model's current options/recovery model/compatibility level (the
// Options/General baselines: CREATE DATABASE inherits these from model, so
// seeding each row's Dirty() baseline from model makes apply-if-dirty correct
// for a brand-new database, see new_database_pages.go), and the server's
// default data/log paths (for blank file Path fields).
type ndbPrefetch struct {
	existingNames *nameSet
	loginNames    []string
	modelOptions  *gosmo.DatabaseOptions
	modelRecovery gosmo.RecoveryModel
	modelCompat   gosmo.CompatibilityLevel

	defaultDataPath string
	defaultLogPath  string
	defaultOwner    string
}

func fetchNewDatabasePrefetch(ctx context.Context, sc *db.ServerConn) (*ndbPrefetch, error) {
	dbs, err := sc.Server.Databases(ctx)
	if err != nil {
		return nil, err
	}
	existing := newNameSet(serverCollation(sc))
	for _, d := range dbs {
		existing.Add(d.Name)
	}

	logins, err := sc.Server.Logins(ctx)
	if err != nil {
		return nil, err
	}
	loginNames := make([]string, len(logins))
	for i, l := range logins {
		loginNames[i] = l.Name
	}
	slices.Sort(loginNames)

	model, err := sc.Server.DatabaseByName(ctx, "model")
	if err != nil {
		return nil, err
	}
	modelOpts, err := model.Options(ctx)
	if err != nil {
		return nil, err
	}

	paths := currentDefaultPaths(ctx, sc)
	return &ndbPrefetch{
		existingNames:   existing,
		loginNames:      loginNames,
		modelOptions:    modelOpts,
		modelRecovery:   model.RecoveryModel,
		modelCompat:     model.CompatibilityLevel,
		defaultDataPath: paths.Data,
		defaultLogPath:  paths.Log,
		defaultOwner:    sc.Opts.User,
	}, nil
}

// NewDatabaseDialog is the New Database dialog (Object Explorer's server node
// and "Databases" folder). Unlike PropDialog (prop_dialog.go) it doesn't diff
// dirty pages against a loaded baseline: there is no existing object. General's
// apply always runs first, unconditionally, since it creates the database before
// Options'/Filegroups' applies can target it: a fixed three-step sequence, not
// a discovered dirty set. One instance is reused for every invocation.
type NewDatabaseDialog struct {
	newObjectDialog[ndbPrefetch]
}

// NewNewDatabaseDialog creates the dialog and wires its callbacks.
func NewNewDatabaseDialog(app *App) *NewDatabaseDialog {
	d := &NewDatabaseDialog{}
	d.init(app, newObjectConfig[ndbPrefetch]{
		title:          "New Database",
		noun:           "Database",
		pages:          []string{"General", "Options", "Filegroups"},
		scriptDatabase: "",
		fetch:          fetchNewDatabasePrefetch,
		build:          d.buildPages,
		refresh:        func(sc *db.ServerConn) { d.app.explorer.ReloadFolders(sc, folderOf("", NodeDatabases)) },
	})
	return d
}

func (d *NewDatabaseDialog) buildPages(pf *ndbPrefetch) {
	sc := d.sc

	generalForm, generalApply, nameField, collation := buildNewDatabaseGeneralPage(sc, pf)
	dbName := func() string { return strings.TrimSpace(nameField.Value()) }
	optionsForm, optionsApply := buildNewDatabaseOptionsPage(sc, pf, dbName)
	filegroupsForm, filegroupsApply := buildNewDatabaseFilegroupsPage(sc, pf, dbName, collation)

	d.forms = []*propsheet.Form{generalForm, optionsForm, filegroupsForm}
	d.applyFns = []propApply{generalApply, optionsApply, filegroupsApply}
	d.objectName = dbName
	d.preflight = func() error {
		name := dbName()
		if name == "" {
			return fmt.Errorf("database name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a database named %q already exists", name)
		}
		return nil
	}
}

// onConfirmDiscard guards F5/Refresh (which would silently rebuild the current
// page, discarding pending edits) behind the confirmation prompt PropDialog
// uses.
