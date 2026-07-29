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

// ndbPrefetch holds the one shared, one-time fetch every New Database page
// is built from: existing database names (for the name-uniqueness preflight
// check), server logins (for the Owner row), model's current options/
// recovery model/compatibility level (the Options/General pages' baselines —
// CREATE DATABASE inherits these from model, so seeding each row's Dirty()
// baseline from model makes the apply-if-dirty logic correct for a brand-new
// database too, see new_database_pages.go), and the server's default
// data/log paths (for file Path fields left blank).
type ndbPrefetch struct {
	existingNames map[string]bool
	loginNames    []string
	modelOptions  *gosmo.DatabaseOptions
	modelRecovery gosmo.RecoveryModel
	modelCompat   gosmo.CompatibilityLevel

	defaultDataPath string
	defaultLogPath  string
	defaultOwner    string
}

func fetchNewDatabasePrefetch(ctx context.Context, sc *db.ServerConn) (*ndbPrefetch, error) {
	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(dbs))
	for _, d := range dbs {
		existing[strings.ToLower(d.Name())] = true
	}

	logins, err := sc.Server.LoginsContext(ctx)
	if err != nil {
		return nil, err
	}
	loginNames := make([]string, len(logins))
	for i, l := range logins {
		loginNames[i] = l.Name
	}
	slices.Sort(loginNames)

	model, err := sc.Server.DatabaseByNameContext(ctx, "model")
	if err != nil {
		return nil, err
	}
	modelOpts, err := model.OptionsContext(ctx)
	if err != nil {
		return nil, err
	}

	info := sc.Server.Info()
	return &ndbPrefetch{
		existingNames:   existing,
		loginNames:      loginNames,
		modelOptions:    modelOpts,
		modelRecovery:   model.RecoveryModel(),
		modelCompat:     model.CompatibilityLevel(),
		defaultDataPath: info.DefaultDataPath,
		defaultLogPath:  info.DefaultLogPath,
		defaultOwner:    sc.Opts.User,
	}, nil
}

// NewDatabaseDialog is the New Database creation dialog (Object Explorer's
// server node and "Databases" folder, "New Database..."). Unlike PropDialog
// (prop_dialog.go), it doesn't diff "dirty" pages against a loaded baseline
// — there's no existing object to diff against. General's apply always runs
// first, unconditionally, since it's what creates the database, before
// Options'/Filegroups' applies can target it: a fixed three-step sequence,
// not a discovered dirty set. One instance is reused for every invocation.
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
		refresh:        func(sc *db.ServerConn) { d.app.explorer.RefreshDatabasesFolder(sc) },
	})
	return d
}

func (d *NewDatabaseDialog) buildPages(pf *ndbPrefetch) {
	sc := d.sc

	generalForm, generalApply, nameField := buildNewDatabaseGeneralPage(sc, pf)
	dbName := func() string { return strings.TrimSpace(nameField.Value()) }
	optionsForm, optionsApply := buildNewDatabaseOptionsPage(sc, pf, dbName)
	filegroupsForm, filegroupsApply := buildNewDatabaseFilegroupsPage(sc, pf, dbName)

	d.forms = []*propsheet.Form{generalForm, optionsForm, filegroupsForm}
	d.applyFns = []propApply{generalApply, optionsApply, filegroupsApply}
	d.objectName = dbName
	d.preflight = func() error {
		name := dbName()
		if name == "" {
			return fmt.Errorf("database name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a database named %q already exists", name)
		}
		return nil
	}
}

// onConfirmDiscard guards F5/Refresh (which would otherwise silently
// rebuild whichever page is current from scratch, discarding any pending
// edits on it) behind the same confirmation prompt PropDialog uses.
