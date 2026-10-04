package tui

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	a.focus = focusOnPanels
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
	if a.paste.bracketed {
		t.Fatal("still in paste mode after the end marker")
	}
	// One Paste call, so one undo step takes the whole paste back out.
	qp.editor.Undo()
	if got := qp.editor.Text(); got != "" {
		t.Fatalf("editor text after one Undo = %q, want it emptied by a single undo step", got)
	}
}

// TestBracketedPasteKeepsEveryLineBreakShape confirms a pasted line break
// survives whichever byte the terminal sends for it. tcell's legacy key mode
// decodes a raw LF as KeyCtrlJ, which the paste buffer used to drop, running
// `SELECT a⏎FROM t` together as `SELECT aFROM t` from Alacritty or tmux
// `paste-buffer -r`. The events are built from the raw bytes so they go
// through tcell's own normalization instead of naming KeyCtrlJ here.
func TestBracketedPasteKeepsEveryLineBreakShape(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"LF", "a\nb", "a\nb"},
		{"CR", "a\rb", "a\nb"},
		{"CRLF", "a\r\nb", "a\nb"},
		{"CR CR", "a\r\rb", "a\n\nb"},
		{"LF LF", "a\n\nb", "a\n\nb"},
		{"LF CR", "a\n\rb", "a\n\nb"},
		{"CRLF CRLF", "a\r\n\r\nb", "a\n\nb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newClipboardTestApp()
			qp := focusedQueryPanel(t, a)
			a.beginBracketedPaste()
			for _, r := range tc.in {
				a.bufferPastedKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
			}
			a.endBracketedPaste()
			if got := qp.editor.Text(); got != tc.want {
				t.Fatalf("editor text after pasting %q = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A CR ending one paste must not swallow an LF opening the next.
func TestBracketedPasteCRDoesNotCarryOver(t *testing.T) {
	a := newClipboardTestApp()
	qp := focusedQueryPanel(t, a)
	for _, in := range []string{"a\r", "\nb"} {
		a.beginBracketedPaste()
		for _, r := range in {
			a.bufferPastedKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
		}
		a.endBracketedPaste()
	}
	if got, want := qp.editor.Text(), "a\n\nb"; got != want {
		t.Fatalf("editor text after two pastes = %q, want %q", got, want)
	}
}

// newConnectDialogApp opens the Connect dialog over a focused query panel,
// with no saved connections — the dialog opens on the most recent one when
// there is one, and these tests want an untouched form.
func newConnectDialogApp(t *testing.T) (*App, *QueryPanel) {
	t.Helper()
	a := newClipboardTestApp()
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

// The same paste applied while the dialog is still open goes in — to the
// field that was focused when the read started, and to no other. It used to
// be able to reach another: every edit site checked connectDialog.Visible()
// and re-ran the server lookup directly, so pasting a password popped the
// autocomplete list open over a server name nobody had touched. That list is
// gone (the History pane replaced it), but the targeting rule it exposed is
// what this pins.
func TestPasteLandsOnlyInTheFieldItWasAimedAt(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	a.pasteInto(a.activeClipboardTarget(), clipboardTargetToken(a.topDialog()), "ubus")

	d := a.connectDialog
	if got := d.fServer.Value(); got != "ubus" {
		t.Fatalf("server field = %q, want %q", got, "ubus")
	}

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
	if got := d.fServer.Value(); got != "ubus" {
		t.Errorf("server field = %q — the password paste reached it too", got)
	}
}

// Quick copies reach the clipboard one at a time, and the last one wins (T71):
// with a goroutine each, a copy whose tool ran slow finished after the next
// one and left the older text on the clipboard. One superseded while it
// waited for the lock is not written at all.
func TestQuickCopiesLeaveTheLastOnTheClipboard(t *testing.T) {
	a := newTestApp()
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var writes []string
	done := make(chan struct{})
	old := clipboardWriter
	clipboardWriter = func(text string) bool {
		if text == "A" {
			close(entered)
			<-release
		}
		mu.Lock()
		writes = append(writes, text)
		mu.Unlock()
		if text == "C" {
			close(done)
		}
		return true
	}
	defer func() { clipboardWriter = old }()

	a.writeClipboard("A")
	<-entered // A's tool is running
	a.writeClipboard("B")
	a.writeClipboard("C")
	time.Sleep(20 * time.Millisecond) // let B and C reach the tool, if they can
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("C never reached the clipboard")
	}
	time.Sleep(20 * time.Millisecond) // and B, if it is going to

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"A", "C"}; !slices.Equal(writes, want) {
		t.Errorf("clipboard writes = %q, want %q: in order, the superseded B skipped", writes, want)
	}
}

// TestBracketedPasteDrawsOnceAtTheEnd pins B15: a frame per pasted key made a
// 10,001-line, 170 KB paste take ~6 minutes. The start marker may draw, the
// buffered keys must not, and the end marker draws the result — a constant
// number of frames whatever the paste's length.
func TestBracketedPasteDrawsOnceAtTheEnd(t *testing.T) {
	a := newClipboardTestApp()
	qp := focusedQueryPanel(t, a)

	frames := 0
	feed := func(ev tcell.Event) {
		t.Helper()
		quit, need := a.handleEvent(ev)
		if quit {
			t.Fatalf("%T quit the event loop", ev)
		}
		if need != skipFrame {
			frames++
		}
	}
	const lines = 1000
	feed(tcell.NewEventPaste(true))
	for range lines {
		feed(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone))
		feed(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	}
	feed(tcell.NewEventPaste(false))

	if frames > 2 {
		t.Errorf("paste of %d keys drew %d frames, want at most 2 (start and end markers)", 2*lines, frames)
	}
	if got, want := qp.editor.Text(), strings.Repeat("x\n", lines); got != want {
		t.Errorf("editor holds %d bytes after the paste, want %d", len(got), len(want))
	}

	// A lost end marker must not freeze the screen: anything but a key
	// still draws.
	feed(tcell.NewEventPaste(true))
	before := frames
	feed(tcell.NewEventInterrupt(nil))
	if frames != before+1 {
		t.Error("a background result arriving mid-paste drew no frame")
	}
}
