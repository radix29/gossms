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

// newTestMenuBarWithDisabled builds a single "File" menu with a disabled
// item on either side of one enabled item ("Middle") — the shared fixture
// for every Enabled-gating test below. calls is incremented each time
// "Middle"'s Action fires.
func newTestMenuBarWithDisabled(calls *int) *MenuBar {
	mb := NewMenuBar()
	mb.SetBounds(0, 0, 40)
	mb.SetMenus([]Menu{
		{Label: "File", Items: []MenuItem{
			{Label: "First", Enabled: func() bool { return false }},
			{Label: "Middle", Action: func() { *calls++ }},
			{Label: "Last", Enabled: func() bool { return false }},
		}},
	})
	return mb
}

// itemRowY returns the screen row MenuBar draws item index i at, for a bar
// opened via Open()/newTestMenuBar* (rect.Y == 0) — mirrors drawDropdown's
// own y := mb.rect.Y + 2 + i.
func itemRowY(i int) int { return 2 + i }

func TestMenuBarOpenSkipsDisabledFirstItem(t *testing.T) {
	mb := newTestMenuBarWithDisabled(new(int))
	mb.Open()

	if mb.selectedItem != 1 {
		t.Fatalf("selectedItem = %d after Open(), want 1 (\"Middle\", skipping the disabled \"First\")", mb.selectedItem)
	}
}

func TestMenuBarKeyDownAndUpWrapSkippingDisabledItems(t *testing.T) {
	mb := newTestMenuBarWithDisabled(new(int))
	mb.Open() // selects "Middle" (index 1)

	mb.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if mb.selectedItem != 1 {
		t.Fatalf("selectedItem = %d after KeyDown, want 1 (\"Middle\" again — both neighbors are disabled)", mb.selectedItem)
	}

	mb.HandleKey(tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone))
	if mb.selectedItem != 1 {
		t.Fatalf("selectedItem = %d after KeyUp, want 1 (\"Middle\" again — both neighbors are disabled)", mb.selectedItem)
	}
}

func TestMenuBarKeyDownSkipsDisabledItemToNextEnabled(t *testing.T) {
	// A four-item menu (enabled/disabled interleaved) proves Up/Down walks
	// *over* a disabled item to the next enabled one, not merely wrapping
	// back to itself as the three-item fixture above would also show.
	mb := NewMenuBar()
	mb.SetBounds(0, 0, 40)
	mb.SetMenus([]Menu{
		{Label: "File", Items: []MenuItem{
			{Label: "A", Enabled: func() bool { return false }},
			{Label: "B"},
			{Label: "C", Enabled: func() bool { return false }},
			{Label: "D"},
		}},
	})
	mb.Open() // selects "B" (index 1)

	mb.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if mb.selectedItem != 3 {
		t.Fatalf("selectedItem = %d after KeyDown from \"B\", want 3 (\"D\", skipping disabled \"C\")", mb.selectedItem)
	}

	mb.HandleKey(tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone))
	if mb.selectedItem != 1 {
		t.Fatalf("selectedItem = %d after KeyUp from \"D\", want 1 (\"B\", skipping disabled \"C\")", mb.selectedItem)
	}
}

func TestMenuBarEnterOnDisabledItemDoesNotFireButCloses(t *testing.T) {
	var calls int
	mb := newTestMenuBarWithDisabled(&calls)
	mb.Open()
	mb.selectedItem = 0 // force onto the disabled "First", bypassing normal nav

	mb.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))

	if calls != 0 {
		t.Fatalf("Action fired for a disabled item via KeyEnter, want it not to")
	}
	if mb.IsOpen() {
		t.Fatalf("menu stayed open after KeyEnter on a disabled item; want it closed, same as any other item")
	}
}

func TestMenuBarClickOnDisabledItemDoesNotFireButCloses(t *testing.T) {
	var calls int
	mb := newTestMenuBarWithDisabled(&calls)
	mb.Open()

	// "First" (index 0) is the disabled item, drawn at itemRowY(0).
	mb.HandleMouse(tcell.NewEventMouse(2, itemRowY(0), tcell.Button1, tcell.ModNone))

	if calls != 0 {
		t.Fatalf("Action fired for a disabled item via mouse click, want it not to")
	}
	if mb.IsOpen() {
		t.Fatalf("menu stayed open after clicking a disabled item; want it closed, same as clicking a divider")
	}
}

func TestMenuBarHoverDoesNotSelectDisabledItem(t *testing.T) {
	mb := newTestMenuBarWithDisabled(new(int))
	mb.Open() // selects "Middle" (index 1)

	// Hover over "Last" (index 2), which is disabled.
	mb.HandleMouse(tcell.NewEventMouse(2, itemRowY(2), tcell.ButtonNone, tcell.ModNone))

	if mb.selectedItem != 1 {
		t.Fatalf("selectedItem = %d after hovering a disabled item, want 1 (unchanged, still \"Middle\")", mb.selectedItem)
	}
}

// TestNoteWhenKeepsTheNoteOffAnItemDisabledForAnotherReason. An item can be
// withheld for more than one reason at once, and Note describes exactly one of
// them. "Fail Over to This Replica..." is disabled on the primary because a
// replica cannot fail over to itself, and it read "needs ALTER ANY
// AVAILABILITY GROUP" — a right the login may well hold, sending them to ask
// for a permission that would change nothing.
func TestNoteWhenKeepsTheNoteOffAnItemDisabledForAnotherReason(t *testing.T) {
	// Disabled, and the note's own reason does not apply.
	item := MenuItem{
		Label:    "Fail Over to This Replica...",
		Shortcut: "",
		Note:     "needs ALTER ANY AVAILABILITY GROUP",
		Enabled:  func() bool { return false },
		NoteWhen: func() bool { return false },
	}
	if got := menuRowSuffix(item); got != "" {
		t.Errorf("suffix = %q, want nothing — the note is not why this item is grey", got)
	}

	// Same item, now grey for the reason the note does describe.
	item.NoteWhen = func() bool { return true }
	if got := menuRowSuffix(item); got != "needs ALTER ANY AVAILABILITY GROUP" {
		t.Errorf("suffix = %q, want the note", got)
	}

	// A nil NoteWhen is the unconditional form every other caller relies on.
	item.NoteWhen = nil
	if got := menuRowSuffix(item); got != "needs ALTER ANY AVAILABILITY GROUP" {
		t.Errorf("suffix = %q with no NoteWhen, want the note", got)
	}
}

// TestADisabledItemShowsItsNoteInsteadOfItsShortcut. A disabled item cannot be
// selected or clicked, so no keypress is left to answer with a status message —
// the note is the only place left to say why it is grey.
func TestADisabledItemShowsItsNoteInsteadOfItsShortcut(t *testing.T) {
	enabled := MenuItem{Label: "Back Up Database...", Shortcut: "Ctrl+B", Note: "needs BACKUP DATABASE"}
	if got := menuRowSuffix(enabled); got != "Ctrl+B" {
		t.Errorf("suffix = %q for an enabled item, want its shortcut", got)
	}

	disabled := enabled
	disabled.Enabled = func() bool { return false }
	if got := menuRowSuffix(disabled); got != "needs BACKUP DATABASE" {
		t.Errorf("suffix = %q for a disabled item, want the note", got)
	}

	// A cascade keeps its marker either way — it has a submenu, not a reason.
	cascade := MenuItem{Label: "New Index", Note: "needs ALTER", Sub: []MenuItem{{Label: "Clustered"}}}
	cascade.Enabled = func() bool { return false }
	if got := menuRowSuffix(cascade); got != "▸" {
		t.Errorf("cascade suffix = %q, want the submenu marker", got)
	}
}
