package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
	"github.com/radix29/tuikit/widgets"
)

// databaseFileColumns heads the Files page grid, rebuilt from four call sites
// and read back by column index.
var databaseFileColumns = []string{"Logical name", "Type", "Filegroup", "Size", "Autogrowth", "Max size", "Path"}

// logFileType is sys.database_files' type_desc for a transaction log file,
// the one file type that belongs to no filegroup.
const logFileType = string(gosmo.FileLog)

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
// at a ROWS filegroup produces an ordinary data file (measured against a real
// FILESTREAM database). See fileEdit.spec.
const filestreamFileType = string(gosmo.FileStream)

// noFilegroupItem is what the Filegroup dropdown shows for a LOG file. The list
// is filegroup names with no empty entry, so indexOf's not-found 0 left PRIMARY
// in the box and commitCurrent wrote it onto the edit, so the grid reported a
// filegroup for a log file: a wrong fact in a properties dialog.
const noFilegroupItem = "(not applicable)"

// fileEdit tracks one Files-page row's pending state: an existing file whose
// logical name/size/growth/max size changed, a new file pending Add (IsNew), or
// an existing file pending Remove.
type fileEdit struct {
	origName string // "" for a brand-new file
	propsheet.PendingState

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
		origName: fl.Name, name: fl.Name, fileType: string(fl.Type), fileGroup: fl.FileGroup, path: fl.PhysicalName,
		sizeKB: fl.SizeKB, isPercentGrowth: fl.IsPercentGrowth, growthKB: fl.GrowthKB, growthPercent: fl.GrowthPercent, maxSizeKB: fl.MaxSizeKB,
		origSizeKB: fl.SizeKB, origIsPercentGrowth: fl.IsPercentGrowth, origGrowthKB: fl.GrowthKB, origGrowthPercent: fl.GrowthPercent, origMaxSizeKB: fl.MaxSizeKB,
	}
}

// changed reports whether this file's definition differs from what the server
// reported. Only the four things ALTER DATABASE ... MODIFY FILE can change are
// compared: fileType, fileGroup and path are fixed for an existing file, so an
// edit to them is not a change this page can write, and treating it as one would
// send an ALTER that silently does nothing.
//
// A method rather than two copies because the Files page needs the same answer in
// two places that must agree: GridRow.DirtyFn (is the page dirty at all) and apply
// (does this file get an ALTER). Two expressions listing the same fields drift: a
// field added to one only makes a page that never reports itself dirty (OK writes
// nothing) or is always dirty (OK always writes).
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

// modify builds the partial ALTER for an existing file: every field is left zero
// unless it changed, because gosmo reads a zero as "leave this property alone" and
// omits it.
//
// Hence each assignment is guarded. Sending the unchanged current value looks
// harmless but SIZE bites: MODIFY FILE treats it as a grow-to target and rejects a
// value below the file's current size, so editing only the autogrowth of a file
// that has since grown past its recorded size would fail with "MODIFY FILE failed.
// Specified size is less than or equal to current size". For the same reason a
// smaller size is not sent here at all (Msg 5039): it is a shrink (see shrinks).
func (e *fileEdit) modify() gosmo.DatabaseFileChanges {
	var m gosmo.DatabaseFileChanges
	if e.name != e.origName {
		m.NewName = e.name
	}
	if e.sizeKB > e.origSizeKB {
		m.SizeKB = e.sizeKB
	}
	if e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB || e.growthPercent != e.origGrowthPercent {
		// Exactly one of the two is set: gosmo lets GrowthPercent win when both are, and
		// the growth kind is a radio, so sending both would carry the radio's losing half
		// for nothing.
		//
		// A growth of zero must go through DisableGrowth, not the amount fields, because
		// gosmo reads a zero amount as "leave FILEGROWTH alone": turning autogrowth off
		// would produce an ALTER with no FILEGROWTH clause, and where growth was the only
		// edit, no ALTER at all (OK reported success and the file still grew).
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

// shrinks reports whether an existing file's size was lowered: written as DBCC
// SHRINKFILE (DatabaseFileInfo.Shrink), as SSMS does, since MODIFY FILE refuses
// a smaller SIZE (review plan M5).
func (e *fileEdit) shrinks() bool {
	return !e.IsNew && !e.Removing && e.sizeKB < e.origSizeKB
}

// spec builds the CREATE-side description of a new file. Unlike modify, every
// field is sent (no previous value to leave alone), except for a FILESTREAM
// file, which has no size or autogrowth to send.
func (e *fileEdit) spec() gosmo.DatabaseFileSpec {
	spec := gosmo.DatabaseFileSpec{
		Name: e.name, Type: gosmo.FileType(e.fileType), Path: e.path, MaxSizeKB: e.maxSizeKB,
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
	return fileSizeText(growthKB)
}

// maxSizeText shows a log's UNLIMITED max size as 2097152 MB, not Unlimited:
// the server stores it as 268435456 pages and SSMS 21 shows the same
// (docs/decisions.md § By design).
func maxSizeText(maxSizeKB int64) string {
	if maxSizeKB < 0 {
		return "Unlimited"
	}
	return fileSizeText(maxSizeKB)
}

// fileSizeText shows a file amount in MB where it is whole MB and in KB where it
// is not. Truncated to MB, a 64 KB growth read "0 MB" and a 9016 KB file 8 MB:
// the grid stated a value the file does not have.
func fileSizeText(kb int64) string {
	if kb%1024 != 0 {
		return strconv.FormatInt(kb, 10) + " KB"
	}
	return strconv.FormatInt(kb/1024, 10) + " MB"
}

// shrinkStoppedNote says where a shrink to targetKB stopped when that is above
// the target, "" otherwise: SHRINKFILE stops at the space the file's data
// occupies, without an error, and without this the only sign is the reloaded
// grid (review plan N2). The target is compared as sent — whole MB, rounded up
// by the apply, at least 1 (gosmo's Shrink) — so 0 MB reaching 1 MB isn't reported. Under WithScript
// reached is 0, never above.
func shrinkStoppedNote(name string, targetKB, reachedKB int64) string {
	sentKB := max((targetKB+1023)/1024, 1) * 1024
	if reachedKB <= sentKB {
		return ""
	}
	return fmt.Sprintf("%s stopped shrinking at %s, not %s: its data needs that much.",
		name, fileSizeText(reachedKB), fileSizeText(targetKB))
}

// databaseFilesPage is the Files page's state once loaded: the pending file
// edits, the grid over them, and the "Selected file" fields that edit the row
// the grid's cursor is on (current).
type databaseFilesPage struct {
	sc     *db.ServerConn
	dbName string

	edits   *propsheet.PendingEdits[*fileEdit]
	fgNames []string
	// fsGroup is which filegroups make a file put into them a FILESTREAM file.
	// gosmo reports the filegroup's own type_desc; nothing about the file being
	// added says it.
	fsGroup map[string]bool

	grid            *controls.DataGrid
	nameField       *propsheet.TextRow
	typeSelect      *propsheet.SelectRow
	filegroupSelect *propsheet.SelectRow
	pathField       *propsheet.TextRow
	sizeField       *propsheet.TextRow
	growthKind      *propsheet.RadioRow
	growthField     *propsheet.TextRow
	maxKind         *propsheet.RadioRow
	maxField        *propsheet.TextRow
	hint            *propsheet.HintRow

	current *fileEdit
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
			p := &databaseFilesPage{
				sc: sc, dbName: dbName,
				fgNames: make([]string, len(fgs)),
				fsGroup: make(map[string]bool, len(fgs)),
			}
			for i, fg := range fgs {
				p.fgNames[i] = fg.Name
				if fg.IsFileStream() {
					p.fsGroup[fg.Name] = true
				}
			}

			loaded := make([]*fileEdit, len(files))
			for i, fl := range files {
				loaded[i] = fileEditFromInfo(fl)
			}
			p.edits = newPendingEdits(databaseCollation(d), loaded,
				func(e *fileEdit) string { return e.name }, (*fileEdit).changed, (*fileEdit).reset)
			return p.form(opts.Owner), p.apply, nil
		},
	}
}

// form builds the page's rows and wires them to p.
func (p *databaseFilesPage) form(owner string) *propsheet.Form {
	p.grid = controls.NewDataGrid()
	p.grid.SetData(databaseFileColumns, p.rows())
	p.grid.SetCellCursor(true)

	p.nameField = propsheet.Text("Logical name", "", 24)
	p.typeSelect = propsheet.Select("File type", addableFileTypes, 0)
	p.filegroupSelect = propsheet.Select("Filegroup", p.fgNames, 0)
	p.pathField = propsheet.Text("Path", "", 40)
	p.sizeField = propsheet.Int("Initial size", 0, 0, 16777216, "MB")
	p.growthKind = propsheet.Radio("Growth by", []string{"Megabytes", "Percent"}, 0)
	p.growthField = propsheet.Int("Growth amount", 0, 0, 2097151, "")
	p.maxKind = propsheet.Radio("Max size", []string{"Unlimited", "Limited"}, 0)
	p.maxField = propsheet.Int("Max size limit", 0, 0, 16777216, "MB")

	p.typeSelect.SetOnChange(func(string) {
		p.showFilegroupFor(p.typeSelect.Value(), "")
		p.syncSizeRows(p.effectiveType(p.typeSelect.Value(), p.pickedFilegroup(p.typeSelect.Value())))
	})
	p.filegroupSelect.SetOnChange(func(string) {
		p.syncSizeRows(p.effectiveType(p.typeSelect.Value(), p.pickedFilegroup(p.typeSelect.Value())))
	})

	// BindGridDetail redraws after the commit, so the row moved off shows
	// what Apply will send rather than its loaded values.
	propsheet.BindGridDetail(p.grid, databaseFileColumns, p.rows, p.commitCurrent, p.syncFieldsFromSelection)

	p.hint = propsheet.Hint()
	addBtn := widgets.NewButton("Add", p.addFile)
	removeBtn := widgets.NewButton("Remove", p.removeFile)

	gridRow := propsheet.NewGridRow(p.grid, 10)
	gridRow.DirtyFn = p.edits.Dirty
	gridRow.RevertFn = func() {
		p.edits.Revert()
		resetGrid(p.grid, databaseFileColumns, p.rows(), 0)
		p.syncFieldsFromSelection()
	}

	f := propsheet.NewForm(
		propsheet.Section("Database files"),
		propsheet.Static("Owner", owner),
		gridRow,
		propsheet.Section("Selected file"),
		p.nameField, p.typeSelect, p.filegroupSelect, p.sizeField,
		p.growthKind, p.growthField, p.maxKind, p.maxField, p.pathField,
		propsheet.Buttons(addBtn, removeBtn),
		p.hint,
		propsheet.Note("A file added to a FILESTREAM filegroup is a FILESTREAM file: its path is a directory rather than a file, and it takes neither an initial size nor autogrowth, so those two are greyed. Choosing the filegroup is what makes it one — there is no file type to pick."),
	)
	f.SetCommit(p.commitCurrent)
	f.SetApplyConfirm(p.shrinkConfirm)
	return f
}

func (p *databaseFilesPage) rows() [][]string {
	vis := p.edits.Visible()
	rows := make([][]string, len(vis))
	for i, e := range vis {
		rows[i] = []string{
			e.name, e.fileType, e.fileGroup,
			fileSizeText(e.sizeKB),
			growthText(e.isPercentGrowth, e.growthKB, e.growthPercent),
			maxSizeText(e.maxSizeKB), e.path,
		}
	}
	return rows
}

func (p *databaseFilesPage) selected() *fileEdit {
	vis := p.edits.Visible()
	i := p.grid.SelectedRow()
	if i < 0 || i >= len(vis) {
		return nil
	}
	return vis[i]
}

// showFilegroupFor swaps the Filegroup dropdown between the real filegroup list
// and the single "(not applicable)" entry a LOG file gets, so the row never
// offers a choice that wouldn't be used.
func (p *databaseFilesPage) showFilegroupFor(fileType, fileGroup string) {
	if fileType == logFileType {
		p.filegroupSelect.SetItems([]string{noFilegroupItem})
		return
	}
	if fileGroup == "" {
		p.filegroupSelect.SetItems(p.fgNames)
		p.filegroupSelect.SetSelected(0)
		return
	}
	// Widened as the type picker is: a file can sit in a filegroup FileGroups
	// didn't list, and a stand-in name would read as the server's answer.
	items, i := propsheet.PreservingItems(p.fgNames, fileGroup)
	p.filegroupSelect.SetItems(items)
	p.filegroupSelect.SetSelected(i)
}

// pickedFilegroup reads the dropdown back, as "" for a LOG file (what
// DatabaseFileInfo.FileGroup holds for one).
func (p *databaseFilesPage) pickedFilegroup(fileType string) string {
	if fileType == logFileType {
		return ""
	}
	return p.filegroupSelect.Value()
}

// effectiveType is what a file put in fileGroup becomes. The picker offers ROWS
// and LOG; the filegroup turns a ROWS pick into FILESTREAM, so they are read
// together.
func (p *databaseFilesPage) effectiveType(picked, fileGroup string) string {
	if picked != logFileType && p.fsGroup[fileGroup] {
		return filestreamFileType
	}
	return picked
}

// syncSizeRows gates the size and growth spinners on whether the file can carry
// either. A FILESTREAM file cannot, and a live spinner whose value the
// statement drops is the silent wrong-thing this page's pickers are gated
// against; worse here, since sending them fails the whole Add with Msg 5509.
func (p *databaseFilesPage) syncSizeRows(fileType string) {
	fs := fileType == filestreamFileType
	p.sizeField.SetEnabled(!fs)
	p.growthField.SetEnabled(!fs)
	if fs {
		p.sizeField.SetValue("0")
		p.growthField.SetValue("0")
	}
}

// commitCurrent writes the fields back into the file they were synced from.
func (p *databaseFilesPage) commitCurrent() {
	current := p.current
	if current == nil {
		return
	}
	current.name = p.nameField.Value()
	// Only a new file reads type/filegroup/path back. They are fixed for an existing
	// one (changed() excludes them because MODIFY FILE can neither retype nor move a
	// file), so writing them could only record something untrue: a FILESTREAM file's
	// type is outside the picker's two items, so the picker shows a stand-in and
	// merely selecting the row would rewrite the file as ROWS. Same defect as
	// noFilegroupItem, one field over.
	if current.IsNew {
		current.fileGroup = p.pickedFilegroup(p.typeSelect.Value())
		current.fileType = p.effectiveType(p.typeSelect.Value(), current.fileGroup)
		current.path = p.pathField.Value()
	}
	// The spinners are whole MB, so each reads back only once the user has edited it
	// (Dirty is "since this file was selected": syncFieldsFromSelection resets each
	// baseline). Copied back unconditionally, a file not sized in whole MB (grown by a
	// percentage or a KB amount) would round down on every OK of an untouched page: a
	// smaller SIZE the server refuses, a cap lowered, and a growth under 1 MB read as
	// 0, which growthOff sends as FILEGROWTH = 0 — autogrowth silently switched off.
	if p.sizeField.Dirty() {
		if n, err := p.sizeField.IntValue(); err == nil {
			current.sizeKB = n * 1024
		}
	}
	// The radio and the amount travel together: switching the unit alone must
	// re-read the amount in the new one.
	if p.growthKind.Dirty() || p.growthField.Dirty() {
		current.isPercentGrowth = p.growthKind.Selected() == 1
		if n, err := p.growthField.IntValue(); err == nil {
			if current.isPercentGrowth {
				current.growthPercent = int(n)
			} else {
				current.growthKB = n * 1024
			}
		}
	}
	if p.maxKind.Dirty() || p.maxField.Dirty() {
		if p.maxKind.Selected() == 0 {
			current.maxSizeKB = -1
		} else if n, err := p.maxField.IntValue(); err == nil {
			current.maxSizeKB = n * 1024
		}
	}
}

// syncFieldsFromSelection makes the grid's selected file current and shows it
// in the fields.
func (p *databaseFilesPage) syncFieldsFromSelection() {
	p.current = p.selected()
	current := p.current
	if current == nil {
		p.nameField.SetValue("")
		p.pathField.SetValue("")
		p.sizeField.SetValue("0")
		p.growthField.SetValue("0")
		p.maxField.SetValue("0")
		return
	}
	p.nameField.SetValue(current.name)
	// propsheet.PreservingItems, not indexOf: sys.database_files also reports FILESTREAM (and
	// memory-optimized) files, whose type is in neither item this page can create.
	// indexOf would show "ROWS" for one as though the server had said so.
	typeItems, typeIdx := propsheet.PreservingItems(addableFileTypes, current.fileType)
	p.typeSelect.SetItems(typeItems)
	p.typeSelect.SetSelected(typeIdx)
	p.showFilegroupFor(current.fileType, current.fileGroup)
	p.syncSizeRows(current.fileType)
	p.pathField.SetValue(current.path)
	p.sizeField.SetValue(strconv.FormatInt(current.sizeKB/1024, 10))
	if current.isPercentGrowth {
		p.growthKind.SetSelected(1)
		p.growthField.SetValue(strconv.Itoa(current.growthPercent))
	} else {
		p.growthKind.SetSelected(0)
		p.growthField.SetValue(strconv.FormatInt(current.growthKB/1024, 10))
	}
	if current.maxSizeKB < 0 {
		p.maxKind.SetSelected(0)
		p.maxField.SetValue("0")
	} else {
		p.maxKind.SetSelected(1)
		p.maxField.SetValue(strconv.FormatInt(current.maxSizeKB/1024, 10))
	}
}

// addFile is Add: a new file from the fields.
func (p *databaseFilesPage) addFile() {
	// Deliberately does NOT call commitCurrent(): these fields double as the
	// previously-selected file's live edit, and commitCurrent() writes nameField's
	// text into that file's rename target, so a new name typed here to Add would
	// silently rename the wrong file. Any not-yet-applied edit to the previously
	// selected file is left as last synced from its own selection.
	name := p.nameField.Value()
	if name == "" {
		p.hint.Set("Type a logical file name first.")
		return
	}
	// The type picker widens to show a selected file's real type (FILESTREAM, say);
	// Add must not carry one of those into a spec this page can't build correctly.
	if !slices.Contains(addableFileTypes, p.typeSelect.Value()) {
		p.hint.Set("Set File type to ROWS or LOG — this page adds data and log files only.")
		return
	}
	if i := p.edits.Index(name); i >= 0 {
		// Already present: say so and select it, rather than leave the button looking
		// broken.
		p.hint.Set("A file named " + name + " is already listed — its row is selected below.")
		p.grid.SetSelectedRow(i)
		p.syncFieldsFromSelection()
		return
	}
	p.hint.Clear()
	fileGroup := p.pickedFilegroup(p.typeSelect.Value())
	e := &fileEdit{
		name: name, fileType: p.effectiveType(p.typeSelect.Value(), fileGroup),
		fileGroup: fileGroup, path: p.pathField.Value(),
		maxSizeKB: -1,
	}
	if n, err := p.sizeField.IntValue(); err == nil {
		e.sizeKB = n * 1024
	}
	e.isPercentGrowth = p.growthKind.Selected() == 1
	if n, err := p.growthField.IntValue(); err == nil {
		if e.isPercentGrowth {
			e.growthPercent = int(n)
		} else {
			e.growthKB = n * 1024
		}
	}
	if p.maxKind.Selected() == 1 {
		if n, err := p.maxField.IntValue(); err == nil {
			e.maxSizeKB = n * 1024
		}
	}
	p.edits.Add(e)
	resetGrid(p.grid, databaseFileColumns, p.rows(), len(p.edits.Visible())-1)
	p.syncFieldsFromSelection()
}

// removeFile is Remove: mark the selected file for dropping.
func (p *databaseFilesPage) removeFile() {
	e := p.selected()
	if e == nil {
		p.hint.Set("Select a file in the grid above to remove it.")
		return
	}
	p.hint.Clear()
	p.edits.Remove(e)
	p.current = nil
	resetGrid(p.grid, databaseFileColumns, p.rows(), 0)
	p.syncFieldsFromSelection()
}

// shrinkConfirm is the Apply confirmation when a lowered size means SHRINKFILE.
func (p *databaseFilesPage) shrinkConfirm() string {
	var names []string
	for _, e := range p.edits.All() {
		if e.shrinks() {
			names = append(names, e.name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "Lowering the size of " + strings.Join(names, ", ") + " shrinks the file (DBCC SHRINKFILE). " +
		"It can be slow on a large file, moves pages and so fragments indexes, and stops short " +
		"of the target where the file's data does not fit."
}

func (p *databaseFilesPage) apply(ctx context.Context) error {
	d, err := p.sc.Server.DatabaseByName(ctx, p.dbName)
	if err != nil {
		return err
	}
	for _, e := range p.edits.All() {
		switch {
		case e.Removing:
			if err := d.FileRef(e.origName).Drop(ctx); err != nil {
				return err
			}
		case e.IsNew:
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
			// After the Alter, so a renamed file is shrunk by its new name.
			if e.shrinks() {
				// SHRINKFILE takes whole MB: rounded up, so the file never ends below the size asked for.
				res, err := d.FileRef(e.name).Shrink(ctx, gosmo.ShrinkFileOptions{TargetMB: new((e.sizeKB + 1023) / 1024)})
				if err != nil {
					return err
				}
				if note := shrinkStoppedNote(e.name, e.sizeKB, res.CurrentKB); note != "" {
					noteApply(ctx, note)
				}
			}
		}
	}
	return nil
}
