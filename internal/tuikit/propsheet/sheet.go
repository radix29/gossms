package propsheet

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// PageState is where a page's data currently stands.
type PageState int

const (
	PageNotLoaded PageState = iota
	PageLoading
	PageReady
	PageError
)

type pageSlot struct {
	title string
	state PageState
	seq   int
	form  *Form
	err   error
}

// focusZone is which of the sheet's three regions currently has keyboard
// focus: the page list, the current page's form, or the button row.
type focusZone int

const (
	zonePages focusZone = iota
	zoneForm
	zoneButtons
)

var sheetButtonLabels = []string{"OK", "Cancel", "Apply", "Script Changes"}

const defaultHints = "Tab Move focus   ↑↓ Navigate   F5 Refresh   Ctrl+C Copy   Esc Cancel"

const pageListWidth = 24

// PropertySheet is a multi-page, editable properties dialog: a page list
// on the left, the selected page's Form on the right, and an OK/Cancel/
// Apply row — the framework behind Server/Database/Login Properties. See
// the package doc for the async load contract.
type PropertySheet struct {
	dialogs.ModalDialog

	screen tcell.Screen

	pages    []pageSlot
	pageList *controls.ListBox
	current  int

	zone     focusZone
	btnFocus int

	headerLeft, headerRight string
	hints                   string
	message                 string
	messageIsErr            bool
	applying                bool

	// OnLoadPage is called whenever a page needs (re)loading — on first
	// display or via Refresh. The caller should fetch the page's data,
	// typically on a background goroutine, and report the result via
	// SetPageForm or SetPageError, passing seq back unchanged.
	OnLoadPage func(page, seq int)
	// OnApply is called when the user activates the Apply button.
	OnApply func()
	// OnOK is called when the user activates the OK button.
	OnOK func()
	// OnClose is called after the sheet hides itself (Cancel or Esc).
	OnClose func()
	// ConfirmDiscard is called by Refresh when the target page has unsaved
	// edits, instead of refreshing immediately; call proceed to continue
	// the refresh (discarding those edits) or don't to leave them in
	// place. A nil ConfirmDiscard means Refresh always discards silently.
	ConfirmDiscard func(page int, proceed func())
	// OnScript is called when the user activates the Script Changes
	// button: generate the SQL for every dirty page's pending edits and
	// hand it off (e.g. to a new query editor) instead of running it.
	OnScript func()
}

// NewPropertySheet creates an empty PropertySheet. Call SetPages,
// SetHeader, and wire the On* callbacks before the first Show.
func NewPropertySheet(s tcell.Screen, title string) *PropertySheet {
	p := &PropertySheet{screen: s, current: -1, hints: defaultHints}
	p.InitModal(s, title, 90, 28)
	p.pageList = controls.NewListBox()
	p.pageList.OnSelect = func(i int) { p.SelectPage(i) }
	p.pageList.OnActivate = func(i int) { p.SelectPage(i); p.setZone(zoneForm) }
	return p
}

// SetPages replaces the page list, discarding any previously loaded forms
// — every page starts NotLoaded again. Call this once per dialog "open"
// (e.g. from the internal/tui show() wrapper), not on every Draw.
func (p *PropertySheet) SetPages(titles []string) {
	p.pages = make([]pageSlot, len(titles))
	items := make([]string, len(titles))
	for i, t := range titles {
		p.pages[i] = pageSlot{title: t}
		items[i] = t
	}
	p.pageList.SetItems(items)
	p.current = 0
}

// SetHeader sets the left- and right-aligned text shown above the page
// list/content split (e.g. "Instance: SQLMI-PROD" / "Connected: yes").
func (p *PropertySheet) SetHeader(left, right string) { p.headerLeft, p.headerRight = left, right }

// SetHints overrides the default footer key-hint line.
func (p *PropertySheet) SetHints(hints string) { p.hints = hints }

// SetTitle is re-exposed (ModalDialog already has it) purely so callers
// working entirely through PropertySheet's own method set don't need to
// reach into the embedded type — no behavior change.
func (p *PropertySheet) SetTitle(t string) { p.ModalDialog.SetTitle(t) }

// Show sizes the sheet to the current screen, resets to the first page,
// and (if not already loaded) kicks off its load.
func (p *PropertySheet) Show() {
	p.recomputeSize()
	p.zone = zonePages
	p.btnFocus = 0
	p.message = ""
	p.pageList.Focus(true)
	p.ModalDialog.Show()
	if len(p.pages) > 0 {
		p.pageList.SetSelected(0)
		p.SelectPage(0)
	}
}

func (p *PropertySheet) recomputeSize() {
	if p.screen == nil {
		return
	}
	sw, sh := p.screen.Size()
	p.SetSize(core.Clamp(sw-8, 72, 110), core.Clamp(sh-4, 20, 34))
}

// CurrentPage returns the index of the page currently shown.
func (p *PropertySheet) CurrentPage() int { return p.current }

// PageForm returns page i's loaded form, or nil if it hasn't loaded yet.
func (p *PropertySheet) PageForm(i int) *Form {
	if i < 0 || i >= len(p.pages) {
		return nil
	}
	return p.pages[i].form
}

// PageState reports page i's current load state.
func (p *PropertySheet) PageState(i int) PageState {
	if i < 0 || i >= len(p.pages) {
		return PageNotLoaded
	}
	return p.pages[i].state
}

// SelectPage switches the visible page, kicking off its load if it hasn't
// loaded yet.
func (p *PropertySheet) SelectPage(i int) {
	if i < 0 || i >= len(p.pages) {
		return
	}
	p.current = i
	p.pageList.SetSelected(i)
	p.message = ""
	if p.pages[i].state == PageNotLoaded {
		p.startLoad(i)
	}
}

func (p *PropertySheet) startLoad(i int) {
	slot := &p.pages[i]
	slot.state = PageLoading
	slot.seq++
	slot.err = nil
	if p.OnLoadPage != nil {
		p.OnLoadPage(i, slot.seq)
	}
}

// SetPageForm reports a successful load for page, provided seq still
// matches — a result for a page that's been refreshed again (or a sheet
// that's since been hidden) since the load started is silently ignored.
// Call only from the UI goroutine.
func (p *PropertySheet) SetPageForm(page, seq int, f *Form) {
	if page < 0 || page >= len(p.pages) || !p.Visible() {
		return
	}
	slot := &p.pages[page]
	if seq != slot.seq {
		return
	}
	slot.form = f
	slot.state = PageReady
	slot.err = nil
}

// SetPageError reports a failed load, under the same seq/visibility
// staleness guard as SetPageForm.
func (p *PropertySheet) SetPageError(page, seq int, err error) {
	if page < 0 || page >= len(p.pages) || !p.Visible() {
		return
	}
	slot := &p.pages[page]
	if seq != slot.seq {
		return
	}
	slot.err = err
	slot.state = PageError
}

// Refresh re-queries page, prompting via ConfirmDiscard first if it has
// unsaved edits.
func (p *PropertySheet) Refresh(page int) {
	if page < 0 || page >= len(p.pages) {
		return
	}
	slot := &p.pages[page]
	if slot.form != nil && slot.form.Dirty() && p.ConfirmDiscard != nil {
		p.ConfirmDiscard(page, func() { p.startLoad(page) })
		return
	}
	p.startLoad(page)
}

// InvalidateAll marks every page NotLoaded (used after a successful
// Apply, since the server is the source of truth) and reloads the
// current one immediately.
func (p *PropertySheet) InvalidateAll() {
	for i := range p.pages {
		p.pages[i].state = PageNotLoaded
		p.pages[i].form = nil
	}
	if p.current >= 0 && p.current < len(p.pages) {
		p.startLoad(p.current)
	}
}

// Dirty reports whether any loaded page has unsaved edits.
func (p *PropertySheet) Dirty() bool {
	for _, slot := range p.pages {
		if slot.form != nil && slot.form.Dirty() {
			return true
		}
	}
	return false
}

// DirtyPages returns the indices of every loaded page with unsaved edits.
func (p *PropertySheet) DirtyPages() []int {
	var out []int
	for i, slot := range p.pages {
		if slot.form != nil && slot.form.Dirty() {
			out = append(out, i)
		}
	}
	return out
}

// Validate runs every dirty page's validator, in page order, stopping at
// the first error.
func (p *PropertySheet) Validate() (page int, err error) {
	for i, slot := range p.pages {
		if slot.form == nil || !slot.form.Dirty() {
			continue
		}
		if err := slot.form.Validate(); err != nil {
			return i, err
		}
	}
	return -1, nil
}

// SetMessage sets the one-line message shown in place of the hint row
// (e.g. an Apply error); pass "" to clear it back to the hints.
func (p *PropertySheet) SetMessage(msg string, isErr bool) {
	p.message = msg
	p.messageIsErr = isErr
}

// SetApplying marks whether an Apply/OK is in flight — while true, the
// button row ignores further activation, so a slow Apply can't be fired
// twice.
func (p *PropertySheet) SetApplying(v bool) { p.applying = v }

func (p *PropertySheet) setZone(z focusZone) {
	if f := p.PageForm(p.current); f != nil {
		f.Focus(z == zoneForm)
	}
	p.pageList.Focus(z == zonePages)
	p.zone = z
}

func (p *PropertySheet) activateButton(i int) {
	if p.applying {
		return
	}
	switch i {
	case 0:
		if p.OnOK != nil {
			p.OnOK()
		}
	case 1:
		p.cancel()
	case 2:
		if p.OnApply != nil {
			p.OnApply()
		}
	case 3:
		if p.OnScript != nil {
			p.OnScript()
		}
	}
}

func (p *PropertySheet) cancel() {
	p.Hide()
	if p.OnClose != nil {
		p.OnClose()
	}
}

// ---------------------------------------------------------------------------
// Draw
// ---------------------------------------------------------------------------

func (p *PropertySheet) Draw(s tcell.Screen) {
	if !p.Visible() {
		return
	}
	p.recomputeSize()
	p.DrawBase(s)
	pal := theme.Active()
	inner := p.InnerRect()

	headerSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Text)
	core.DrawText(s, inner.X, inner.Y, headerSt, p.headerLeft)
	core.DrawTextRight(s, inner.X, inner.Y, inner.W, headerSt, p.headerRight)
	sep := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Border)
	core.DrawHLine(s, inner.X, inner.Y+1, inner.W, sep)

	bodyY := inner.Y + 2
	bodyBottom := p.ButtonRowY() - 2 // one row reserved for the hint/message line
	bodyH := core.Max(0, bodyBottom-bodyY)

	p.pageList.SetBounds(inner.X, bodyY, pageListWidth, bodyH)
	p.pageList.Draw(s)
	core.DrawVLine(s, inner.X+pageListWidth, bodyY, bodyH, sep)

	contentX := inner.X + pageListWidth + 2
	contentW := core.Max(0, inner.Right()-contentX)
	p.drawContent(s, contentX, bodyY, contentW, bodyH)

	msgY := bodyBottom
	if p.message != "" {
		st := headerSt
		if p.messageIsErr {
			st = tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Error)
		}
		core.DrawTextClipped(s, inner.X, msgY, inner.W, st, p.message)
	} else {
		hintSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.TextDim)
		hint := p.hints
		if p.applying {
			hint = "Applying…"
		}
		core.DrawTextClipped(s, inner.X, msgY, inner.W, hintSt, hint)
	}

	p.DrawSeparator(s)
	activeIdx := -1
	if p.zone == zoneButtons {
		activeIdx = p.btnFocus
	}
	p.DrawButtons(s, sheetButtonLabels, activeIdx)

	if p.zone == zoneForm {
		if f := p.PageForm(p.current); f != nil {
			f.DrawOverlays(s)
		}
	}
}

func (p *PropertySheet) drawContent(s tcell.Screen, x, y, w, h int) {
	if p.current < 0 || p.current >= len(p.pages) || h <= 0 {
		return
	}
	slot := &p.pages[p.current]
	pal := theme.Active()
	titleSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Text).Bold(true)
	core.DrawText(s, x, y, titleSt, slot.title)
	sep := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Border)
	core.DrawHLine(s, x, y+1, w, sep)

	contentY, contentH := y+2, core.Max(0, h-2)
	dimSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.TextDim)

	switch slot.state {
	case PageNotLoaded, PageLoading:
		core.DrawText(s, x, contentY, dimSt, "Loading…")
	case PageError:
		errSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Error)
		msg := "Error"
		if slot.err != nil {
			msg = "Error: " + slot.err.Error()
		}
		core.DrawTextClipped(s, x, contentY, w, errSt, msg)
		core.DrawText(s, x, contentY+2, dimSt, "Press F5 to retry.")
	case PageReady:
		if slot.form != nil {
			slot.form.SetBounds(x, contentY, w, contentH)
			slot.form.Draw(s)
		}
	}
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

func (p *PropertySheet) HandleKey(ev *tcell.EventKey) bool {
	if !p.Visible() {
		return false
	}
	if ev.Key() == tcell.KeyF5 {
		p.Refresh(p.current)
		return true
	}
	// Escape cancels the whole sheet everywhere except zoneForm, where the
	// focused row gets first refusal — an open dropdown overlay consumes
	// Escape to close itself (see DropDown.HandleKey) rather than the
	// whole dialog vanishing out from under it. If the form doesn't want
	// the key, Escape falls through to cancel below, same as elsewhere.
	if ev.Key() == tcell.KeyEscape && p.zone != zoneForm {
		p.cancel()
		return true
	}

	switch p.zone {
	case zonePages:
		if p.pageList.HandleKey(ev) {
			return true
		}
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyRight, tcell.KeyEnter:
			p.setZone(zoneForm)
		case tcell.KeyBacktab:
			p.setZone(zoneButtons)
		}
	case zoneForm:
		if f := p.PageForm(p.current); f != nil && f.HandleKey(ev) {
			return true
		}
		switch ev.Key() {
		case tcell.KeyEscape:
			p.cancel()
			return true
		case tcell.KeyTab:
			p.setZone(zoneButtons)
		case tcell.KeyBacktab, tcell.KeyLeft:
			p.setZone(zonePages)
		}
	case zoneButtons:
		switch ev.Key() {
		case tcell.KeyLeft:
			if p.btnFocus > 0 {
				p.btnFocus--
			}
		case tcell.KeyRight:
			if p.btnFocus < len(sheetButtonLabels)-1 {
				p.btnFocus++
			}
		case tcell.KeyEnter:
			p.activateButton(p.btnFocus)
		case tcell.KeyTab:
			p.setZone(zonePages)
		case tcell.KeyBacktab:
			p.setZone(zoneForm)
		}
	}
	return true
}

func (p *PropertySheet) HandleMouse(ev *tcell.EventMouse) bool {
	if !p.Visible() {
		return false
	}
	if p.ConsumeOutsideClick(ev) {
		return true
	}
	if i := p.ButtonClicked(ev, sheetButtonLabels); i >= 0 {
		p.setZone(zoneButtons)
		p.btnFocus = i
		p.activateButton(i)
		return true
	}
	if p.pageList.HandleMouse(ev) {
		p.setZone(zonePages)
		return true
	}
	if f := p.PageForm(p.current); f != nil && f.HandleMouse(ev) {
		p.setZone(zoneForm)
		return true
	}
	return true
}

// ---------------------------------------------------------------------------
// clipboardTarget — see internal/tui/clipboard.go
// ---------------------------------------------------------------------------

func (p *PropertySheet) currentCopyText() (string, bool) {
	if p.zone != zoneForm {
		return "", false
	}
	f := p.PageForm(p.current)
	if f == nil {
		return "", false
	}
	return f.CopyText()
}

func (p *PropertySheet) focusedClipboardRow() (ClipboardRow, bool) {
	if p.zone != zoneForm {
		return nil, false
	}
	f := p.PageForm(p.current)
	if f == nil {
		return nil, false
	}
	cr, ok := f.Focused().(ClipboardRow)
	return cr, ok
}

// HasSelection reports whether Ctrl+C has something to copy: a real text
// selection within the focused field, or — lacking one — any non-empty
// copyable value on the focused row (a static row, a checkbox's state, a
// grid's selected row/cell, or a text field's whole value).
func (p *PropertySheet) HasSelection() bool {
	if cr, ok := p.focusedClipboardRow(); ok && cr.HasSelection() {
		return true
	}
	txt, ok := p.currentCopyText()
	return ok && txt != ""
}

// SelectedText returns what Ctrl+C would copy — see HasSelection.
func (p *PropertySheet) SelectedText() string {
	if cr, ok := p.focusedClipboardRow(); ok && cr.HasSelection() {
		return cr.SelectedText()
	}
	txt, _ := p.currentCopyText()
	return txt
}

// Cut removes and returns the focused field's real selection; every other
// row kind has nothing to remove, so it degrades to Copy — cutSelection()
// (internal/tui/clipboard.go) only calls Cut() when HasSelection() was
// already true, so this still only fires when there's something to copy.
func (p *PropertySheet) Cut() string {
	if cr, ok := p.focusedClipboardRow(); ok && cr.HasSelection() {
		return cr.Cut()
	}
	return p.SelectedText()
}

// Paste inserts text into the focused field, if it's an editable one.
func (p *PropertySheet) Paste(text string) {
	if cr, ok := p.focusedClipboardRow(); ok {
		cr.Paste(text)
	}
}

// SelectAll selects the focused field's entire contents, if editable.
func (p *PropertySheet) SelectAll() {
	if cr, ok := p.focusedClipboardRow(); ok {
		cr.SelectAll()
	}
}
