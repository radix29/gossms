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

	found := false
	for x := 0; x < 60 && !found; x++ {
		pm.HandleMouse(tcell.NewEventMouse(x, 0, tcell.Button1, tcell.ModNone))
		if closed == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("OnCloseTab never fired for panel 1, which is closable")
	}
}

// fakePanelNoClosable is a Panel that doesn't implement Closable at all —
// used to check panelClosable's default (see TestPanelClosableDefaultsTrueWithoutInterface).
type fakePanelNoClosable struct{ title string }

func (p fakePanelNoClosable) SetBounds(x, y, w, h int)              {}
func (p fakePanelNoClosable) Draw(s tcell.Screen)                   {}
func (p fakePanelNoClosable) HandleKey(ev *tcell.EventKey) bool     { return false }
func (p fakePanelNoClosable) HandleMouse(ev *tcell.EventMouse) bool { return false }
func (p fakePanelNoClosable) Title() string                         { return p.title }

func TestPanelClosableDefaultsTrueWithoutInterface(t *testing.T) {
	if !panelClosable(fakePanelNoClosable{title: "x"}) {
		t.Fatalf("panelClosable = false for a panel not implementing Closable, want true (default)")
	}
	if panelClosable(&fakePanel{title: "x", closable: false}) {
		t.Fatalf("panelClosable = true for Closable() == false")
	}
	if !panelClosable(&fakePanel{title: "x", closable: true}) {
		t.Fatalf("panelClosable = false for Closable() == true")
	}
}
