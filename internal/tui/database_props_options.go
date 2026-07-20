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

			var tracked []dbOptRow

			pageVerifyItems := []string{"NONE", "TORN_PAGE_DETECTION", "CHECKSUM"}
			containmentItems := []string{"NONE", "PARTIAL"}
			cursorDefaultItems := []string{"GLOBAL", "LOCAL"}
			userAccessItems := []string{"MULTI_USER", "SINGLE_USER", "RESTRICTED_USER"}
			compatItems := []string{"100", "110", "120", "130", "140", "150", "160", "170"}
			snapshotIsolationOn := o.SnapshotIsolation == "ON" || o.SnapshotIsolation == "ENABLED"

			compatRow := propsheet.Select("Compatibility level", compatItems,
				indexOf(compatItems, strconv.Itoa(int(d.CompatibilityLevel()))))
			userAccessRow := propsheet.Select("Restrict access", userAccessItems,
				indexOf(userAccessItems, o.UserAccess))

			f := propsheet.NewForm(
				propsheet.Section("Automatic"),
				dbOptBoolRow(&tracked, gosmo.DBOptAutoClose, "Auto close", o.AutoClose),
				dbOptBoolRow(&tracked, gosmo.DBOptAutoCreateStatistics, "Auto create statistics", o.AutoCreateStats),
				dbOptBoolRow(&tracked, gosmo.DBOptAutoShrink, "Auto shrink", o.AutoShrink),
				dbOptBoolRow(&tracked, gosmo.DBOptAutoUpdateStatistics, "Auto update statistics", o.AutoUpdateStats),
				dbOptBoolRow(&tracked, gosmo.DBOptAutoUpdateStatisticsAsync, "Auto update statistics asynchronously", o.AutoUpdateStatsAsync),
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
				propsheet.Static("Broker enabled", boolStr(o.IsBrokerEnabled)),
				compatRow,
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				for _, r := range tracked {
					if !r.row.Dirty() {
						continue
					}
					value := r.items[r.row.Selected()]
					if err := d.SetDatabaseOptionContext(ctx, r.opt, value); err != nil {
						return err
					}
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
				if userAccessRow.Dirty() {
					mode := userAccessItems[userAccessRow.Selected()]
					if err := d.SetUserAccessContext(ctx, mode); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
