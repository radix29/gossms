package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestMenuBar() *MenuBar {
	mb := NewMenuBar()
	mb.SetBounds(0, 0, 40)
	mb.SetMenus([]Menu{
		{Label: "File", Items: []MenuItem{{Label: "Open"}, {Label: "Exit"}}},
		{Label: "Edit", Items: []MenuItem{{Label: "Copy"}}},
	})
	return mb
}

func TestMenuBarHoverOutsideDropdownDoesNotClose(t *testing.T) {
	mb := newTestMenuBar()
	mb.Open()

	handled := mb.HandleMouse(tcell.NewEventMouse(5, 10, tcell.ButtonNone, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true (swallowed) while a dropdown is open")
	}
	if !mb.IsOpen() {
		t.Fatalf("dropdown closed on a hover outside it; want it to stay open")
	}
}

func TestMenuBarClickOutsideDropdownCloses(t *testing.T) {
	mb := newTestMenuBar()
	mb.Open()

	handled := mb.HandleMouse(tcell.NewEventMouse(5, 10, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true (swallowed) while a dropdown was open")
	}
	if mb.IsOpen() {
		t.Fatalf("dropdown stayed open after a click outside it; want it closed")
	}
}

func TestMenuBarHoverOverBarRowOffLabelsDoesNotClose(t *testing.T) {
	mb := newTestMenuBar()
	mb.Open()

	// Row 0 (the bar itself) but past every menu label — e.g. the toolbar's
	// region on the same row.
	handled := mb.HandleMouse(tcell.NewEventMouse(35, 0, tcell.ButtonNone, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true (swallowed) while a dropdown is open")
	}
	if !mb.IsOpen() {
		t.Fatalf("dropdown closed on a hover over the bar row outside its labels; want it to stay open")
	}
}

func TestMenuBarClickOverBarRowOffLabelsCloses(t *testing.T) {
	mb := newTestMenuBar()
	mb.Open()

	handled := mb.HandleMouse(tcell.NewEventMouse(35, 0, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true (swallowed) while a dropdown was open")
	}
	if mb.IsOpen() {
		t.Fatalf("dropdown stayed open after a click over the bar row outside its labels; want it closed")
	}
}

func TestMenuBarHeldButtonOverHeaderDoesNotReToggle(t *testing.T) {
	mb := newTestMenuBar()

	// Press opens "File" (columns 1-6 per " File ").
	mb.HandleMouse(tcell.NewEventMouse(2, 0, tcell.Button1, tcell.ModNone))
	if !mb.IsOpen() {
		t.Fatalf("press on header did not open the menu")
	}

	// The button is still down and the mouse merely shifted a column while
	// staying over the same header — tcell's all-motion tracking resends
	// Buttons()==Button1 for this. It must not re-toggle the menu closed.
	handled := mb.HandleMouse(tcell.NewEventMouse(3, 0, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true while still over the header")
	}
	if !mb.IsOpen() {
		t.Fatalf("menu closed on a held-button move over the same header; want it to stay open")
	}

	// Release, then a genuine new press on the same header, does toggle it.
	mb.HandleMouse(tcell.NewEventMouse(3, 0, tcell.ButtonNone, tcell.ModNone))
	mb.HandleMouse(tcell.NewEventMouse(3, 0, tcell.Button1, tcell.ModNone))
	if mb.IsOpen() {
		t.Fatalf("a fresh press after release did not close the menu")
	}
}

func TestMenuBarClosedIgnoresEventsOffTheBar(t *testing.T) {
	mb := newTestMenuBar()

	if handled := mb.HandleMouse(tcell.NewEventMouse(5, 10, tcell.ButtonNone, tcell.ModNone)); handled {
		t.Fatalf("HandleMouse() = true, want false when no menu is open and the event is elsewhere")
	}
}
