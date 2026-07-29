package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// dirtyPanel adds a query panel with unsaved editor content.
func dirtyPanel(a *App, title, text string) *QueryPanel {
	qp := NewQueryPanel(a, title)
	qp.editor.SetText(text)
	a.panels.AddPanel(qp)
	return qp
}

func esc() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone) }

// answer drives the open confirm dialog by tabbing to a button and pressing
// Enter — 0 Yes, 1 No, 2 Cancel.
func answer(a *App, button int) {
	for range button {
		a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	}
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
}

// With nothing unsaved there's nothing to ask about, so Ctrl+Q still quits
// on the spot.
func TestRequestQuitWithNothingDirtyQuitsImmediately(t *testing.T) {
	a := newTestApp()
	NewQueryPanel(a, "Query 1") // never added, never dirtied

	if quitting := a.requestQuit(); !quitting {
		t.Error("requestQuit reported false with no dirty panels")
	}
	if !a.quitting {
		t.Error("app did not quit with no dirty panels")
	}
	if a.confirmDialog.Visible() {
		t.Error("prompted about nothing")
	}
}

// The bug: Ctrl+Q used to call quit() straight through, discarding every
// unsaved panel with no prompt at all, even though closing one panel has
// always asked.
func TestRequestQuitPromptsBeforeDiscardingUnsavedWork(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")

	if quitting := a.requestQuit(); quitting {
		t.Error("requestQuit reported true while a prompt is still pending")
	}
	if a.quitting {
		t.Fatal("app quit without asking about an unsaved query panel")
	}
	if !a.confirmDialog.Visible() {
		t.Fatal("no prompt shown for an unsaved query panel")
	}
}

func TestRequestQuitCancelledLeavesEverythingOpen(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")
	a.requestQuit()

	answer(a, 2) // Cancel

	if a.quitting {
		t.Error("app quit even though the prompt was cancelled")
	}
	if a.panels.Count() != 1 {
		t.Errorf("panel count after Cancel = %d, want 1 — Cancel closes nothing", a.panels.Count())
	}
	if a.confirmDialog.Visible() {
		t.Error("prompt still up after Cancel")
	}
}

// Escape is the reflex for dismissing a dialog, and it must not be read as
// "discard my work" — that's why Close Query and this prompt are three-way.
func TestRequestQuitEscapeIsCancelNotDiscard(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")
	a.requestQuit()

	a.confirmDialog.HandleKey(esc())

	if a.quitting {
		t.Error("Escape on the exit prompt quit and discarded the unsaved query")
	}
}

// Each dirty panel gets its own prompt, and the app only quits once the
// last one has been answered.
func TestRequestQuitAsksAboutEveryDirtyPanel(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")
	dirtyPanel(a, "Query 2", "SELECT 2")
	dirtyPanel(a, "Query 3", "SELECT 3")

	a.requestQuit()
	for i := range 3 {
		if !a.confirmDialog.Visible() {
			t.Fatalf("no prompt for dirty panel %d", i+1)
		}
		if a.quitting {
			t.Fatalf("quit after only %d of 3 prompts", i)
		}
		answer(a, 1) // No — don't save, but keep going
	}

	if !a.quitting {
		t.Error("app did not quit after every prompt was answered")
	}
}

// A clean panel between two dirty ones must not be asked about.
func TestDirtyQueryPanelsSkipsSavedAndNonQueryPanels(t *testing.T) {
	a := newTestApp()
	d1 := dirtyPanel(a, "Query 1", "SELECT 1")
	clean := dirtyPanel(a, "Query 2", "SELECT 2")
	clean.savedText = clean.editor.Text() // as if it had just been saved
	d2 := dirtyPanel(a, "Query 3", "SELECT 3")
	a.panels.AddPanel(NewDetailBrowser("Object Explorer Details"))

	got := a.dirtyQueryPanels()
	if len(got) != 2 || got[0] != d1 || got[1] != d2 {
		t.Fatalf("dirtyQueryPanels() = %v, want exactly the two dirty query panels", got)
	}
}

// Ctrl+W's own prompt has the same shape and the same hazard: "No" here
// discards the panel, so Escape must cancel the close instead.
func TestCloseQueryEscapeKeepsThePanel(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")

	a.requestClosePanel(0)
	if !a.confirmDialog.Visible() {
		t.Fatal("no prompt shown when closing a dirty panel")
	}
	a.confirmDialog.HandleKey(esc())

	if a.panels.Count() != 1 {
		t.Error("Escape on the close prompt discarded the unsaved query")
	}
}

func TestCloseQueryNoStillDiscards(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")

	a.requestClosePanel(0)
	answer(a, 1) // No — don't save

	if a.panels.Count() != 0 {
		t.Error("answering No left the panel open")
	}
}

func TestCloseQueryCancelKeepsThePanel(t *testing.T) {
	a := newTestApp()
	dirtyPanel(a, "Query 1", "SELECT 1")

	a.requestClosePanel(0)
	answer(a, 2) // Cancel

	if a.panels.Count() != 1 {
		t.Error("Cancel closed the panel anyway")
	}
}
