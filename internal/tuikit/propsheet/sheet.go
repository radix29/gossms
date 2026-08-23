package propsheet

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
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

// focusZone is which of the sheet's three regions has keyboard focus: the page
// list, the current page's form, or the button row.
type focusZone int

const (
	zonePages focusZone = iota
	zoneForm
	zoneButtons
)

// zoneNone is not a focusable zone — it is PropertySheet.dragZone's "no gesture
// in progress" value.
const zoneNone focusZone = -1

var sheetButtonLabels = []string{"OK", "Cancel", "Apply", "Script Changes"}

const defaultHints = "Tab Move focus   ↑↓ Navigate   F5 Refresh   Ctrl+Z Revert   Ctrl+C Copy   Esc Cancel"

const pageListWidth = 24

// PropertySheet is a multi-page, editable properties dialog: a page list on the
// left, the selected page's Form on the right, and an OK/Cancel/Apply row. See
// the package doc for the async load contract.
type PropertySheet struct {
	dialogs.ModalDialog

	screen tcell.Screen

	pages    []pageSlot
	pageList *controls.ListBox
	current  int

	zone     focusZone
	btnFocus int

	// dragZone is the zone that claimed the Button1 press being held, or zoneNone
	// between gestures. tcell resends Button1 on every motion while the button is
	// down, so without this a drag armed inside the form — its scrollbar thumb, a
	// GridRow's DataGrid selection — that wanders onto the button row three lines
	// below activates OK/Cancel/Apply mid-gesture. ModalDialog's own
	// mouseDragging latch can't stop that, being armed only by a press on a
	// button. Cleared on the ButtonNone release.
	dragZone focusZone

	headerLeft, headerRight string
	hints                   string
	message                 string
	messageIsErr            bool
	applying                bool

	// OnLoadPage is called whenever a page needs (re)loading, on first display or
	// via Refresh. The caller fetches the page's data, typically on a background
	// goroutine, and reports the result via SetPageForm or SetPageError, passing
	// seq back unchanged.
	OnLoadPage func(page, seq int)
	// OnApply is called when the user activates the Apply button.
	OnApply func()
	// OnOK is called when the user activates the OK button.
	OnOK func()
	// OnClose is called after the sheet hides itself (Cancel or Esc).
	OnClose func()
	// ConfirmDiscard is called by Refresh when the target page has unsaved edits,
	// instead of refreshing immediately; call proceed to continue and discard
	// them, or don't to leave them. nil means Refresh always discards silently.
	ConfirmDiscard func(page int, proceed func())
	// OnScript is called when the user activates Script Changes: generate the SQL
	// for every dirty page's pending edits and hand it off instead of running
	// it.
	OnScript func()
}

// NewPropertySheet creates an empty PropertySheet. Call SetPages, SetHeader and
// wire the On* callbacks before the first Show.
func NewPropertySheet(s tcell.Screen, title string) *PropertySheet {
	p := &PropertySheet{screen: s, current: -1, hints: defaultHints, dragZone: zoneNone}
	p.InitModal(s, title, 90, 28)
	p.pageList = controls.NewListBox()
	p.pageList.OnSelect = func(i int) { p.SelectPage(i) }
	p.pageList.OnActivate = func(i int) { p.SelectPage(i); p.setZone(zoneForm) }
	return p
}

// SetPages replaces the page list, discarding any loaded forms — every page
// starts NotLoaded again. Call it once per dialog opening, not on every Draw.
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

// SetTitle is re-exposed from ModalDialog so callers working through
// PropertySheet's own method set needn't reach into the embedded type.
func (p *PropertySheet) SetTitle(t string) { p.ModalDialog.SetTitle(t) }

// Show sizes the sheet to the current screen, resets to the first page,
// and (if not already loaded) kicks off its load.
func (p *PropertySheet) Show() {
	p.recomputeSize()
	p.zone = zonePages
	p.btnFocus = 0
	p.message = ""
	// A button action that hides the sheet (OK) leaves the gesture armed: the
	// release lands while invisible and HandleMouse returns early.
	p.dragZone = zoneNone
	p.pageList.Focus(true)
	p.ModalDialog.Show()
	if len(p.pages) > 0 {
		p.pageList.SetSelected(0)
		p.SelectPage(0)
	}
}

// Relayout re-derives the sheet's screen-relative size, then recentres. Draw
// does the same every frame, so this only makes it correct at the moment the
// host broadcasts rather than one draw later.
func (p *PropertySheet) Relayout() { p.recomputeSize() }

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

// SelectPage switches the visible page, starting its load if it hasn't loaded
// and no Apply/OK/Script is running. Navigating to an already-loaded or
// still-loading page is always allowed; it is starting a *new* load while
// applying that isn't. A dirty page's apply closure runs on its own goroutine
// and, for pages built around a shared rename pointer, writes to it — a
// concurrent OnLoadPage dispatch would read that pointer from a second goroutine
// unsynchronized. Deferred rather than dropped: SetApplying(false) starts the
// load then, if the page is still selected and unloaded.
func (p *PropertySheet) SelectPage(i int) {
	if i < 0 || i >= len(p.pages) {
		return
	}
	p.current = i
	p.pageList.SetSelected(i)
	p.message = ""
	if p.pages[i].state == PageNotLoaded && !p.applying {
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

// SetPageForm reports a successful load for page, provided seq still matches: a
// result for a page refreshed again, or a sheet since hidden, is ignored. Call
// only from the UI goroutine.
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

// RevertPage restores page's rows to the values it loaded with, without
// re-querying the server, and reports whether anything changed. Refresh is the
// expensive half of the pair: it throws the loaded form away and asks the server
// again.
//
// Unlike Refresh this asks no confirmation — discarding the edits *is* what was
// asked for — and the message it leaves says so, since a form that reverts to
// values identical to what was typed would look inert.
func (p *PropertySheet) RevertPage(page int) bool {
	if page < 0 || page >= len(p.pages) {
		return false
	}
	slot := &p.pages[page]
	if slot.form == nil || !slot.form.Dirty() {
		return false
	}
	slot.form.Revert()
	return true
}

// InvalidateAll marks every page NotLoaded, after a successful Apply, and
// reloads the current one immediately.
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

// SetMessage sets the one-line message shown in place of the hint row; "" clears
// it back to the hints.
func (p *PropertySheet) SetMessage(msg string, isErr bool) {
	p.message = msg
	p.messageIsErr = isErr
}

// SetApplying marks whether an Apply/OK is in flight. While true the button row
// ignores further activation, so a slow Apply can't fire twice, and SelectPage
// won't start a new page load. Turning it off retries the selected page's load
// if the user navigated to an unloaded page while applying.
func (p *PropertySheet) SetApplying(v bool) {
	p.applying = v
	if !v && p.current >= 0 && p.current < len(p.pages) && p.pages[p.current].state == PageNotLoaded {
		p.startLoad(p.current)
	}
}

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

func (p *PropertySheet) cancel() { p.Dismiss() }

// Dismiss closes the sheet and notifies its owner via OnClose. Every path that
// closes the sheet from the inside — Cancel, Escape, an OK handler whose save
// succeeded — goes through here rather than ModalDialog's Hide, which only takes
// the dialog off screen. OnClose is what releases the per-showing resources: for
// PropDialog and every New* dialog, cancelling the context their page loads run
// under, so a fetch in flight when OK is pressed is dropped instead of holding a
// pooled connection to its own timeout.
func (p *PropertySheet) Dismiss() {
	p.Hide()
	if p.OnClose != nil {
		p.OnClose()
	}
}
