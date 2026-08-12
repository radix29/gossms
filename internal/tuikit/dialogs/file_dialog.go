package dialogs

import (
	"slices"
	"strings"

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
// reaches the filesystem only through a FileSystem — no os/filepath calls of
// its own, and no gosmo/SQL Server knowledge — so a single instance can be
// reused (via ShowOpen/ShowSave for the local machine, ShowOpenOn/ShowSaveOn
// for anything else) everywhere a host app needs to choose a file or a save
// destination.
type FileDialog struct {
	ModalDialog

	mode FileDialogMode

	// fs is whose filesystem is being browsed. Every Show* entry point sets
	// it, so one caller's remote filesystem can never leak into the next
	// caller's local browse.
	fs FileSystem

	dir     string
	entries []FileEntry
	listErr string
	// busy, when set, replaces the listing with a one-line message while a
	// FileSystem call is in flight — see showBusy.
	busy      string
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

	// dragField is the input field that claimed the current Button1
	// gesture, nil between gestures. Motion goes to it wherever the pointer
	// is, so dragging a selection out of the field's rect keeps extending it
	// instead of freezing at the boundary — the hit test below it is what
	// used to stop the drag dead.
	dragField *widgets.InputField

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
	d.ShowOpenOn(LocalFileSystem{}, title, startPath, onChoose)
}

// ShowOpenOn is ShowOpen against fs instead of the local machine.
func (d *FileDialog) ShowOpenOn(fs FileSystem, title, startPath string, onChoose func(path string)) {
	d.mode = FileDialogOpen
	d.fs = fs
	d.SetTitle(title)
	d.OnChoose = onChoose
	d.start(startPath, ffList)
}

// ShowSave configures the dialog to pick or confirm a destination path and
// displays it.
func (d *FileDialog) ShowSave(title, startPath string, onChoose func(path string)) {
	d.ShowSaveOn(LocalFileSystem{}, title, startPath, onChoose)
}

// ShowSaveOn is ShowSave against fs instead of the local machine.
func (d *FileDialog) ShowSaveOn(fs FileSystem, title, startPath string, onChoose func(path string)) {
	d.mode = FileDialogSave
	d.fs = fs
	d.SetTitle(title)
	d.OnChoose = onChoose
	d.start(startPath, ffName)
}

// FileSystem returns the filesystem the dialog is currently browsing. Never
// nil once any Show* has run; the local machine before that.
func (d *FileDialog) FileSystem() FileSystem {
	if d.fs == nil {
		d.fs = LocalFileSystem{}
	}
	return d.fs
}

// start resets per-session state, loads startPath's directory, and shows
// the dialog with initialFocus focused.
func (d *FileDialog) start(startPath string, initialFocus int) {
	dir, name := d.splitStartPath(startPath)
	d.nameField.SetValue(name)
	d.entries, d.listErr = nil, ""
	d.btnFocus = 0
	// A latch must not survive into the next showing: a dialog dismissed
	// mid-drag would otherwise reopen still routing every click to that field.
	d.dragField = nil
	d.setFocus(initialFocus)
	// Shown *before* the first listing, not after. That listing is a
	// FileSystem call like any other, and on a remote filesystem it is the
	// slowest of the session — showBusy can only paint a dialog that is
	// already visible, so loading first left the whole open with no feedback.
	d.ModalDialog.Show()
	d.loadDir(dir)
	if name != "" {
		d.selectByName(name)
	}
}

// showBusy paints the dialog with msg in place of the listing and flushes it,
// ahead of a FileSystem call that may block.
//
// FileSystem is synchronous by design, so a remote one spends a network round
// trip inside the event handler and the whole TUI stops until it returns —
// with the *previous* directory still on screen, which is indistinguishable
// from a hang. This is the one place tuikit paints outside the app's own draw
// cycle, so it is limited to the filesystems that need it: only a
// BlockingFileSystem gets a repaint, which is also what keeps the local
// browse from flickering on every navigation.
func (d *FileDialog) showBusy(msg string) {
	if d.screen == nil || !d.Visible() {
		return
	}
	if b, ok := d.FileSystem().(BlockingFileSystem); !ok || !b.Blocking() {
		return
	}
	d.busy = msg
	d.Draw(d.screen)
	d.screen.Show()
}

// entryFor returns the listing row for path when path names something in the
// directory already on screen, which is where a typed path usually points.
// It spares a FileSystem.Exists round trip — a remote filesystem answers that
// one over the network, on a keystroke.
func (d *FileDialog) entryFor(path string) (FileEntry, bool) {
	fs := d.FileSystem()
	dir, name := fs.Split(path)
	if name == "" || name == ".." || fs.Clean(dir) != d.dir {
		return FileEntry{}, false
	}
	for _, e := range d.entries {
		if e.Name == name {
			return e, true
		}
	}
	return FileEntry{}, false
}

// splitStartPath separates a caller-supplied initial path into a directory
// to open and a filename to preselect — "" (no name), a bare filename
// ("query.sql"), or a full path (an already-open file) are all valid.
func (d *FileDialog) splitStartPath(startPath string) (dir, name string) {
	fs := d.FileSystem()
	if startPath == "" {
		return fs.Default(), ""
	}
	if dirPart, base := fs.Split(startPath); dirPart != "" {
		return dirPart, base
	}
	return fs.Default(), startPath
}

// loadDir lists dir's contents into d.entries — directories first, then
// files, both alphabetical case-insensitive — prefixed with a ".." entry
// unless dir is already the filesystem root. Resets selection/scroll and
// updates the path field to reflect the new current directory.
func (d *FileDialog) loadDir(dir string) {
	fs := d.FileSystem()
	sep := fs.Separator()
	clean := fs.Clean(dir)
	d.dir = clean
	d.pathField.SetValue(strings.TrimSuffix(clean, sep) + sep)
	d.sel, d.scroll = 0, 0

	d.showBusy("Listing " + clean + " ...")
	infos, err := fs.List(clean)
	d.busy = ""
	if err != nil {
		d.entries = nil
		d.listErr = err.Error()
		return
	}
	d.listErr = ""

	var dirs, files []FileEntry
	for _, e := range infos {
		if e.Name == "." || e.Name == ".." {
			continue // the ".." row below is the dialog's own
		}
		if e.IsDir {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	byLowerName := func(a, b FileEntry) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	}
	slices.SortStableFunc(dirs, byLowerName)
	slices.SortStableFunc(files, byLowerName)

	entries := make([]FileEntry, 0, len(dirs)+len(files)+1)
	if parent := fs.Parent(clean); parent != clean {
		entries = append(entries, FileEntry{Name: "..", IsDir: true})
	}
	entries = append(entries, dirs...)
	entries = append(entries, files...)
	d.entries = entries
}

// selectByName moves the list selection to the entry named name, if any.
func (d *FileDialog) selectByName(name string) {
	for i, e := range d.entries {
		if e.Name == name {
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
	fs := d.FileSystem()
	typed := strings.TrimSpace(d.pathField.Value())
	target := d.dir
	if typed != "" {
		target = typed
		if !fs.IsAbs(target) {
			target = fs.Join(d.dir, target)
		}
	}
	isFile := false
	if e, ok := d.entryFor(target); ok {
		isFile = !e.IsDir
	} else {
		// A failed probe falls through to loadDir, which surfaces the same
		// failure in listErr rather than swallowing it here.
		exists, isDir, _ := fs.Exists(target)
		isFile = exists && !isDir
	}
	if isFile {
		dir, name := fs.Split(target)
		d.loadDir(dir)
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
	fs := d.FileSystem()
	e := d.entries[d.sel]
	if e.IsDir {
		target := fs.Join(d.dir, e.Name)
		if e.Name == ".." {
			target = fs.Parent(d.dir)
		}
		d.loadDir(target)
		return
	}
	d.nameField.SetValue(e.Name)
	d.confirmChoice()
}

// syncNameFromSelection copies the selected entry's name into the name
// field, unless it's a directory — matching every desktop file dialog's
// convention of leaving a typed filename alone while browsing folders.
func (d *FileDialog) syncNameFromSelection() {
	if d.sel < 0 || d.sel >= len(d.entries) {
		return
	}
	if e := d.entries[d.sel]; !e.IsDir {
		d.nameField.SetValue(e.Name)
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
	if fs := d.FileSystem(); !fs.IsAbs(path) {
		path = fs.Join(d.dir, path)
	}
	d.finish(path)
}

// finish is the common tail of every "the user picked path" route: in Save
// mode, an existing target is routed through OnConfirmOverwrite (if set)
// before OnChoose fires.
func (d *FileDialog) finish(path string) {
	if d.mode == FileDialogSave && d.OnConfirmOverwrite != nil {
		// "Couldn't ask" prompts as if the file were there. A remote
		// filesystem answers this over the network, and treating its timeout
		// as "not there" skips the overwrite guard silently — the one
		// outcome worse than an unnecessary prompt, whose Yes does exactly
		// what the user asked for anyway.
		exists, _, err := d.FileSystem().Exists(path)
		if exists || err != nil {
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
