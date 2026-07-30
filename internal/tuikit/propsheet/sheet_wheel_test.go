package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// newScrollableSheet builds a sheet whose current page is a form taller than
// its band, so the form has a scrollbar to drag and room to scroll.
func newScrollableSheet(t *testing.T) *PropertySheet {
	t.Helper()
	// A real (fake) screen, not newTestSheet's nil one: ModalDialog.recentre
	// no-ops without a screen, leaving p.rect zero-sized — and then
	// ConsumeOutsideClick treats every event as an outside click and swallows
	// it before any of the routing under test runs.
	p := NewPropertySheet(&fakeScreen{w: 100, h: 40}, "Test Properties")
	p.SetSize(90, 28)
	p.SetPages([]string{"General", "Options"})
	p.Show()

	// A short GridRow at the top gives the press something focusable to arm
	// the gesture on; the plain rows below it are what the wheel is aimed at,
	// since a wheel over the grid is claimed by the grid itself and never
	// reaches the form's own scroll.
	g := controls.NewDataGrid()
	g.SetData([]string{"A"}, [][]string{{"1"}, {"2"}, {"3"}})
	f := NewForm(Section("S1"), NewGridRow(g, 3),
		Section("S2"), Note("x"), Note("x2"),
		Section("S3"), Note("y"), Note("y2"),
		Section("S4"), Note("z"), Note("z2"))
	p.SetPageForm(0, p.pages[0].seq, f)

	// The form's bounds are set by hand rather than by p.Draw: the sheet's
	// DrawBase dims the whole screen through Screen.Get, which the package's
	// fakeScreen doesn't implement. Nothing under test needs the sheet drawn
	// — only the form laid out inside p.rect so positional routing lands on
	// it.
	f.SetBounds(p.Rect().X+2, p.Rect().Y+2, p.Rect().W-6, 10)
	f.Focus(true)
	f.Draw(&fakeScreen{w: 100, h: 40})
	return p
}

// A wheel tick that arrives while a drag is in progress must be swallowed,
// not routed. Without this it fell past routeDrag into the positional
// dispatch below, which both scrolls whatever the pointer happens to be over
// and calls setZone — so wheeling while dragging the form's scrollbar moved
// the focus zone out from under the drag.
//
// App.handleMouse's gestureOwner cannot cover this one: it dispatches the top
// dialog before its own gesture check and never arms a gesture for a click
// inside a dialog.
func TestSheetSwallowsWheelDuringADrag(t *testing.T) {
	p := newScrollableSheet(t)
	f := p.PageForm(0)

	// Press inside the form to arm the gesture.
	inner := f.rect
	p.HandleMouse(tcell.NewEventMouse(inner.X+2, inner.Y+2, tcell.Button1, tcell.ModNone))
	if p.dragZone == zoneNone {
		t.Fatal("press inside the form did not arm dragZone")
	}
	p.setZone(zoneButtons) // whatever the drag is, the zone must not move under it
	scrollBefore := f.scroll

	// Wheel, mid-gesture, over the form.
	handled := p.HandleMouse(tcell.NewEventMouse(inner.X+2, inner.Y+8, tcell.WheelDown, tcell.ModNone))

	if !handled {
		t.Error("HandleMouse returned false for a wheel tick during a drag; it must consume it")
	}
	if f.scroll != scrollBefore {
		t.Errorf("form scrolled %d -> %d during a drag, want it unchanged", scrollBefore, f.scroll)
	}
	if p.zone != zoneButtons {
		t.Errorf("focus zone moved to %v during a drag, want it left at zoneButtons", p.zone)
	}
	if p.dragZone == zoneNone {
		t.Error("the wheel tick cleared dragZone; only a release may do that")
	}
}

// The release still ends the gesture, and a wheel tick after it scrolls
// normally — the swallow must be scoped to the gesture, not permanent.
func TestSheetWheelWorksAgainAfterTheRelease(t *testing.T) {
	p := newScrollableSheet(t)
	f := p.PageForm(0)
	inner := f.rect

	p.HandleMouse(tcell.NewEventMouse(inner.X+2, inner.Y+2, tcell.Button1, tcell.ModNone))
	p.HandleMouse(tcell.NewEventMouse(inner.X+2, inner.Y+2, tcell.ButtonNone, tcell.ModNone))
	if p.dragZone != zoneNone {
		t.Fatal("release did not clear dragZone")
	}

	before := f.scroll
	p.HandleMouse(tcell.NewEventMouse(inner.X+2, inner.Y+8, tcell.WheelDown, tcell.ModNone))
	if f.scroll == before {
		t.Errorf("wheel after the release did not scroll (still %d)", before)
	}
}
