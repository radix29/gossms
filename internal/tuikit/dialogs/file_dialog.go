package dialogs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// FileDialogMode selects Open (pick an existing file) or Save (pick or
// confirm a destination path) behaviour for FileDialog.
type FileDialogMode int

const (
	FileDialogOpen FileDialogMode = iota
	FileDialogSave
)

// fileEntry is one row of the current directory listing.
type fileEntry struct {
	name  string
	isDir bool
	size  int64
	mod   time.Time
}

// Geometry: fileDialogW/H size the dialog; fileListRows is how many entries
// are visible at once; fileSizeColW/fileModColW are the two right-hand
// columns' widths (Name takes whatever's left).
const (
	fileDialogW  = 76
	fileDialogH  = 24
	fileListRows = 12
	fileSizeColW = 10
	fileModColW  = 17
)

// Tab-order focus stops.
const (
	ffPath = iota
	ffList
	ffName
	ffButtons
)

// FileDialog is a generic, embeddable Open/Save file picker: a persistent
// path bar, a scrollable Name/Size/Modified directory listing, and a
// filename field, with shell-style Tab completion on both text fields. It
// only ever touches the local filesystem — no gosmo/SQL Server knowledge —
// so a single instance can be reused (via ShowOpen/ShowSave) everywhere a
// host app needs to choose a file or a save destination.
type FileDialog struct {
	ModalDialog

	mode FileDialogMode

	dir       string
	entries   []fileEntry
	listErr   string
	sel       int
	scroll    int
	typeahead string

	// listMouseDragging distinguishes a fresh Button1 press on the file/dir
	// list from a continued hold over the same row — mirrors TreeView's/
	// DataGrid's field of the same name and purpose. Without it, tcell's
	// all-motion mouse tracking resends Buttons()==Button1 on every cursor
	// motion while the button stays down, so a single click on an
	// already-selected row can call activateSelected() more than once.
	// Named distinctly from the embedded ModalDialog's own mouseDragging
	// field (a separate latch, for the button row) to avoid shadowing it.
	listMouseDragging bool

	pathField *widgets.InputField
	nameField *widgets.InputField

	focus    int
	btnFocus int

	// OnChoose fires once the user confirms a file (Open) or destination
	// path (Save) — after the OnConfirmOverwrite round-trip, for Save mode,
	// when the target already exists and a handler is set.
	OnChoose func(path string)
	// OnCancel fires on Escape or the Cancel button. Optional.
	OnCancel func()
	// OnConfirmOverwrite, if set, is called instead of firing OnChoose
	// directly (Save mode only) when the chosen path already exists. The
	// host decides how to ask — typically its own ConfirmDialog — and calls
	// proceed() to continue, or does nothing to leave the dialog open.
	OnConfirmOverwrite func(path string, proceed func())
}

// NewFileDialog creates the dialog.
func NewFileDialog(s tcell.Screen) *FileDialog {
	d := &FileDialog{}
	d.InitModal(s, "Open File", fileDialogW, fileDialogH)
	contentW := fileDialogW - 4                         // InnerRect (-2) minus this dialog's own 1-col margin on each side
	fieldW := contentW - core.DisplayWidth("Path:") - 3 // label gap + '[' + ']'
	d.pathField = widgets.NewInputField("Path:", fieldW, false)
	d.nameField = widgets.NewInputField("File:", fieldW, false)
	return d
}

// ShowOpen configures the dialog to pick an existing file for reading and
// displays it. startPath seeds the initial directory (and, if it names a
// file, the initial selection) — an already-open file's path, or "" for
// the working directory.
func (d *FileDialog) ShowOpen(title, startPath string, onChoose func(path string)) {
	d.mode = FileDialogOpen
	d.SetTitle(title)
	d.OnChoose = onChoose
	d.start(startPath, ffList)
}

// ShowSave configures the dialog to pick or confirm a destination path and
// displays it.
func (d *FileDialog) ShowSave(title, startPath string, onChoose func(path string)) {
	d.mode = FileDialogSave
	d.SetTitle(title)
	d.OnChoose = onChoose
	d.start(startPath, ffName)
}

// start resets per-session state, loads startPath's directory, and shows
// the dialog with initialFocus focused.
func (d *FileDialog) start(startPath string, initialFocus int) {
	dir, name := splitStartPath(startPath)
	d.loadDir(dir)
	d.nameField.SetValue(name)
	if name != "" {
		d.selectByName(name)
	}
	d.btnFocus = 0
	d.setFocus(initialFocus)
	d.ModalDialog.Show()
}

// splitStartPath separates a caller-supplied initial path into a directory
// to open and a filename to preselect — "" (no name), a bare filename
// ("query.sql"), or a full path (an already-open file) are all valid.
func splitStartPath(startPath string) (dir, name string) {
	if startPath == "" {
		wd, _ := os.Getwd()
		return wd, ""
	}
	if dirPart, base := filepath.Split(startPath); dirPart != "" {
		return dirPart, base
	}
	wd, _ := os.Getwd()
	return wd, startPath
}

// loadDir lists dir's contents into d.entries — directories first, then
// files, both alphabetical case-insensitive — prefixed with a ".." entry
// unless dir is already the filesystem root. Resets selection/scroll and
// updates the path field to reflect the new current directory.
func (d *FileDialog) loadDir(dir string) {
	clean := filepath.Clean(dir)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	d.dir = clean
	d.pathField.SetValue(clean + string(filepath.Separator))
	d.sel, d.scroll = 0, 0

	infos, err := os.ReadDir(clean)
	if err != nil {
		d.entries = nil
		d.listErr = err.Error()
		return
	}
	d.listErr = ""

	var dirs, files []fileEntry
	for _, e := range infos {
		fe := fileEntry{name: e.Name(), isDir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			fe.size = info.Size()
			fe.mod = info.ModTime()
		}
		if fe.isDir {
			dirs = append(dirs, fe)
		} else {
			files = append(files, fe)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].name) < strings.ToLower(dirs[j].name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].name) < strings.ToLower(files[j].name) })

	entries := make([]fileEntry, 0, len(dirs)+len(files)+1)
	if parent := filepath.Dir(clean); parent != clean {
		entries = append(entries, fileEntry{name: "..", isDir: true})
	}
	entries = append(entries, dirs...)
	entries = append(entries, files...)
	d.entries = entries
}

// selectByName moves the list selection to the entry named name, if any.
func (d *FileDialog) selectByName(name string) {
	for i, e := range d.entries {
		if e.name == name {
			d.sel = i
			d.ensureVisible()
			return
		}
	}
}

// FocusedField returns whichever text field currently has keyboard focus
// (the path bar or the filename field), or nil when the list or the button
// row is focused. Exported so a host app's clipboard plumbing — which
// resolves Cut/Copy/Paste's target from whichever InputField/Editor is
// focused across every dialog — can participate without FileDialog needing
// any notion of a clipboard itself.
func (d *FileDialog) FocusedField() *widgets.InputField {
	switch d.focus {
	case ffPath:
		return d.pathField
	case ffName:
		return d.nameField
	}
	return nil
}

func (d *FileDialog) setFocus(f int) {
	d.focus = f
	d.pathField.Focus(f == ffPath)
	d.nameField.Focus(f == ffName)
	d.typeahead = ""
}

func (d *FileDialog) buttonLabels() []string {
	label := "Open"
	if d.mode == FileDialogSave {
		label = "Save"
	}
	return []string{label, "Cancel"}
}

// listRect returns the on-screen rectangle of the Name/Size/Modified list,
// shared by Draw and HandleMouse so hit-testing always matches what was
// actually drawn.
func (d *FileDialog) listRect() core.Rect {
	inner := d.InnerRect()
	return core.Rect{X: inner.X + 1, Y: inner.Y + 4, W: inner.W - 2, H: fileListRows}
}

// nameColWidth returns the Name column's width for a list area contentW
// wide — whatever's left after the Size/Modified columns and their gaps.
func nameColWidth(contentW int) int {
	return contentW - fileSizeColW - fileModColW - 2
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

// confirmFocused runs Enter's context-dependent action for whichever
// control currently has focus.
func (d *FileDialog) confirmFocused() {
	switch d.focus {
	case ffPath:
		d.navigateTyped()
	case ffList:
		d.activateSelected()
	case ffName:
		d.confirmChoice()
	case ffButtons:
		d.activateButton()
	}
}

// navigateTyped runs Enter on the path field: descends into the typed path
// if it's a directory, selects it in the listing (switching focus to the
// name field) if it's an existing file, or attempts to list it anyway
// (surfacing the resulting error) if it's neither.
func (d *FileDialog) navigateTyped() {
	typed := strings.TrimSpace(d.pathField.Value())
	target := d.dir
	if typed != "" {
		target = typed
		if !filepath.IsAbs(target) {
			target = filepath.Join(d.dir, target)
		}
	}
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		d.loadDir(filepath.Dir(target))
		name := filepath.Base(target)
		d.nameField.SetValue(name)
		d.selectByName(name)
		d.setFocus(ffName)
		return
	}
	d.loadDir(target)
	d.setFocus(ffList)
}

// activateSelected runs Enter/a same-row click on the list: descends into
// the selected directory, or confirms the selected file as the chosen path.
func (d *FileDialog) activateSelected() {
	if d.sel < 0 || d.sel >= len(d.entries) {
		return
	}
	e := d.entries[d.sel]
	if e.isDir {
		target := filepath.Join(d.dir, e.name)
		if e.name == ".." {
			target = filepath.Dir(d.dir)
		}
		d.loadDir(target)
		return
	}
	d.nameField.SetValue(e.name)
	d.confirmChoice()
}

// syncNameFromSelection copies the selected entry's name into the name
// field, unless it's a directory — matching every desktop file dialog's
// convention of leaving a typed filename alone while browsing folders.
func (d *FileDialog) syncNameFromSelection() {
	if d.sel < 0 || d.sel >= len(d.entries) {
		return
	}
	if e := d.entries[d.sel]; !e.isDir {
		d.nameField.SetValue(e.name)
	}
}

// confirmChoice builds the full path from the name field and current
// directory and runs finish on it.
func (d *FileDialog) confirmChoice() {
	name := strings.TrimSpace(d.nameField.Value())
	if name == "" {
		return
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(d.dir, path)
	}
	d.finish(path)
}

// finish is the common tail of every "the user picked path" route: in Save
// mode, an existing target is routed through OnConfirmOverwrite (if set)
// before OnChoose fires.
func (d *FileDialog) finish(path string) {
	if d.mode == FileDialogSave && d.OnConfirmOverwrite != nil {
		if _, err := os.Stat(path); err == nil {
			d.OnConfirmOverwrite(path, func() { d.choose(path) })
			return
		}
	}
	d.choose(path)
}

func (d *FileDialog) choose(path string) {
	d.Hide()
	if d.OnChoose != nil {
		d.OnChoose(path)
	}
}

func (d *FileDialog) cancel() {
	d.Hide()
	if d.OnCancel != nil {
		d.OnCancel()
	}
}

func (d *FileDialog) activateButton() {
	if d.btnFocus == 0 {
		d.confirmChoice()
	} else {
		d.cancel()
	}
}
