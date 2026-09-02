package tui

import (
	"context"
	"slices"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// serverAuditSpecificationPropPages builds the page set for Server Audit
// Specification Properties — one page, as SSMS's own dialog is.
func serverAuditSpecificationPropPages(sc *db.ServerConn, specName string) []propPage {
	return []propPage{
		withRequires(pageServerAuditSpecificationGeneral(sc, specName), "", rightAlterAnyAudit),
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
			spec, err := sc.Server.ServerAuditSpecificationByNameContext(ctx, specName)
			if err != nil {
				return nil, nil, err
			}
			audits, err := sc.Server.ServerAuditsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			groups, err := sc.Server.AuditActionGroupsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			auditNames := make([]string, len(audits))
			for i, a := range audits {
				auditNames[i] = a.Name
			}
			// An orphaned specification — its audit dropped out from under it,
			// which SQL Server allows — has a name in no list. Offering the
			// dropdown with the first audit preselected would make a stray
			// Apply silently rebind it, so the missing name is added and
			// selected instead.
			selected := slices.Index(auditNames, spec.AuditName)
			if selected < 0 {
				auditNames = append([]string{missingAuditItem}, auditNames...)
				selected = 0
			}
			auditRow := propsheet.Select("Audit", auditNames, selected)

			// The pick list comes from the server, so a group the
			// specification already records is always in it; a group the
			// instance no longer defines would otherwise vanish from the page
			// while still being recorded.
			for _, g := range spec.ActionGroups {
				if !slices.Contains(groups, g) {
					groups = append(groups, g)
				}
			}
			slices.Sort(groups)

			grid := propsheet.NewToggleGrid([]string{"Record", "Audit Action Group"}, []int{0}, 14)
			text := make([][]string, len(groups))
			values := make([][]bool, len(groups))
			for i, g := range groups {
				text[i] = []string{g}
				values[i] = []bool{slices.Contains(spec.ActionGroups, g)}
			}
			grid.SetRows(text, values)

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
				handle := sc.Server.ServerAuditSpecification(specName)
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
						if err := handle.DropActionGroupsContext(ctx, drop...); err != nil {
							return err
						}
						if err := handle.AddActionGroupsContext(ctx, add...); err != nil {
							return err
						}
					}

					if auditRow.Dirty() && auditRow.Value() != missingAuditItem {
						if err := handle.SetAuditContext(ctx, auditRow.Value()); err != nil {
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
