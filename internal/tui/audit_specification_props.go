package tui

import (
	"context"
	"slices"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// serverAuditSpecificationPropPages builds the page set for Server Audit
// Specification Properties — one page, as SSMS's own dialog is.
func serverAuditSpecificationPropPages(sc *db.ServerConn, specName string) []propPage {
	return []propPage{
		withRequires(pageServerAuditSpecificationGeneral(sc, specName), "", gate.AlterAnyAudit),
	}
}

// pageServerAuditSpecificationGeneral is Server Audit Specification
// Properties > General: the audit it writes to, and the action groups it
// records.
//
// The name is static. ALTER SERVER AUDIT SPECIFICATION has no MODIFY NAME form
// at all — verified live, it is a parse error, not a permission failure — so
// the page does not set renames.
func pageServerAuditSpecificationGeneral(sc *db.ServerConn, specName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			spec, err := sc.Server.ServerAuditSpecificationByName(ctx, specName)
			if err != nil {
				return nil, nil, err
			}
			audits, err := sc.Server.ServerAudits(ctx)
			if err != nil {
				return nil, nil, err
			}
			groups, err := sc.Server.AuditActionGroups(ctx)
			if err != nil {
				return nil, nil, err
			}

			auditNames := make([]string, len(audits))
			for i, a := range audits {
				auditNames[i] = a.Name
			}
			auditRow := auditSelectRow(auditNames, spec.AuditName)
			groups = unionSorted(groups, spec.ActionGroups)

			grid := auditGroupGrid(groups, spec.ActionGroups, 14)

			f := propsheet.NewForm(
				propsheet.Section("Specification"),
				propsheet.Static("Name", spec.Name),
				propsheet.Static("State", enabledText(spec.IsEnabled)),
				auditRow,
				propsheet.Section("Audit action groups"),
				grid,
				propsheet.Note("Space toggles the selected group. Applying a change disables the specification for the duration and re-enables it, which is the only order SQL Server accepts."),
				propsheet.Section("Summary"),
				propsheet.Static("Created", formatSQLDate(spec.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(spec.ModifyDate)),
			)

			apply := func(ctx context.Context) error {
				if !grid.Dirty() && !auditRow.Dirty() {
					return nil
				}
				handle := sc.Server.ServerAuditSpecificationRef(specName)
				// One disable window for the whole apply. Each write opens its
				// own otherwise, so a full Apply stops recording three times
				// and a failure part-way leaves the earlier writes committed
				// with the specification off.
				return handle.WithDisabled(ctx, func(ctx context.Context) error {
					if grid.Dirty() {
						var add, drop []string
						for i, g := range groups {
							was := slices.Contains(spec.ActionGroups, g)
							now := grid.Values()[i][0]
							switch {
							case now && !was:
								add = append(add, g)
							case !now && was:
								drop = append(drop, g)
							}
						}
						// Dropped before added: a specification is allowed no
						// duplicate group, and doing it the other way round leaves
						// the set momentarily larger for no reason.
						if err := handle.DropActionGroups(ctx, drop...); err != nil {
							return err
						}
						if err := handle.AddActionGroups(ctx, add...); err != nil {
							return err
						}
					}

					if auditRow.Dirty() && auditRow.Value() != missingAuditItem {
						if err := handle.SetAudit(ctx, auditRow.Value()); err != nil {
							return err
						}
					}
					return nil
				})
			}
			return f, apply, nil
		},
	}
}

// missingAuditItem is the dropdown entry standing in for the audit an orphaned
// specification names and the server no longer has.
const missingAuditItem = "<audit no longer exists>"
