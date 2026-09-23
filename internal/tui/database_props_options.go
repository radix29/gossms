package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// dbOptRow pairs an editable Select row with the DatabaseOption it edits
// and the exact value strings (SQL Server keywords, in the same order as
// the row's items) SetDatabaseOption should receive.
type dbOptRow struct {
	opt   gosmo.DatabaseOption
	row   *propsheet.SelectRow
	items []string
}

var onOff = []string{"OFF", "ON"}

// dbOptSelectRow creates a Select row bound to a DatabaseOption, appending
// it to *tracked so the page's apply closure can find it later.
func dbOptSelectRow(tracked *[]dbOptRow, opt gosmo.DatabaseOption, label string, items []string, selected int) *propsheet.SelectRow {
	row := propsheet.Select(label, items, selected)
	*tracked = append(*tracked, dbOptRow{opt: opt, row: row, items: items})
	return row
}

// dbOptBoolRow is dbOptSelectRow specialised for the many plain ON/OFF
// database options.
func dbOptBoolRow(tracked *[]dbOptRow, opt gosmo.DatabaseOption, label string, value bool) *propsheet.SelectRow {
	idx := 0
	if value {
		idx = 1
	}
	return dbOptSelectRow(tracked, opt, label, onOff, idx)
}

// userAccessItems is the Restrict access dropdown, deliberately not one of the
// tracked options: it is written with SetUserAccess, whose ALTER carries
// WITH ROLLBACK IMMEDIATE. Through SetDatabaseOption the same choice emits a
// bare SET SINGLE_USER, which blocks until every other connection leaves — a
// dialog that appears to hang, on a database nobody can reconnect to.
var userAccessItems = []string{"MULTI_USER", "SINGLE_USER", "RESTRICTED_USER"}

// databaseOptionRows builds the option rows Database Properties > Options and
// New Database > Options share, tracked for a Dirty()-gated apply, plus the
// Restrict access row its two callers apply themselves. The two pages differ
// only in what they add around these — a compatibility level and the Broker
// static on the properties page, nothing on the new-database one.
func databaseOptionRows(o *gosmo.DatabaseOptions) ([]propsheet.Row, []dbOptRow, *propsheet.SelectRow) {
	var tracked []dbOptRow

	pageVerifyItems := []string{"NONE", "TORN_PAGE_DETECTION", "CHECKSUM"}
	containmentItems := []string{"NONE", "PARTIAL"}
	cursorDefaultItems := []string{"GLOBAL", "LOCAL"}
	snapshotIsolationOn := o.SnapshotIsolation == "ON" || o.SnapshotIsolation == "ENABLED"

	userAccessRow := propsheet.Select("Restrict access", userAccessItems,
		indexOf(userAccessItems, string(o.UserAccess)))

	rows := []propsheet.Row{
		propsheet.Section("Automatic"),
		dbOptBoolRow(&tracked, gosmo.DBOptAutoClose, "Auto close", o.AutoClose),
		dbOptBoolRow(&tracked, gosmo.DBOptAutoCreateStatistics, "Auto create statistics", o.AutoCreateStats),
		dbOptBoolRow(&tracked, gosmo.DBOptAutoShrink, "Auto shrink", o.AutoShrink),
		dbOptBoolRow(&tracked, gosmo.DBOptAutoUpdateStatistics, "Auto update statistics", o.AutoUpdateStats),
		dbOptBoolRow(&tracked, gosmo.DBOptAutoUpdateStatisticsAsync, "Auto update statistics async", o.AutoUpdateStatsAsync),
		propsheet.Section("Containment"),
		dbOptSelectRow(&tracked, gosmo.DBOptContainment, "Containment type", containmentItems, indexOf(containmentItems, o.Containment)),
		propsheet.Section("Cursor"),
		dbOptBoolRow(&tracked, gosmo.DBOptCursorCloseOnCommit, "Close cursor on commit", o.CursorCloseOnCommit),
		dbOptSelectRow(&tracked, gosmo.DBOptCursorDefault, "Default cursor", cursorDefaultItems, indexOf(cursorDefaultItems, o.DefaultCursor)),
		propsheet.Section("Miscellaneous"),
		dbOptBoolRow(&tracked, gosmo.DBOptANSINullDefault, "ANSI NULL default", o.ANSINullDefault),
		dbOptBoolRow(&tracked, gosmo.DBOptANSINulls, "ANSI NULLS enabled", o.ANSINulls),
		dbOptBoolRow(&tracked, gosmo.DBOptANSIPadding, "ANSI padding enabled", o.ANSIPadding),
		dbOptBoolRow(&tracked, gosmo.DBOptANSIWarnings, "ANSI warnings enabled", o.ANSIWarnings),
		dbOptBoolRow(&tracked, gosmo.DBOptArithAbort, "Arithmetic abort enabled", o.ArithAbort),
		dbOptBoolRow(&tracked, gosmo.DBOptConcatNullYieldsNull, "Concat null yields null", o.ConcatNullYieldsNull),
		dbOptBoolRow(&tracked, gosmo.DBOptNumericRoundAbort, "Numeric round-abort", o.NumericRoundAbort),
		dbOptBoolRow(&tracked, gosmo.DBOptQuotedIdentifier, "Quoted identifier", o.QuotedIdentifier),
		dbOptBoolRow(&tracked, gosmo.DBOptRecursiveTriggers, "Recursive triggers", o.RecursiveTriggers),
		dbOptBoolRow(&tracked, gosmo.DBOptReadCommittedSnapshot, "Read committed snapshot", o.ReadCommittedSnapshot),
		dbOptBoolRow(&tracked, gosmo.DBOptSnapshotIsolation, "Allow snapshot isolation", snapshotIsolationOn),
		dbOptSelectRow(&tracked, gosmo.DBOptPageVerify, "Page verify", pageVerifyItems, indexOf(pageVerifyItems, o.PageVerify)),
		userAccessRow,
		dbOptBoolRow(&tracked, gosmo.DBOptTrustworthy, "Trustworthy", o.IsTrustworthy),
	}
	return rows, tracked, userAccessRow
}

// needsExclusiveAccess reports whether setting opt needs the database to
// itself. Such an ALTER waits, with nothing bounding it, for every other
// session in the database to leave — a Properties dialog that appears to hang
// — so the Options page applies it WITH ROLLBACK IMMEDIATE, after asking (see
// exclusiveOptionWarning).
func needsExclusiveAccess(opt gosmo.DatabaseOption) bool {
	return opt == gosmo.DBOptReadCommittedSnapshot
}

// exclusiveOptionWarning is the Options page's Form.SetApplyConfirm function:
// the question an Apply must be answered yes to when a pending edit is one
// needsExclusiveAccess names. Same consequence, same wording, as renaming a
// database (databaseOps' renameWarning).
func exclusiveOptionWarning(tracked []dbOptRow) func() string {
	return func() string {
		for _, r := range tracked {
			if needsExclusiveAccess(r.opt) && r.row.Dirty() {
				return "Changing " + r.row.Label() + " needs exclusive access to the database. " +
					"Existing connections to it will be closed and their transactions rolled back."
			}
		}
		return ""
	}
}

// applyTrackedOptions writes every tracked row the user changed, as one
// ALTER DATABASE SET each. exclusive is the termination for the options that
// need exclusive access — TerminationNone where nothing else can be connected,
// a database this dialog has just created.
func applyTrackedOptions(ctx context.Context, d *gosmo.Database, tracked []dbOptRow, exclusive gosmo.Termination) error {
	for _, r := range tracked {
		if !r.row.Dirty() {
			continue
		}
		term := gosmo.TerminationNone
		if needsExclusiveAccess(r.opt) {
			term = exclusive
		}
		value := r.items[r.row.Selected()]
		if err := d.SetDatabaseOption(ctx, r.opt, value, term); err != nil {
			return err
		}
	}
	return nil
}

// applyRestrictAccess writes the Restrict access row through the method that
// carries WITH ROLLBACK IMMEDIATE — see userAccessItems.
func applyRestrictAccess(ctx context.Context, d *gosmo.Database, row *propsheet.SelectRow) error {
	if !row.Dirty() {
		return nil
	}
	return d.SetUserAccess(ctx, gosmo.UserAccess(userAccessItems[row.Selected()]))
}

func pageDatabaseOptions(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Options",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			o, err := d.Options(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows, tracked, userAccessRow := databaseOptionRows(o)

			compatItems := compatItemsFor(int(d.CompatibilityLevel), serverMajor(sc))
			compatRow := propsheet.Select("Compatibility level", compatItems,
				indexOf(compatItems, strconv.Itoa(int(d.CompatibilityLevel))))

			f := propsheet.NewForm(append(rows,
				propsheet.Static("Broker enabled", boolStr(o.IsBrokerEnabled)),
				compatRow)...)
			f.SetApplyConfirm(exclusiveOptionWarning(tracked))

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByName(ctx, dbName)
				if err != nil {
					return err
				}
				if err := applyTrackedOptions(ctx, d, tracked, gosmo.TerminationRollbackImmediate); err != nil {
					return err
				}
				if compatRow.Dirty() {
					n, err := strconv.Atoi(compatItems[compatRow.Selected()])
					if err != nil {
						return err
					}
					if err := d.SetCompatibilityLevel(ctx, gosmo.CompatibilityLevel(n)); err != nil {
						return err
					}
				}
				if err := applyRestrictAccess(ctx, d, userAccessRow); err != nil {
					return err
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
