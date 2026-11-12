package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// valueViewerW and valueViewerLines size the popup, which is centred on the
// screen rather than on its host's rect so it reads as a modal even over a
// small grid.
const valueViewerW = 60
const valueViewerLines = 8

// valueViewerDimNum/valueViewerDimDen fade the screen behind the popup toward
// DialogOverlay. Duplicates dialogs.ModalDialog's dialogDimNum/Den, since
// controls must not depend on dialogs.
const valueViewerDimNum, valueViewerDimDen = 3, 5

// ValueViewer is a modal popup showing one value in full: a read-only,
// word-wrapped Editor (navigable, selectable, copyable) in a titled box over the
// dimmed screen, with a hint and a "[ Close ]" button on its last row. DataGrid
// holds one for its "Show Value"; a host can hold its own for any long text.
//
// Only Escape or Close dismiss it: a stray click must not lose a long value
// part-way through reading or selecting it. While showing it takes every key and
// mouse event (see OverlayActive). The zero value is ready to use.
type ValueViewer struct {
	title  string
	editor *Editor
	open   bool

	// closeRect is the "[ Close ]" button's screen position, laid out by
	// DrawOverlay (the popup is screen-centred, so it isn't known before) and
	// hit-tested by HandleMouse.
	closeRect core.Rect

	// dismiss latches from the press on Close to its release: the press closes
	// the popup, and tcell's Button1 resends while held would otherwise land on
	// whatever is underneath as fresh clicks.
	dismiss core.PressLatch
}

// Show opens the popup on text, titled title, with the caret at the start.
func (v *ValueViewer) Show(title, text string) {
	if v.editor == nil {
		v.editor = NewEditor(nil)
		v.editor.SetGutterVisible(false)
		v.editor.SetWrapMode(true)
		v.editor.SetReadOnly(true)
	}
	v.title = title
	v.editor.SetText(text)
	v.editor.Focus(true)
	v.open = true
	// A latch must not survive into the next showing; see dismiss, and
	// dialogs.ModalDialog.Show, which clears its own for the same reason.
	v.dismiss.Clear()
}

// Close hides the popup. A host whose content the popup was showing calls it
// when that content is replaced: Escape and Close are the only ways out from
// the UI, so a popup left open strands itself over stale text while it still
// claims every key and mouse event.
func (v *ValueViewer) Close() {
	v.open = false
	v.closeRect = core.Rect{}
}

// Title returns the popup's title, as last passed to Show.
func (v *ValueViewer) Title() string { return v.title }

// Text returns the value shown, as last passed to Show ("" before the first).
func (v *ValueViewer) Text() string {
	if v.editor == nil {
		return ""
	}
	return v.editor.Text()
}

// OverlayActive reports whether the popup is showing. A host must give it every
// key and mouse event while true: it is positioned independently of the host's
// rect, so position- or focus-based routing would hand its input to whatever
// sits under those coordinates.
func (v *ValueViewer) OverlayActive() bool { return v.open }

// valueViewerCloseLabel is the popup's dismiss button, at the right end of the hint row.
func valueViewerCloseLabel() string { return "[ " + Strings.ViewerClose + " ]" }

// DrawOverlay draws the popup, if showing. Call after everything else in the
// frame has drawn.
func (v *ValueViewer) DrawOverlay(s tcell.Screen) {
	if !v.open {
		return
	}
	sw, sh := s.Size()
	w := min(valueViewerW, max(20, sw-4))
	h := valueViewerLines + 4 // border top/bottom + valueViewerLines text rows + 1 hint row
	x := max(0, (sw-w)/2)
	y := max(0, (sh-h)/2)
	rect := core.Rect{X: x, Y: y, W: w, H: h}

	p := theme.Active()
	core.DimArea(s, core.Rect{X: 0, Y: 0, W: sw, H: sh}, p.DialogOverlay, valueViewerDimNum, valueViewerDimDen)
	core.FillRect(s, rect, ' ', theme.StyleDialog())
	borderSt := theme.On(p.DialogBg, p.DialogBorder)
	titleSt := theme.On(p.DialogBg, p.DialogTitle).Bold(true)
	core.DrawBoxTitle(s, rect, v.title, borderSt, titleSt)

	v.editor.SetBounds(x+2, y+1, w-4, valueViewerLines)
	v.editor.Draw(s)

	// Hint text and the Close button share the last row: the button is right-aligned
	// and the hint clipped to what is left, so they can't overlap.
	label := valueViewerCloseLabel()
	btnW := core.DisplayWidth(label)
	v.closeRect = core.Rect{X: x + w - 2 - btnW, Y: y + h - 2, W: btnW, H: 1}
	core.DrawTextClipped(s, x+2, y+h-2, w-6-btnW, theme.StyleDialogDim(), Strings.ViewerHint)
	btnSt := theme.StyleActiveBlock().Bold(true)
	core.DrawTextClipped(s, v.closeRect.X, v.closeRect.Y, btnW, btnSt, label)
}

// HandleKey takes every key while the popup is showing: Escape closes it, the
// rest go to its Editor. It returns false only when the popup is not showing.
func (v *ValueViewer) HandleKey(ev *tcell.EventKey) bool {
	if !v.open {
		return false
	}
	if ev.Key() == tcell.KeyEscape {
		v.Close()
		return true
	}
	v.editor.HandleKey(ev)
	return true
}

// HandleMouse takes every mouse event while the popup is showing, and the rest
// of the gesture whose press closed it (see dismiss). A click outside the
// Editor and Close is swallowed and the popup stays open. It returns false
// otherwise.
func (v *ValueViewer) HandleMouse(ev *tcell.EventMouse) bool {
	if v.dismiss.Held() {
		v.dismiss.Release(ev)
		return true
	}
	if !v.open {
		return false
	}
	if px, py := ev.Position(); v.closeRect.Contains(px, py) && v.dismiss.Fresh(ev) {
		v.Close()
		return true
	}
	v.editor.HandleMouse(ev)
	return true
}

// HasSelection and the SelectedText, Cut, Paste and SelectAll beside it make
// *ValueViewer a core.ClipboardTarget over its read-only Editor. HasSelection
// is false while the popup is not showing, so a host falling back to its own
// copy keeps doing so.
func (v *ValueViewer) HasSelection() bool {
	return v.open && v.editor.HasSelection()
}

func (v *ValueViewer) SelectedText() string {
	if !v.open {
		return ""
	}
	return v.editor.SelectedText()
}

// Cut returns "" and removes nothing: the popup is read-only. "" tells the host
// nothing was cut, so it neither overwrites the clipboard nor reports a cut
// (Copy is the way to take the text).
func (v *ValueViewer) Cut() string { return "" }

// Paste is a no-op: the popup is read-only.
func (v *ValueViewer) Paste(text string) {}

func (v *ValueViewer) SelectAll() {
	if v.open {
		v.editor.SelectAll()
	}
}
