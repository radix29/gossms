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
// folder), built on newObjectDialog.
//
// As with the server-scope one, the specification is created disabled: Enable
// turns it on, and one enabled at creation records before the user has seen it
// in the tree.

// ndbAuditSpecPrefetch holds what the dialog needs before it opens: existing
// specification names in this database for the uniqueness preflight, the server
// audits to bind to, and the two pick lists, read from the server so they stay
// right across versions.
type ndbAuditSpecPrefetch struct {
	existingNames *nameSet
	auditNames    []string
	actionGroups  []string
	actionNames   []string
}

// freeAuditNames are the audits in auditNames that no specification in held
// (the audit names other specifications write to) already holds.
//
// SQL Server allows one database audit specification per audit per database,
// and one server audit specification per audit; a second is Msg 33230 "An audit
// specification for audit 'x' already exists" (verified live on major 17, on
// CREATE and on an ALTER ... FOR SERVER AUDIT rebind). Offering a taken audit
// would make OK the only way to find out, so the dropdown lists what can be
// chosen. Audits are server objects, so serverCollation decides which names are
// the same.
func freeAuditNames(serverCollation string, auditNames, held []string) []string {
	taken := newNameSet(serverCollation)
	for _, n := range held {
		taken.Add(n)
	}
	out := make([]string, 0, len(auditNames))
	for _, n := range auditNames {
		if !taken.Has(n) {
			out = append(out, n)
		}
	}
	return out
}

func fetchNewDBAuditSpecPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*ndbAuditSpecPrefetch, error) {
	dbObj, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	specs, err := dbObj.DatabaseAuditSpecifications(ctx)
	if err != nil {
		return nil, err
	}
	audits, err := sc.Server.ServerAudits(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := sc.Server.DatabaseAuditActionGroups(ctx)
	if err != nil {
		return nil, err
	}
	actions, err := sc.Server.DatabaseAuditActions(ctx)
	if err != nil {
		return nil, err
	}
	existing := newNameSet(databaseCollation(dbObj))
	for _, s := range specs {
		existing.Add(s.Name)
	}
	names := make([]string, len(audits))
	for i, a := range audits {
		names[i] = a.Name
	}
	held := make([]string, len(specs))
	for i, sp := range specs {
		held[i] = sp.AuditName
	}
	return &ndbAuditSpecPrefetch{
		existingNames: existing, auditNames: freeAuditNames(serverCollation(sc), names, held),
		actionGroups: groups, actionNames: actions,
	}, nil
}

// NewDatabaseAuditSpecificationDialog is the New Database Audit Specification
// dialog.
type NewDatabaseAuditSpecificationDialog struct {
	dbFolderDialog[ndbAuditSpecPrefetch]
}

// NewNewDatabaseAuditSpecificationDialog creates the dialog and wires its
// callbacks.
func NewNewDatabaseAuditSpecificationDialog(app *App) *NewDatabaseAuditSpecificationDialog {
	d := &NewDatabaseAuditSpecificationDialog{}
	d.initInFolder(app, newObjectConfig[ndbAuditSpecPrefetch]{
		title: "New Database Audit Specification",
		noun:  "Database Audit Specification",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*ndbAuditSpecPrefetch, error) {
			return fetchNewDBAuditSpecPrefetch(ctx, sc, d.dbName)
		},
		build: d.buildPages,
	})
	return d
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

	// A server with no audit cannot carry a specification (FOR SERVER AUDIT is
	// required, and the audit is a *server* object even for a database
	// specification). Say so rather than show an empty dropdown and an error on OK.
	var auditField *propsheet.SelectRow
	if len(pf.auditNames) == 0 {
		rows = append(rows, propsheet.Note(
			"No audit is available. A database may hold one specification per audit, so every audit this server has is already spoken for here — create another under Security > Audits, or edit the existing specification instead."))
	} else {
		auditField = propsheet.Select("Audit", pf.auditNames, 0)
		rows = append(rows, auditField)
	}

	groups := slices.Clone(pf.actionGroups)
	grid := auditGroupGrid(groups, nil, 12)

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
		if pf.existingNames.Has(name) {
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
		chosen := tickedAuditGroups(grid, groups)
		var actions []gosmo.DatabaseAuditAction
		if actionField.Value() != noAuditAction {
			a, err := auditActionFromFields(actionField.Value(), classField.Value(),
				securableField.Value(), principalField.Value())
			if err != nil {
				return err
			}
			actions = append(actions, a)
		}
		// The name-only database handle, not DatabaseByName: the create needs nothing
		// off sys.databases, and this form also works under Script-To's script
		// context.
		_, err := sc.Server.DatabaseRef(dbName).CreateDatabaseAuditSpecification(ctx,
			gosmo.CreateDatabaseAuditSpecificationRequest{
				Name:         d.objectName(),
				AuditName:    auditField.Value(),
				ActionGroups: chosen,
				Actions:      actions,
			})
		return err
	}
}
