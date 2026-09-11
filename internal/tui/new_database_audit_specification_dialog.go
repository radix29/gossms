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

// new_database_audit_specification_dialog.go is the New Database Audit
// Specification dialog (a database's Security > Database Audit Specifications
// folder), built on newObjectDialog like every other New-X.
//
// As with the server-scope one, the specification is created disabled: turning
// it on is Enable's job, and one enabled at creation starts recording before
// the user has seen it in the tree.

// ndbAuditSpecPrefetch holds what the dialog needs before it opens: the
// existing specification names in this database for the uniqueness preflight,
// the server audits to bind to, and the two pick lists, both read from the
// server rather than hard-coded so they stay right across versions.
type ndbAuditSpecPrefetch struct {
	existingNames map[string]bool
	auditNames    []string
	actionGroups  []string
	actionNames   []string
}

// freeAuditNames are the audits not already spoken for in this database.
//
// SQL Server allows one database audit specification per audit per database —
// a second is Msg 33230, "An audit specification for audit 'x' already
// exists", verified live on major 17. Offering a taken audit would make OK
// the only way to find that out, so the dropdown lists what can actually be
// chosen.
func freeAuditNames(auditNames []string, specs []*gosmo.DatabaseAuditSpecification) []string {
	taken := make(map[string]bool, len(specs))
	for _, s := range specs {
		taken[strings.ToLower(s.AuditName)] = true
	}
	out := make([]string, 0, len(auditNames))
	for _, n := range auditNames {
		if !taken[strings.ToLower(n)] {
			out = append(out, n)
		}
	}
	return out
}

func fetchNewDBAuditSpecPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*ndbAuditSpecPrefetch, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	specs, err := dbObj.DatabaseAuditSpecificationsContext(ctx)
	if err != nil {
		return nil, err
	}
	audits, err := sc.Server.ServerAuditsContext(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := sc.Server.DatabaseAuditActionGroupsContext(ctx)
	if err != nil {
		return nil, err
	}
	actions, err := sc.Server.DatabaseAuditActionsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(specs))
	for _, s := range specs {
		existing[strings.ToLower(s.Name)] = true
	}
	names := make([]string, len(audits))
	for i, a := range audits {
		names[i] = a.Name
	}
	return &ndbAuditSpecPrefetch{
		existingNames: existing, auditNames: freeAuditNames(names, specs),
		actionGroups: groups, actionNames: actions,
	}, nil
}

// NewDatabaseAuditSpecificationDialog is the New Database Audit Specification
// dialog.
type NewDatabaseAuditSpecificationDialog struct {
	newObjectDialog[ndbAuditSpecPrefetch]

	// dbName is the database the specification is created in, and node the
	// folder to refresh afterwards. Both are set by show, before the embedded
	// dialog's own show runs the prefetch that reads them.
	dbName string
	node   *explorerNode
}

// NewNewDatabaseAuditSpecificationDialog creates the dialog and wires its
// callbacks.
func NewNewDatabaseAuditSpecificationDialog(app *App) *NewDatabaseAuditSpecificationDialog {
	d := &NewDatabaseAuditSpecificationDialog{}
	d.init(app, newObjectConfig[ndbAuditSpecPrefetch]{
		title: "New Database Audit Specification",
		noun:  "Database Audit Specification",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*ndbAuditSpecPrefetch, error) {
			return fetchNewDBAuditSpecPrefetch(ctx, sc, d.dbName)
		},
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	return d
}

// show opens the dialog for one database's Database Audit Specifications
// folder.
func (d *NewDatabaseAuditSpecificationDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	// Script Changes opens its query window in the database the statement runs
	// in, not the connection's default.
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *NewDatabaseAuditSpecificationDialog) buildPages(pf *ndbAuditSpecPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Name", "", 40)

	rows := []propsheet.Row{
		propsheet.Section("Specification"),
		nameField,
		propsheet.Static("Database", dbName),
	}

	// A server with no audit cannot carry a specification at all — FOR SERVER
	// AUDIT is required, and the audit is a *server* object even for a
	// database specification. Saying so beats a dropdown with nothing in it
	// and an error only on OK.
	var auditField *propsheet.SelectRow
	if len(pf.auditNames) == 0 {
		rows = append(rows, propsheet.Note(
			"No audit is available. A database may hold one specification per audit, so every audit this server has is already spoken for here — create another under Security > Audits, or edit the existing specification instead."))
	} else {
		auditField = propsheet.Select("Audit", pf.auditNames, 0)
		rows = append(rows, auditField)
	}

	grid := propsheet.NewToggleGrid([]string{"Record", "Audit Action Group"}, []int{0}, 12)
	groups := slices.Clone(pf.actionGroups)
	text := make([][]string, len(groups))
	values := make([][]bool, len(groups))
	for i, g := range groups {
		text[i] = []string{g}
		values[i] = []bool{false}
	}
	grid.SetRows(text, values)

	actionField := propsheet.Select("Action", append([]string{noAuditAction}, pf.actionNames...), 0)
	classField := propsheet.Select("Securable class", auditSecurableClassNames, 0)
	securableField := propsheet.Text("Securable", "", 40)
	principalField := propsheet.Text("Principal", "public", 30)

	rows = append(rows,
		propsheet.Section("Audit action groups"),
		grid,
		propsheet.Section("Audited action (optional)"),
		actionField, classField, securableField, principalField,
		propsheet.Note("Space toggles the selected group. An action is optional and audits one action on one securable — Securable is schema.object for an OBJECT, the schema for a SCHEMA, the database for a DATABASE. The specification is created disabled; use Enable on the new node to start recording."),
	)

	d.forms[0] = propsheet.NewForm(rows...)
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("specification name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a database audit specification named %q already exists in %s", name, dbName)
		}
		if auditField == nil {
			return fmt.Errorf("no audit is free to bind the specification to")
		}
		if actionField.Value() != noAuditAction && strings.TrimSpace(securableField.Value()) == "" {
			return fmt.Errorf("an audited action needs a securable to audit it on")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		var chosen []string
		for i, g := range groups {
			if grid.Values()[i][0] {
				chosen = append(chosen, g)
			}
		}
		var actions []gosmo.DatabaseAuditAction
		if actionField.Value() != noAuditAction {
			a, err := auditActionFromFields(actionField.Value(), classField.Value(),
				securableField.Value(), principalField.Value())
			if err != nil {
				return err
			}
			actions = append(actions, a)
		}
		// The name-only database handle, not DatabaseByName: the create needs
		// nothing off sys.databases, and this is the form that also works
		// under the script context Script-To uses.
		_, err := sc.Server.Database(dbName).CreateDatabaseAuditSpecificationContext(ctx,
			gosmo.DatabaseAuditSpecificationSpec{
				Name:         d.objectName(),
				AuditName:    auditField.Value(),
				ActionGroups: chosen,
				Actions:      actions,
			})
		return err
	}
}
