package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

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

// auditQueueDelayRow builds the "Queue delay" row shared by Audit Properties
// and New Audit. QUEUE_DELAY takes 0 (synchronous) or at least 1000 ms —
// 1..999 is refused by the engine, and on the properties page that rejection
// arrives after WithDisabled has already stopped the audit, so the row refuses
// the value itself rather than letting the round trip find it.
func auditQueueDelayRow(value int64) *propsheet.TextRow {
	r := propsheet.Int("Queue delay", value, 0, 2147483647, "ms")
	r.SetValidate(func(v string) error {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return fmt.Errorf("must be a whole number")
		}
		if n < 0 || n > 2147483647 {
			return fmt.Errorf("must be between 0 and 2147483647")
		}
		if n > 0 && n < 1000 {
			return fmt.Errorf("queue delay must be 0 or at least 1000 ms")
		}
		return nil
	})
	return r
}

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
// The destination is editable, where SSMS greys it: ALTER SERVER AUDIT accepts
// a new TO clause on a disabled audit, and the apply already runs inside a
// disable window. Changing it away from FILE discards the file block and
// starts a new audit file on the way back, which the page's note says out
// loud; it is not a reason to withhold the write.
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
			delayRow := auditQueueDelayRow(int64(a.QueueDelay))
			failureRow := propsheet.Select("On audit log failure", auditFailureItems,
				max(slices.Index(auditFailureValues, a.OnFailure), 0))

			destRow := propsheet.Select("Audit destination", auditDestinationItems,
				max(slices.Index(auditDestinationValues, a.Type), 0))

			// The file rows exist whatever the audit writes to now, because
			// the destination can be changed to FILE here, and switching to
			// FILE with no FILEPATH fails Msg 33072 "The audit log file path
			// is invalid" — verified live. A log audit has no row in
			// sys.server_file_audits, so they come up empty and the sync
			// below greys them.
			pathRow := propsheet.Text("File path", a.LogFilePath, 50)
			sizeRow := propsheet.Int("Maximum file size", a.MaxFileSize, 0, 2147483647, "MB (0 = unlimited)")

			kind, count := auditFileCountUnlimited, int64(0)
			switch {
			case a.MaxFiles > 0:
				kind, count = auditFileCountMax, int64(a.MaxFiles)
			case a.MaxRolloverFiles > 0 && a.MaxRolloverFiles != gosmo.AuditUnlimited:
				kind, count = auditFileCountRollover, int64(a.MaxRolloverFiles)
			}
			countKindRow := propsheet.Select("File count limit", auditFileCountItems, kind)
			countRow := propsheet.Int("Number of files", count, 0, 2147483647, "")
			reserveRow := propsheet.Check("Reserve disk space", a.ReserveDiskSpace)

			writesToFile := func() bool {
				return auditDestinationValues[destRow.Selected()] == gosmo.AuditToFile
			}
			// The file block is only sent for a FILE target, so a log audit's
			// file rows are gated rather than left inviting input the ALTER
			// will not carry.
			syncDest := func() {
				f := writesToFile()
				pathRow.SetEnabled(f)
				sizeRow.SetEnabled(f)
				countRow.SetEnabled(f)
				countKindRow.SetReadOnly(!f)
				reserveRow.SetReadOnly(!f)
			}
			destRow.SetOnChange(func(string) { syncDest() })
			syncDest()

			rows := []propsheet.Row{
				propsheet.Section("Audit"),
				nameRow,
				propsheet.Static("Audit GUID", a.GUID),
				propsheet.Static("State", enabledText(a.IsEnabled)),
				delayRow,
				failureRow,
				propsheet.Section("Audit destination"),
				destRow,
				pathRow, sizeRow, countKindRow, countRow, reserveRow,
				propsheet.Note("The file settings apply to a File audit only. Changing the destination discards the settings the old one had, and a File audit resumed later starts a new audit file."),
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
				isFile := writesToFile()
				dirty := delayRow.Dirty() || failureRow.Dirty() || predicateRow.Dirty() ||
					destRow.Dirty()
				if isFile {
					dirty = dirty || pathRow.Dirty() || sizeRow.Dirty() ||
						countKindRow.Dirty() || countRow.Dirty() || reserveRow.Dirty()
				}
				if !dirty && !nameRow.Dirty() {
					return nil
				}
				if isFile && strings.TrimSpace(pathRow.Value()) == "" {
					return fmt.Errorf("a file audit needs a file path")
				}
				// One disable window for the whole apply. Each write opens its
				// own otherwise, so a settings-plus-rename Apply stops auditing
				// twice and a rename that fails leaves the ALTER committed with
				// the audit off.
				handle := sc.Server.ServerAudit(*auditName)
				err := handle.WithDisabled(ctx, func(ctx context.Context) error {
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
							Type:       auditDestinationValues[destRow.Selected()],
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
							spec.FilePath = strings.TrimSpace(pathRow.Value())
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
				if err != nil {
					return auditApplyFailure(ctx, sc, *auditName, a.IsEnabled, err)
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// auditApplyFailure reports a failed Audit Properties apply, marking it
// committed when the audit has been left switched off.
//
// The whole apply runs inside gosmo's disable window, which re-enables the
// audit last and reports that failure with everything before it already
// committed. Switching an enabled audit to SECURITY LOG does exactly this on a
// host whose service account may not write the Windows security log: the ALTER
// lands, the restore is refused, and the page goes on showing "State: Enabled"
// for an audit the server has stopped — verified live on win10cli. Re-reading
// the state is the only way to tell that from the ordinary failure where
// nothing landed and the user's edits must survive to be retried.
func auditApplyFailure(ctx context.Context, sc *db.ServerConn, name string, wasEnabled bool, applyErr error) error {
	if !wasEnabled || gosmo.Scripting(ctx) {
		return applyErr
	}
	a, err := sc.Server.ServerAuditByNameContext(ctx, name)
	if err != nil || a.IsEnabled {
		return applyErr
	}
	// The note leads: the sheet's message line hard-clips, and SQL Server's
	// own reason is long enough to push anything appended off the end.
	return applyCommitted(fmt.Errorf("auditing is now stopped — %w", applyErr))
}
