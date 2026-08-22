package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// keyDiagDialogForTest builds a shown KeyDiagnosticsDialog on a screen big
// enough for ModalDialog.recentre to produce a real Rect.
func keyDiagDialogForTest(t *testing.T) (*App, *KeyDiagnosticsDialog) {
	t.Helper()
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewKeyDiagnosticsDialog(a)
	d.Show()
	return a, d
}

// TestKeyDiagnosticsRecordKeyPrependsNewest pins down newest-first
// ordering, so pressing one key and glancing at the dialog shows the right
// line without scrolling.
func TestKeyDiagnosticsRecordKeyPrependsNewest(t *testing.T) {
	d := &KeyDiagnosticsDialog{}
	d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModShift))

	if len(d.lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(d.lines))
	}
	if !strings.Contains(d.lines[0], "Shift") {
		t.Fatalf("lines[0] = %q, want the most recent (Shift+Left) event first", d.lines[0])
	}
	if strings.Contains(d.lines[1], "Shift") {
		t.Fatalf("lines[1] = %q, want the older (plain Left) event second", d.lines[1])
	}
}

// TestKeyDiagnosticsCopiesTheWholeLog is the point of the feature: what the
// dialog shows is the text someone pastes into a bug report, so it has to be
// reachable as a core.ClipboardTarget. Before the log moved into a read-only
// editor there was no widget to hand back and Ctrl+C did nothing.
func TestKeyDiagnosticsCopiesTheWholeLog(t *testing.T) {
	_, d := keyDiagDialogForTest(t)
	d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModShift))
	d.RecordKey(tcell.NewEventKey(tcell.KeyRune, "q", tcell.ModCtrl))
	d.syncIfDirty()

	target := d.FocusedClipboardTarget()
	if target == nil {
		t.Fatal("FocusedClipboardTarget returned nil — Ctrl+C would do nothing")
	}
	target.SelectAll()
	if !target.HasSelection() {
		t.Fatal("SelectAll left no selection")
	}
	got := target.SelectedText()
	for _, want := range []string{"Shift+Left", "Ctrl+Q"} {
		if !strings.Contains(got, want) {
			t.Errorf("copied text %q does not contain %q", got, want)
		}
	}
}

// TestKeyDiagnosticsKeepsASelectionWhileMoreKeysArrive pins the one rule that
// makes this dialog different from StatusHistoryDialog: it records the very
// keys used to copy from it. Ctrl+A is itself logged, so a sync on the next
// frame would call Editor.SetText and drop the selection the user just made,
// and the Ctrl+C after it would copy nothing.
func TestKeyDiagnosticsKeepsASelectionWhileMoreKeysArrive(t *testing.T) {
	_, d := keyDiagDialogForTest(t)
	d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModShift))
	d.syncIfDirty()

	d.editor.SelectAll()
	before := d.editor.SelectedText()
	if before == "" {
		t.Fatal("nothing selected to begin with")
	}

	// Exactly what happens between the user's Ctrl+A and their Ctrl+C: the
	// Ctrl+A is recorded, then the app draws a frame.
	d.RecordKey(tcell.NewEventKey(tcell.KeyCtrlA, "", tcell.ModCtrl))
	d.syncIfDirty()

	if !d.editor.HasSelection() {
		t.Fatal("the selection was dropped by a sync — Ctrl+A then Ctrl+C copies nothing")
	}
	if got := d.editor.SelectedText(); got != before {
		t.Errorf("selected text changed from %q to %q", before, got)
	}
	if !d.dirty {
		t.Error("dirty was cleared without rebuilding; the deferred lines are lost")
	}
}

// TestKeyDiagnosticsSyncsOnceTheSelectionIsGone is the other half of the rule
// above: skipping the rebuild defers it, it does not abandon it.
func TestKeyDiagnosticsSyncsOnceTheSelectionIsGone(t *testing.T) {
	_, d := keyDiagDialogForTest(t)
	d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	d.syncIfDirty()
	d.editor.SelectAll()

	d.RecordKey(tcell.NewEventKey(tcell.KeyF5, "", tcell.ModNone))
	d.syncIfDirty() // skipped: a selection is live

	d.editor.ClearSelection()
	d.syncIfDirty()

	if d.dirty {
		t.Error("still dirty after the selection was cleared")
	}
	if !strings.Contains(d.editor.Text(), "F5") {
		t.Errorf("the line recorded during the selection never reached the editor: %q", d.editor.Text())
	}
}

// TestKeyDiagnosticsShowResetsTheLog pins the existing behaviour that each
// open starts a fresh diagnostic session, including through a selection left
// live from the previous showing — which syncIfDirty deliberately refuses to
// rebuild past, so Show cannot go through it.
func TestKeyDiagnosticsShowResetsTheLog(t *testing.T) {
	_, d := keyDiagDialogForTest(t)
	d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	d.syncIfDirty()
	d.editor.SelectAll()
	d.Hide()

	d.Show()
	if len(d.lines) != 0 {
		t.Errorf("len(lines) = %d after Show, want 0", len(d.lines))
	}
	if got := d.editor.Text(); got != "" {
		t.Errorf("editor still shows the previous session: %q", got)
	}
}

// TestKeyDiagnosticsRecordKeyCapsAtMax pins that a long session doesn't grow
// the log without bound, and that it's the oldest entries that go.
func TestKeyDiagnosticsRecordKeyCapsAtMax(t *testing.T) {
	_, d := keyDiagDialogForTest(t)
	d.RecordKey(tcell.NewEventKey(tcell.KeyF1, "", tcell.ModNone))
	for i := 0; i < maxKeyDiagLines; i++ {
		d.RecordKey(tcell.NewEventKey(tcell.KeyF2, "", tcell.ModNone))
	}
	if len(d.lines) != maxKeyDiagLines {
		t.Fatalf("len(lines) = %d, want %d (capped)", len(d.lines), maxKeyDiagLines)
	}
	for _, line := range d.lines {
		if strings.Contains(line, "F1") {
			t.Fatal("the oldest entry survived the cap; newest-first ordering or the trim is wrong")
		}
	}
}

// TestKeyDiagnosticsEscapeAndEnterStillClose guards the delegation added with
// the editor: it gets first refusal of every key, and a read-only Editor that
// started answering true to Escape would make this dialog unclosable from the
// keyboard.
func TestKeyDiagnosticsEscapeAndEnterStillClose(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tcell.Key
	}{{"Escape", tcell.KeyEscape}, {"Enter", tcell.KeyEnter}} {
		t.Run(tc.name, func(t *testing.T) {
			_, d := keyDiagDialogForTest(t)
			d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
			d.syncIfDirty()

			d.HandleKey(tcell.NewEventKey(tc.key, "", tcell.ModNone))
			if d.Visible() {
				t.Errorf("%s did not close the dialog", tc.name)
			}
		})
	}
}

// The Copy button selects the whole log, and a selection is what syncIfDirty
// refuses to rebuild past — so the selection has to go once the text has been
// read. Left standing it freezes the log: a read-only Editor ignores every
// plain rune key without clearing its selection, so the very keys this dialog
// exists to decode stop appearing until the user happens to press an arrow.
//
// The clipboard write itself is not exercised here (it shells out to the OS);
// what is pinned is the selection's lifetime and the recording that depends
// on it.
func TestKeyDiagnosticsKeepsRecordingAfterCopy(t *testing.T) {
	_, d := keyDiagDialogForTest(t)
	d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	d.syncIfDirty()

	d.copyAll()

	if d.editor.HasSelection() {
		t.Error("the Copy button left its select-all behind — the log stops updating")
	}
	d.RecordKey(tcell.NewEventKey(tcell.KeyF5, "", tcell.ModNone))
	d.syncIfDirty()
	if !strings.Contains(d.editor.Text(), "F5") {
		t.Errorf("a key pressed after Copy never reached the log: %q", d.editor.Text())
	}
}
