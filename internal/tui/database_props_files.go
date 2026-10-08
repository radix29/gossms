package tui

import (
	"context"
	"slices"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// databaseFileColumns heads the Files page grid, rebuilt from four call sites
// and read back by column index.
var databaseFileColumns = []string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}

// logFileType is sys.database_files' type_desc for a transaction log file,
// the one file type that belongs to no filegroup.
const logFileType = "LOG"

// addableFileTypes are the file types the picker offers: the two
// sys.database_files reports that ALTER DATABASE ... ADD FILE names in the
// statement. Not the set of types a file can *have* (Files also reports
// FILESTREAM), so the picker is widened for display when a selected file's type
// is outside this list.
var addableFileTypes = []string{"ROWS", logFileType}

// filestreamFileType is sys.database_files' type_desc for a FILESTREAM data
// file. Deliberately not in addableFileTypes, as it is not something to pick:
// ALTER DATABASE ADD FILE has no file-type keyword, and a file becomes
// FILESTREAM purely by going into a FILESTREAM filegroup. The same clause aimed
// at a ROWS filegroup produces an ordinary data file (measured on win10cli
// against a real FILESTREAM database, 2026-09-05). See fileEdit.spec.
const filestreamFileType = "FILESTREAM"

// noFilegroupItem is what the Filegroup dropdown shows for a LOG file. The list
// is filegroup names with no empty entry, so indexOf's not-found 0 left PRIMARY
// in the box and commitCurrent wrote it onto the edit, so the grid reported a
// filegroup for a log file: a wrong fact in a properties dialog.
const noFilegroupItem = "(not applicable)"

// fileEdit tracks one Files-page row's pending state: an existing file whose
// logical name/size/growth/max size changed, a new file pending Add (isNew), or
// an existing file pending Remove.
type fileEdit struct {
	origName string // "" for a brand-new file
	pendingState

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
}

func fileEditFromInfo(fl *gosmo.DatabaseFileInfo) *fileEdit {
	return &fileEdit{
		origName: fl.Name, name: fl.Name, fileType: fl.Type, fileGroup: fl.FileGroup, path: fl.PhysicalName,
		sizeKB: fl.SizeKB, isPercentGrowth: fl.IsPercentGrowth, growthKB: fl.GrowthKB, growthPercent: fl.GrowthPercent, maxSizeKB: fl.MaxSizeKB,
		origSizeKB: fl.SizeKB, origIsPercentGrowth: fl.IsPercentGrowth, origGrowthKB: fl.GrowthKB, origGrowthPercent: fl.GrowthPercent, origMaxSizeKB: fl.MaxSizeKB,
	}
}

// changed reports whether this file's definition differs from what the server
// reported. Only the four things ALTER DATABASE ... MODIFY FILE can change are
// compared: fileType, fileGroup and path are fixed for an existing file, so an
// edit to them is not a change this page can write, and treating it as one
// would send an ALTER that silently does nothing.
//
// A method rather than two copies because the Files page needs the same answer
// in two places that must agree: GridRow.DirtyFn (is the page dirty at all) and
// apply (does this file get an ALTER). Two expressions listing the same six
// fields drift: a field added to one only makes a page that never reports
// itself dirty (OK writes nothing) or is always dirty (OK always writes).
func (e *fileEdit) changed() bool {
	return e.name != e.origName || e.sizeKB != e.origSizeKB ||
		e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB ||
		e.growthPercent != e.origGrowthPercent || e.maxSizeKB != e.origMaxSizeKB
}

// reset undoes every change changed reports (the same fields, kept beside it
// for the same reason).
func (e *fileEdit) reset() {
	e.name, e.sizeKB, e.isPercentGrowth = e.origName, e.origSizeKB, e.origIsPercentGrowth
	e.growthKB, e.growthPercent, e.maxSizeKB = e.origGrowthKB, e.origGrowthPercent, e.origMaxSizeKB
}

// modify builds the partial ALTER for an existing file: every field is left
// zero unless it changed, because gosmo reads a zero as "leave this property
// alone" and omits it.
//
// Hence each assignment is guarded, not unconditional. Sending the unchanged
// current value looks harmless but SIZE bites: ALTER DATABASE ... MODIFY FILE
// treats it as a grow-to target and rejects a value below the file's current
// size. A user editing only the autogrowth of a file that has since grown past
// its recorded size would get "MODIFY FILE failed. Specified size is less than
// or equal to current size" for an edit they never made.
func (e *fileEdit) modify() gosmo.FileModify {
	var m gosmo.FileModify
	if e.name != e.origName {
		m.NewName = e.name
	}
	if e.sizeKB != e.origSizeKB {
		m.SizeKB = e.sizeKB
	}
	if e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB || e.growthPercent != e.origGrowthPercent {
		// Exactly one of the two is set: gosmo lets GrowthPercent win when both are,
		// and the growth kind is a radio, so sending both would carry the radio's
		// losing half for nothing.
		//
		// A growth of zero must go through DisableGrowth, not the amount fields,
		// because gosmo reads a zero amount as "leave FILEGROWTH alone": turning
		// autogrowth off would produce an ALTER with no FILEGROWTH clause, and where
		// growth was the only edit, no ALTER at all (OK reported success and the file
		// still grew).
		switch {
		case e.growthOff():
			m.DisableGrowth = true
		case e.isPercentGrowth:
			m.GrowthPercent = e.growthPercent
		default:
			m.GrowthKB = e.growthKB
		}
	}
	if e.maxSizeKB != e.origMaxSizeKB {
		m.MaxSizeKB = e.maxSizeKB
	}
	return m
}

// spec builds the CREATE-side description of a new file. Unlike modify, every
// field is sent (no previous value to leave alone), except for a FILESTREAM
// file, which has no size or autogrowth to send.
func (e *fileEdit) spec() gosmo.DatabaseFileSpec {
	spec := gosmo.DatabaseFileSpec{
		Name: e.name, Type: e.fileType, Path: e.path, MaxSizeKB: e.maxSizeKB,
	}
	// A LOG file belongs to no filegroup and gosmo ignores the field; leaving it
	// empty keeps the spec honest rather than relying on that.
	if e.fileType != logFileType {
		spec.FileGroup = e.fileGroup
	}
	// SIZE and FILEGROWTH on a FILESTREAM file are refused outright: "The
	// properties SIZE or FILEGROWTH cannot be specified for the FILESTREAM data
	// file" (Msg 5509). MAXSIZE is accepted (measured, not assumed, since the
	// clauses read as one family and are not). The omission lives here, not in the
	// Add button, so every route to a spec goes through it.
	if e.fileType == filestreamFileType {
		return spec
	}
	spec.SizeKB = e.sizeKB
	switch {
	case e.growthOff():
		spec.DisableGrowth = true
	case e.isPercentGrowth:
		spec.GrowthPercent = e.growthPercent
	default:
		spec.GrowthKB = e.growthKB
	}
	return spec
}

// growthOff reports whether the row asks for autogrowth to be switched off: the
// growth spinner at zero, in either unit. SSMS clears "Enable Autogrowth"; here
// the spinner bottoms out at 0 with the same meaning.
func (e *fileEdit) growthOff() bool {
	if e.isPercentGrowth {
		return e.growthPercent == 0
	}
	return e.growthKB == 0
}

func growthText(isPercent bool, growthKB int64, growthPercent int) string {
	// SQL Server records autogrowth-off as a growth of zero, and the grid must say
	// so in words: "0 MB" reads as a field nobody filled in beside six real values.
	if isPercent {
		if growthPercent == 0 {
			return "None"
		}
		return strconv.Itoa(growthPercent) + "%"
	}
	if growthKB == 0 {
		return "None"
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
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			opts, err := d.Options(ctx)
			if err != nil {
				return nil, nil, err
			}
			files, err := d.Files(ctx)
			if err != nil {
				return nil, nil, err
			}
			fgs, err := d.FileGroups(ctx)
			if err != nil {
				return nil, nil, err
			}
			fgNames := make([]string, len(fgs))
			// fsGroup is which filegroups make a file put into them a FILESTREAM file.
			// gosmo reports the filegroup's own type_desc; nothing about the file being
			// added says it.
			fsGroup := make(map[string]bool, len(fgs))
			for i, fg := range fgs {
				fgNames[i] = fg.Name
				if fg.IsFileStream() {
					fsGroup[fg.Name] = true
				}
			}

			loaded := make([]*fileEdit, len(files))
			for i, fl := range files {
				loaded[i] = fileEditFromInfo(fl)
			}
			edits := newPendingEdits(databaseCollation(d), loaded,
				func(e *fileEdit) string { return e.name }, (*fileEdit).changed, (*fileEdit).reset)
			visible := edits.visible
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
			grid.SetData(databaseFileColumns, rowsFor())
			grid.SetCellCursor(true)

			nameField := propsheet.Text("Logical name", "", 24)
			typeSelect := propsheet.Select("File type", addableFileTypes, 0)
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
			// showFilegroupFor swaps the Filegroup dropdown between the real filegroup list
			// and the single "(not applicable)" entry a LOG file gets, so the row never
			// offers a choice that wouldn't be used.
			showFilegroupFor := func(fileType, fileGroup string) {
				if fileType == logFileType {
					filegroupSelect.SetItems([]string{noFilegroupItem})
					return
				}
				if fileGroup == "" {
					filegroupSelect.SetItems(fgNames)
					filegroupSelect.SetSelected(0)
					return
				}
				// Widened as the type picker is: a file can sit in a filegroup FileGroups
				// didn't list, and a stand-in name would read as the server's answer.
				items, i := preservingItems(fgNames, fileGroup)
				filegroupSelect.SetItems(items)
				filegroupSelect.SetSelected(i)
			}
			// pickedFilegroup reads the dropdown back, as "" for a LOG file (what
			// DatabaseFileInfo.FileGroup holds for one).
			pickedFilegroup := func(fileType string) string {
				if fileType == logFileType {
					return ""
				}
				return filegroupSelect.Value()
			}
			// effectiveType is what a file put in fileGroup becomes. The picker offers ROWS
			// and LOG; the filegroup turns a ROWS pick into FILESTREAM, so they are read
			// together.
			effectiveType := func(picked, fileGroup string) string {
				if picked != logFileType && fsGroup[fileGroup] {
					return filestreamFileType
				}
				return picked
			}
			// syncSizeRows gates the size and growth spinners on whether the file can carry
			// either. A FILESTREAM file cannot, and a live spinner whose value the
			// statement drops is the silent wrong-thing this page's pickers are gated
			// against; worse here, since sending them fails the whole Add with Msg 5509.
			syncSizeRows := func(fileType string) {
				fs := fileType == filestreamFileType
				sizeField.SetEnabled(!fs)
				growthField.SetEnabled(!fs)
				if fs {
					sizeField.SetValue("0")
					growthField.SetValue("0")
				}
			}
			typeSelect.SetOnChange(func(string) {
				showFilegroupFor(typeSelect.Value(), "")
				syncSizeRows(effectiveType(typeSelect.Value(), pickedFilegroup(typeSelect.Value())))
			})
			filegroupSelect.SetOnChange(func(string) {
				syncSizeRows(effectiveType(typeSelect.Value(), pickedFilegroup(typeSelect.Value())))
			})

			var current *fileEdit
			commitCurrent := func() {
				if current == nil {
					return
				}
				current.name = nameField.Value()
				// Only a new file reads type/filegroup/path back. They are fixed for an
				// existing one (changed() excludes them because MODIFY FILE can neither retype
				// nor move a file), so writing them could only record something untrue: a
				// FILESTREAM file's type is outside the picker's two items, so the picker
				// showed a stand-in and merely selecting the row rewrote the file as ROWS,
				// which the grid then reported as fact. Same defect as noFilegroupItem, one
				// field over.
				if current.isNew {
					current.fileGroup = pickedFilegroup(typeSelect.Value())
					current.fileType = effectiveType(typeSelect.Value(), current.fileGroup)
					current.path = pathField.Value()
				}
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
				// preservingItems, not indexOf: sys.database_files also reports FILESTREAM (and
				// memory-optimized) files, whose type is in neither item this page can create.
				// indexOf would show "ROWS" for one as though the server had said so.
				typeItems, typeIdx := preservingItems(addableFileTypes, current.fileType)
				typeSelect.SetItems(typeItems)
				typeSelect.SetSelected(typeIdx)
				showFilegroupFor(current.fileType, current.fileGroup)
				syncSizeRows(current.fileType)
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
			// wireGridEditor redraws after the commit, so the row moved off shows
			// what Apply will send rather than its loaded values.
			wireGridEditor(grid, databaseFileColumns, rowsFor, commitCurrent, syncFieldsFromSelection)

			hint := propsheet.Hint()
			var addBtn, removeBtn *widgets.Button
			addBtn = widgets.NewButton("Add", func() {
				// Deliberately does NOT call commitCurrent(): these fields double as the
				// previously-selected file's live edit, and commitCurrent() writes nameField's
				// text into that file's rename target, so a new name typed here to Add would
				// silently rename the wrong file. Any not-yet-applied edit to the previously
				// selected file is left as last synced from its own selection.
				name := nameField.Value()
				if name == "" {
					hint.Set("Type a logical file name first.")
					return
				}
				// The type picker widens to show a selected file's real type (FILESTREAM, say);
				// Add must not carry one of those into a spec this page can't build correctly.
				if !slices.Contains(addableFileTypes, typeSelect.Value()) {
					hint.Set("Set File type to ROWS or LOG — this page adds data and log files only.")
					return
				}
				if i := edits.index(name); i >= 0 {
					// Already present: say so and select it, rather than leave the button looking
					// broken.
					hint.Set("A file named " + name + " is already listed — its row is selected below.")
					grid.SetSelectedRow(i)
					syncFieldsFromSelection()
					return
				}
				hint.Clear()
				fileGroup := pickedFilegroup(typeSelect.Value())
				e := &fileEdit{
					name: name, fileType: effectiveType(typeSelect.Value(), fileGroup),
					fileGroup: fileGroup, path: pathField.Value(),
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
				edits.add(e)
				resetGrid(grid, databaseFileColumns, rowsFor(), len(visible())-1)
				syncFieldsFromSelection()
			})
			removeBtn = widgets.NewButton("Remove", func() {
				e := selected()
				if e == nil {
					hint.Set("Select a file in the grid above to remove it.")
					return
				}
				hint.Clear()
				edits.remove(e)
				current = nil
				resetGrid(grid, databaseFileColumns, rowsFor(), 0)
				syncFieldsFromSelection()
			})

			gridRow := propsheet.NewGridRow(grid, 10)
			gridRow.DirtyFn = edits.dirty
			gridRow.RevertFn = func() {
				edits.revert()
				resetGrid(grid, databaseFileColumns, rowsFor(), 0)
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
				hint,
				propsheet.Note("A file added to a FILESTREAM filegroup is a FILESTREAM file: its path is a directory rather than a file, and it takes neither an initial size nor autogrowth, so those two are greyed. Choosing the filegroup is what makes it one — there is no file type to pick."),
			)
			f.SetCommit(commitCurrent)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByName(ctx, dbName)
				if err != nil {
					return err
				}
				for _, e := range edits.all() {
					switch {
					case e.removing:
						if err := d.FileRef(e.origName).Drop(ctx); err != nil {
							return err
						}
					case e.isNew:
						if err := d.AddFile(ctx, e.spec()); err != nil {
							return err
						}
					default:
						if !e.changed() {
							continue // nothing about this file actually changed
						}
						// Addressed by origName, not name: a rename is carried inside the modify as
						// NEWNAME, so the file still answers to its old name here.
						if err := d.FileRef(e.origName).Alter(ctx, e.modify()); err != nil {
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
