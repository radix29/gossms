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

// database_audit_specification_props.go is Database Audit Specification
// Properties — the database-scope twin of audit_specification_props.go.
//
// It is not the same page with a different noun. A database specification
// records individual actions on securables as well as action groups, so the
// page carries a second grid for what it already records and a small row of
// fields for adding one more. Everything else — the static name, the one
// page, the single disable window around the whole apply — follows the server
// half for the same reasons its comments give.

// databaseAuditSpecificationPropPages builds the page set for Database Audit
// Specification Properties.
//
// The right is ALTER ANY DATABASE AUDIT at *database* scope, not the
// server-scope ALTER ANY SERVER AUDIT the audit itself takes.
func databaseAuditSpecificationPropPages(sc *db.ServerConn, dbName, specName string) []propPage {
	return []propPage{
		withRequires(pageDatabaseAuditSpecificationGeneral(sc, dbName, specName), dbName, rightAlterAnyDBAudit),
	}
}

// noAuditAction is the "add nothing" entry the Action dropdown opens on. A
// page that opened on a real action would add one on every stray Apply.
const noAuditAction = "<none>"

// auditSecurableClassNames are the securable classes the ADD clause accepts,
// in the order SSMS lists them.
var auditSecurableClassNames = []string{"OBJECT", "SCHEMA", "DATABASE"}

// pageDatabaseAuditSpecificationGeneral is Database Audit Specification
// Properties > General: the audit it writes to, the action groups it records,
// the per-securable actions it records, and one row of fields for adding
// another.
//
// The name is static, as on the server half: ALTER DATABASE AUDIT
// SPECIFICATION has no MODIFY NAME form.
func pageDatabaseAuditSpecificationGeneral(sc *db.ServerConn, dbName, specName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			spec, err := dbObj.DatabaseAuditSpecificationByNameContext(ctx, specName)
			if err != nil {
				return nil, nil, err
			}
			audits, err := sc.Server.ServerAuditsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			groups, err := sc.Server.DatabaseAuditActionGroupsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			actionNames, err := sc.Server.DatabaseAuditActionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			// Every specification in the database, so the dropdown can leave
			// out the audits another one already holds: SQL Server allows one
			// specification per audit per database (Msg 33230, verified live
			// on major 17), so a rebind onto a taken audit is refused. This
			// specification's own audit stays, or the page would open showing
			// something other than what it is bound to.
			all, err := dbObj.DatabaseAuditSpecificationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			taken := map[string]bool{}
			for _, other := range all {
				if !strings.EqualFold(other.Name, specName) {
					taken[strings.ToLower(other.AuditName)] = true
				}
			}
			auditNames := make([]string, 0, len(audits))
			for _, a := range audits {
				if !taken[strings.ToLower(a.Name)] {
					auditNames = append(auditNames, a.Name)
				}
			}
			// An orphaned specification's audit is in no list — see the server
			// half for why the missing name is added rather than the first
			// real audit preselected.
			selected := slices.Index(auditNames, spec.AuditName)
			if selected < 0 {
				auditNames = append([]string{missingAuditItem}, auditNames...)
				selected = 0
			}
			auditRow := propsheet.Select("Audit", auditNames, selected)

			// A group the instance no longer defines is still recorded, so it
			// is added to the pick list rather than vanishing from the page.
			for _, g := range spec.ActionGroups {
				if !slices.Contains(groups, g) {
					groups = append(groups, g)
				}
			}
			slices.Sort(groups)

			groupGrid := propsheet.NewToggleGrid([]string{"Record", "Audit Action Group"}, []int{0}, 12)
			gText := make([][]string, len(groups))
			gValues := make([][]bool, len(groups))
			for i, g := range groups {
				gText[i] = []string{g}
				gValues[i] = []bool{slices.Contains(spec.ActionGroups, g)}
			}
			groupGrid.SetRows(gText, gValues)

			rows := []propsheet.Row{
				propsheet.Section("Specification"),
				propsheet.Static("Name", spec.Name),
				propsheet.Static("Database", dbName),
				propsheet.Static("State", enabledText(spec.IsEnabled)),
				auditRow,
				propsheet.Section("Audit action groups"),
				groupGrid,
			}

			// The actions grid is built only when there are actions: an empty
			// grid is a box with a header and nothing to do in it.
			var actionGrid *propsheet.ToggleGridRow
			if len(spec.Actions) > 0 {
				actionGrid = propsheet.NewToggleGrid([]string{"Record", "Action on securable"}, []int{0}, 8)
				aText := make([][]string, len(spec.Actions))
				aValues := make([][]bool, len(spec.Actions))
				for i, a := range spec.Actions {
					aText[i] = []string{auditActionText(a)}
					aValues[i] = []bool{true}
				}
				actionGrid.SetRows(aText, aValues)
				rows = append(rows,
					propsheet.Section("Audited actions"),
					actionGrid,
					propsheet.Note("Space unticks an action to stop recording it. An action cannot be edited in place — untick it and add the replacement below."))
			}

			actionRow := propsheet.Select("Action", append([]string{noAuditAction}, actionNames...), 0)
			classRow := propsheet.Select("Securable class", auditSecurableClassNames, 0)
			securableRow := propsheet.Text("Securable", "", 40)
			principalRow := propsheet.Text("Principal", "public", 30)

			rows = append(rows,
				propsheet.Section("Add an audited action"),
				actionRow, classRow, securableRow, principalRow,
				propsheet.Note("Securable is schema.object for an OBJECT, the schema for a SCHEMA, the database for a DATABASE. Applying a change disables the specification for the duration and re-enables it, which is the only order SQL Server accepts."),
				propsheet.Section("Summary"),
				propsheet.Static("Created", formatSQLDate(spec.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(spec.ModifyDate)),
			)

			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				actionDirty := actionRow.Dirty() && actionRow.Value() != noAuditAction
				gridDirty := groupGrid.Dirty() || (actionGrid != nil && actionGrid.Dirty())
				if !gridDirty && !actionDirty && !auditRow.Dirty() {
					return nil
				}

				var addGroups, dropGroups []string
				for i, g := range groups {
					was := slices.Contains(spec.ActionGroups, g)
					now := groupGrid.Values()[i][0]
					switch {
					case now && !was:
						addGroups = append(addGroups, g)
					case !now && was:
						dropGroups = append(dropGroups, g)
					}
				}

				var addActions, dropActions []gosmo.DatabaseAuditAction
				if actionGrid != nil {
					for i, a := range spec.Actions {
						if !actionGrid.Values()[i][0] {
							dropActions = append(dropActions, a)
						}
					}
				}
				if actionDirty {
					a, err := auditActionFromFields(actionRow.Value(), classRow.Value(),
						securableRow.Value(), principalRow.Value())
					if err != nil {
						return err
					}
					addActions = append(addActions, a)
				}

				// The database handle is the name-only one deliberately: the
				// specification's writes need nothing off sys.databases, and
				// this is the form that also works under a script context.
				handle := sc.Server.Database(dbName).DatabaseAuditSpecification(specName)
				// One disable window for the whole apply — see the server
				// half: a window per write stops recording once per statement
				// and leaves a part-failed apply with the specification off.
				return handle.WithDisabled(ctx, func(ctx context.Context) error {
					// Dropped before added: a specification allows no
					// duplicate group, and re-adding one it still records is
					// an error rather than a no-op.
					if err := handle.DropActionsContext(ctx, dropGroups, dropActions); err != nil {
						return err
					}
					if err := handle.AddActionsContext(ctx, addGroups, addActions); err != nil {
						return err
					}
					if auditRow.Dirty() && auditRow.Value() != missingAuditItem {
						return handle.SetAuditContext(ctx, auditRow.Value())
					}
					return nil
				})
			}
			return f, apply, nil
		},
	}
}

// auditActionFromFields turns the four add-an-action fields into the action
// gosmo takes, refusing what it cannot make a clause out of rather than
// letting gosmo build a statement with an empty securable in it.
func auditActionFromFields(action, class, securable, principal string) (gosmo.DatabaseAuditAction, error) {
	securable = strings.TrimSpace(securable)
	if securable == "" {
		return gosmo.DatabaseAuditAction{}, fmt.Errorf("an audited action needs a securable to audit it on")
	}
	schema, object := "", securable
	// Only an OBJECT is schema-qualified. Splitting on the first dot rather
	// than the last is what SSMS's own box does, and a name containing a dot
	// can be written in brackets.
	if strings.EqualFold(class, "OBJECT") {
		if s, o, ok := strings.Cut(securable, "."); ok {
			schema, object = trimIdentBrackets(s), trimIdentBrackets(o)
		} else {
			object = trimIdentBrackets(securable)
		}
	} else {
		object = trimIdentBrackets(securable)
	}
	principal = strings.TrimSpace(principal)
	if principal == "" {
		principal = "public"
	}
	return gosmo.DatabaseAuditAction{
		ActionName: action,
		ClassDesc:  class,
		SchemaName: schema,
		ObjectName: object,
		Principal:  trimIdentBrackets(principal),
	}, nil
}

// trimIdentBrackets strips one layer of [] a user may have typed around a
// name. gosmo bracket-quotes the securable itself, so leaving them on would
// produce [[dbo]] — a name no object has.
func trimIdentBrackets(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return strings.ReplaceAll(s[1:len(s)-1], "]]", "]")
	}
	return s
}
