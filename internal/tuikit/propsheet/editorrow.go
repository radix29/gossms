package propsheet

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// EditorRow embeds a *controls.Editor as a Form row: a multi-line text box for
// a value a single-line TextRow can't show — a job step's T-SQL command, which
// is a whole script.
//
// The label sits on its own line above the box rather than in the LabelWidth
// column, so the editor gets the sheet's full width for its text.
type EditorRow struct {
	label string
	ed    *controls.Editor

	orig     string
	origVer  uint64
	onChange func(string)

	fixedHeight int
	drawHeight  int
	x, y, w     int

	// drawReadOnly and pageReadOnly track the two independent reasons the
	// editor can be uneditable — see SetDrawReadOnly.
	drawReadOnly bool
	pageReadOnly bool
}

// SetReadOnly is the page's own gate on the editor, independent of the form's.
// A job step whose command is not T-SQL uses it: the page can write back only a
// T-SQL step, so the box shows the script and refuses to edit it.
//
// Setting it while the form's gate is on records it without lifting that gate —
// the two reasons are independent, and whichever is set last must not cancel
// the other out.
func (r *EditorRow) SetReadOnly(v bool) {
	r.pageReadOnly = v
	if !r.drawReadOnly {
		r.ed.SetReadOnly(v)
	}
}

// ReadOnly reports the page's own gate, not the form's.
func (r *EditorRow) ReadOnly() bool { return r.pageReadOnly }

// SetDrawReadOnly implements ReadOnlyDrawer: the editor stops accepting
// mutating keys. Its box stays — a whole T-SQL script needs to be scrollable
// and selectable, and a text box is not by itself a claim that it is editable.
//
// The page's own SetReadOnly is remembered and restored rather than cleared:
// lifting the form's permission gate must not make a non-T-SQL step editable.
func (r *EditorRow) SetDrawReadOnly(v bool) {
	if r.drawReadOnly == v {
		return
	}
	r.drawReadOnly = v
	if v {
		r.pageReadOnly = r.ed.ReadOnly()
		r.ed.SetReadOnly(true)
		return
	}
	r.ed.SetReadOnly(r.pageReadOnly)
}

// NewEditorRow wraps ed as a Form row occupying height screen lines — one for
// the label plus height-1 for the box. The caller builds the editor, so the
// highlighter and the line-number gutter are its choice (propsheet knows
// nothing about SQL), the way NewGridRow leaves the grid's columns to the page.
func NewEditorRow(label string, ed *controls.Editor, height int) *EditorRow {
	return &EditorRow{label: label, ed: ed, fixedHeight: height, drawHeight: height}
}

// Editor returns the wrapped editor, for a page that has to reach past the row
// — installing a right-click menu, say.
func (r *EditorRow) Editor() *controls.Editor { return r.ed }

// Label returns the row's label, matching TextRow.Label — what identifies a row
// to anything working with a Form it did not build, such as a test driving a
// page. No padding is applied, so unlike TextRow's it is never truncated.
func (r *EditorRow) Label() string { return strings.TrimRight(r.label, " ") }

// Value returns the editor's text.
func (r *EditorRow) Value() string { return r.ed.Text() }

// SetValue replaces the text and resets the dirty baseline — the post-load
// setter, for after a successful load, an Apply, or a change of selected
// object. See TextRow.SetValue for why this and Edit are different operations.
func (r *EditorRow) SetValue(v string) {
	r.ed.SetText(v)
	// Read the baseline back out of the editor, not from v: SetText normalizes
	// (tabs expanded, CRLF folded), so seeding orig from the source string
	// leaves the row dirty the moment it loads.
	r.orig = r.ed.Text()
	r.origVer = r.version()
}

// Edit sets the text the way typing does: the value changes, the row goes
// dirty, and OnChange fires.
func (r *EditorRow) Edit(v string) {
	ver := r.version()
	r.ed.SetText(v)
	r.notifyChanged(ver)
}

// SetOnChange installs a callback fired whenever an edit changes the text.
func (r *EditorRow) SetOnChange(fn func(string)) { r.onChange = fn }

// version is the editor document's revision counter, which every mutation
// bumps. notifyChanged compares against it rather than against the text itself:
// the row's keys run on every keystroke, and rebuilding the whole script to
// compare it would make a long command's arrow keys quadratic.
func (r *EditorRow) version() uint64 { return r.ed.Document().Version() }

func (r *EditorRow) notifyChanged(ver uint64) {
	if r.onChange != nil && r.version() != ver {
		r.onChange(r.ed.Text())
	}
}

// Dirty, Revert and Validate implement Editable. Dirty is asked on every draw
// of a sheet showing a modified marker, so an untouched document answers from
// the revision counter alone and only an edited one is rebuilt and compared —
// which it must be, since an edit undone by hand is clean again.
func (r *EditorRow) Dirty() bool {
	if r.version() == r.origVer {
		return false
	}
	return r.ed.Text() != r.orig
}
func (r *EditorRow) Revert()         { r.SetValue(r.orig) }
func (r *EditorRow) Validate() error { return nil }

func (r *EditorRow) Height(w int) int { return r.fixedHeight }
func (r *EditorRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.ed.SetBounds(x, y+1, w, max(1, r.drawHeight-1))
}

// MinDrawHeight and SetDrawHeight implement Shrinkable: the label plus two
// lines of text is the least the box still reads as one, and the editor scrolls
// its own lines within whatever space it gets.
func (r *EditorRow) MinDrawHeight() int  { return 3 }
func (r *EditorRow) SetDrawHeight(h int) { r.drawHeight = h }

func (r *EditorRow) Focusable() bool { return true }

func (r *EditorRow) Draw(s tcell.Screen, focused bool) {
	p := theme.Active()
	lst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	if focused {
		lst = theme.StyleSelected()
	}
	core.DrawTextClipped(s, r.x, r.y, r.w, lst, r.label)
	r.ed.Focus(focused)
	r.ed.Draw(s)
}

// HandleKey forwards to the editor, translating the keys a Form needs back for
// its own navigation — the same job GridRow does for DataGrid, and for the same
// reason: Editor answers true to Up at the first line, Down at the last and Tab
// (which it indents with), so forwarding its answer verbatim makes the box a
// keyboard trap escapable only by mouse.
//
// Tab and Backtab are never forwarded: on a property sheet they are the way out
// of every other field, so indenting with them would cost the row its only
// unconditional exit. Movement keys are forwarded and the result measured —
// what actually moved, never what should have — so a key the editor ignored at
// a boundary falls through to Form.
func (r *EditorRow) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyTab, tcell.KeyBacktab:
		return false
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight, tcell.KeyPgUp, tcell.KeyPgDn:
	default:
		ver := r.version()
		handled := r.ed.HandleKey(ev)
		r.notifyChanged(ver)
		return handled
	}
	row, col := r.ed.CursorPos()
	srow, scol := r.ed.ScrollPos()
	ver := r.version()
	if !r.ed.HandleKey(ev) {
		return false
	}
	if r.version() != ver {
		// Ctrl+Shift+Up/Down move lines rather than the caret: the text changed
		// even where the caret did not, so the key was acted on either way.
		r.notifyChanged(ver)
		return true
	}
	nrow, ncol := r.ed.CursorPos()
	nsrow, nscol := r.ed.ScrollPos()
	return nrow != row || ncol != col || nsrow != srow || nscol != scol
}

func (r *EditorRow) HandleMouse(ev *tcell.EventMouse) bool {
	ver := r.version()
	handled := r.ed.HandleMouse(ev)
	r.notifyChanged(ver)
	return handled
}

// DrawOverlay and OverlayActive implement OverlayDrawer/OverlayActiver for the
// editor's completion popup, which floats past the row's own lines.
func (r *EditorRow) DrawOverlay(s tcell.Screen) { r.ed.DrawOverlay(s) }
func (r *EditorRow) OverlayActive() bool        { return r.ed.CompletionActive() }

// CopyText, HasSelection, SelectedText, Cut, Paste and SelectAll implement
// ClipboardRow by forwarding to the editor, so the sheet's Ctrl+C/X/V and
// Select All act on the text the caret is in. With nothing selected, Ctrl+C
// copies the whole script — a TextRow copies its whole value the same way.
func (r *EditorRow) CopyText() string   { return r.ed.Text() }
func (r *EditorRow) HasSelection() bool { return r.ed.HasSelection() }
func (r *EditorRow) SelectedText() string {
	return r.ed.SelectedText()
}
func (r *EditorRow) Cut() string {
	ver := r.version()
	out := r.ed.Cut()
	r.notifyChanged(ver)
	return out
}
func (r *EditorRow) Paste(text string) {
	ver := r.version()
	r.ed.Paste(text)
	r.notifyChanged(ver)
}
func (r *EditorRow) SelectAll() { r.ed.SelectAll() }
