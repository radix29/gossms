package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// log_search_dialog.go is the Log File Viewer's server-side search — SSMS's
// "Filter..." on the same panel. It edits the four arguments xp_readerrorlog
// takes (two substrings and a date range) and hands them back for the next
// read.
//
// This is not the panel's Filter box, and the two are deliberately different
// things: the filter narrows what was already read, instantly and with no
// round trip, and says how much of the file it is showing. A search here
// changes what the *server* returns, which is what a log too large to read in
// one go needs.

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

	focusIdx int

	status    string
	statusErr bool
	statusY   int
}

// NewLogSearchDialog builds the dialog.
func NewLogSearchDialog(app *App) *LogSearchDialog {
	d := &LogSearchDialog{app: app}
	d.InitModal(app.screen, "Search Log", logSearchDialogW, logSearchDialogH)
	d.fText1 = widgets.NewInputField("Contains:     ", logSearchFieldW, false)
	d.fText2 = widgets.NewInputField("And contains: ", logSearchFieldW, false)
	d.fFrom = widgets.NewInputField("From:         ", logSearchFieldW, false)
	d.fTo = widgets.NewInputField("To:           ", logSearchFieldW, false)
	return d
}

// ShowLogSearch opens the dialog seeded with the search currently in force,
// so reopening it shows what the panel is reading with rather than a blank
// form.
func (d *LogSearchDialog) ShowLogSearch(current gosmo.LogSearch, onApply func(gosmo.LogSearch)) {
	d.onApply = onApply
	d.fText1.SetValue(current.Text1)
	d.fText2.SetValue(current.Text2)
	d.fFrom.SetValue(formatLogSearchTime(current.From))
	d.fTo.SetValue(formatLogSearchTime(current.To))
	d.status, d.statusErr = "", false
	// A latch must not survive into the next showing — see ModalDialog.Show.
	d.drag.Clear()
	d.focusIdx = 0
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

func (d *LogSearchDialog) focusCount() int { return len(d.fields()) + len(d.buttons()) }

func (d *LogSearchDialog) syncFocus() {
	for i, f := range d.fields() {
		f.Focus(i == d.focusIdx)
	}
}

// btnFocus is the focused button's index, or 0 (Search) while focus is still
// in the fields — so Enter from a field searches, which is what typing into
// this dialog is for.
func (d *LogSearchDialog) btnFocus() int {
	if i := d.focusIdx - len(d.fields()); i >= 0 {
		return i
	}
	return 0
}

// Draw renders the dialog.
func (d *LogSearchDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	d.layout()

	p := theme.Active()
	hint := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	inner := d.InnerRect()

	for _, f := range d.inputFields() {
		f.Draw(s)
	}
	// Two short lines rather than one long one: the dialog is 62 columns and
	// DrawTextClipped truncates without saying so — the single-line form lost
	// the second half of the date format, which is the part nobody can guess.
	core.DrawTextClipped(s, inner.X+1, d.statusY-2, inner.W-2, hint,
		"Both texts must appear in the same entry.")
	core.DrawTextClipped(s, inner.X+1, d.statusY-1, inner.W-2, hint,
		"Dates: YYYY-MM-DD or YYYY-MM-DD HH:MM:SS.")

	st := hint
	if d.statusErr {
		st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Warning)
	}
	core.DrawTextClipped(s, inner.X+1, d.statusY, inner.W-2, st, d.status)

	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttons(), d.btnFocus())
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
		d.dismiss()
		return true
	case tcell.KeyEnter:
		d.pressButton(d.btnFocus())
		return true
	}
	if fields := d.fields(); d.focusIdx < len(fields) {
		if f, ok := fields[d.focusIdx].(*widgets.InputField); ok {
			return f.HandleKey(ev)
		}
	}
	return true
}

// HandleMouse routes mouse events — the same shape as FindReplaceDialog's,
// including the release that must reach a latch-bearing field wherever it
// lands.
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
		d.focusIdx = len(d.fields()) + i
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
		// Clears the fields as well as the search: the dialog is seeded from
		// what is in force, so leaving them filled would re-apply the search
		// the user just cleared on the next Enter.
		for _, f := range d.inputFields() {
			f.SetValue("")
		}
		d.deliver(gosmo.LogSearch{})
	case "Cancel":
		d.dismiss()
	}
}

// deliver hands the search to the panel and closes. onApply is read and
// cleared first, the way ConfirmDialog.finish does: it starts a read that can
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
// rather than sending it to a server that answers with a formatting lecture.
func (d *LogSearchDialog) parse() (gosmo.LogSearch, error) {
	// These three are capitalized on purpose: the dialog draws err.Error()
	// verbatim as its status line, so the leading word is the field's own
	// label as the user sees it. Nothing wraps them into a larger sentence.
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

// FocusedClipboardTarget implements core.ClipboardHost — every field here is
// text the user pastes into.
func (d *LogSearchDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return focusedClipboardTarget(d.fields(), d.focusIdx)
}
