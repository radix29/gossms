package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
)

// TestPanelHosted confirms panelHosted distinguishes a panel that's still
// in a.panels from one that's been removed — the check connectForQueryPanel
// relies on to avoid leaking a connection that resolves after its panel was
// already closed (see connectForQueryPanel's postEvent callback).
func TestPanelHosted(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	i := a.panels.AddPanel(qp)

	if !a.panelHosted(qp) {
		t.Fatal("panelHosted(qp) = false while qp is still in a.panels, want true")
	}

	a.panels.RemovePanel(i)
	if a.panelHosted(qp) {
		t.Error("panelHosted(qp) = true after qp was removed from a.panels, want false")
	}
}

// closePanelAt must cancel an in-flight query — otherwise it keeps running
// server-side after the panel (and its results UI) is gone. The completion
// goroutine's own postEvent callback still checks panelHosted before doing
// any UI work, but the query itself should stop, not just its display.
func TestClosePanelAtCancelsInFlightQuery(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	i := a.panels.AddPanel(qp)

	cancelled := false
	qp.executing = true
	qp.cancel = func() { cancelled = true }

	a.closePanelAt(i)

	if !cancelled {
		t.Error("closePanelAt did not cancel the in-flight query")
	}
}

// Closing a panel with no query running must not panic on a nil qp.cancel.
func TestClosePanelAtNoQueryRunningDoesNotPanic(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	i := a.panels.AddPanel(qp)

	a.closePanelAt(i)
}

// Closing a panel kicks off the background memory reclaim, and the guard that
// coalesces a burst of closes into one sweep must re-arm afterwards —
// otherwise the very first close disables the reclaim for the rest of the
// session.
func TestClosePanelAtReclaimGuardRearms(t *testing.T) {
	a := newTestApp()
	i := a.panels.AddPanel(NewQueryPanel(a, "Query 1"))
	a.closePanelAt(i)

	deadline := time.Now().Add(5 * time.Second)
	for a.reclaiming.Load() {
		if time.Now().After(deadline) {
			t.Fatal("a.reclaiming still set 5s after closing a panel, want the guard re-armed")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestQueryActionsReportWhenThereIsNoQueryPanel drives every Query-menu action
// and toolbar button that acts on the editor with no query panel active.
//
// The Enabled predicates gate these ahead of the click, so this is the second
// line: an action reached anyway must say why nothing happened rather than
// doing nothing (docs/ui-rules.md on context-gating). Each of the
// seven carried its own copy of the check, which is the arrangement where one
// gets added without it.
func TestQueryActionsReportWhenThereIsNoQueryPanel(t *testing.T) {
	actions := map[string]func(*App){
		"Execute":                (*App).executeActiveQuery,
		"Execute Selection":      (*App).executeSelectedQuery,
		"Display Estimated Plan": (*App).showEstimatedExecutionPlan,
		"Cancel Executing Query": (*App).cancelExecutingQuery,
		"Reconnect":              (*App).reconnectActiveQuery,
		"Refresh IntelliSense":   (*App).refreshCompletionCache,
		"Results To Grid":        func(a *App) { a.setResultsMode(ResultsModeGrid) },
	}
	for name, act := range actions {
		t.Run(name, func(t *testing.T) {
			a := newTestApp() // no panels at all, so no active query panel
			act(a)
			if a.statusText != noActiveQueryPanelMessage {
				t.Errorf("status after %s with no query panel = %q, want %q",
					name, a.statusText, noActiveQueryPanelMessage)
			}
		})
	}
}

// TestWithQueryPanelRunsOnTheActivePanel is the other half: a guard that
// refused everything would pass the table above.
func TestWithQueryPanelRunsOnTheActivePanel(t *testing.T) {
	a := newTestApp()
	other := NewQueryPanel(a, "Query 1")
	active := NewQueryPanel(a, "Query 2")
	a.panels.AddPanel(other)
	// Act on a panel that is not the first one added: a helper that reached
	// for a.panels[0] would pass with only one.
	a.panels.SetActiveIndex(a.panels.AddPanel(active))

	var got *QueryPanel
	a.withQueryPanel(func(qp *QueryPanel) { got = qp })

	if got != active {
		t.Errorf("withQueryPanel ran on %v, want the active panel", got)
	}
	if a.statusText != "" {
		t.Errorf("status = %q, want nothing said when there is a panel to act on", a.statusText)
	}
}

// A query window opened from Object Explorer (New Query, Script As) drew an
// empty results grid, a lone "|" header over a separator, before anything had
// run. Until the first run the editor takes the whole panel; the first run
// attempt, even one refused with a notice, brings the results pane in.
func TestQueryWindowHasNoResultsPaneBeforeItsFirstRun(t *testing.T) {
	s, err := tcell.NewTerminfoScreenFromTty(vt.NewMockTerm(vt.MockOptSize{X: 80, Y: 24}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Fini)
	a := newTestApp()
	a.screen = s
	a.openQueryWithText(nil, "", "SELECT 1")
	qp := a.activeQueryPanel()
	if qp == nil {
		t.Fatal("openQueryWithText left no active query panel")
	}
	qp.SetBounds(0, 0, 80, 24)
	screen := func() string {
		s.Clear()
		qp.Draw(s)
		var b strings.Builder
		for y := range 24 {
			for x := range 80 {
				str, _, _ := s.Get(x, y)
				b.WriteString(str)
			}
			b.WriteByte('\n')
		}
		return b.String()
	}

	if got := qp.editor.Bounds().H; got != 23 {
		t.Errorf("editor height before any run = %d, want 23 (everything below the title bar)", got)
	}
	if text := screen(); strings.Contains(text, "Results") || strings.Contains(text, "──────────") {
		t.Fatalf("results pane drawn before any run:\n%s", text)
	}
	// Where the splitter bar would be: the click belongs to the editor.
	qp.editor.SetText(strings.Repeat("SELECT 1\n", 20))
	bar := qp.splitter.SplitPos()
	qp.HandleMouse(tcell.NewEventMouse(5, bar, tcell.Button1, tcell.ModNone))
	qp.HandleMouse(tcell.NewEventMouse(5, bar, tcell.ButtonNone, tcell.ModNone))
	if qp.resultsFocused {
		t.Error("a click on the hidden splitter's row focused the results")
	}
	if row, _ := qp.editor.CursorPos(); row == 0 {
		t.Errorf("click on row %d left the editor caret on row 0; the editor didn't get it", bar)
	}

	qp.editor.SetText("")
	qp.Execute()
	text := screen()
	if !strings.Contains(text, "Results") || !strings.Contains(text, "No query to execute") {
		t.Fatalf("after a refused run, want the results pane with its notice:\n%s", text)
	}
	if got := qp.editor.Bounds().H; got >= 23 {
		t.Errorf("editor height after a run = %d, want it to share the panel", got)
	}
}
