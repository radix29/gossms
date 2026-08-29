package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// databaseFileColumns heads the Files page grid, which is rebuilt from four
// call sites and read back by column index.
var databaseFileColumns = []string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}

// logFileType is sys.database_files' type_desc for a transaction log file,
// the one file type that belongs to no filegroup.
const logFileType = "LOG"

// noFilegroupItem is what the Filegroup dropdown shows for a LOG file. The
// list is filegroup names with no empty entry, so indexOf's not-found 0 left
// PRIMARY in the box and commitCurrent wrote it onto the edit — the grid then
// reported a filegroup for a log file, which is a wrong fact about the
// database in a properties dialog.
const noFilegroupItem = "(not applicable)"

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

// changed reports whether this file's definition differs from what the
// server reported. Only the four things ALTER DATABASE ... MODIFY FILE can
// actually change are compared: fileType, fileGroup and path are fixed for
// an existing file — MODIFY FILE cannot move or retype one — so an edit to
// them is not a change this page can write, and treating it as one would
// send an ALTER that silently does nothing.
//
// It is a method rather than two copies because the Files page needs the
// same answer twice, in two places that must agree: GridRow.DirtyFn, which
// decides whether the page is dirty at all, and apply, which decides whether
// this particular file gets an ALTER. As two expressions listing the same six
// fields they drift: a field added to one and not the other either makes a page
// that never reports itself dirty (so OK writes nothing) or one that is always
// dirty (so OK always writes).
func (e *fileEdit) changed() bool {
	return e.name != e.origName || e.sizeKB != e.origSizeKB ||
		e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB ||
		e.growthPercent != e.origGrowthPercent || e.maxSizeKB != e.origMaxSizeKB
}

// modify builds the partial ALTER for an existing file: every field is left
// zero unless it actually changed, because gosmo reads a zero as "leave this
// property alone" and omits it from the statement.
//
// That is why each assignment is guarded rather than unconditional. Sending
// the unchanged current value instead would look harmless and is not: SIZE is
// the case that bites, since ALTER DATABASE ... MODIFY FILE treats it as a
// grow-to target and rejects a value below the file's current size outright.
// A user who edits only the autogrowth of a file that has since grown past
// its recorded size would get "MODIFY FILE failed. Specified size is less
// than or equal to current size" for an edit they never made.
func (e *fileEdit) modify() gosmo.FileModify {
	var m gosmo.FileModify
	if e.name != e.origName {
		m.NewName = e.name
	}
	if e.sizeKB != e.origSizeKB {
		m.SizeKB = e.sizeKB
	}
	if e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB || e.growthPercent != e.origGrowthPercent {
		// Exactly one of the two is set: gosmo lets GrowthPercent win when
		// both are, and the growth kind is a radio, so sending both would
		// make the radio's losing half decide nothing while still being
		// carried.
		//
		// A growth of zero has to go through DisableGrowth rather than the
		// amount fields, because gosmo reads a zero amount as "leave
		// FILEGROWTH alone" — so turning autogrowth off produced an ALTER
		// with no FILEGROWTH clause at all, and where growth was the only
		// edit, no ALTER at all: OK reported success and the file still
		// grew.
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

// spec builds the CREATE-side description of a brand-new file. Unlike
// modify, every field is sent: there is no previous value to leave alone.
func (e *fileEdit) spec() gosmo.DatabaseFileSpec {
	spec := gosmo.DatabaseFileSpec{
		Name: e.name, Type: e.fileType, Path: e.path, SizeKB: e.sizeKB, MaxSizeKB: e.maxSizeKB,
	}
	// A LOG file belongs to no filegroup, and gosmo ignores the field for
	// one; leaving it empty keeps the spec honest rather than relying on that.
	if e.fileType != logFileType {
		spec.FileGroup = e.fileGroup
	}
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

// growthOff reports whether the row asks for autogrowth to be switched off:
// the growth spinner at zero, in whichever unit the radio has selected.
// SSMS's equivalent is clearing "Enable Autogrowth"; here the spinner bottoms
// out at 0 and means the same thing.
func (e *fileEdit) growthOff() bool {
	if e.isPercentGrowth {
		return e.growthPercent == 0
	}
	return e.growthKB == 0
}

func growthText(isPercent bool, growthKB int64, growthPercent int) string {
	// SQL Server records autogrowth-off as a growth of zero, and the grid has
	// to say so in words: "0 MB" reads as a field nobody filled in, next to
	// six columns that are all real values.
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
			grid.SetData(databaseFileColumns, rowsFor())
			grid.SetCellCursor(true)

			nameField := propsheet.Text("Logical name", "", 24)
			typeSelect := propsheet.Select("File type", []string{"ROWS", logFileType}, 0)
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
			// showFilegroupFor swaps the Filegroup dropdown between the real
			// filegroup list and the single "(not applicable)" entry a LOG file
			// gets, so the row can never offer a choice that wouldn't be used.
			showFilegroupFor := func(fileType, fileGroup string) {
				if fileType == logFileType {
					filegroupSelect.SetItems([]string{noFilegroupItem})
					return
				}
				filegroupSelect.SetItems(fgNames)
				filegroupSelect.SetSelected(indexOf(fgNames, fileGroup))
			}
			// pickedFilegroup reads the dropdown back, as "" for a LOG file —
			// which is what DatabaseFileInfo.FileGroup already holds for one.
			pickedFilegroup := func(fileType string) string {
				if fileType == logFileType {
					return ""
				}
				return filegroupSelect.Value()
			}
			typeSelect.SetOnChange(func(string) {
				showFilegroupFor(typeSelect.Value(), "")
			})

			var current *fileEdit
			commitCurrent := func() {
				if current == nil {
					return
				}
				current.name = nameField.Value()
				current.fileType = typeSelect.Value()
				current.fileGroup = pickedFilegroup(current.fileType)
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
				typeSelect.SetSelected(indexOf([]string{"ROWS", logFileType}, current.fileType))
				showFilegroupFor(current.fileType, current.fileGroup)
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

			hint := propsheet.Hint()
			var addBtn, removeBtn *widgets.Button
			addBtn = widgets.NewButton("Add", func() {
				// Deliberately does NOT call commitCurrent(): these fields
				// double as the previously-selected file's live edit, and
				// commitCurrent() writes nameField's text into that file's
				// rename target, so a brand-new name typed here intending
				// to Add would silently rename the wrong file. Any
				// not-yet-applied edit to the previously selected file is
				// left as last synced from its own selection.
				name := nameField.Value()
				if name == "" {
					hint.Set("Type a logical file name first.")
					return
				}
				for i, e := range visible() {
					if e.name == name {
						// Already present — say so and select it, rather than
						// leaving the button looking broken.
						hint.Set("A file named " + name + " is already listed — its row is selected below.")
						grid.SetSelectedRow(i)
						syncFieldsFromSelection()
						return
					}
				}
				hint.Clear()
				e := &fileEdit{
					isNew: true, name: name, fileType: typeSelect.Value(), fileGroup: pickedFilegroup(typeSelect.Value()), path: pathField.Value(),
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
				e.pendingRemove = true
				current = nil
				resetGrid(grid, databaseFileColumns, rowsFor(), 0)
				syncFieldsFromSelection()
			})

			gridRow := propsheet.NewGridRow(grid, 10)
			dirty := func() bool {
				for _, e := range edits {
					if e.isNew || e.pendingRemove || e.changed() {
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
						if err := d.AddFileContext(ctx, e.spec()); err != nil {
							return err
						}
					case !e.isNew && !e.pendingRemove:
						if !e.changed() {
							continue // nothing about this file actually changed
						}
						// Addressed by origName, not name: a rename is carried
						// inside the modify as NEWNAME, so the file still
						// answers to its old name at this point.
						if err := d.AlterFileContext(ctx, e.origName, e.modify()); err != nil {
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
