package propsheet

import (
	"errors"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// newTestSheet builds a sheet with a nil screen (ModalDialog/PropertySheet
// both guard against that — recentre/recomputeSize simply no-op) and a
// fixed size, so tests don't depend on an actual terminal.
func newTestSheet(pages ...string) *PropertySheet {
	p := NewPropertySheet(nil, "Test Properties")
	p.SetSize(90, 28)
	p.SetPages(pages)
	return p
}

func TestSheetLoadLifecycle(t *testing.T) {
	p := newTestSheet("General")
	var loadedPage, loadedSeq int
	loads := 0
	p.OnLoadPage = func(page, seq int) { loadedPage, loadedSeq = page, seq; loads++ }

	p.Show()
	if loads != 1 {
		t.Fatalf("OnLoadPage fired %d times on Show(), want 1", loads)
	}
	if p.PageState(0) != PageLoading {
		t.Fatalf("PageState(0) = %v, want PageLoading", p.PageState(0))
	}

	f := NewForm(Static("Name", "srv01"))
	p.SetPageForm(loadedPage, loadedSeq, f)
	if p.PageState(0) != PageReady {
		t.Fatalf("PageState(0) after SetPageForm = %v, want PageReady", p.PageState(0))
	}
	if p.PageForm(0) != f {
		t.Fatal("PageForm(0) did not return the form passed to SetPageForm")
	}
}

func TestSheetStalePageFormIgnored(t *testing.T) {
	p := newTestSheet("General")
	var seq1 int
	first := true
	p.OnLoadPage = func(page, s int) {
		if first {
			seq1 = s // capture only the *first* load's seq
			first = false
		}
	}
	p.Show() // triggers load #1, seq1 captured

	// A second load starts (e.g. a refresh) before the first's result comes
	// back, bumping the page's seq.
	p.Refresh(0)

	// The stale result from load #1 arrives late.
	staleForm := NewForm(Static("Stale", "yes"))
	p.SetPageForm(0, seq1, staleForm)

	if p.PageForm(0) == staleForm {
		t.Fatal("SetPageForm with a stale seq was applied, want it ignored")
	}
	if p.PageState(0) != PageLoading {
		t.Fatalf("PageState(0) after a stale result = %v, want still PageLoading", p.PageState(0))
	}
}

func TestSheetStalePageFormIgnoredWhenHidden(t *testing.T) {
	p := newTestSheet("General")
	var seq int
	p.OnLoadPage = func(page, s int) { seq = s }
	p.Show()
	p.Hide()

	f := NewForm(Static("Name", "srv01"))
	p.SetPageForm(0, seq, f)
	if p.PageForm(0) == f {
		t.Fatal("SetPageForm applied a result after the sheet was hidden, want it ignored")
	}
}

func TestSheetPageErrorLifecycle(t *testing.T) {
	p := newTestSheet("General")
	var seq int
	p.OnLoadPage = func(page, s int) { seq = s }
	p.Show()

	p.SetPageError(0, seq, errors.New("boom"))
	if p.PageState(0) != PageError {
		t.Fatalf("PageState(0) = %v, want PageError", p.PageState(0))
	}
}

func TestSheetRefreshDiscardsSilentlyWithoutConfirmDiscard(t *testing.T) {
	p := newTestSheet("General")
	loads := 0
	p.OnLoadPage = func(page, seq int) { loads++ }
	p.Show()
	f := NewForm(Text("Name", "orig", 10))
	p.SetPageForm(0, 1, f)
	f.Focus(true)
	f.Focused().(*TextRow).field.SetValue("changed")

	if !f.Dirty() {
		t.Fatal("test setup: form should be dirty")
	}
	p.Refresh(0) // no ConfirmDiscard installed -> discards immediately
	if loads != 2 {
		t.Fatalf("loads = %d after Refresh(), want 2 (initial + refresh)", loads)
	}
}

func TestSheetRefreshPromptsConfirmDiscardWhenDirty(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	f := NewForm(Text("Name", "orig", 10))
	p.SetPageForm(0, 1, f)
	f.Focus(true)
	f.Focused().(*TextRow).field.SetValue("changed")

	var confirmedPage = -1
	var proceedFn func()
	p.ConfirmDiscard = func(page int, proceed func()) {
		confirmedPage = page
		proceedFn = proceed
	}
	p.Refresh(0)
	if confirmedPage != 0 {
		t.Fatalf("ConfirmDiscard called with page %d, want 0", confirmedPage)
	}
	if p.PageState(0) != PageReady {
		t.Fatal("Refresh() started loading before ConfirmDiscard's proceed was called")
	}
	proceedFn()
	if p.PageState(0) != PageLoading {
		t.Fatal("calling proceed() did not start the refresh")
	}
}

func TestSheetDirtyPages(t *testing.T) {
	p := newTestSheet("General", "Memory")
	seqs := map[int]int{}
	p.OnLoadPage = func(page, seq int) { seqs[page] = seq }
	p.Show()
	p.SelectPage(1) // page 1 is only loaded on demand, unlike page 0 on Show()
	p.SetPageForm(0, seqs[0], NewForm(Text("Name", "orig", 10)))
	p.SetPageForm(1, seqs[1], NewForm(Text("Max", "orig", 10)))

	if p.Dirty() {
		t.Fatal("Dirty() = true before any edits")
	}
	p.PageForm(1).rows[0].(*TextRow).field.SetValue("changed")
	if !p.Dirty() {
		t.Fatal("Dirty() = false after editing page 1")
	}
	if got := p.DirtyPages(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("DirtyPages() = %v, want [1]", got)
	}
}

func TestSheetZoneTabCycling(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	p.SetPageForm(0, 1, NewForm(Static("Name", "srv01")))

	if p.zone != zonePages {
		t.Fatalf("zone after Show() = %v, want zonePages", p.zone)
	}
	p.HandleKey(key(tcell.KeyTab, tcell.ModNone))
	if p.zone != zoneForm {
		t.Fatalf("zone after Tab from pages = %v, want zoneForm", p.zone)
	}
	p.HandleKey(key(tcell.KeyTab, tcell.ModNone))
	if p.zone != zoneButtons {
		t.Fatalf("zone after Tab from form (1-row form) = %v, want zoneButtons", p.zone)
	}
	p.HandleKey(key(tcell.KeyTab, tcell.ModNone))
	if p.zone != zonePages {
		t.Fatalf("zone after Tab from buttons = %v, want zonePages (wrapped around)", p.zone)
	}
}

func TestSheetScriptButtonFiresOnScript(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	p.SetPageForm(0, 1, NewForm(Static("Name", "srv01")))

	scripted := false
	p.OnScript = func() { scripted = true }

	p.setZone(zoneButtons)
	p.btnFocus = 0
	for _, label := range sheetButtonLabels {
		if label == "Script Changes" {
			break
		}
		p.HandleKey(key(tcell.KeyRight, tcell.ModNone))
	}
	p.HandleKey(key(tcell.KeyEnter, tcell.ModNone))

	if !scripted {
		t.Fatal("OnScript was not called after activating the Script Changes button")
	}
}

// TestSheetEscapeClosesOpenDropdownInsteadOfCancelling is a regression
// test: with a SelectRow's dropdown open, Escape must close the dropdown
// (DropDown.HandleKey consumes it) rather than the whole sheet vanishing
// out from under it.
func TestSheetEscapeClosesOpenDropdownInsteadOfCancelling(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	sel := Select("Pick", []string{"a", "b"}, 0)
	p.SetPageForm(0, 1, NewForm(sel))
	p.HandleKey(key(tcell.KeyTab, tcell.ModNone)) // pages -> form, focuses sel

	// Form focus is logical only; the row's own focused flag is normally
	// synced by Draw (SelectRow.Draw calls dd.Focus). No Draw happens in
	// this test, so set it directly — without it dd.HandleKey ignores the
	// Enter below (DropDown.HandleKey returns false when unfocused).
	sel.dd.Focus(true)
	sel.dd.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) // open the dropdown
	if !sel.dd.IsOpen() {
		t.Fatal("setup: expected dropdown to be open")
	}

	closed := false
	p.OnClose = func() { closed = true }
	p.HandleKey(key(tcell.KeyEscape, tcell.ModNone))

	if sel.dd.IsOpen() {
		t.Fatal("Escape should have closed the open dropdown")
	}
	if !p.Visible() || closed {
		t.Fatal("Escape with an open dropdown should not have cancelled the sheet")
	}
}

func TestSheetEscapeCancelsAndFiresOnClose(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	closed := false
	p.OnClose = func() { closed = true }
	p.HandleKey(key(tcell.KeyEscape, tcell.ModNone))
	if p.Visible() {
		t.Fatal("sheet still visible after Escape")
	}
	if !closed {
		t.Fatal("OnClose was not called after Escape")
	}
}

func TestSheetF5RefreshesCurrentPage(t *testing.T) {
	p := newTestSheet("General")
	loads := 0
	p.OnLoadPage = func(page, seq int) { loads++ }
	p.Show()
	p.SetPageForm(0, 1, NewForm(Static("Name", "srv01")))
	p.HandleKey(key(tcell.KeyF5, tcell.ModNone))
	if loads != 2 {
		t.Fatalf("loads after F5 = %d, want 2", loads)
	}
}

func TestSheetClipboardStaticRow(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	p.SetPageForm(0, 1, NewForm(Static("Name", "srv01")))
	p.HandleKey(key(tcell.KeyTab, tcell.ModNone)) // pages -> form

	if !p.HasSelection() {
		t.Fatal("HasSelection() = false for a focused Static row with a value")
	}
	if got := p.SelectedText(); got != "srv01" {
		t.Fatalf("SelectedText() = %q, want %q", got, "srv01")
	}
}

func TestSheetClipboardTextRowWholeValueWithoutSelection(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	p.SetPageForm(0, 1, NewForm(Text("Name", "sa", 10)))
	p.HandleKey(key(tcell.KeyTab, tcell.ModNone))

	if !p.HasSelection() {
		t.Fatal("HasSelection() = false for a focused text row with no explicit selection but a value")
	}
	if got := p.SelectedText(); got != "sa" {
		t.Fatalf("SelectedText() = %q, want the whole field value %q", got, "sa")
	}
}

func TestSheetClipboardTextRowExplicitSelectionWins(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	p.SetPageForm(0, 1, NewForm(Text("Name", "hello world", 20)))
	p.HandleKey(key(tcell.KeyTab, tcell.ModNone))

	row := p.PageForm(0).Focused().(*TextRow)
	row.field.SelectAll()
	if got := p.SelectedText(); got != "hello world" {
		t.Fatalf("SelectedText() with an explicit selection = %q, want %q", got, "hello world")
	}
}

func TestSheetClipboardPasteOnlyIntoEditableRow(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	p.SetPageForm(0, 1, NewForm(Static("Name", "srv01"), Text("Owner", "", 10)))
	p.HandleKey(key(tcell.KeyTab, tcell.ModNone)) // focuses Static first

	p.Paste("nope")
	if p.PageForm(0).rows[0].(*StaticRow).Value() != "srv01" {
		t.Fatal("Paste() mutated a read-only Static row")
	}

	p.PageForm(0).FocusNext() // move to the Text row
	p.Paste("pasted")
	if got := p.PageForm(0).rows[1].(*TextRow).Value(); got != "pasted" {
		t.Fatalf("Paste() into the focused Text row = %q, want %q", got, "pasted")
	}
}

func TestSheetHasNoSelectionOutsideFormZone(t *testing.T) {
	p := newTestSheet("General")
	p.OnLoadPage = func(page, seq int) {}
	p.Show()
	p.SetPageForm(0, 1, NewForm(Static("Name", "srv01")))
	// zone is zonePages right after Show(); never Tab'd into the form.
	if p.HasSelection() {
		t.Fatal("HasSelection() = true while the page list (not the form) has focus")
	}
}

// TestSheetHandleMouseForwardsReleaseToPageList pins the fix for a real
// bug: PropertySheet.HandleMouse's "forward a release to a latch-bearing
// child before any early return" branch (see the ButtonNone block at the
// top of HandleMouse) used to forward only to the current page's Form, not
// to pageList — even though pageList (a controls.ListBox) gained its own
// mouseDragging latch in the same change. A release landing outside the
// whole dialog (consumed by ConsumeOutsideClick) would leave that latch
// stuck, silently swallowing pageList's next press. Uses pageList's own
// HandleMouse directly to arm/observe the latch, since giving the
// embedded dialogs.ModalDialog a real (non-zero) rect would require an
// actual tcell.Screen, unavailable in this package's unit tests.
func TestSheetHandleMouseForwardsReleaseToPageList(t *testing.T) {
	p := newTestSheet("General", "Memory")
	p.pageList.SetBounds(0, 0, 20, 10)
	p.Show()

	var selected []int
	p.pageList.OnSelect = func(i int) { selected = append(selected, i) }

	// Arm the page list's latch: a press on row 1 ("Memory").
	p.pageList.HandleMouse(tcell.NewEventMouse(0, 1, tcell.Button1, tcell.ModNone))
	if want := []int{1}; len(selected) != 1 || selected[0] != want[0] {
		t.Fatalf("selected = %v after first press, want %v", selected, want)
	}

	// A release landing far outside the dialog must still reach the page
	// list and clear its latch.
	p.HandleMouse(tcell.NewEventMouse(-100, -100, tcell.ButtonNone, tcell.ModNone))

	// A genuine second press on a different row must register. If the
	// release above hadn't reached pageList, ListBox.HandleMouse's
	// mouseDragging check swallows *every* Button1 press while armed —
	// regardless of row — so this would otherwise silently do nothing.
	p.pageList.HandleMouse(tcell.NewEventMouse(0, 0, tcell.Button1, tcell.ModNone))
	if want := []int{1, 0}; len(selected) != 2 || selected[1] != want[1] {
		t.Fatalf("selected = %v after second press, want %v (release must clear the page list's latch)", selected, want)
	}
}
