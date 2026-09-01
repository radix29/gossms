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
// tracked options: it is written with SetUserAccessContext, whose ALTER carries
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
		indexOf(userAccessItems, o.UserAccess))

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

// applyTrackedOptions writes every tracked row the user changed, as one
// ALTER DATABASE SET each.
func applyTrackedOptions(ctx context.Context, d *gosmo.Database, tracked []dbOptRow) error {
	for _, r := range tracked {
		if !r.row.Dirty() {
			continue
		}
		value := r.items[r.row.Selected()]
		if err := d.SetDatabaseOptionContext(ctx, r.opt, value); err != nil {
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
	return d.SetUserAccessContext(ctx, userAccessItems[row.Selected()])
}

func pageDatabaseOptions(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Options",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			o, err := d.OptionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows, tracked, userAccessRow := databaseOptionRows(o)

			compatItems := compatItemsFor(int(d.CompatibilityLevel()))
			compatRow := propsheet.Select("Compatibility level", compatItems,
				indexOf(compatItems, strconv.Itoa(int(d.CompatibilityLevel()))))

			f := propsheet.NewForm(append(rows,
				propsheet.Static("Broker enabled", boolStr(o.IsBrokerEnabled)),
				compatRow)...)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				if err := applyTrackedOptions(ctx, d, tracked); err != nil {
					return err
				}
				if compatRow.Dirty() {
					n, err := strconv.Atoi(compatItems[compatRow.Selected()])
					if err != nil {
						return err
					}
					if err := d.SetCompatibilityLevelContext(ctx, gosmo.CompatibilityLevel(n)); err != nil {
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
