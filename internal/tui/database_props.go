package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// databasePropPages builds the page set for Database Properties.
// General/Files/Filegroups stay read-only: General is an info page, and
// while gosmo can now resize/rename existing files and toggle a
// filegroup's default/read-only flag (database_files.go), a correct
// Add-a-new-file/filegroup flow needs several coordinated fields (name,
// type, path, filegroup, size, growth, max size) that don't fit this
// pass's time budget without risking an unverified, half-built UX —
// left for a follow-up. Options, Change Tracking, Permissions, and
// Extended Properties are fully editable.
func databasePropPages(sc *db.ServerConn, dbName string) []propPage {
	return []propPage{
		pageDatabaseGeneral(sc, dbName),
		pageDatabaseFiles(sc, dbName),
		pageDatabaseFilegroups(sc, dbName),
		pageDatabaseOptions(sc, dbName),
		pageDatabaseChangeTracking(sc, dbName),
		pageDatabasePermissions(sc, dbName),
		pageDatabaseExtendedProperties(sc, dbName),
	}
}

func pageDatabaseGeneral(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			space, err := d.SpaceUsedContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			opts, err := d.OptionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			history, err := sc.Server.BackupHistoryContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			lastFull, lastLog := "Never", "Never"
			for _, b := range history {
				switch b.BackupType {
				case gosmo.BackupActionDatabase:
					if lastFull == "Never" {
						lastFull = formatSQLDate(b.BackupFinish)
					}
				case gosmo.BackupActionLog:
					if lastLog == "Never" {
						lastLog = formatSQLDate(b.BackupFinish)
					}
				}
			}

			f := propsheet.NewForm(
				propsheet.Section("Database information"),
				propsheet.Static("Name", d.Name()),
				propsheet.Static("Status", d.State()),
				propsheet.Static("Owner", opts.Owner),
				propsheet.Static("Date created", formatSQLDate(d.CreateDate())),
				propsheet.Static("Size (MB)", fmt.Sprintf("%.2f", space.TotalMB)),
				propsheet.Static("Space available (MB)", fmt.Sprintf("%.2f", space.UnallocatedMB)),
				propsheet.Static("Number of users", strconv.Itoa(len(users))),
				propsheet.Section("Maintenance"),
				propsheet.Static("Collation", d.Collation()),
				propsheet.Static("Compatibility level", strconv.Itoa(int(d.CompatibilityLevel()))),
				propsheet.Static("Recovery model", string(d.RecoveryModel())),
				propsheet.Static("Page verify", opts.PageVerify),
				propsheet.Static("Read-only", boolStr(d.IsReadOnly())),
				propsheet.Static("Auto close", boolStr(opts.AutoClose)),
				propsheet.Static("Auto shrink", boolStr(opts.AutoShrink)),
				propsheet.Static("Last database backup", lastFull),
				propsheet.Static("Last log backup", lastLog),
			)
			return f, nil, nil
		},
	}
}

func pageDatabaseFiles(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Files",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			opts, err := d.OptionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			files, err := d.FilesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(files))
			for i, fl := range files {
				maxSize := "Unlimited"
				if fl.MaxSizeKB >= 0 {
					maxSize = strconv.FormatInt(fl.MaxSizeKB/1024, 10) + " MB"
				}
				growth := strconv.FormatInt(fl.GrowthKB/1024, 10) + " MB"
				if fl.IsPercentGrowth {
					growth = strconv.Itoa(fl.GrowthPercent) + "%"
				}
				rows[i] = []string{
					fl.Name, fl.Type, fl.FileGroup,
					strconv.FormatInt(fl.SizeKB/1024, 10), growth, maxSize, fl.PhysicalName,
				}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Database files"),
				propsheet.Static("Owner", opts.Owner),
				propsheet.NewGridRow(grid, 10),
			)
			return f, nil, nil
		},
	}
}

func pageDatabaseFilegroups(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Filegroups",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			fgs, err := d.FileGroupsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(fgs))
			for i, fg := range fgs {
				// gosmo.FileGroup doesn't currently expose is_read_only
				// (sys.filegroups); shown as N/A rather than guessed.
				rows[i] = []string{fg.Name, strconv.Itoa(len(fg.Files)), "N/A", boolStr(fg.IsDefault)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Name", "Files", "Read-only", "Default"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Filegroups"),
				propsheet.NewGridRow(grid, 10),
			)
			return f, nil, nil
		},
	}
}

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
			compatItems := []string{"100", "110", "120", "130", "140", "150", "160"}
			snapshotIsolationOn := o.SnapshotIsolation == "ON" || o.SnapshotIsolation == "ENABLED"

			compatRow := propsheet.Select("Compatibility level", compatItems,
				indexOf(compatItems, strconv.Itoa(int(d.CompatibilityLevel()))))

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
				propsheet.Static("Restrict access", o.UserAccess),
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
				return nil
			}
			return f, apply, nil
		},
	}
}

var retentionUnits = []string{"DAYS", "HOURS", "MINUTES"}

func pageDatabaseChangeTracking(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Change Tracking",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			ct, err := d.ChangeTrackingContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			tables, err := d.TableChangeTrackingContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			text := make([][]string, len(tables))
			values := make([][]bool, len(tables))
			for i, t := range tables {
				text[i] = []string{t.Schema + "." + t.Name}
				values[i] = []bool{t.Enabled, t.TrackColumnsUpdated}
			}
			tablesRow := propsheet.NewToggleGrid([]string{"Table name", "Enabled", "Track columns updated"}, []int{1, 2}, 10)
			tablesRow.SetRows(text, values)

			enabledRow := propsheet.Select("Change tracking", onOff, boolIdx(ct.Enabled))
			retentionRow := propsheet.Int("Retention period", int64(ct.RetentionPeriod), 1, 100000, "")
			unitRow := propsheet.Select("Retention period units", retentionUnits, indexOf(retentionUnits, orDefault(ct.RetentionUnit, "DAYS")))
			autoCleanupRow := propsheet.Select("Auto cleanup", onOff, boolIdx(ct.AutoCleanup))

			f := propsheet.NewForm(
				propsheet.Section("Change Tracking"),
				enabledRow, retentionRow, unitRow, autoCleanupRow,
				propsheet.Section("Tables using change tracking"),
				tablesRow,
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				if enabledRow.Dirty() || retentionRow.Dirty() || unitRow.Dirty() || autoCleanupRow.Dirty() {
					period, err := retentionRow.IntValue()
					if err != nil {
						return err
					}
					info := gosmo.ChangeTrackingInfo{
						Enabled:         enabledRow.Selected() == 1,
						AutoCleanup:     autoCleanupRow.Selected() == 1,
						RetentionPeriod: int(period),
						RetentionUnit:   retentionUnits[unitRow.Selected()],
					}
					if err := d.SetChangeTrackingContext(ctx, info); err != nil {
						return err
					}
				}
				for i, v := range tablesRow.Values() {
					t := tables[i]
					if v[0] == t.Enabled && v[1] == t.TrackColumnsUpdated {
						continue
					}
					if err := d.SetTableChangeTrackingContext(ctx, t.Schema, t.Name, v[0], v[1]); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

func pageDatabasePermissions(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			perms, err := d.DatabasePermissionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			entries := make([]permEntry, len(perms))
			for i, p := range perms {
				entries[i] = permEntry{
					Principal: p.Principal, PrincipalType: p.PrincipalType,
					Grantor: p.Grantor, Permission: p.Permission, State: p.State,
				}
			}
			f, apply := buildPermissionsForm("Database-level permissions", entries, 10,
				d.GrantDatabasePermissionContext, d.DenyDatabasePermissionContext, d.RevokeDatabasePermissionContext)
			return f, apply, nil
		},
	}
}

// extPropEdit tracks one extended property's pending state: an existing
// property whose value changed, or a brand-new one pending Add.
type extPropEdit struct {
	name          string
	origValue     string
	value         string
	isNew         bool
	pendingRemove bool
}

func pageDatabaseExtendedProperties(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Extended Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			props, err := d.DatabaseExtendedProperties()
			if err != nil {
				return nil, nil, err
			}
			edits := make([]*extPropEdit, 0, len(props))
			for _, p := range props {
				edits = append(edits, &extPropEdit{name: p.Name, origValue: p.Value, value: p.Value})
			}

			visible := func() []*extPropEdit {
				out := make([]*extPropEdit, 0, len(edits))
				for _, e := range edits {
					if !e.pendingRemove {
						out = append(out, e)
					}
				}
				return out
			}
			rowsFor := func() [][]string {
				vis := visible()
				rows := make([][]string, len(vis))
				for i, e := range vis {
					rows[i] = []string{e.name, e.value}
				}
				return rows
			}

			grid := controls.NewDataGrid()
			grid.SetData([]string{"Name", "Value"}, rowsFor())

			nameField := propsheet.Text("Name", "", 24)
			valueField := propsheet.Text("Value", "", 30)
			selected := func() *extPropEdit {
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					return nil
				}
				return vis[i]
			}
			// current tracks whichever edit the fields below the grid are
			// currently showing, so a value typed into valueField can be
			// committed back into it before the selection moves on (a
			// plain OnSelectRow callback only tells us the *new*
			// selection, not which edit the still-visible field text
			// belongs to).
			var current *extPropEdit
			commitCurrent := func() {
				if current != nil {
					current.value = valueField.Value()
				}
			}
			syncFieldsFromSelection := func() {
				current = selected()
				if current != nil {
					nameField.SetValue(current.name)
					valueField.SetValue(current.value)
				} else {
					nameField.SetValue("")
					valueField.SetValue("")
				}
			}
			grid.OnSelectRow = func(row int) {
				commitCurrent()
				syncFieldsFromSelection()
			}
			syncFieldsFromSelection() // seed `current` for the initial selection (row 0)

			var addBtn, removeBtn *widgets.Button
			addBtn = widgets.NewButton("Add", func() {
				commitCurrent()
				name := nameField.Value()
				if name == "" {
					return
				}
				for _, e := range visible() {
					if e.name == name {
						return // already present — edit its Value row instead
					}
				}
				edits = append(edits, &extPropEdit{name: name, value: valueField.Value(), isNew: true})
				grid.SetData([]string{"Name", "Value"}, rowsFor())
				grid.SetSelectedRow(len(visible()) - 1)
				syncFieldsFromSelection()
			})
			removeBtn = widgets.NewButton("Remove", func() {
				if e := selected(); e != nil {
					e.pendingRemove = true
					current = nil // its old value is void; don't let commitCurrent write back into it
					grid.SetData([]string{"Name", "Value"}, rowsFor())
					grid.SetSelectedRow(0)
					syncFieldsFromSelection()
				}
			})

			gridRow := propsheet.NewGridRow(grid, 12)
			dirty := func() bool {
				for _, e := range edits {
					if e.pendingRemove || e.isNew || e.value != e.origValue {
						return true
					}
				}
				return false
			}
			gridRow.DirtyFn = dirty
			gridRow.RevertFn = func() {
				kept := edits[:0]
				for _, e := range edits {
					if e.isNew {
						continue
					}
					e.value = e.origValue
					e.pendingRemove = false
					kept = append(kept, e)
				}
				edits = kept
				grid.SetData([]string{"Name", "Value"}, rowsFor())
			}

			f := propsheet.NewForm(
				propsheet.Section("Extended properties"),
				gridRow,
				propsheet.Section("Selected property"),
				nameField, valueField,
				propsheet.Buttons(addBtn, removeBtn),
			)
			apply := func(ctx context.Context) error {
				commitCurrent()
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				level := gosmo.ExtendedPropertyLevel{}
				for _, e := range edits {
					switch {
					case e.pendingRemove && !e.isNew:
						if err := d.DropExtendedPropertyContext(ctx, e.name, level); err != nil {
							return err
						}
					case e.isNew && !e.pendingRemove:
						if err := d.AddExtendedPropertyContext(ctx, e.name, e.value, level); err != nil {
							return err
						}
					case !e.isNew && !e.pendingRemove && e.value != e.origValue:
						if err := d.SetExtendedPropertyContext(ctx, e.name, e.value, level); err != nil {
							return err
						}
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
