package tui

import (
	"context"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// buildFileSpec builds a gosmo.DatabaseFileSpec from a Data/Log file
// section's four rows, or nil if every one of them was left blank — the
// server's own bare CREATE DATABASE default for that file (a single file
// at the server's default path/size). A field left blank when at least
// one sibling field was set falls back to defaultName/defaultDir+ext, the
// same identity SQL Server itself would have chosen.
func buildFileSpec(nameRow, pathRow *propsheet.TextRow, sizeRow, growthRow *propsheet.TextRow, defaultName, defaultDir, ext string) *gosmo.DatabaseFileSpec {
	name := strings.TrimSpace(nameRow.Value())
	path := strings.TrimSpace(pathRow.Value())
	sizeMB, _ := sizeRow.IntValue()
	growthMB, _ := growthRow.IntValue()
	if name == "" && path == "" && sizeMB == 0 && growthMB == 0 {
		return nil
	}
	if name == "" {
		name = defaultName
	}
	if path == "" {
		path = defaultDir + name + ext
	}
	return &gosmo.DatabaseFileSpec{Name: name, Path: path, SizeKB: sizeMB * 1024, GrowthKB: growthMB * 1024}
}

// buildNewDatabaseGeneralPage builds the General page: identity (name,
// owner, collation), maintenance (recovery model, compatibility level —
// seeded from model's current settings, matching a real bare CREATE
// DATABASE's own inheritance), and the initial data/log file. It returns
// the Name field alongside the form/apply so the dialog can read it back
// for the Options/Filegroups pages' own dbName lookups and the
// name-uniqueness preflight check.
func buildNewDatabaseGeneralPage(sc *db.ServerConn, pf *ndbPrefetch) (*propsheet.Form, propApply, *propsheet.TextRow) {
	nameField := propsheet.Text("Database name", "", 30)
	ownerRow := propsheet.Select("Owner", pf.loginNames, indexOf(pf.loginNames, pf.defaultOwner))
	collationField := propsheet.Text("Collation", "", 30)

	recoveryItems := []string{"SIMPLE", "FULL", "BULK_LOGGED"}
	recoveryRow := propsheet.Select("Recovery model", recoveryItems, indexOf(recoveryItems, string(pf.modelRecovery)))
	compatItems := []string{"100", "110", "120", "130", "140", "150", "160", "170"}
	compatRow := propsheet.Select("Compatibility level", compatItems, indexOf(compatItems, strconv.Itoa(int(pf.modelCompat))))

	dataNameField := propsheet.Text("Logical name", "", 24)
	dataPathField := propsheet.Text("Path", "", 40)
	dataSizeField := propsheet.Int("Initial size", 0, 0, 16777216, "MB")
	dataGrowthField := propsheet.Int("Growth", 0, 0, 2097151, "MB")

	logNameField := propsheet.Text("Logical name", "", 24)
	logPathField := propsheet.Text("Path", "", 40)
	logSizeField := propsheet.Int("Initial size", 0, 0, 16777216, "MB")
	logGrowthField := propsheet.Int("Growth", 0, 0, 2097151, "MB")

	f := propsheet.NewForm(
		propsheet.Section("Database identity"),
		nameField, ownerRow, collationField,
		propsheet.Section("Maintenance"),
		recoveryRow, compatRow,
		propsheet.Section("Data file"),
		dataNameField, dataPathField, dataSizeField, dataGrowthField,
		propsheet.Section("Log file"),
		logNameField, logPathField, logSizeField, logGrowthField,
		propsheet.Note("Leave a file's fields blank to use the server default (logical name/path derived from the database name, server-default size and growth)."),
	)

	apply := func(ctx context.Context) error {
		name := strings.TrimSpace(nameField.Value())
		opts := &gosmo.CreateDatabaseOptions{
			Collation:   strings.TrimSpace(collationField.Value()),
			PrimaryFile: buildFileSpec(dataNameField, dataPathField, dataSizeField, dataGrowthField, name, pf.defaultDataPath, ".mdf"),
			LogFile:     buildFileSpec(logNameField, logPathField, logSizeField, logGrowthField, name+"_log", pf.defaultLogPath, ".ldf"),
		}
		// Recovery model/compatibility level are only set explicitly when
		// the user chose something other than what a bare CREATE DATABASE
		// would already inherit from model — matching every other row on
		// this dialog's Dirty()-gated apply, and keeping Script Changes free
		// of no-op ALTER statements for untouched fields.
		if recoveryRow.Dirty() {
			opts.RecoveryModel = gosmo.RecoveryModel(recoveryItems[recoveryRow.Selected()])
		}
		if compatRow.Dirty() {
			compatN, _ := strconv.Atoi(compatItems[compatRow.Selected()])
			opts.CompatLevel = gosmo.CompatibilityLevel(compatN)
		}
		if err := sc.Server.CreateDatabaseContext(ctx, name, opts); err != nil {
			return err
		}
		if ownerRow.Dirty() {
			if err := sc.Server.Database(name).SetOwnerContext(ctx, ownerRow.Value()); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply, nameField
}

// buildNewDatabaseOptionsPage reuses database_props.go's pageDatabaseOptions
// row-building/apply idiom verbatim (dbOptSelectRow/dbOptBoolRow + a
// tracked []dbOptRow list, applied only when a row is Dirty()), fed from
// model's current options instead of an existing database's — since a
// real CREATE DATABASE already inherits every one of these from model,
// "the row is dirty" now means exactly "the user chose something other
// than what this database would have inherited anyway," and only those
// need an explicit follow-on ALTER DATABASE SET once dbName() exists.
func buildNewDatabaseOptionsPage(sc *db.ServerConn, pf *ndbPrefetch, dbName func() string) (*propsheet.Form, propApply) {
	o := pf.modelOptions
	var tracked []dbOptRow

	pageVerifyItems := []string{"NONE", "TORN_PAGE_DETECTION", "CHECKSUM"}
	containmentItems := []string{"NONE", "PARTIAL"}
	cursorDefaultItems := []string{"GLOBAL", "LOCAL"}
	userAccessItems := []string{"MULTI_USER", "SINGLE_USER", "RESTRICTED_USER"}
	snapshotIsolationOn := o.SnapshotIsolation == "ON" || o.SnapshotIsolation == "ENABLED"

	userAccessRow := propsheet.Select("Restrict access", userAccessItems, indexOf(userAccessItems, o.UserAccess))

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
	)

	apply := func(ctx context.Context) error {
		d := sc.Server.Database(dbName())
		for _, r := range tracked {
			if !r.row.Dirty() {
				continue
			}
			value := r.items[r.row.Selected()]
			if err := d.SetDatabaseOptionContext(ctx, r.opt, value); err != nil {
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
	return f, apply
}

// buildNewDatabaseFilegroupsPage adapts pageDatabaseFilegroups' grid/Add/
// Remove/default/read-only UI: seeded empty (nothing exists yet, so every
// entry is a pending add, unlike the edit-existing version's isNew/
// pendingRemove diffing), plus an inline "optional first file" mini-form
// under the Add-filegroup fields, mirroring the mockup's "Add Filegroup"
// modal inline instead of as a popup.
func buildNewDatabaseFilegroupsPage(sc *db.ServerConn, pf *ndbPrefetch, dbName func() string) (*propsheet.Form, propApply) {
	type fgEdit struct {
		name         string
		isDefault    bool
		isReadOnly   bool
		fileName     string // "" = no first file
		filePath     string
		fileSizeKB   int64
		fileGrowthKB int64
	}
	var edits []*fgEdit

	rowsFor := func() ([][]string, [][]bool) {
		text := make([][]string, len(edits))
		values := make([][]bool, len(edits))
		for i, e := range edits {
			fileCount := "0"
			if e.fileName != "" {
				fileCount = "1"
			}
			text[i] = []string{e.name, fileCount}
			values[i] = []bool{e.isReadOnly, e.isDefault}
		}
		return text, values
	}
	fgRow := propsheet.NewToggleGrid([]string{"Name", "Files", "Read-only", "Default"}, []int{2, 3}, 8)
	syncToggles := func() {
		for i, v := range fgRow.Values() {
			if i < len(edits) {
				edits[i].isReadOnly, edits[i].isDefault = v[0], v[1]
			}
		}
	}

	nameField := propsheet.Text("New filegroup name", "", 24)
	fileNameField := propsheet.Text("First file logical name", "", 24)
	filePathField := propsheet.Text("First file path", "", 40)
	fileSizeField := propsheet.Int("First file initial size", 0, 0, 16777216, "MB")
	fileGrowthField := propsheet.Int("First file growth", 0, 0, 2097151, "MB")

	var addBtn, removeBtn *widgets.Button
	addBtn = widgets.NewButton("Add", func() {
		syncToggles()
		name := nameField.Value()
		if name == "" {
			return
		}
		for _, e := range edits {
			if e.name == name {
				return
			}
		}
		sizeMB, _ := fileSizeField.IntValue()
		growthMB, _ := fileGrowthField.IntValue()
		edits = append(edits, &fgEdit{
			name:         name,
			fileName:     strings.TrimSpace(fileNameField.Value()),
			filePath:     strings.TrimSpace(filePathField.Value()),
			fileSizeKB:   sizeMB * 1024,
			fileGrowthKB: growthMB * 1024,
		})
		text, values := rowsFor()
		fgRow.SetRows(text, values)
		nameField.SetValue("")
		fileNameField.SetValue("")
		filePathField.SetValue("")
		fileSizeField.SetValue("0")
		fileGrowthField.SetValue("0")
	})
	removeBtn = widgets.NewButton("Remove", func() {
		syncToggles()
		row := fgRow.Grid.SelectedRow()
		if row < 0 || row >= len(edits) {
			return
		}
		edits = append(edits[:row], edits[row+1:]...)
		text, values := rowsFor()
		fgRow.SetRows(text, values)
	})

	fgRow.DirtyFn = func() bool {
		syncToggles()
		return len(edits) > 0
	}
	fgRow.RevertFn = func() {
		edits = edits[:0]
		text, values := rowsFor()
		fgRow.SetRows(text, values)
	}

	f := propsheet.NewForm(
		propsheet.Section("Filegroups"),
		fgRow,
		propsheet.Section("Add filegroup"),
		nameField,
		propsheet.Section("Optional first file"),
		fileNameField, filePathField, fileSizeField, fileGrowthField,
		propsheet.Buttons(addBtn, removeBtn),
		propsheet.Note("Only one row-data filegroup can be the default. Leave the first-file fields blank to add an empty filegroup — the database's PRIMARY filegroup and its data/log files come from the General page, not here."),
	)

	apply := func(ctx context.Context) error {
		syncToggles()
		if len(edits) == 0 {
			return nil
		}
		d := sc.Server.Database(dbName())
		for _, e := range edits {
			if err := d.AddFileGroupContext(ctx, e.name); err != nil {
				return err
			}
			if e.fileName != "" {
				path := e.filePath
				if path == "" {
					path = pf.defaultDataPath + e.fileName + ".ndf"
				}
				spec := gosmo.DatabaseFileSpec{
					Name: e.fileName, FileGroup: e.name, Path: path,
					SizeKB: e.fileSizeKB, GrowthKB: e.fileGrowthKB,
				}
				if err := d.AddFileContext(ctx, spec); err != nil {
					return err
				}
			}
			if e.isReadOnly {
				if err := d.SetFileGroupReadOnlyContext(ctx, e.name, true); err != nil {
					return err
				}
			}
			if e.isDefault {
				if err := d.SetDefaultFileGroupContext(ctx, e.name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
}
