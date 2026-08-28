package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// FindReplaceDialog is the query editor's Find and Replace dialog. One dialog
// serves both modes: Find hides the Replace field and its two buttons, Replace
// shows them.
//
// Being modal it covers part of the editor it is searching, which is why the
// status line reports "Match 2 of 7 — line 143, col 12" rather than relying on
// the highlighted hit being visible. Repeat searching happens with the dialog
// closed, via F3 / Shift+F3; the search, its options and the highlighting all
// survive closing it.
type FindReplaceDialog struct {
	dialogs.ModalDialog
	app *App

	fFind    *widgets.InputField
	fReplace *widgets.InputField
	cbCase   *widgets.CheckBox
	cbWord   *widgets.CheckBox
	cbRegex  *widgets.CheckBox
	cbSel    *widgets.CheckBox

	// target is the editor this showing searches, captured at Show time. The
	// dialog is modal, so the active panel can't change underneath it.
	target *controls.Editor

	replaceMode bool

	// drag is the text-selection gesture a click in one of the dialog's text
	// fields starts — see dialogs.FieldGesture for the ordering its three calls
	// depend on.
	drag dialogs.FieldGesture

	// focusIdx walks fields first, then buttons — see focusCount. Buttons are in
	// the same cycle, so Tab alone reaches everything the dialog can do.
	focusIdx int

	// status is the readout under the fields: match position, replacement
	// count, or a regexp compile error. statusErr picks the colour.
	status    string
	statusErr bool

	// statusY is the row status is drawn on, computed by layout and read
	// back by Draw once the widgets above it have been placed.
	statusY int
}

// NewFindReplaceDialog builds the dialog. Fields and options persist across
// showings, so reopening offers the previous search back.
func NewFindReplaceDialog(app *App) *FindReplaceDialog {
	d := &FindReplaceDialog{app: app}
	d.InitModal(app.screen, findDialogTitle(false), findDialogW, 13)
	d.fFind = widgets.NewInputField("Find what:   ", 34, false)
	d.fReplace = widgets.NewInputField("Replace with:", 34, false)
	d.cbCase = widgets.NewCheckBox("Match case")
	d.cbWord = widgets.NewCheckBox("Match whole word")
	d.cbRegex = widgets.NewCheckBox("Regular expression")
	d.cbSel = widgets.NewCheckBox("Replace All in selection only")
	return d
}

func findDialogTitle(replace bool) string {
	if replace {
		return "Replace"
	}
	return "Find"
}

// ShowFind opens the dialog in Find mode against the active query panel's
// editor. ShowReplace opens the same dialog with the Replace field and its
// buttons shown.
func (d *FindReplaceDialog) ShowFind()    { d.show(false) }
func (d *FindReplaceDialog) ShowReplace() { d.show(true) }

func (d *FindReplaceDialog) show(replace bool) {
	qp := d.app.activeQueryPanel()
	if qp == nil {
		d.app.setStatus(noActiveQueryPanelMessage)
		return
	}
	d.target = qp.editor
	d.replaceMode = replace
	d.SetTitle(findDialogTitle(replace))
	d.SetSize(findDialogW, d.height())
	d.status, d.statusErr = "", false
	// A latch must not survive into the next showing: a dialog dismissed mid-drag
	// would reopen still routing every click to that field.
	d.drag.Clear()

	// Seed from a single-line selection, as every other editor's Find does. A
	// multi-line selection is left alone — that is what "in selection only" is
	// for.
	if d.target.HasSelection() {
		if sel := d.target.SelectedText(); sel != "" && !strings.Contains(sel, "\n") {
			d.fFind.SetValue(sel)
		}
	}
	d.focusIdx = 0
	d.syncFocus()
	d.Show()
}

// findDialogW is wide enough for Replace mode's four-button row, the widest
// thing the dialog draws; narrower, that row runs over the left border.
const findDialogW = 62

// height is the dialog's total height for the current mode; Replace adds the
// Replace field and the "in selection only" option.
func (d *FindReplaceDialog) height() int {
	if d.replaceMode {
		return 15
	}
	return 13
}

// buttons returns this mode's button row labels. Index 0 is what Enter presses
// while focus is still in the fields.
func (d *FindReplaceDialog) buttons() []string {
	if d.replaceMode {
		return []string{"Find Next", "Replace", "Replace All", "Close"}
	}
	return []string{"Find Next", "Find Previous", "Close"}
}

// fields returns the focusable widgets, in Tab order, for the current mode.
func (d *FindReplaceDialog) fields() []focusable {
	if d.replaceMode {
		return []focusable{d.fFind, d.fReplace, d.cbCase, d.cbWord, d.cbRegex, d.cbSel}
	}
	return []focusable{d.fFind, d.cbCase, d.cbWord, d.cbRegex}
}

// syncFocus applies focusIdx to the widgets. An index past the last field means
// a button is focused and no widget is.
func (d *FindReplaceDialog) syncFocus() {
	fields := d.fields()
	for i, f := range fields {
		f.Focus(i == d.focusIdx)
	}
}

// focusCount is the number of Tab stops: every field, then every button.
func (d *FindReplaceDialog) focusCount() int { return len(d.fields()) + len(d.buttons()) }

// btnFocus is the index into buttons() of the focused button, or 0 (Find Next)
// while focus is in the fields, which is what Enter presses from a text field.
func (d *FindReplaceDialog) btnFocus() int {
	if i := d.focusIdx - len(d.fields()); i >= 0 {
		return i
	}
	return 0
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

// applyOptions compiles the current field values onto the target editor. Reports
// false, with the status line set, when the pattern is empty or the regular
// expression doesn't compile, so callers can bail out.
func (d *FindReplaceDialog) applyOptions() bool {
	if d.target == nil {
		return false
	}
	term := d.fFind.Value()
	if term == "" {
		d.setStatus("Enter something to find", true)
		return false
	}
	err := d.target.SetSearch(controls.SearchOptions{
		Query:       term,
		Replace:     d.fReplace.Value(),
		MatchCase:   d.cbCase.Checked(),
		WholeWord:   d.cbWord.Checked(),
		Regexp:      d.cbRegex.Checked(),
		InSelection: d.replaceMode && d.cbSel.Checked(),
	})
	if err != nil {
		// regexp's message is prefixed with "error parsing regexp: ", which costs
		// a third of the status width and says nothing the label doesn't.
		msg := strings.TrimPrefix(err.Error(), "error parsing regexp: ")
		d.setStatus("Invalid regex: "+msg, true)
		return false
	}
	return true
}

// optionsChanged reports whether the target's compiled search still matches what
// the fields say, so a Find Next that changed nothing doesn't recompile and
// throw the current match away.
func (d *FindReplaceDialog) optionsChanged() bool {
	if d.target == nil || !d.target.HasSearch() {
		return true
	}
	o := d.target.SearchOpts()
	return o.Query != d.fFind.Value() ||
		o.Replace != d.fReplace.Value() ||
		o.MatchCase != d.cbCase.Checked() ||
		o.WholeWord != d.cbWord.Checked() ||
		o.Regexp != d.cbRegex.Checked() ||
		o.InSelection != (d.replaceMode && d.cbSel.Checked())
}

// ensureSearch compiles the fields onto the editor only when they differ from
// the active search: recompiling resets the current-match position, so Find Next
// would restart from the cursor instead of stepping on.
func (d *FindReplaceDialog) ensureSearch() bool {
	if !d.optionsChanged() {
		return true
	}
	return d.applyOptions()
}

func (d *FindReplaceDialog) doFind(dir int) {
	if !d.ensureSearch() {
		return
	}
	if !d.target.FindNext(dir) {
		d.setStatus(fmt.Sprintf("No matches for %q", d.fFind.Value()), true)
		return
	}
	d.reportMatch()
}

func (d *FindReplaceDialog) doReplace() {
	if !d.ensureSearch() {
		return
	}
	if d.target.ReplaceCurrent() {
		d.reportMatch()
		return
	}
	// Nothing was replaced because no match was current: find one, the SSMS
	// behaviour where the first Replace press finds and the second replaces.
	d.doFind(1)
}

func (d *FindReplaceDialog) doReplaceAll() {
	if !d.ensureSearch() {
		return
	}
	n := d.target.ReplaceAll()
	switch {
	case n > 0:
		d.setStatus(fmt.Sprintf("Replaced %d occurrence(s)", n), false)
	case d.cbSel.Checked():
		d.setStatus("No matches inside the selection", true)
	default:
		d.setStatus(fmt.Sprintf("No matches for %q", d.fFind.Value()), true)
	}
}

// reportMatch writes the current match's ordinal and position into the status
// line — the feedback standing in for a hit the dialog is covering.
func (d *FindReplaceDialog) reportMatch() {
	i, n := d.target.MatchPosition()
	line, col, ok := d.target.CurrentMatchPos()
	if !ok {
		d.setStatus(fmt.Sprintf("%d match(es)", n), false)
		return
	}
	d.setStatus(fmt.Sprintf("Match %d of %d — line %d, col %d", i, n, line, col), false)
}

func (d *FindReplaceDialog) setStatus(msg string, isErr bool) {
	d.status, d.statusErr = msg, isErr
}

// ---------------------------------------------------------------------------
// Drawing
// ---------------------------------------------------------------------------

func (d *FindReplaceDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	d.layout()

	d.fFind.Draw(s)
	if d.replaceMode {
		d.fReplace.Draw(s)
		d.cbSel.Draw(s)
	}
	d.cbCase.Draw(s)
	d.cbWord.Draw(s)
	d.cbRegex.Draw(s)

	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	if d.statusErr {
		st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Warning)
	}
	inner := d.InnerRect()
	core.DrawTextClipped(s, inner.X+1, d.statusY, inner.W-2, st, d.status)

	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttons(), d.btnFocus())
}

// layout positions the fields for the current mode and records statusY for Draw,
// which needs it after the widgets have been placed.
func (d *FindReplaceDialog) layout() {
	inner := d.InnerRect()
	lx := inner.X + 1
	row := inner.Y + 1

	d.fFind.SetBounds(lx, row)
	row++
	if d.replaceMode {
		d.fReplace.SetBounds(lx, row)
		row++
	}
	row++ // blank row between the fields and the options
	d.cbCase.SetBounds(lx, row)
	row++
	d.cbWord.SetBounds(lx, row)
	row++
	d.cbRegex.SetBounds(lx, row)
	row++
	if d.replaceMode {
		d.cbSel.SetBounds(lx, row)
		row++
	}
	d.statusY = row + 1
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

// HandleKey routes keyboard events. Escape closes the dialog but leaves the
// search active, so F3 keeps working.
func (d *FindReplaceDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	switch ev.Key() {
	case tcell.KeyTab:
		d.focusIdx = nextFocus(d.focusIdx, d.focusCount())
		d.syncFocus()
		return true
	case tcell.KeyBacktab:
		d.focusIdx = prevFocus(d.focusIdx, d.focusCount())
		d.syncFocus()
		return true
	case tcell.KeyEscape:
		d.Hide()
		return true
	case tcell.KeyEnter:
		d.pressButton(d.btnFocus())
		return true
	case tcell.KeyF3:
		dir := 1
		if ev.Modifiers()&tcell.ModShift != 0 {
			dir = -1
		}
		d.doFind(dir)
		return true
	}

	if fields := d.fields(); d.focusIdx < len(fields) {
		switch w := fields[d.focusIdx].(type) {
		case *widgets.InputField:
			return w.HandleKey(ev)
		case *widgets.CheckBox:
			return w.HandleKey(ev)
		}
	}
	return true
}

// pressButton runs the action of buttons()[i] for the current mode.
func (d *FindReplaceDialog) pressButton(i int) {
	label := ""
	if btns := d.buttons(); i >= 0 && i < len(btns) {
		label = btns[i]
	}
	switch label {
	case "Find Next":
		d.doFind(1)
	case "Find Previous":
		d.doFind(-1)
	case "Replace":
		d.doReplace()
	case "Replace All":
		d.doReplaceAll()
	case "Close":
		d.Hide()
	}
}

// HandleMouse routes mouse events; ModalDialog blocks clicks outside the
// dialog's bounds.
func (d *FindReplaceDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach every latch-bearing widget even when it lands outside
	// the dialog, or the widget's next press is swallowed as a continuation of
	// the stale drag. Each returns false on ButtonNone, so this only resets
	// latches.
	if ev.Buttons() == tcell.ButtonNone {
		for _, cb := range d.checkboxes() {
			cb.HandleMouse(ev)
		}
		// End a text-selection drag in the field that claimed the press,
		// wherever the release landed. Before ConsumeOutsideClick, which returns
		// early on a release outside the dialog and would strand the latch.
		d.drag.Release(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if ev.Buttons() == tcell.ButtonNone {
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}

	// The gesture belongs to whichever field claimed its press, so motion is
	// replayed there without hit-testing, which would stop a selection extending
	// the moment the pointer left the field's rect.
	if d.drag.Replay(ev) {
		return true
	}

	if i := d.ButtonClicked(ev, d.buttons()); i >= 0 {
		d.focusIdx = len(d.fields()) + i
		d.syncFocus()
		d.pressButton(i)
		return true
	}
	for _, cb := range d.checkboxes() {
		if cb.HandleMouse(ev) {
			d.focusWidget(cb)
			return true
		}
	}
	mx, my := ev.Position()
	for _, f := range d.inputFields() {
		if f.HitTest(mx, my) {
			d.focusWidget(f)
			d.drag.Claim(f, ev)
			return true
		}
	}
	return true
}

// checkboxes and inputFields are the current mode's widgets by concrete type,
// for the mouse routing above.
func (d *FindReplaceDialog) checkboxes() []*widgets.CheckBox {
	if d.replaceMode {
		return []*widgets.CheckBox{d.cbCase, d.cbWord, d.cbRegex, d.cbSel}
	}
	return []*widgets.CheckBox{d.cbCase, d.cbWord, d.cbRegex}
}

func (d *FindReplaceDialog) inputFields() []*widgets.InputField {
	if d.replaceMode {
		return []*widgets.InputField{d.fFind, d.fReplace}
	}
	return []*widgets.InputField{d.fFind}
}

// focusWidget moves the Tab cursor onto w, so a clicked widget becomes the
// keyboard-focused one.
func (d *FindReplaceDialog) focusWidget(w focusable) {
	for i, f := range d.fields() {
		if f == w {
			d.focusIdx = i
			d.syncFocus()
			return
		}
	}
}

// ---------------------------------------------------------------------------
// App-side entry points
// ---------------------------------------------------------------------------

// findNextInEditor repeats the active search in the active query panel's editor
// with the dialog closed — F3 (dir 1) and Shift+F3 (dir -1). With no search set
// it opens the dialog rather than doing nothing.
func (a *App) findNextInEditor(dir int) {
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus(noActiveQueryPanelMessage)
		return
	}
	if !qp.editor.HasSearch() {
		a.findDialog.ShowFind()
		return
	}
	if !qp.editor.FindNext(dir) {
		a.setStatus(fmt.Sprintf("No matches for %q", qp.editor.SearchOpts().Query))
		return
	}
	i, n := qp.editor.MatchPosition()
	line, col, _ := qp.editor.CurrentMatchPos()
	a.setStatus(fmt.Sprintf("Match %d of %d — line %d, col %d", i, n, line, col))
}

// findWordAtCursor is Ctrl+F3: search for the identifier the caret is on,
// without opening the dialog.
func (a *App) findWordAtCursor() {
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus(noActiveQueryPanelMessage)
		return
	}
	word := qp.editor.WordAtCursor()
	if word == "" {
		a.setStatus("No word at the cursor")
		return
	}
	// Whole-word, so Ctrl+F3 on "id" doesn't stop inside every "identity".
	// Replace comes from the dialog rather than being blanked: it doesn't affect
	// matching, and clearing it would throw away text the user typed. No error is
	// possible — the term is literal and escaped before compiling.
	opts := controls.SearchOptions{
		Query:     word,
		Replace:   a.findDialog.fReplace.Value(),
		WholeWord: true,
	}
	_ = qp.editor.SetSearch(opts)
	a.findDialog.adoptSearch(opts)
	a.findNextInEditor(1)
}

// adoptSearch points the dialog's fields at opts, so what it shows is what the
// editor is searching for. Every field of opts is written, including the ones
// the caller left zero: a partial sync leaves optionsChanged reporting a
// difference that isn't real, and the next Find Next then recompiles and
// restarts from the cursor.
func (d *FindReplaceDialog) adoptSearch(opts controls.SearchOptions) {
	d.fFind.SetValue(opts.Query)
	d.fReplace.SetValue(opts.Replace)
	d.cbCase.SetChecked(opts.MatchCase)
	d.cbWord.SetChecked(opts.WholeWord)
	d.cbRegex.SetChecked(opts.Regexp)
	d.cbSel.SetChecked(opts.InSelection)
}

// hasEditorSearch reports whether Find Next/Previous have a search to repeat —
// the Enabled gate on both menu items.
func (a *App) hasEditorSearch() bool {
	qp := a.activeQueryPanel()
	return qp != nil && qp.editor.HasSearch()
}

// FocusedClipboardTarget implements core.ClipboardHost: whichever of the Find
// what / Replace with fields has focus, nil on the checkboxes. Without it Ctrl+X
// cuts the *editor's* selection behind the dialog.
func (d *FindReplaceDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return focusedClipboardTarget(d.fields(), d.focusIdx)
}
