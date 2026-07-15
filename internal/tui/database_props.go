package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// databasePropPages builds the page set for Database Properties. General
// is mostly a read-only info page, aside from Owner/Recovery model; every
// other page is fully or partially editable — Files/Filegroups support
// rename/resize/growth/max size and Add/Remove, Database Scoped
// Configurations covers the well-known options with a read-only dump of
// the rest, and Query Store exposes its full configuration plus
// Flush/Clear actions.
func databasePropPages(sc *db.ServerConn, dbName string) []propPage {
	return []propPage{
		pageDatabaseGeneral(sc, dbName),
		pageDatabaseFiles(sc, dbName),
		pageDatabaseFilegroups(sc, dbName),
		pageDatabaseOptions(sc, dbName),
		pageDatabaseChangeTracking(sc, dbName),
		pageDatabaseQueryStore(sc, dbName),
		pageDatabasePermissions(sc, dbName),
		pageDatabaseExtendedProperties(sc, dbName),
		pageDatabaseScopedConfig(sc, dbName),
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
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			loginNames := make([]string, len(logins))
			for i, l := range logins {
				loginNames[i] = l.Name
			}
			sort.Strings(loginNames)
			lastFull, lastDiff, lastLog := "Never", "Never", "Never"
			for _, b := range history {
				switch b.BackupType {
				case gosmo.BackupActionDatabase:
					if lastFull == "Never" {
						lastFull = formatSQLDate(b.BackupFinish)
					}
				case gosmo.BackupActionDifferential:
					if lastDiff == "Never" {
						lastDiff = formatSQLDate(b.BackupFinish)
					}
				case gosmo.BackupActionLog:
					if lastLog == "Never" {
						lastLog = formatSQLDate(b.BackupFinish)
					}
				}
			}

			ownerRow := propsheet.Select("Owner", loginNames, indexOf(loginNames, opts.Owner))
			recoveryItems := []string{"SIMPLE", "FULL", "BULK_LOGGED"}
			recoveryRow := propsheet.Select("Recovery model", recoveryItems, indexOf(recoveryItems, string(d.RecoveryModel())))

			f := propsheet.NewForm(
				propsheet.Section("Database information"),
				propsheet.Static("Name", d.Name()),
				propsheet.Static("Status", d.State()),
				ownerRow,
				propsheet.Static("Date created", formatSQLDate(d.CreateDate())),
				propsheet.Static("Size (MB)", fmt.Sprintf("%.2f", space.TotalMB)),
				propsheet.Static("Space available (MB)", fmt.Sprintf("%.2f", space.UnallocatedMB)),
				propsheet.Static("Number of users", strconv.Itoa(len(users))),
				propsheet.Section("Maintenance"),
				propsheet.Static("Collation", d.Collation()),
				propsheet.Static("Compatibility level", strconv.Itoa(int(d.CompatibilityLevel()))),
				recoveryRow,
				propsheet.Static("Page verify", opts.PageVerify),
				propsheet.Static("Auto close", boolStr(opts.AutoClose)),
				propsheet.Static("Auto shrink", boolStr(opts.AutoShrink)),
				propsheet.Static("Last database backup", lastFull),
				propsheet.Static("Last differential backup", lastDiff),
				propsheet.Static("Last log backup", lastLog),
				propsheet.Section("Containment"),
				propsheet.Static("Containment type", opts.Containment),
				propsheet.Static("Encrypted", boolStr(opts.IsEncrypted)),
				propsheet.Static("Trustworthy", boolStr(opts.IsTrustworthy)),
				propsheet.Static("Read only", boolStr(d.IsReadOnly())),
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				if ownerRow.Dirty() {
					if err := d.SetOwnerContext(ctx, ownerRow.Value()); err != nil {
						return err
					}
				}
				if recoveryRow.Dirty() {
					model := gosmo.RecoveryModel(recoveryItems[recoveryRow.Selected()])
					if err := d.SetRecoveryModelContext(ctx, model); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// fileEdit tracks one Files-page row's pending state: an existing file
// whose logical name/size/growth/max size changed, a brand-new file
// pending Add (isNew), or an existing file pending Remove.
type fileEdit struct {
	origName string // "" for a brand-new file
	isNew    bool

	name      string // current (possibly renamed) logical name
	fileType  string // "ROWS" or "LOG"; fixed for an existing file, chosen for a new one
	fileGroup string // "" for LOG files
	path      string // only meaningful for a new file — MODIFY FILE can't move/rename the physical file

	sizeKB          int64
	isPercentGrowth bool
	growthKB        int64
	growthPercent   int
	maxSizeKB       int64 // -1 = unlimited

	// originals, for diffing at apply time (zero-valued when isNew)
	origSizeKB          int64
	origIsPercentGrowth bool
	origGrowthKB        int64
	origGrowthPercent   int
	origMaxSizeKB       int64

	pendingRemove bool
}

func fileEditFromInfo(fl *gosmo.DatabaseFileInfo) *fileEdit {
	return &fileEdit{
		origName: fl.Name, name: fl.Name, fileType: fl.Type, fileGroup: fl.FileGroup, path: fl.PhysicalName,
		sizeKB: fl.SizeKB, isPercentGrowth: fl.IsPercentGrowth, growthKB: fl.GrowthKB, growthPercent: fl.GrowthPercent, maxSizeKB: fl.MaxSizeKB,
		origSizeKB: fl.SizeKB, origIsPercentGrowth: fl.IsPercentGrowth, origGrowthKB: fl.GrowthKB, origGrowthPercent: fl.GrowthPercent, origMaxSizeKB: fl.MaxSizeKB,
	}
}

func growthText(isPercent bool, growthKB int64, growthPercent int) string {
	if isPercent {
		return strconv.Itoa(growthPercent) + "%"
	}
	return strconv.FormatInt(growthKB/1024, 10) + " MB"
}

func maxSizeText(maxSizeKB int64) string {
	if maxSizeKB < 0 {
		return "Unlimited"
	}
	return strconv.FormatInt(maxSizeKB/1024, 10) + " MB"
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
			fgs, err := d.FileGroupsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			fgNames := make([]string, len(fgs))
			for i, fg := range fgs {
				fgNames[i] = fg.Name
			}

			edits := make([]*fileEdit, len(files))
			for i, fl := range files {
				edits[i] = fileEditFromInfo(fl)
			}

			visible := func() []*fileEdit {
				out := make([]*fileEdit, 0, len(edits))
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
					rows[i] = []string{
						e.name, e.fileType, e.fileGroup,
						strconv.FormatInt(e.sizeKB/1024, 10),
						growthText(e.isPercentGrowth, e.growthKB, e.growthPercent),
						maxSizeText(e.maxSizeKB), e.path,
					}
				}
				return rows
			}

			grid := controls.NewDataGrid()
			grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rowsFor())
			grid.SetCellCursor(true)

			nameField := propsheet.Text("Logical name", "", 24)
			typeSelect := propsheet.Select("File type", []string{"ROWS", "LOG"}, 0)
			filegroupSelect := propsheet.Select("Filegroup", fgNames, 0)
			pathField := propsheet.Text("Path", "", 40)
			sizeField := propsheet.Int("Initial size", 0, 0, 16777216, "MB")
			growthKind := propsheet.Radio("Growth by", []string{"Megabytes", "Percent"}, 0)
			growthField := propsheet.Int("Growth amount", 0, 0, 2097151, "")
			maxKind := propsheet.Radio("Max size", []string{"Unlimited", "Limited"}, 0)
			maxField := propsheet.Int("Max size limit", 0, 0, 16777216, "MB")

			selected := func() *fileEdit {
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					return nil
				}
				return vis[i]
			}
			var current *fileEdit
			commitCurrent := func() {
				if current == nil {
					return
				}
				current.name = nameField.Value()
				current.fileType = typeSelect.Value()
				current.fileGroup = filegroupSelect.Value()
				current.path = pathField.Value()
				if n, err := sizeField.IntValue(); err == nil {
					current.sizeKB = n * 1024
				}
				current.isPercentGrowth = growthKind.Selected() == 1
				if n, err := growthField.IntValue(); err == nil {
					if current.isPercentGrowth {
						current.growthPercent = int(n)
					} else {
						current.growthKB = n * 1024
					}
				}
				if maxKind.Selected() == 0 {
					current.maxSizeKB = -1
				} else if n, err := maxField.IntValue(); err == nil {
					current.maxSizeKB = n * 1024
				}
			}
			syncFieldsFromSelection := func() {
				current = selected()
				if current == nil {
					nameField.SetValue("")
					pathField.SetValue("")
					sizeField.SetValue("0")
					growthField.SetValue("0")
					maxField.SetValue("0")
					return
				}
				nameField.SetValue(current.name)
				typeSelect.SetSelected(indexOf([]string{"ROWS", "LOG"}, current.fileType))
				filegroupSelect.SetSelected(indexOf(fgNames, current.fileGroup))
				pathField.SetValue(current.path)
				sizeField.SetValue(strconv.FormatInt(current.sizeKB/1024, 10))
				if current.isPercentGrowth {
					growthKind.SetSelected(1)
					growthField.SetValue(strconv.Itoa(current.growthPercent))
				} else {
					growthKind.SetSelected(0)
					growthField.SetValue(strconv.FormatInt(current.growthKB/1024, 10))
				}
				if current.maxSizeKB < 0 {
					maxKind.SetSelected(0)
					maxField.SetValue("0")
				} else {
					maxKind.SetSelected(1)
					maxField.SetValue(strconv.FormatInt(current.maxSizeKB/1024, 10))
				}
			}
			grid.OnSelectRow = func(row int) {
				commitCurrent()
				syncFieldsFromSelection()
			}
			syncFieldsFromSelection()

			var addBtn, removeBtn *widgets.Button
			addBtn = widgets.NewButton("Add", func() {
				// Deliberately does NOT call commitCurrent(): these fields
				// double as the previously-selected file's live edit, and
				// commitCurrent() writes nameField's text into that file's
				// rename target — if the user typed a brand-new name here
				// intending to Add, that write would silently rename the
				// wrong file instead (Logical name is the one field this
				// page lets you both edit-in-place and repurpose for Add,
				// unlike Extended Properties' Name, which is never
				// rewritten by its own commitCurrent). Any not-yet-applied
				// edit to the previously selected file is simply left as
				// last synced from its own selection instead.
				name := nameField.Value()
				if name == "" {
					return
				}
				for _, e := range visible() {
					if e.name == name {
						return // already present — edit its row instead
					}
				}
				e := &fileEdit{
					isNew: true, name: name, fileType: typeSelect.Value(), fileGroup: filegroupSelect.Value(), path: pathField.Value(),
					maxSizeKB: -1,
				}
				if n, err := sizeField.IntValue(); err == nil {
					e.sizeKB = n * 1024
				}
				e.isPercentGrowth = growthKind.Selected() == 1
				if n, err := growthField.IntValue(); err == nil {
					if e.isPercentGrowth {
						e.growthPercent = int(n)
					} else {
						e.growthKB = n * 1024
					}
				}
				if maxKind.Selected() == 1 {
					if n, err := maxField.IntValue(); err == nil {
						e.maxSizeKB = n * 1024
					}
				}
				edits = append(edits, e)
				grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rowsFor())
				grid.SetSelectedRow(len(visible()) - 1)
				syncFieldsFromSelection()
			})
			removeBtn = widgets.NewButton("Remove", func() {
				if e := selected(); e != nil {
					e.pendingRemove = true
					current = nil
					grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rowsFor())
					grid.SetSelectedRow(0)
					syncFieldsFromSelection()
				}
			})

			gridRow := propsheet.NewGridRow(grid, 10)
			dirty := func() bool {
				for _, e := range edits {
					if e.isNew || e.pendingRemove {
						return true
					}
					if e.name != e.origName || e.sizeKB != e.origSizeKB ||
						e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB ||
						e.growthPercent != e.origGrowthPercent || e.maxSizeKB != e.origMaxSizeKB {
						return true
					}
				}
				return false
			}
			gridRow.DirtyFn = dirty
			gridRow.RevertFn = func() {
				edits = edits[:0]
				for _, fl := range files {
					edits = append(edits, fileEditFromInfo(fl))
				}
				grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rowsFor())
				syncFieldsFromSelection()
			}

			f := propsheet.NewForm(
				propsheet.Section("Database files"),
				propsheet.Static("Owner", opts.Owner),
				gridRow,
				propsheet.Section("Selected file"),
				nameField, typeSelect, filegroupSelect, sizeField,
				growthKind, growthField, maxKind, maxField, pathField,
				propsheet.Buttons(addBtn, removeBtn),
			)

			apply := func(ctx context.Context) error {
				commitCurrent()
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				for _, e := range edits {
					switch {
					case e.pendingRemove && !e.isNew:
						if err := d.RemoveFileContext(ctx, e.origName); err != nil {
							return err
						}
					case e.isNew && !e.pendingRemove:
						spec := gosmo.DatabaseFileSpec{
							Name: e.name, Type: e.fileType, Path: e.path, SizeKB: e.sizeKB, MaxSizeKB: e.maxSizeKB,
						}
						if e.fileType != "LOG" {
							spec.FileGroup = e.fileGroup
						}
						if e.isPercentGrowth {
							spec.GrowthPercent = e.growthPercent
						} else {
							spec.GrowthKB = e.growthKB
						}
						if err := d.AddFileContext(ctx, spec); err != nil {
							return err
						}
					case !e.isNew && !e.pendingRemove:
						growthChanged := e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB || e.growthPercent != e.origGrowthPercent
						maxSizeChanged := e.maxSizeKB != e.origMaxSizeKB
						nameChanged := e.name != e.origName
						sizeChanged := e.sizeKB != e.origSizeKB
						if !nameChanged && !sizeChanged && !growthChanged && !maxSizeChanged {
							continue // nothing about this file actually changed
						}
						var m gosmo.FileModify
						if nameChanged {
							m.NewName = e.name
						}
						if sizeChanged {
							m.SizeKB = e.sizeKB
						}
						if growthChanged {
							if e.isPercentGrowth {
								m.GrowthPercent = e.growthPercent
							} else {
								m.GrowthKB = e.growthKB
							}
						}
						if maxSizeChanged {
							m.MaxSizeKB = e.maxSizeKB
						}
						if err := d.AlterFileContext(ctx, e.origName, m); err != nil {
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

// fgEdit tracks one Filegroups-page row's pending state, mirroring
// fileEdit's isNew/pendingRemove shape. Read-only/Default toggles live in
// the ToggleGridRow itself (see syncToggles); fgEdit only needs to know
// their loaded baseline to diff against at apply time.
type fgEdit struct {
	name          string // "" for a brand-new filegroup
	fileCount     int
	isNew         bool
	pendingRemove bool
	isReadOnly    bool
	origReadOnly  bool
	isDefault     bool
	origIsDefault bool
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

			edits := make([]*fgEdit, len(fgs))
			for i, fg := range fgs {
				edits[i] = &fgEdit{
					name: fg.Name, fileCount: len(fg.Files),
					isReadOnly: fg.IsReadOnly, origReadOnly: fg.IsReadOnly,
					isDefault: fg.IsDefault, origIsDefault: fg.IsDefault,
				}
			}

			visible := func() []*fgEdit {
				out := make([]*fgEdit, 0, len(edits))
				for _, e := range edits {
					if !e.pendingRemove {
						out = append(out, e)
					}
				}
				return out
			}
			rowsFor := func() ([][]string, [][]bool) {
				vis := visible()
				text := make([][]string, len(vis))
				values := make([][]bool, len(vis))
				for i, e := range vis {
					text[i] = []string{e.name, strconv.Itoa(e.fileCount)}
					values[i] = []bool{e.isReadOnly, e.isDefault}
				}
				return text, values
			}
			fgRow := propsheet.NewToggleGrid([]string{"Name", "Files", "Read-only", "Default"}, []int{2, 3}, min(len(fgs)+3, 10))
			text, values := rowsFor()
			fgRow.SetRows(text, values)

			// syncToggles pulls the grid's current toggle state back into
			// edits before any row-count change (Add/Remove) or Apply —
			// SetRows resets ToggleGridRow's own dirty baseline every time
			// it's called, so edits is the only durable record.
			syncToggles := func() {
				vis := visible()
				for i, v := range fgRow.Values() {
					if i < len(vis) {
						vis[i].isReadOnly, vis[i].isDefault = v[0], v[1]
					}
				}
			}

			nameField := propsheet.Text("New filegroup name", "", 24)
			var addBtn, removeBtn *widgets.Button
			addBtn = widgets.NewButton("Add", func() {
				syncToggles()
				name := nameField.Value()
				if name == "" {
					return
				}
				for _, e := range visible() {
					if e.name == name {
						return
					}
				}
				edits = append(edits, &fgEdit{name: name, isNew: true})
				text, values := rowsFor()
				fgRow.SetRows(text, values)
				nameField.SetValue("")
			})
			removeBtn = widgets.NewButton("Remove", func() {
				syncToggles()
				vis := visible()
				row := fgRow.Grid.SelectedRow()
				if row < 0 || row >= len(vis) {
					return
				}
				vis[row].pendingRemove = true
				text, values := rowsFor()
				fgRow.SetRows(text, values)
			})

			fgRow.DirtyFn = func() bool {
				syncToggles()
				for _, e := range edits {
					if e.isNew || e.pendingRemove || e.isReadOnly != e.origReadOnly || e.isDefault != e.origIsDefault {
						return true
					}
				}
				return false
			}
			fgRow.RevertFn = func() {
				edits = edits[:0]
				for _, fg := range fgs {
					edits = append(edits, &fgEdit{
						name: fg.Name, fileCount: len(fg.Files),
						isReadOnly: fg.IsReadOnly, origReadOnly: fg.IsReadOnly,
						isDefault: fg.IsDefault, origIsDefault: fg.IsDefault,
					})
				}
				text, values := rowsFor()
				fgRow.SetRows(text, values)
			}

			f := propsheet.NewForm(
				propsheet.Section("Filegroups"),
				fgRow,
				propsheet.Section("Add filegroup"),
				nameField,
				propsheet.Buttons(addBtn, removeBtn),
				propsheet.Note("Only one row-data filegroup can be the default. A filegroup must be empty before it can be removed."),
			)

			apply := func(ctx context.Context) error {
				syncToggles()
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				for _, e := range edits {
					switch {
					case e.pendingRemove && !e.isNew:
						if err := d.RemoveFileGroupContext(ctx, e.name); err != nil {
							return err
						}
						continue
					case e.isNew && !e.pendingRemove:
						if err := d.AddFileGroupContext(ctx, e.name); err != nil {
							return err
						}
					}
					if e.pendingRemove {
						continue
					}
					if e.isReadOnly != e.origReadOnly {
						if err := d.SetFileGroupReadOnlyContext(ctx, e.name, e.isReadOnly); err != nil {
							return err
						}
					}
					if e.isDefault && !e.origIsDefault {
						if err := d.SetDefaultFileGroupContext(ctx, e.name); err != nil {
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
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.DatabaseRolesContext(ctx)
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
			principals := make([]permPrincipal, 0, len(users)+len(roles))
			for _, u := range users {
				principals = append(principals, permPrincipal{Name: u.Name, Type: u.UserType})
			}
			for _, r := range roles {
				principals = append(principals, permPrincipal{Name: r.Name, Type: "DATABASE_ROLE"})
			}

			f, apply := buildPermissionsMatrix(principals, gosmo.DatabasePermissionNames(), entries, 8, 12,
				d.GrantDatabasePermissionContext, d.DenyDatabasePermissionContext, d.RevokeDatabasePermissionContext)
			return f, apply, nil
		},
	}
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
			f, apply := buildExtendedPropertiesForm(sc, dbName, gosmo.ExtendedPropertyLevel{}, props)
			return f, apply, nil
		},
	}
}

// pageDatabaseQueryStore exposes Query Store's configuration (operation
// mode, capture mode, storage/cleanup, capture policy) plus its two
// maintenance actions as Apply-gated checkboxes — Flush and Clear are
// plain writes like everything else on this page, so they go through the
// same dirty-tracking/Apply/Script Changes pipeline as every other
// change here rather than firing immediately off a button click.
func pageDatabaseQueryStore(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Query Store",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			qs, err := d.QueryStoreContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			stateItems := []string{"OFF", "READ_ONLY", "READ_WRITE"}
			captureItems := []string{"NONE", "AUTO", "ALL", "CUSTOM"}
			cleanupItems := []string{"AUTO", "OFF"}

			stateRow := propsheet.Select("Requested state", stateItems, indexOf(stateItems, qs.DesiredState))
			captureRow := propsheet.Select("Query capture mode", captureItems, indexOf(captureItems, qs.CaptureMode))
			maxSizeRow := propsheet.Int("Max size", qs.MaxStorageMB, 10, 2147483647, "MB")
			cleanupRow := propsheet.Select("Size based cleanup mode", cleanupItems, indexOf(cleanupItems, qs.SizeCleanupMode))
			staleRow := propsheet.Int("Stale query threshold", int64(qs.StaleThresholdDays), 0, 999999, "days")
			flushIntervalRow := propsheet.Int("Data flush interval", int64(qs.FlushIntervalSec), 1, 86400, "sec")
			intervalRow := propsheet.Int("Statistics interval", int64(qs.IntervalMinutes), 1, 1440, "min")
			maxPlansRow := propsheet.Int("Max plans per query", int64(qs.MaxPlansPerQuery), 0, 999999, "")
			waitStatsRow := propsheet.Select("Wait stats capture", onOff, indexOf(onOff, qs.WaitStatsCaptureMode))
			execCountRow := propsheet.Int("Custom: execution count", int64(qs.CapturePolicyExecCount), 0, 999999, "")
			compileCPURow := propsheet.Int("Custom: total compile CPU", qs.CapturePolicyCompileCPUMs, 0, 999999999, "ms")
			execCPURow := propsheet.Int("Custom: total execution CPU", qs.CapturePolicyExecCPUMs, 0, 999999999, "ms")
			staleHoursRow := propsheet.Int("Custom: stale capture threshold", int64(qs.CapturePolicyStaleHours), 0, 999999, "hours")

			flushCheck := propsheet.Check("Flush data to disk on Apply", false)
			clearCheck := propsheet.Check("Clear Query Store on Apply", false)

			f := propsheet.NewForm(
				propsheet.Section("Operation mode"),
				propsheet.Static("Actual state", qs.ActualState),
				stateRow,
				captureRow,
				propsheet.Section("Storage"),
				propsheet.Static("Current size", strconv.FormatInt(qs.CurrentStorageMB, 10)+" MB"),
				maxSizeRow,
				cleanupRow,
				staleRow,
				propsheet.Section("Capture policy"),
				flushIntervalRow,
				intervalRow,
				maxPlansRow,
				waitStatsRow,
				execCountRow, compileCPURow, execCPURow, staleHoursRow,
				propsheet.Section("Actions"),
				flushCheck, clearCheck,
				propsheet.Note("Flush and Clear take effect the next time you Apply or OK, same as every other change on this page."),
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				if stateRow.Dirty() || captureRow.Dirty() || maxSizeRow.Dirty() || cleanupRow.Dirty() ||
					staleRow.Dirty() || flushIntervalRow.Dirty() || intervalRow.Dirty() || maxPlansRow.Dirty() ||
					waitStatsRow.Dirty() || execCountRow.Dirty() || compileCPURow.Dirty() || execCPURow.Dirty() || staleHoursRow.Dirty() {
					maxSize, err := maxSizeRow.IntValue()
					if err != nil {
						return err
					}
					stale, err := staleRow.IntValue()
					if err != nil {
						return err
					}
					flushInterval, err := flushIntervalRow.IntValue()
					if err != nil {
						return err
					}
					interval, err := intervalRow.IntValue()
					if err != nil {
						return err
					}
					maxPlans, err := maxPlansRow.IntValue()
					if err != nil {
						return err
					}
					execCount, err := execCountRow.IntValue()
					if err != nil {
						return err
					}
					compileCPU, err := compileCPURow.IntValue()
					if err != nil {
						return err
					}
					execCPU, err := execCPURow.IntValue()
					if err != nil {
						return err
					}
					staleHours, err := staleHoursRow.IntValue()
					if err != nil {
						return err
					}
					opts := gosmo.QueryStoreOptions{
						DesiredState: stateItems[stateRow.Selected()], MaxStorageMB: maxSize,
						CaptureMode: captureItems[captureRow.Selected()], SizeCleanupMode: cleanupItems[cleanupRow.Selected()],
						StaleThresholdDays: int(stale), FlushIntervalSec: int(flushInterval), IntervalMinutes: int(interval),
						MaxPlansPerQuery: int(maxPlans), WaitStatsCaptureMode: onOff[waitStatsRow.Selected()],
						CapturePolicyExecCount: int(execCount), CapturePolicyCompileCPUMs: compileCPU,
						CapturePolicyExecCPUMs: execCPU, CapturePolicyStaleHours: int(staleHours),
					}
					if err := d.SetQueryStoreOptionsContext(ctx, opts); err != nil {
						return err
					}
				}
				if flushCheck.Checked() {
					if err := d.FlushQueryStoreContext(ctx); err != nil {
						return err
					}
				}
				if clearCheck.Checked() {
					if err := d.ClearQueryStoreContext(ctx); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// scopedConfigRow pairs an editable Int row with the database scoped
// configuration option name it edits, so a page's apply closure can write
// back only the ones that changed.
type scopedConfigRow struct {
	name string
	row  *propsheet.TextRow
}

// scopedConfigBoolRow is scopedConfigRow's Select-row counterpart, for
// ON/OFF-style options — SetDatabaseScopedConfigContext takes the keyword
// ALTER DATABASE SCOPED CONFIGURATION expects ("ON"/"OFF"), not the "0"/"1"
// sys.database_scoped_configurations reports a boolean option's value as.
type scopedConfigBoolRow struct {
	name string
	row  *propsheet.SelectRow
}

func findScopedConfig(configs []*gosmo.DatabaseScopedConfig, name string) *gosmo.DatabaseScopedConfig {
	for _, c := range configs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// newScopedConfigIntEditor returns a builder that creates an editable Int
// row for a named scoped configuration option, appending it to *tracked.
// An option missing on this server/edition renders as a disabled "N/A" row.
func newScopedConfigIntEditor(configs []*gosmo.DatabaseScopedConfig, tracked *[]scopedConfigRow) func(name, label, unit string) *propsheet.TextRow {
	return func(name, label, unit string) *propsheet.TextRow {
		c := findScopedConfig(configs, name)
		if c == nil {
			row := propsheet.Text(label, "N/A", 12)
			row.SetEnabled(false)
			return row
		}
		v, _ := strconv.ParseInt(c.Value, 10, 64)
		row := propsheet.Int(label, v, 0, 2147483647, unit)
		*tracked = append(*tracked, scopedConfigRow{name: name, row: row})
		return row
	}
}

// newScopedConfigBoolEditor is newScopedConfigIntEditor's counterpart for
// options whose value is conventionally "0"/"1".
func newScopedConfigBoolEditor(configs []*gosmo.DatabaseScopedConfig, tracked *[]scopedConfigBoolRow) func(name, label string) *propsheet.SelectRow {
	return func(name, label string) *propsheet.SelectRow {
		c := findScopedConfig(configs, name)
		idx := 0
		if c != nil && c.Value == "1" {
			idx = 1
		}
		row := propsheet.Select(label, onOff, idx)
		if c == nil {
			return row
		}
		*tracked = append(*tracked, scopedConfigBoolRow{name: name, row: row})
		return row
	}
}

// applyScopedConfigRows writes back every dirty row in intRows/boolRows
// via Database.SetDatabaseScopedConfigContext.
func applyScopedConfigRows(ctx context.Context, d *gosmo.Database, intRows []scopedConfigRow, boolRows []scopedConfigBoolRow) error {
	for _, r := range intRows {
		if !r.row.Dirty() {
			continue
		}
		v, err := r.row.IntValue()
		if err != nil {
			return err
		}
		if err := d.SetDatabaseScopedConfigContext(ctx, r.name, strconv.FormatInt(v, 10), false); err != nil {
			return err
		}
	}
	for _, r := range boolRows {
		if !r.row.Dirty() {
			continue
		}
		value := onOff[r.row.Selected()]
		if err := d.SetDatabaseScopedConfigContext(ctx, r.name, value, false); err != nil {
			return err
		}
	}
	return nil
}

// pageDatabaseScopedConfig groups the well-known
// sys.database_scoped_configurations options into editable rows, then
// lists every option — including ones this build doesn't expose
// individually — in a read-only grid underneath, the same pattern Server
// Properties' Advanced page uses for sys.configurations.
func pageDatabaseScopedConfig(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Database Scoped Configurations",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			configs, err := d.DatabaseScopedConfigsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []scopedConfigRow
			var boolRows []scopedConfigBoolRow
			cfgInt := newScopedConfigIntEditor(configs, &intRows)
			cfgBool := newScopedConfigBoolEditor(configs, &boolRows)

			rows := make([][]string, len(configs))
			for i, c := range configs {
				rows[i] = []string{c.Name, c.Value, c.ValueForSecondary}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Configuration", "Value", "Value for secondary"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Query optimizer"),
				cfgInt("MAXDOP", "Max DOP", ""),
				cfgBool("LEGACY_CARDINALITY_ESTIMATION", "Legacy cardinality estimation"),
				cfgBool("PARAMETER_SNIFFING", "Parameter sniffing"),
				cfgBool("QUERY_OPTIMIZER_HOTFIXES", "Query optimizer hotfixes"),
				cfgBool("INTERLEAVED_EXECUTION_TVF", "Interleaved execution for TVFs"),
				cfgBool("BATCH_MODE_MEMORY_GRANT_FEEDBACK", "Batch mode memory grant feedback"),
				cfgBool("BATCH_MODE_ADAPTIVE_JOINS", "Batch mode adaptive joins"),
				cfgBool("TSQL_SCALAR_UDF_INLINING", "TSQL scalar UDF inlining"),
				cfgBool("ACCELERATED_PLAN_FORCING", "Accelerated plan forcing"),
				cfgBool("OPTIMIZED_PLAN_FORCING", "Optimized plan forcing"),
				propsheet.Section("Miscellaneous"),
				cfgBool("GLOBAL_TEMPORARY_TABLE_AUTO_DROP", "Global temporary table auto drop"),
				propsheet.Section("All database scoped configurations (sys.database_scoped_configurations)"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Note("The grid above is read-only — edit an option from its group above if it has one. \"Value for secondary\" (Always On readable secondaries) isn't editable in this build."),
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				return applyScopedConfigRows(ctx, d, intRows, boolRows)
			}
			return f, apply, nil
		},
	}
}
