package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/widgets"
)

// log_search_dialog.go is the Log File Viewer's server-side search (SSMS's
// "Filter..."). It edits the four arguments xp_readerrorlog takes (two
// substrings and a date range) and hands them back for the next read.
//
// This is not the panel's Filter box: that narrows what was already read,
// instantly, with no round trip. A search here changes what the *server*
// returns, which a log too large to read in one go needs.

const (
	logSearchDialogW = 62
	logSearchDialogH = 14
	// logSearchFieldW leaves room for a full "YYYY-MM-DD HH:MM:SS" and then
	// some, inside logSearchDialogW.
	logSearchFieldW = 38
)

// LogSearchDialog edits one gosmo.LogSearch.
type LogSearchDialog struct {
	dialogs.ModalDialog
	app *App

	fText1 *widgets.InputField
	fText2 *widgets.InputField
	fFrom  *widgets.InputField
	fTo    *widgets.InputField

	// onApply receives the search when Search or Clear is pressed. Cleared on
	// close so a dismissed showing cannot deliver into the next panel.
	onApply func(gosmo.LogSearch)

	// drag is the text-selection gesture a click in one of the dialog's text
	// fields starts — see dialogs.FieldGesture for the ordering its three calls
	// depend on.
	drag dialogs.FieldGesture

	// focusIdx is the focused field, or len(fields()) for the button row, one
	// Tab stop crossed with the arrows (dialogs.RingStep, ButtonRowKey).
	// btnFocus is the button the row would press: Search while focus is in the
	// fields, so Enter from a field searches.
	focusIdx int
	btnFocus int

	status    string
	statusErr bool
	statusY   int
}

// NewLogSearchDialog builds the dialog.
func NewLogSearchDialog(app *App) *LogSearchDialog {
	d := &LogSearchDialog{app: app}
	d.InitModal("Search Log", logSearchDialogW, logSearchDialogH)
	d.fText1 = widgets.NewInputField("Contains:     ", logSearchFieldW, false)
	d.fText2 = widgets.NewInputField("And contains: ", logSearchFieldW, false)
	d.fFrom = widgets.NewInputField("From:         ", logSearchFieldW, false)
	d.fTo = widgets.NewInputField("To:           ", logSearchFieldW, false)
	return d
}

// ShowLogSearch opens the dialog seeded with the search currently in force,
// not a blank form.
func (d *LogSearchDialog) ShowLogSearch(current gosmo.LogSearch, onApply func(gosmo.LogSearch)) {
	d.onApply = onApply
	d.fText1.SetValue(current.Text1)
	d.fText2.SetValue(current.Text2)
	d.fFrom.SetValue(formatLogSearchTime(current.From))
	d.fTo.SetValue(formatLogSearchTime(current.To))
	d.status, d.statusErr = "", false
	// A latch must not survive into the next showing — see ModalDialog.Show.
	d.drag.Clear()
	d.focusIdx, d.btnFocus = 0, 0
	d.syncFocus()
	d.Show()
}

func (d *LogSearchDialog) buttons() []string { return []string{"Search", "Clear", "Cancel"} }

func (d *LogSearchDialog) fields() []focusable {
	return []focusable{d.fText1, d.fText2, d.fFrom, d.fTo}
}

func (d *LogSearchDialog) inputFields() []*widgets.InputField {
	return []*widgets.InputField{d.fText1, d.fText2, d.fFrom, d.fTo}
}

func (d *LogSearchDialog) syncFocus() {
	for i, f := range d.fields() {
		f.Focus(i == d.focusIdx)
	}
}

func (d *LogSearchDialog) onButtons() bool { return d.focusIdx == len(d.fields()) }

// moveFocus steps the ring by dir. Leaving the button row resets it to Search,
// the button Enter presses from a field.
func (d *LogSearchDialog) moveFocus(dir int) {
	d.focusIdx = dialogs.RingStep(d.fields(), d.focusIdx, dir)
	if !d.onButtons() {
		d.btnFocus = 0
	}
	d.syncFocus()
}

// Draw renders the dialog.
func (d *LogSearchDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	d.layout()

	p := theme.Active()
	hint := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogTextDim)
	inner := d.InnerRect()

	for _, f := range d.inputFields() {
		f.Draw(s)
	}
	// Two short lines: the dialog is 62 columns and DrawTextClipped truncates
	// silently; one line lost the second half of the date format.
	core.DrawTextClipped(s, inner.X+1, d.statusY-2, inner.W-2, hint,
		"Both texts must appear in the same entry.")
	core.DrawTextClipped(s, inner.X+1, d.statusY-1, inner.W-2, hint,
		"Dates: YYYY-MM-DD or YYYY-MM-DD HH:MM:SS.")

	st := hint
	if d.statusErr {
		st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogWarning)
	}
	core.DrawTextClipped(s, inner.X+1, d.statusY, inner.W-2, st, d.status)

	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttons(), d.btnFocus)
}

func (d *LogSearchDialog) layout() {
	inner := d.InnerRect()
	lx, row := inner.X+1, inner.Y+1
	for _, f := range d.inputFields() {
		f.SetBounds(lx, row)
		row++
	}
	d.statusY = row + 2
}

// HandleKey routes keyboard events.
func (d *LogSearchDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	press := func() { d.pressButton(d.btnFocus) }
	if d.onButtons() {
		return dialogs.ButtonRowKey(ev, &d.btnFocus, len(d.buttons()), nil, press, d.dismiss, d.moveFocus)
	}
	return dialogs.FieldRingKey(ev, d.fields()[d.focusIdx], press, d.dismiss, d.moveFocus)
}

// HandleMouse routes mouse events like tuikit's dialogs.FindReplaceDialog, including the
// release that must reach a latch-bearing field wherever it lands.
func (d *LogSearchDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	d.drag.Release(ev)
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if ev.Buttons() == tcell.ButtonNone {
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	if d.drag.Replay(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, d.buttons()); i >= 0 {
		d.focusIdx, d.btnFocus = len(d.fields()), i
		d.syncFocus()
		d.pressButton(i)
		return true
	}
	mx, my := ev.Position()
	for i, f := range d.inputFields() {
		if f.HitTest(mx, my) {
			d.focusIdx = i
			d.syncFocus()
			d.drag.Claim(f, ev)
			return true
		}
	}
	return true
}

func (d *LogSearchDialog) pressButton(i int) {
	switch d.buttons()[i] {
	case "Search":
		search, err := d.parse()
		if err != nil {
			d.status, d.statusErr = err.Error(), true
			return
		}
		d.deliver(search)
	case "Clear":
		// Clears the fields too: the dialog is seeded from what is in force, so
		// leaving them filled would re-apply the cleared search on the next Enter.
		for _, f := range d.inputFields() {
			f.SetValue("")
		}
		d.deliver(gosmo.LogSearch{})
	case "Cancel":
		d.dismiss()
	}
}

// deliver hands the search to the panel and closes. onApply is read and
// cleared first, as ConfirmDialog.finish does: it starts a read that can
// itself report back into the UI.
func (d *LogSearchDialog) deliver(search gosmo.LogSearch) {
	onApply := d.onApply
	d.dismiss()
	if onApply != nil {
		onApply(search)
	}
}

func (d *LogSearchDialog) dismiss() {
	d.onApply = nil
	d.Hide()
}

// parse turns the four fields into a LogSearch, rejecting an unparseable date
// rather than sending it to the server.
func (d *LogSearchDialog) parse() (gosmo.LogSearch, error) {
	// Capitalized on purpose: the dialog draws err.Error() verbatim as its status
	// line, so the leading word is the field's label. Nothing wraps them.
	from, err := parseLogSearchTime(d.fFrom.Value())
	if err != nil {
		//lint:ignore ST1005 the capital is the "From" field's label
		return gosmo.LogSearch{}, fmt.Errorf("From: %w", err)
	}
	to, err := parseLogSearchTime(d.fTo.Value())
	if err != nil {
		//lint:ignore ST1005 the capital is the "To" field's label
		return gosmo.LogSearch{}, fmt.Errorf("To: %w", err)
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		//lint:ignore ST1005 both capitals are field labels
		return gosmo.LogSearch{}, errors.New("To is before From")
	}
	return gosmo.LogSearch{
		Text1: strings.TrimSpace(d.fText1.Value()),
		Text2: strings.TrimSpace(d.fText2.Value()),
		From:  from,
		To:    to,
	}, nil
}

// logSearchTimeLayouts are what a date bound may be typed as. A bare date is
// midnight, which is what "From 2026-08-20" means.
var logSearchTimeLayouts = []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"}

// parseLogSearchTime parses one bound; an empty field is no bound at all.
func parseLogSearchTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range logSearchTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("expected YYYY-MM-DD or YYYY-MM-DD HH:MM:SS")
}

// formatLogSearchTime renders a bound back into its field, and "" for none.
func formatLogSearchTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(logSearchTimeLayouts[0])
}

// FocusClipboardTargetAt implements core.ClipboardFocuser: a right-click on a
// search field focuses it.
func (d *LogSearchDialog) FocusClipboardTargetAt(x, y int) bool {
	i := dialogs.ClipboardFieldAt(d.fields(), x, y)
	if i < 0 {
		return false
	}
	d.focusIdx = i
	d.syncFocus()
	return true
}

// FocusedClipboardTarget implements core.ClipboardHost — every field here is
// text the user pastes into.
func (d *LogSearchDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return dialogs.FocusedClipboardTarget(d.fields(), d.focusIdx)
}
