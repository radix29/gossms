package tui

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
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

// newConnectDialogApp opens the Connect dialog over a focused query panel,
// with one saved connection for its autocomplete list to find.
func newConnectDialogApp(t *testing.T) (*App, *QueryPanel) {
	t.Helper()
	a := newClipboardTestApp()
	a.cfg.Connections = []config.Connection{{Server: "ubusql1", Port: 1433}}
	qp := focusedQueryPanel(t, a)
	a.allDialogs = []Dialog{a.connectDialog}
	a.connectDialog.Show()
	a.syncDialogStack()
	return a, qp
}

// A paste is aimed at one widget, and the clipboard read that feeds it is
// asynchronous — the native tool is shelled out to, the OSC 52 reply arrives
// as an event later still. If the dialog it was meant for has closed by then,
// the text must be dropped, not re-aimed at whatever is focused now: that is
// the same "silently edits the query editor behind the dialog" failure the
// ClipboardHost work removed from the read half.
func TestPasteIsDroppedWhenItsTargetIsNoLongerFocused(t *testing.T) {
	a, qp := newConnectDialogApp(t)
	target := a.activeClipboardTarget()
	token := clipboardTargetToken(a.topDialog())
	if target != clipboardTarget(a.connectDialog.fServer) {
		t.Fatalf("setup: target = %T, want the Connect dialog's server field", target)
	}
	before := qp.editor.Text()

	// The read comes back after the user has closed the dialog.
	a.connectDialog.Hide()
	a.syncDialogStack()
	a.pasteInto(target, token, "ubusql2")

	if got := a.connectDialog.fServer.Value(); got != "" {
		t.Errorf("server field = %q, want it untouched — the dialog was closed", got)
	}
	if got := qp.editor.Text(); got != before {
		t.Errorf("the paste landed in the query editor behind the dialog: %q", got)
	}
}

// The same paste applied while the dialog is still open goes in, and the
// dialog gets told so it can re-filter — the ClipboardEditHandler half.
func TestPasteIntoTheServerFieldRefiltersTheSavedConnections(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	a.pasteInto(a.activeClipboardTarget(), clipboardTargetToken(a.topDialog()), "ubus")

	if got := a.connectDialog.fServer.Value(); got != "ubus" {
		t.Fatalf("server field = %q, want %q", got, "ubus")
	}
	if !a.connectDialog.matchOpen {
		t.Error("the saved-connections list did not reopen after the paste; " +
			"ClipboardEdited never reached the dialog")
	}
}

// ...and a paste into any other field of that dialog must not re-run the
// lookup. It used to: every edit site checked connectDialog.Visible() and
// called updateMatches directly, so pasting a password popped the
// autocomplete list open over a server name nobody had touched.
func TestPasteIntoAnotherFieldLeavesTheSavedConnectionsAlone(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	d := a.connectDialog
	d.fServer.SetValue("ubus") // enough to match, but the list is closed
	d.setFocus(slices.Index(d.focusable, focusable(d.fPassword)))

	target := a.activeClipboardTarget()
	token := clipboardTargetToken(a.topDialog())
	if target != clipboardTarget(d.fPassword) {
		t.Fatalf("setup: target = %T, want the password field", target)
	}
	a.pasteInto(target, token, "hunter2")

	if d.fPassword.Value() != "hunter2" {
		t.Fatalf("password field = %q, want the pasted text", d.fPassword.Value())
	}
	if d.matchOpen {
		t.Error("pasting into the password field opened the server autocomplete list")
	}
}
