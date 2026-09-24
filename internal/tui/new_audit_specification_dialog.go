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

// new_audit_specification_dialog.go is the New Server Audit Specification
// dialog (Object Explorer's Security > Server Audit Specifications folder),
// built on newObjectDialog like every other New-X.
//
// The specification is created disabled, matching New Audit and SSMS: turning
// it on is Enable's job, and a specification enabled at creation starts
// recording before the user has seen it in the tree.

// nauditSpecPrefetch holds what the dialog needs before it opens: the existing
// specification names for the uniqueness preflight, the audits to bind to, and
// the action-group pick list, which is read from the server rather than
// hard-coded so it stays right across versions.
type nauditSpecPrefetch struct {
	existingNames *nameSet
	auditNames    []string
	actionGroups  []string
}

func fetchNewAuditSpecPrefetch(ctx context.Context, sc *db.ServerConn) (*nauditSpecPrefetch, error) {
	specs, err := sc.Server.ServerAuditSpecifications(ctx)
	if err != nil {
		return nil, err
	}
	audits, err := sc.Server.ServerAudits(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := sc.Server.AuditActionGroups(ctx)
	if err != nil {
		return nil, err
	}
	existing := newNameSet(serverCollation(sc))
	for _, s := range specs {
		existing.Add(s.Name)
	}
	names := make([]string, len(audits))
	for i, a := range audits {
		names[i] = a.Name
	}
	return &nauditSpecPrefetch{existingNames: existing, auditNames: names, actionGroups: groups}, nil
}

// NewAuditSpecificationDialog is the New Server Audit Specification dialog.
type NewAuditSpecificationDialog struct {
	newObjectDialog[nauditSpecPrefetch]
}

// NewNewAuditSpecificationDialog creates the dialog and wires its callbacks.
func NewNewAuditSpecificationDialog(app *App) *NewAuditSpecificationDialog {
	d := &NewAuditSpecificationDialog{}
	d.init(app, newObjectConfig[nauditSpecPrefetch]{
		title: "New Server Audit Specification",
		noun:  "Server Audit Specification",
		pages: []string{"General"},
		fetch: fetchNewAuditSpecPrefetch,
		build: d.buildPages,
		refresh: func(sc *db.ServerConn) {
			d.app.explorer.RefreshFolderByType(sc, NodeServerAuditSpecifications)
		},
	})
	return d
}

func (d *NewAuditSpecificationDialog) buildPages(pf *nauditSpecPrefetch) {
	sc := d.sc

	nameField := propsheet.Text("Name", "", 40)

	rows := []propsheet.Row{
		propsheet.Section("Specification"),
		nameField,
	}

	// A server with no audit cannot carry a specification at all — FOR SERVER
	// AUDIT is required. Saying so is better than a dropdown with nothing in
	// it and an error only on OK.
	var auditField *propsheet.SelectRow
	if len(pf.auditNames) == 0 {
		rows = append(rows, propsheet.Note(
			"This server has no audit yet. Create one under Security > Audits first — a specification must name the audit it writes to."))
	} else {
		auditField = propsheet.Select("Audit", pf.auditNames, 0)
		rows = append(rows, auditField)
	}

	groups := slices.Clone(pf.actionGroups)
	grid := auditGroupGrid(groups, nil, 14)

	rows = append(rows,
		propsheet.Section("Audit action groups"),
		grid,
		propsheet.Note("Space toggles the selected group. The specification is created disabled — use Enable on the new node to start recording."),
	)

	d.forms[0] = propsheet.NewForm(rows...)
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("specification name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a server audit specification named %q already exists", name)
		}
		if auditField == nil {
			return fmt.Errorf("this server has no audit to bind the specification to")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		chosen := tickedAuditGroups(grid, groups)
		_, err := sc.Server.CreateServerAuditSpecification(ctx, gosmo.CreateServerAuditSpecificationRequest{
			Name:         d.objectName(),
			AuditName:    auditField.Value(),
			ActionGroups: chosen,
		})
		return err
	}
}
