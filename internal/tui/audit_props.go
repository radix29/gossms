package tui

import (
	"context"
	"fmt"
	"slices"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// auditPropPages builds the page set for Audit Properties.
//
// It is deliberately one page, where SSMS has General and a separate Filter
// tab. ALTER SERVER AUDIT replaces every setting at once — there is no form
// that changes the queue delay while leaving the predicate alone — so a
// second page with its own apply would either write a second ALTER that
// reverted the first, or need the filter's value before the page holding it
// had ever been opened. One page, one ALTER.
func auditPropPages(sc *db.ServerConn, auditName string) []propPage {
	name := auditName
	return []propPage{
		withRequires(pageAuditGeneral(sc, &name), "", rightAlterAnyAudit),
	}
}

// auditFailureItems and auditFailureValues are one table split in two: item i
// means value i. Swapping an entry in either would make "Fail operation" write
// SHUTDOWN with the page still reading itself back consistently, which is why
// audit_props_page_test.go pins the pairing by name and asserts the two are the
// same length.
var (
	auditFailureItems  = []string{"Continue", "Shut down server", "Fail operation"}
	auditFailureValues = []string{
		gosmo.AuditFailureContinue,
		gosmo.AuditFailureShutdown,
		gosmo.AuditFailureFailOp,
	}
)

// The file-count choices. MAX_ROLLOVER_FILES and MAX_FILES are mutually
// exclusive in the statement, and the catalog leaves max_rollover_files at its
// UNLIMITED sentinel even when max_files is the one in force — so the choice
// has to be explicit rather than inferred from two numbers.
const (
	auditFileCountUnlimited = iota
	auditFileCountRollover
	auditFileCountMax
)

var auditFileCountItems = []string{"Unlimited rollover files", "Rollover files", "Maximum files"}

// pageAuditGeneral is Audit Properties > General.
//
// The destination is static: SSMS greys it, and while ALTER SERVER AUDIT does
// accept a new TO clause, switching a FILE audit to a Windows log discards the
// whole file block and starts a new audit file. That is a different operation
// from editing an audit's settings, and offering it here would make it look
// like one.
func pageAuditGeneral(sc *db.ServerConn, auditName *string) propPage {
	return propPage{
		title:   "General",
		renames: true,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			a, err := sc.Server.ServerAuditByNameContext(ctx, *auditName)
			if err != nil {
				return nil, nil, err
			}

			nameRow := propsheet.Text("Audit name", a.Name, 40)
			delayRow := propsheet.Int("Queue delay", int64(a.QueueDelay), 0, 2147483647, "ms")
			failureRow := propsheet.Select("On audit log failure", auditFailureItems,
				max(slices.Index(auditFailureValues, a.OnFailure), 0))

			rows := []propsheet.Row{
				propsheet.Section("Audit"),
				nameRow,
				propsheet.Static("Audit GUID", a.GUID),
				propsheet.Static("State", enabledText(a.IsEnabled)),
				delayRow,
				failureRow,
				propsheet.Section("Audit destination"),
				propsheet.Static("Audit destination", a.Type),
			}

			isFile := a.Type == gosmo.AuditToFile
			var pathRow, sizeRow, countRow *propsheet.TextRow
			var countKindRow *propsheet.SelectRow
			var reserveRow *propsheet.CheckRow
			if isFile {
				pathRow = propsheet.Text("File path", a.LogFilePath, 50)
				sizeRow = propsheet.Int("Maximum file size", a.MaxFileSize, 0, 2147483647, "MB (0 = unlimited)")

				kind, count := auditFileCountUnlimited, int64(0)
				switch {
				case a.MaxFiles > 0:
					kind, count = auditFileCountMax, int64(a.MaxFiles)
				case a.MaxRolloverFiles > 0 && a.MaxRolloverFiles != gosmo.AuditUnlimited:
					kind, count = auditFileCountRollover, int64(a.MaxRolloverFiles)
				}
				countKindRow = propsheet.Select("File count limit", auditFileCountItems, kind)
				countRow = propsheet.Int("Number of files", count, 0, 2147483647, "")
				reserveRow = propsheet.Check("Reserve disk space", a.ReserveDiskSpace)

				rows = append(rows, pathRow, sizeRow, countKindRow, countRow, reserveRow)
			} else {
				rows = append(rows, propsheet.Note(
					"An audit writing to a Windows event log has no file settings. The destination is fixed once the audit exists."))
			}

			predicateRow := propsheet.Text("Filter predicate", a.Predicate, 60)
			rows = append(rows,
				propsheet.Section("Filter"),
				predicateRow,
				propsheet.Note("The WHERE clause the audit filters on, without the keyword. Leaving it blank removes the filter."),
				propsheet.Section("Summary"),
				propsheet.Static("Created", formatSQLDate(a.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(a.ModifyDate)),
			)

			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				dirty := delayRow.Dirty() || failureRow.Dirty() || predicateRow.Dirty()
				if isFile {
					dirty = dirty || pathRow.Dirty() || sizeRow.Dirty() ||
						countKindRow.Dirty() || countRow.Dirty() || reserveRow.Dirty()
				}
				if !dirty && !nameRow.Dirty() {
					return nil
				}
				// One disable window for the whole apply. Each write opens its
				// own otherwise, so a settings-plus-rename Apply stops auditing
				// twice and a rename that fails leaves the ALTER committed with
				// the audit off.
				handle := sc.Server.ServerAudit(*auditName)
				return handle.WithDisabled(ctx, func(ctx context.Context) error {
					if dirty {
						delay, err := delayRow.IntValue()
						if err != nil {
							return fmt.Errorf("queue delay: %w", err)
						}
						// Every field goes into the spec, dirty or not: ALTER
						// SERVER AUDIT replaces the whole setting block, so a
						// value left out is a value cleared.
						spec := gosmo.ServerAuditSpec{
							Name:       *auditName,
							Type:       a.Type,
							QueueDelay: int(delay),
							OnFailure:  auditFailureValues[failureRow.Selected()],
							Predicate:  predicateRow.Value(),
						}
						if isFile {
							size, err := sizeRow.IntValue()
							if err != nil {
								return fmt.Errorf("maximum file size: %w", err)
							}
							count, err := countRow.IntValue()
							if err != nil {
								return fmt.Errorf("number of files: %w", err)
							}
							spec.FilePath = pathRow.Value()
							spec.MaxFileSize = size
							spec.ReserveDiskSpace = reserveRow.Checked()
							switch countKindRow.Selected() {
							case auditFileCountMax:
								spec.MaxFiles = int(count)
							case auditFileCountRollover:
								spec.MaxRolloverFiles = int(count)
							default:
								spec.MaxRolloverFiles = gosmo.AuditUnlimited
							}
						}
						if err := handle.AlterContext(ctx, spec); err != nil {
							return err
						}
					}
					// Renaming last keeps the write above addressed by the name
					// the server still has — see propPage.renames.
					if nameRow.Dirty() {
						if err := handle.RenameContext(ctx, nameRow.Value()); err != nil {
							return err
						}
						commitRename(ctx, auditName, nameRow.Value())
					}
					return nil
				})
			}
			return f, apply, nil
		},
	}
}
