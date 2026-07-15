package dialogs

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
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

// ---------------------------------------------------------------------------
// Tab completion
// ---------------------------------------------------------------------------

// completeField extends f's current value with the longest name prefix
// shared by every directory entry (dirsOnly, for the path field — it only
// ever navigates directories) or every entry (the name field) matching
// what's typed after the last path separator — shell-style Tab completion.
// Returns false (leaving f untouched) when there's nothing to complete, so
// the caller can fall through to ordinary Tab focus-cycling instead, the
// same convention every other tuikit field follows for keys it doesn't act
// on.
func (d *FileDialog) completeField(f *widgets.InputField, dirsOnly bool) bool {
	val := f.Value()
	dirPart, base := filepath.Split(val)
	searchDir := d.dir
	if dirPart != "" {
		if filepath.IsAbs(dirPart) {
			searchDir = filepath.Clean(dirPart)
		} else {
			searchDir = filepath.Join(d.dir, dirPart)
		}
	}
	infos, err := os.ReadDir(searchDir)
	if err != nil {
		return false
	}
	var candidates []string
	for _, e := range infos {
		if dirsOnly && !e.IsDir() {
			continue
		}
		if base == "" || strings.HasPrefix(e.Name(), base) {
			candidates = append(candidates, e.Name())
		}
	}
	if len(candidates) == 0 {
		return false
	}
	common := commonPrefix(candidates)
	if common == "" || common == base {
		return false
	}
	completed := dirPart + common
	if len(candidates) == 1 {
		if info, err := os.Stat(filepath.Join(searchDir, common)); err == nil && info.IsDir() {
			completed += string(filepath.Separator)
		}
	}
	f.SetValue(completed)
	return true
}

// commonPrefix returns the longest string every element of strs starts
// with. strs is never empty when called.
func commonPrefix(strs []string) string {
	prefix := strs[0]
	for _, s := range strs[1:] {
		for len(prefix) > 0 && !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

// formatFileSize renders n bytes as a short human-readable size (e.g.
// "1.8 KB"), matching the mockup's column style.
func formatFileSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGT"[exp]) + "B"
}

// ---------------------------------------------------------------------------
// List navigation
// ---------------------------------------------------------------------------

func (d *FileDialog) handleListKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp:
		if d.sel > 0 {
			d.sel--
			d.ensureVisible()
			d.syncNameFromSelection()
		}
	case tcell.KeyDown:
		if d.sel < len(d.entries)-1 {
			d.sel++
			d.ensureVisible()
			d.syncNameFromSelection()
		}
	case tcell.KeyPgUp:
		d.sel = core.Max(0, d.sel-fileListRows)
		d.ensureVisible()
		d.syncNameFromSelection()
	case tcell.KeyPgDn:
		d.sel = core.Min(len(d.entries)-1, d.sel+fileListRows)
		d.ensureVisible()
		d.syncNameFromSelection()
	case tcell.KeyHome:
		d.sel, d.scroll = 0, 0
		d.syncNameFromSelection()
	case tcell.KeyEnd:
		d.sel = len(d.entries) - 1
		d.ensureVisible()
		d.syncNameFromSelection()
	default:
		if r := core.EvRune(ev); r != 0 && ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == 0 {
			d.typeaheadJump(r)
		} else {
			return false
		}
	}
	return true
}

// typeaheadJump extends the pending typeahead buffer with r and jumps
// selection to the first entry whose name starts with it (case-
// insensitive); if nothing matches the extended buffer, it restarts from
// just r — classic file-manager "type to jump" navigation.
func (d *FileDialog) typeaheadJump(r rune) {
	for _, candidate := range []string{d.typeahead + string(r), string(r)} {
		lower := strings.ToLower(candidate)
		for i, e := range d.entries {
			if strings.HasPrefix(strings.ToLower(e.name), lower) {
				d.typeahead = candidate
				d.sel = i
				d.ensureVisible()
				d.syncNameFromSelection()
				return
			}
		}
	}
}

func (d *FileDialog) ensureVisible() {
	if d.sel < d.scroll {
		d.scroll = d.sel
	}
	if d.sel >= d.scroll+fileListRows {
		d.scroll = d.sel - fileListRows + 1
	}
	if d.scroll < 0 {
		d.scroll = 0
	}
}

// ---------------------------------------------------------------------------
// Draw
// ---------------------------------------------------------------------------

func (d *FileDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	inner := d.InnerRect()
	p := theme.Active()
	contentX := inner.X + 1
	contentW := inner.W - 2
	// -1: reserve the list's rightmost column for the scrollbar (see
	// listRect/DrawScrollbar below), so a full-width Modified timestamp
	// never gets its last digits overwritten by the scrollbar track/thumb.
	nameColW := nameColWidth(contentW - 1)

	d.pathField.SetBounds(contentX, inner.Y)
	d.pathField.Draw(s)

	headerY := inner.Y + 2
	headerStyle := theme.StyleGridHeader()
	core.FillRect(s, core.Rect{X: contentX, Y: headerY, W: contentW, H: 1}, ' ', headerStyle)
	core.DrawTextClipped(s, contentX, headerY, nameColW, headerStyle, "Name")
	core.DrawTextRight(s, contentX+nameColW+1, headerY, fileSizeColW, headerStyle, "Size")
	core.DrawTextRight(s, contentX+nameColW+1+fileSizeColW+1, headerY, fileModColW, headerStyle, "Modified")

	sepStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, contentX, inner.Y+3, contentW, sepStyle)

	lr := d.listRect()
	baseStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if d.listErr != "" {
		core.FillRect(s, lr, ' ', baseStyle)
		errStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawTextClipped(s, lr.X, lr.Y, lr.W, errStyle, d.listErr)
	} else {
		for row := 0; row < lr.H; row++ {
			idx := d.scroll + row
			y := lr.Y + row
			if idx >= len(d.entries) {
				core.FillRect(s, core.Rect{X: lr.X, Y: y, W: lr.W, H: 1}, ' ', baseStyle)
				continue
			}
			d.drawEntry(s, y, lr.X, nameColW, idx)
		}
		if len(d.entries) > lr.H {
			sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
			sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
			core.DrawScrollbar(s, lr.Right()-1, lr.Y, lr.H, len(d.entries), lr.H, d.scroll, sbStyle, sbThumb)
		}
	}

	d.nameField.SetBounds(contentX, inner.Y+fileListRows+5)
	d.nameField.Draw(s)

	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttonLabels(), d.btnFocus)
}

// drawEntry renders one list row at (x,y) — the Name column icon/marker,
// clipped to nameColW, plus the right-aligned Size/Modified columns.
func (d *FileDialog) drawEntry(s tcell.Screen, y, x, nameColW, idx int) {
	p := theme.Active()
	e := d.entries[idx]

	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	marker := "  "
	if idx == d.sel {
		marker = "▸ "
		if d.focus == ffList {
			st = theme.StyleSelected()
		} else {
			st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextHighlight)
		}
	}
	// +1: also paint the scrollbar gutter column so the row's selection
	// highlight reaches the dialog's right edge even when no scrollbar is
	// drawn over it this frame (see Draw's nameColW comment).
	core.FillRect(s, core.Rect{X: x, Y: y, W: nameColW + 1 + fileSizeColW + 1 + fileModColW + 1, H: 1}, ' ', st)

	icon, name := "📄", e.name
	if e.isDir {
		icon = "📁"
		name += "/"
	}
	core.DrawTextClipped(s, x, y, nameColW, st, marker+icon+" "+name)

	sizeText := formatFileSize(e.size)
	if e.isDir {
		sizeText = "DIR"
	}
	core.DrawTextRight(s, x+nameColW+1, y, fileSizeColW, st, sizeText)

	if e.name != ".." {
		modX := x + nameColW + 1 + fileSizeColW + 1
		core.DrawTextRight(s, modX, y, fileModColW, st, e.mod.Format("2006-01-02 15:04"))
	}
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

func (d *FileDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.cancel()
		return true
	case tcell.KeyEnter:
		d.confirmFocused()
		return true
	case tcell.KeyTab:
		switch d.focus {
		case ffPath:
			if d.completeField(d.pathField, true) {
				return true
			}
		case ffName:
			if d.completeField(d.nameField, false) {
				return true
			}
		}
		d.setFocus((d.focus + 1) % 4)
		return true
	case tcell.KeyBacktab:
		d.setFocus((d.focus - 1 + 4) % 4)
		return true
	}

	switch d.focus {
	case ffPath:
		return d.pathField.HandleKey(ev)
	case ffName:
		return d.nameField.HandleKey(ev)
	case ffList:
		return d.handleListKey(ev)
	case ffButtons:
		if ev.Key() == tcell.KeyLeft || ev.Key() == tcell.KeyRight {
			d.btnFocus = (d.btnFocus + 1) % 2
		}
		return true
	}
	return true
}

func (d *FileDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	// Forward a release to whichever field currently has focus, so a
	// text-selection drag started in it terminates cleanly even if the
	// release happens elsewhere in the dialog — mirrors ConnectDialog.
	if ev.Buttons() == tcell.ButtonNone {
		switch d.focus {
		case ffPath:
			d.pathField.HandleMouse(ev)
		case ffName:
			d.nameField.HandleMouse(ev)
		}
		return true
	}
	if i := d.ButtonClicked(ev, d.buttonLabels()); i >= 0 {
		d.btnFocus = i
		d.activateButton()
		return true
	}

	mx, my := ev.Position()
	switch ev.Buttons() {
	case tcell.Button1:
		if d.pathField.HitTest(mx, my) {
			d.setFocus(ffPath)
			d.pathField.HandleMouse(ev)
			return true
		}
		if d.nameField.HitTest(mx, my) {
			d.setFocus(ffName)
			d.nameField.HandleMouse(ev)
			return true
		}
		if lr := d.listRect(); lr.Contains(mx, my) {
			if idx := d.scroll + (my - lr.Y); idx >= 0 && idx < len(d.entries) {
				same := idx == d.sel
				d.sel = idx
				d.setFocus(ffList)
				d.syncNameFromSelection()
				if same {
					d.activateSelected()
				}
			}
			return true
		}
	case tcell.WheelUp:
		if lr := d.listRect(); lr.Contains(mx, my) && d.scroll > 0 {
			d.scroll--
		}
	case tcell.WheelDown:
		if lr := d.listRect(); lr.Contains(mx, my) && d.scroll < len(d.entries)-lr.H {
			d.scroll++
		}
	}
	return true
}
