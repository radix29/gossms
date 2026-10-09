package propsheet

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// LabelWidth is the display-column width reserved for a row's label before its
// value begins, so a page reads as an aligned two-column form. Checkbox rows
// are the exception, following widgets.CheckBox's "[x] Label" order.
const LabelWidth = 30

const selectControlWidth = 22

// ---------------------------------------------------------------------------
// Section — non-focusable heading with an underline
// ---------------------------------------------------------------------------

// SectionRow is a non-focusable heading with an underline. Most callers need
// only Section's returned Row; SetTitle is for a heading that reflects a later
// selection, like "Explicit permissions for <principal>".
type SectionRow struct {
	title   string
	x, y, w int
}

// Section returns a non-focusable, non-editable heading row.
func Section(title string) *SectionRow { return &SectionRow{title: title} }

// SetTitle changes the heading text in place.
func (r *SectionRow) SetTitle(title string) { r.title = title }

// Title is the heading text. It separates two rows with the same label: a New
// Database page has a "Logical name" under both "Data file" and "Log file".
func (r *SectionRow) Title() string { return r.title }

func (r *SectionRow) Height(w int) int   { return 2 }
func (r *SectionRow) Layout(x, y, w int) { r.x, r.y, r.w = x, y, w }
func (r *SectionRow) Focusable() bool    { return false }
func (r *SectionRow) Draw(s tcell.Screen, focused bool) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text).Bold(true)
	core.DrawText(s, r.x, r.y, st, r.title)
	sep := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, r.x, r.y+1, r.w, sep)
}

// ---------------------------------------------------------------------------
// Note — non-focusable, word-wrapped dim text
// ---------------------------------------------------------------------------

// NoteRow is the row Note returns. It is exported for DynamicNote, the one
// kind of note a page rewrites after building it.
type NoteRow struct {
	text       string
	lines      []string
	x, y, w    int
	drawHeight int
}

// Note returns a non-focusable row of word-wrapped, dimmed text.
func Note(text string) Row { return &NoteRow{text: text, drawHeight: -1} }

// DynamicNote returns a Note whose text the page replaces with SetText (a
// description that follows a picker, too long for a HintRow's two lines). The
// form re-wraps it next frame, since Height is asked every layout.
func DynamicNote(text string) *NoteRow { return &NoteRow{text: text, drawHeight: -1} }

// SetText replaces the note's text.
func (r *NoteRow) SetText(text string) { r.text = text }

// Text returns the note's text. Notes are the only rows with no label to
// address them by, so this is how a caller reads one back.
func (r *NoteRow) Text() string { return r.text }

func (r *NoteRow) Height(w int) int { return len(core.WrapText(r.text, w)) }
func (r *NoteRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.lines = core.WrapText(r.text, w)
}
func (r *NoteRow) Focusable() bool { return false }
func (r *NoteRow) Draw(s tcell.Screen, focused bool) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	lines := r.lines
	if r.drawHeight >= 0 && r.drawHeight < len(lines) {
		lines = lines[:r.drawHeight]
	}
	for i, line := range lines {
		core.DrawText(s, r.x, r.y+i, st, line)
	}
}

// MinDrawHeight and SetDrawHeight implement Shrinkable: a note drops its
// trailing wrapped lines rather than running past the form's bottom edge.
func (r *NoteRow) MinDrawHeight() int  { return 1 }
func (r *NoteRow) SetDrawHeight(h int) { r.drawHeight = h }

// ---------------------------------------------------------------------------
// HintRow — non-focusable message a handler sets at runtime
// ---------------------------------------------------------------------------

// HintRow is a short message a page's button handlers write to, saying why an
// action did nothing: an empty name, a duplicate, nothing selected. Blank and
// invisible until set.
//
// A page is built by a plain function with no App or dialog in scope, so
// without this an Add that hit a duplicate could only `return`, leaving a button
// that looks broken. The row also sits next to the control just used, unlike the
// status bar behind the dialog.
//
// Not focusable, and it reserves its line whether or not it has text, so an
// appearing hint doesn't reflow the rows around it. Text wider than the row
// wraps to a second line, the rest clipped with an ellipsis (N10: at 80 columns
// Accounts' "... is deleted on Apply, and leaves every profile that uses it."
// lost its second half). That line pushes down only the rows below the hint,
// never the button above it that set it.
//
// Set and SetError also ask the form to scroll the row into view (Revealer): a
// hint usually sits below its button, past the bottom edge on a long page.
type HintRow struct {
	text    string
	isError bool
	reveal  bool
	lines   []string
	x, y, w int
}

// hintMaxLines caps a hint's height: one line reserved, one more on demand.
const hintMaxLines = 2

// Hint returns an empty HintRow.
func Hint() *HintRow { return &HintRow{} }

// Set writes an advisory message (Warning colour).
func (r *HintRow) Set(text string) { r.text, r.isError, r.reveal = text, false, text != "" }

// SetError writes a failure message (Error colour).
func (r *HintRow) SetError(text string) { r.text, r.isError, r.reveal = text, true, text != "" }

// Clear blanks the row. Call it from a handler that succeeded, so a stale
// complaint doesn't outlive its cause.
func (r *HintRow) Clear() { r.text, r.isError = "", false }

// Text returns the current message, "" when blank.
func (r *HintRow) Text() string { return r.text }

// TakeReveal implements Revealer: true once after each Set or SetError with
// text.
func (r *HintRow) TakeReveal() bool {
	v := r.reveal
	r.reveal = false
	return v
}

func (r *HintRow) Height(w int) int { return max(1, len(core.WrapTextLimit(r.text, w, hintMaxLines))) }
func (r *HintRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.lines = core.WrapTextLimit(r.text, w, hintMaxLines)
}
func (r *HintRow) Focusable() bool { return false }
func (r *HintRow) Draw(s tcell.Screen, focused bool) {
	if r.text == "" {
		return
	}
	p := theme.Active()
	fg := p.Warning
	if r.isError {
		fg = p.Error
	}
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(fg)
	for i, line := range r.lines {
		core.DrawTextClipped(s, r.x, r.y+i, r.w, st, line)
	}
}

// ---------------------------------------------------------------------------
// StaticRow — focusable, read-only label/value pair
// ---------------------------------------------------------------------------

// StaticRow displays a read-only label/value pair. Still focusable (Up/Down and
// Tab land on it, Ctrl+C copies its value); only editing is unavailable.
type StaticRow struct {
	label, value string
	x, y, w      int
}

// Static returns a read-only label/value row.
func Static(label, value string) *StaticRow { return &StaticRow{label: label, value: value} }

// Label returns the row's label, as TextRow.Label does: what identifies a
// read-only row to anything working with a Form it did not build.
func (r *StaticRow) Label() string { return strings.TrimRight(r.label, " ") }

// SetValue replaces the displayed value (e.g. after a refresh).
func (r *StaticRow) SetValue(v string) { r.value = v }

// Value returns the current displayed value.
func (r *StaticRow) Value() string { return r.value }

func (r *StaticRow) Height(w int) int   { return 1 }
func (r *StaticRow) Layout(x, y, w int) { r.x, r.y, r.w = x, y, w }
func (r *StaticRow) Focusable() bool    { return true }
func (r *StaticRow) CopyText() string   { return r.value }

func (r *StaticRow) Draw(s tcell.Screen, focused bool) {
	p := theme.Active()
	lst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	vst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if focused {
		lst, vst = theme.StyleSelected(), theme.StyleSelected()
		core.FillRect(s, core.Rect{X: r.x, Y: r.y, W: r.w, H: 1}, ' ', vst)
	}
	drawFlatValue(s, r.x, r.y, r.w, r.label, r.value, lst, vst)
}

// drawFlatValue renders a label/value pair as plain text: what StaticRow draws
// and every editable row switches to under Form.SetReadOnly. Shared on purpose:
// a gated page mixes both kinds, and two spellings of "a value you cannot
// change" would read as two kinds of field.
//
// The value never starts on LabelWidth: a label may be exactly that wide (the
// limit TestNoPropertySheetLabelIsTruncated enforces), and drawn flush the two
// run together ("Maximum concurrent connections0", live).
func drawFlatValue(s tcell.Screen, x, y, w int, label, value string, lst, vst tcell.Style) {
	core.DrawTextClipped(s, x, y, LabelWidth, lst, label)
	valX := flatValueX(x)
	core.DrawTextClipped(s, valX, y, max(0, x+w-valX), vst, value)
}

// flatValueX is the column a flat value starts in: where the same row's text
// sits when editable, so a row switching between the two doesn't jog its value
// sideways. An editable row pads its label to LabelWidth and draws '[' after it,
// text one further; TestFlatValueStartsWhereAnEditableValueDoes pins the pair.
func flatValueX(x int) int { return x + LabelWidth + 2 }

// drawFlatReadOnly is drawFlatValue in the styles a read-only row uses.
func drawFlatReadOnly(s tcell.Screen, x, y, w int, label, value string) {
	p := theme.Active()
	lst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	vst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	drawFlatValue(s, x, y, w, label, value, lst, vst)
}
