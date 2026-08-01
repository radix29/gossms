package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// newClipboardTestApp is newTestApp plus the dialogs activeClipboardTarget
// dereferences on its way to the query panel.
func newClipboardTestApp() *App {
	a := newTestApp()
	a.fileDialog = dialogs.NewFileDialog(nil)
	a.propDialog = NewPropDialog(a)
	a.connectDialog = NewConnectDialog(a)
	return a
}

// focusedQueryPanel builds a query panel whose execution left the results
// pane on the Messages tab (setResult with an error selects it — see
// TestMessagesErrorLinesColoredRed) and gives the panel focus, editor side.
func focusedQueryPanel(t *testing.T, a *App) *QueryPanel {
	t.Helper()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.setResult(newTestResult(1, true), false)
	if !qp.onMessagesTab() {
		t.Fatal("setup: expected the results pane to land on the Messages tab")
	}
	a.focus = "panels"
	a.syncActivePanelFocus()
	qp.setResultsFocused(false)
	return qp
}

// TestClipboardTargetIsEditorWhileEditorFocused pins the fix for "Ctrl+V
// does nothing in the query editor": the Messages/plan/text views share the
// results half of the panel, so whichever is showing must only claim the
// clipboard while that half actually holds focus. Before the fix any
// execution that left the pane on Messages redirected Paste to that
// read-only view, which silently dropped it.
func TestClipboardTargetIsEditorWhileEditorFocused(t *testing.T) {
	a := newClipboardTestApp()
	qp := focusedQueryPanel(t, a)

	switch got := a.activeClipboardTarget(); got {
	case clipboardTarget(qp.editor):
	case clipboardTarget(qp.messages):
		t.Fatal("activeClipboardTarget() = the read-only Messages view while the SQL editor holds focus, want the editor")
	default:
		t.Fatalf("activeClipboardTarget() = %T, want the SQL editor", got)
	}
	a.activeClipboardTarget().Paste("SELECT 1")
	if got := qp.editor.Text(); got != "SELECT 1" {
		t.Fatalf("editor text after paste = %q, want %q", got, "SELECT 1")
	}
}

// TestClipboardTargetIsMessagesWhileResultsFocused is the other half of the
// gate above: clicking into the results pane while it shows Messages must
// still make that view the Copy target.
func TestClipboardTargetIsMessagesWhileResultsFocused(t *testing.T) {
	a := newClipboardTestApp()
	qp := focusedQueryPanel(t, a)
	qp.setResultsFocused(true)

	if got := a.activeClipboardTarget(); got != clipboardTarget(qp.messages) {
		t.Fatalf("activeClipboardTarget() = %T, want the Messages view", got)
	}
}

// TestBracketedPasteAppliesAsOneEdit confirms keys arriving between the two
// EventPaste markers are buffered and applied through Paste rather than
// replayed as typing: a pasted newline must stay a newline. Replaying it as
// KeyEnter hands it to the open IntelliSense popup, which commits its
// selected candidate instead and rewrites the pasted text.
func TestBracketedPasteAppliesAsOneEdit(t *testing.T) {
	a := newClipboardTestApp()
	qp := focusedQueryPanel(t, a)

	a.beginBracketedPaste()
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, "a", tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, "b", tcell.ModNone),
		// A key a terminal shouldn't send mid-paste: dropped, not acted on.
		tcell.NewEventKey(tcell.KeyF5, "", tcell.ModNone),
	} {
		a.bufferPastedKey(ev)
	}
	a.endBracketedPaste()

	if got := qp.editor.Text(); got != "a\nb" {
		t.Fatalf("editor text after bracketed paste = %q, want %q", got, "a\nb")
	}
	if a.pasting {
		t.Fatal("still in paste mode after the end marker")
	}
	// One Paste call, so one undo step takes the whole paste back out.
	qp.editor.Undo()
	if got := qp.editor.Text(); got != "" {
		t.Fatalf("editor text after one Undo = %q, want it emptied by a single undo step", got)
	}
}
