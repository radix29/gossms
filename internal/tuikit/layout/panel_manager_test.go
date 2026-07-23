package layout

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// fakePanel is a minimal Panel (+ optionally Closable) for PanelManager
// tests that don't need real rendering or input handling.
type fakePanel struct {
	title    string
	closable bool
}

func (p *fakePanel) SetBounds(x, y, w, h int)              {}
func (p *fakePanel) Draw(s tcell.Screen)                   {}
func (p *fakePanel) HandleKey(ev *tcell.EventKey) bool     { return false }
func (p *fakePanel) HandleMouse(ev *tcell.EventMouse) bool { return false }
func (p *fakePanel) Title() string                         { return p.title }
func (p *fakePanel) Closable() bool                        { return p.closable }

// TestPanelManagerNonClosablePanelNeverFiresOnCloseTab confirms a panel
// that implements Closable and returns false (e.g. Object Explorer
// Details) can never trigger OnCloseTab via a tab-bar click — Draw skips
// drawing its [x] entirely (closeW == 0), and HandleMouse's hit-testing
// walk must skip the close-button check the same way, or a click landing
// where the glyph would have been for a closable panel could still close
// this one.
func TestPanelManagerNonClosablePanelNeverFiresOnCloseTab(t *testing.T) {
	pm := NewPanelManager()
	pm.AddPanel(&fakePanel{title: "Object Explorer Details", closable: false})
	pm.AddPanel(&fakePanel{title: "Query 1", closable: true})
	pm.SetBounds(0, 0, 80, 24)

	closed := -1
	pm.OnCloseTab = func(i int) { closed = i }

	for x := 0; x < 40; x++ {
		pm.HandleMouse(tcell.NewEventMouse(x, 0, tcell.ButtonNone, tcell.ModNone))
		pm.HandleMouse(tcell.NewEventMouse(x, 0, tcell.Button1, tcell.ModNone))
	}
	if closed == 0 {
		t.Fatalf("OnCloseTab fired for panel 0, which is non-closable")
	}
}

// TestPanelManagerClosablePanelStillClosesViaClick is the mirror check:
// a panel that doesn't implement Closable (the common case, e.g. a
// QueryPanel) must still close normally by clicking its [x].
func TestPanelManagerClosablePanelStillClosesViaClick(t *testing.T) {
	pm := NewPanelManager()
	pm.AddPanel(&fakePanel{title: "Object Explorer Details", closable: false})
	pm.AddPanel(&fakePanel{title: "Query 1", closable: true})
	pm.SetBounds(0, 0, 80, 24)

	closed := -1
	pm.OnCloseTab = func(i int) { closed = i }

	// Each x is a separate fresh press (release first) — mouseDragging
	// would otherwise treat this sweep as one held drag and swallow every
	// position after the first, since it has no bearing on cursor motion
	// speed/distance, only on whether a release was seen since the last
	// press.
	found := false
	for x := 0; x < 60 && !found; x++ {
		pm.HandleMouse(tcell.NewEventMouse(x, 0, tcell.ButtonNone, tcell.ModNone))
		pm.HandleMouse(tcell.NewEventMouse(x, 0, tcell.Button1, tcell.ModNone))
		if closed == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("OnCloseTab never fired for panel 1, which is closable")
	}
}

// TestPanelManagerHeldButtonOnCloseDoesNotReFire pins the fix for tcell's
// all-motion mouse tracking resending Buttons()==Button1 on every cursor
// motion while the button stays down: a single physical click on a tab's
// [x] that so much as twitches used to fire OnCloseTab twice, which could
// stack duplicate "save this Dirty panel?" prompts for one click.
func TestPanelManagerHeldButtonOnCloseDoesNotReFire(t *testing.T) {
	pm := NewPanelManager()
	pm.AddPanel(&fakePanel{title: "Object Explorer Details", closable: false})
	pm.AddPanel(&fakePanel{title: "Query 1", closable: true})
	pm.SetBounds(0, 0, 80, 24)

	closeCount := 0
	pm.OnCloseTab = func(i int) {
		if i == 1 {
			closeCount++
		}
	}

	closeX := -1
	for x := 0; x < 60 && closeX < 0; x++ {
		pm.HandleMouse(tcell.NewEventMouse(x, 0, tcell.ButtonNone, tcell.ModNone))
		pm.HandleMouse(tcell.NewEventMouse(x, 0, tcell.Button1, tcell.ModNone))
		if closeCount == 1 {
			closeX = x
		}
	}
	if closeX < 0 {
		t.Fatal("could not locate panel 1's close button to set up the test")
	}

	// A resent Button1 at the same spot, with no release in between, must
	// not fire OnCloseTab again.
	pm.HandleMouse(tcell.NewEventMouse(closeX, 0, tcell.Button1, tcell.ModNone))
	if closeCount != 1 {
		t.Fatalf("closeCount after resent Button1 (no release) = %d, want 1 (still the same physical press)", closeCount)
	}
}

// fakePanelNoClosable is a Panel that doesn't implement Closable at all —
// used to check PanelClosable's default (see TestPanelClosableDefaultsTrueWithoutInterface).
type fakePanelNoClosable struct{ title string }

func (p fakePanelNoClosable) SetBounds(x, y, w, h int)              {}
func (p fakePanelNoClosable) Draw(s tcell.Screen)                   {}
func (p fakePanelNoClosable) HandleKey(ev *tcell.EventKey) bool     { return false }
func (p fakePanelNoClosable) HandleMouse(ev *tcell.EventMouse) bool { return false }
func (p fakePanelNoClosable) Title() string                         { return p.title }

func TestPanelClosableDefaultsTrueWithoutInterface(t *testing.T) {
	if !PanelClosable(fakePanelNoClosable{title: "x"}) {
		t.Fatalf("PanelClosable = false for a panel not implementing Closable, want true (default)")
	}
	if PanelClosable(&fakePanel{title: "x", closable: false}) {
		t.Fatalf("PanelClosable = true for Closable() == false")
	}
	if !PanelClosable(&fakePanel{title: "x", closable: true}) {
		t.Fatalf("PanelClosable = false for Closable() == true")
	}
}

// activatablePanel is a fakePanel that also implements Activatable, so
// RemovePanel tests can assert exactly which panel(s) got a SetActive call.
type activatablePanel struct {
	fakePanel
	active bool
}

func (p *activatablePanel) SetActive(v bool) { p.active = v }

// TestPanelManagerRemovePanelKeepsSamePanelActiveWhenRemovingBeforeIt
// removes a panel to the left of the active one — the active panel's index
// must shift down by one so the same panel stays active. Before the fix,
// pm.active kept its old numeric value and silently ended up pointing at
// whichever panel now occupies that slot instead.
func TestPanelManagerRemovePanelKeepsSamePanelActiveWhenRemovingBeforeIt(t *testing.T) {
	pm := NewPanelManager()
	a := &activatablePanel{fakePanel: fakePanel{title: "A"}}
	b := &activatablePanel{fakePanel: fakePanel{title: "B"}}
	c := &activatablePanel{fakePanel: fakePanel{title: "C"}}
	d := &activatablePanel{fakePanel: fakePanel{title: "D"}}
	pm.AddPanel(a)
	pm.AddPanel(b)
	pm.AddPanel(c)
	pm.AddPanel(d)
	pm.SetActive(2) // C
	c.active = true

	pm.RemovePanel(0) // remove A, to the left of active

	if got := pm.ActivePanel(); got != Panel(c) {
		t.Fatalf("ActivePanel() = %v, want C to remain active", got)
	}
	if pm.ActiveIndex() != 1 {
		t.Fatalf("ActiveIndex() = %d, want 1 (C shifted down after A's removal)", pm.ActiveIndex())
	}
	if d.active {
		t.Fatalf("SetActive(true) fired on D, which was never made active")
	}
}

// TestPanelManagerRemovePanelActivatesNeighborWhenActiveIsRemoved covers
// removing the active panel itself: a neighbor must become active and get
// a SetActive(true) call.
func TestPanelManagerRemovePanelActivatesNeighborWhenActiveIsRemoved(t *testing.T) {
	pm := NewPanelManager()
	a := &activatablePanel{fakePanel: fakePanel{title: "A"}}
	b := &activatablePanel{fakePanel: fakePanel{title: "B"}}
	c := &activatablePanel{fakePanel: fakePanel{title: "C"}}
	pm.AddPanel(a)
	pm.AddPanel(b)
	pm.AddPanel(c)
	pm.SetActive(1) // B
	b.active = true

	pm.RemovePanel(1) // remove B, the active panel

	if got := pm.ActivePanel(); got != Panel(c) {
		t.Fatalf("ActivePanel() = %v, want C (index 1 after removal)", got)
	}
	if !c.active {
		t.Fatalf("SetActive(true) never fired on C after it became active")
	}
}

// TestPanelManagerRemovePanelAfterActiveDoesNotChangeActive removes a
// panel to the right of the active one, which shouldn't move the active
// index at all.
func TestPanelManagerRemovePanelAfterActiveDoesNotChangeActive(t *testing.T) {
	pm := NewPanelManager()
	a := &activatablePanel{fakePanel: fakePanel{title: "A"}}
	b := &activatablePanel{fakePanel: fakePanel{title: "B"}}
	c := &activatablePanel{fakePanel: fakePanel{title: "C"}}
	pm.AddPanel(a)
	pm.AddPanel(b)
	pm.AddPanel(c)
	pm.SetActive(1) // B
	b.active = true

	pm.RemovePanel(2) // remove C, to the right of active

	if got := pm.ActivePanel(); got != Panel(b) {
		t.Fatalf("ActivePanel() = %v, want B to remain active", got)
	}
	if pm.ActiveIndex() != 1 {
		t.Fatalf("ActiveIndex() = %d, want 1 (unchanged)", pm.ActiveIndex())
	}
}
