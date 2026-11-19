package tui

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radix29/tuikit/tuikittest"
)

// TestStatusHistoryRecordPrependsNewest pins down newest-first ordering, so
// clicking the status bar right after a message shows it without
// scrolling.
func TestStatusHistoryRecordPrependsNewest(t *testing.T) {
	d := NewStatusHistoryDialog(nil)
	d.Record("first message")
	d.Record("second message")

	if len(d.Lines()) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(d.Lines()))
	}
	if !strings.Contains(d.Lines()[0], "second message") {
		t.Fatalf("lines[0] = %q, want the most recent (second message) first", d.Lines()[0])
	}
	if !strings.Contains(d.Lines()[1], "first message") {
		t.Fatalf("lines[1] = %q, want the older (first message) second", d.Lines()[1])
	}
}

// TestStatusHistoryRecordCapsAtMax pins down that the history doesn't grow
// without bound during a long session — only the last
// maxStatusHistoryLines messages are kept.
func TestStatusHistoryRecordCapsAtMax(t *testing.T) {
	d := NewStatusHistoryDialog(nil)
	for i := range maxStatusHistoryLines + 10 {
		d.Record("message " + strconv.Itoa(i))
	}
	if len(d.Lines()) != maxStatusHistoryLines {
		t.Fatalf("len(lines) = %d, want %d (capped)", len(d.Lines()), maxStatusHistoryLines)
	}
	if !strings.Contains(d.Lines()[0], "message "+strconv.Itoa(maxStatusHistoryLines+9)) {
		t.Fatalf("lines[0] = %q, want the very last recorded message", d.Lines()[0])
	}
}

// TestStatusHistoryRecordIncludesTimestamp confirms each line begins with
// an HH:MM:SS timestamp of when the message was recorded, per the todo.
func TestStatusHistoryRecordIncludesTimestamp(t *testing.T) {
	d := NewStatusHistoryDialog(nil)
	d.Record("hello")

	if len(d.Lines()) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(d.Lines()))
	}
	line := d.Lines()[0]
	if !strings.HasSuffix(line, "  hello") {
		t.Fatalf("line = %q, want it to end with %q", line, "  hello")
	}
	prefix := strings.TrimSuffix(line, "  hello")
	if _, err := time.Parse("15:04:05", prefix); err != nil {
		t.Fatalf("timestamp prefix %q did not parse as HH:MM:SS: %v", prefix, err)
	}
}

// TestStatusHistoryShowDoesNotResetLog pins down that Show() doesn't clear
// the log: unlike KeyDiagnosticsDialog, which deliberately clears its own
// on every open, the status history accumulates across the whole session
// and only resets on process restart.
func TestStatusHistoryShowDoesNotResetLog(t *testing.T) {
	d := NewStatusHistoryDialog(nil)
	d.Record("hello")

	d.Show()

	if len(d.Lines()) != 1 {
		t.Fatalf("len(lines) after Show() = %d, want 1 (history must not reset on open)", len(d.Lines()))
	}
}

// TestStatusHistoryRecordDefersSyncWhileHidden pins down the dirty flag:
// Record must not rebuild the editor's text (the expensive part) while the
// dialog is hidden, only mark it dirty; Show() then catches it up before
// display.
func TestStatusHistoryRecordDefersSyncWhileHidden(t *testing.T) {
	d := NewStatusHistoryDialog(nil)
	if d.Visible() {
		t.Fatal("zero-value dialog must start hidden")
	}

	d.Record("hidden message")
	if !d.Dirty() {
		t.Fatal("dirty must be true after Record() while hidden")
	}

	d.Show()
	if d.Dirty() {
		t.Fatal("dirty must be false after Show() rebuilds the editor text")
	}
}

// TestStatusHistoryDrawSyncsWhatWasRecordedWhileVisible confirms a message
// recorded while the dialog is open reaches the editor on the next frame —
// the rebuild moved out of Record (which may be on a background goroutine)
// into Draw, so a user watching the dialog must not have to close and
// reopen it to see new messages.
func TestStatusHistoryDrawSyncsWhatWasRecordedWhileVisible(t *testing.T) {
	d := NewStatusHistoryDialog(nil)
	d.ModalDialog.Show()

	d.Record("visible message")
	if !d.Dirty() {
		t.Fatal("dirty must still be set after Record(): the editor is the UI goroutine's to write")
	}

	d.Draw(tuikittest.NewScreen(100, 40))

	if d.Dirty() {
		t.Fatal("dirty must be false after Draw() rebuilt the editor text")
	}
	if !strings.Contains(d.Editor().Text(), "visible message") {
		t.Fatalf("editor text = %q, want it to contain the message recorded while visible", d.Editor().Text())
	}
}

// TestStatusHistoryRecordIsSafeFromBackgroundGoroutines pins the ring down as
// concurrency-safe: App.logStatus reaches Record from the Object Explorer's
// loader goroutine while the UI goroutine draws the dialog, which was an
// unsynchronized append to (and read of) the history. Fails under -race on
// the version before the mutex.
func TestStatusHistoryRecordIsSafeFromBackgroundGoroutines(t *testing.T) {
	d := NewStatusHistoryDialog(nil)
	d.ModalDialog.Show()

	screen := tuikittest.NewScreen(100, 40)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range 100 {
				d.Record("worker " + strconv.Itoa(w) + " message " + strconv.Itoa(i))
			}
		}(w)
	}
	for range 50 {
		d.Draw(screen)
	}
	wg.Wait()
	d.Draw(screen)

	if got := d.Len(); got != maxStatusHistoryLines {
		t.Fatalf("lineCount() = %d, want %d (400 messages recorded, capped)", got, maxStatusHistoryLines)
	}
	if lines := strings.Count(d.Editor().Text(), "\n") + 1; lines != maxStatusHistoryLines {
		t.Fatalf("editor holds %d lines, want %d — the final Draw must show every retained message", lines, maxStatusHistoryLines)
	}
}

// shownStatusHistoryForTest builds a shown StatusHistoryDialog holding n
// messages, drawn once so the editor has bounds.
func shownStatusHistoryForTest(t *testing.T, n int) (*StatusHistoryDialog, *tuikittest.Screen) {
	t.Helper()
	screen := tuikittest.NewScreen(100, 40)
	d := withScreen(NewStatusHistoryDialog(nil), screen)
	for i := range n {
		d.Record("message " + strconv.Itoa(i))
	}
	d.Show()
	d.Draw(screen)
	return d, screen
}

// TestStatusHistoryKeepsTheSelectionWhenAMessageArrives pins B1 of the
// 2026-11-18 review: a sync rebuilt the editor with SetText, so every message
// recorded while the dialog was open, its own "Copied to clipboard" included,
// dropped the selection and a second Ctrl+C copied nothing.
func TestStatusHistoryKeepsTheSelectionWhenAMessageArrives(t *testing.T) {
	d, screen := shownStatusHistoryForTest(t, 5)
	d.Editor().SelectRange(1, 0, 3, 0)
	before := d.Editor().SelectedText()
	if before == "" {
		t.Fatal("nothing selected to begin with")
	}
	row, _ := d.Editor().CursorPos()

	d.Record("Copied to clipboard")
	d.Draw(screen)

	if !d.Editor().HasSelection() {
		t.Fatal("a recorded message dropped the selection")
	}
	if got := d.Editor().SelectedText(); got != before {
		t.Errorf("selected text changed from %q to %q", before, got)
	}
	if got, _ := d.Editor().CursorPos(); got != row+1 {
		t.Errorf("cursor row = %d, want %d (moved down with its text)", got, row+1)
	}
	if !strings.Contains(strings.SplitN(d.Editor().Text(), "\n", 2)[0], "Copied to clipboard") {
		t.Errorf("the new message is not the first line: %q", d.Editor().Text())
	}
}

// TestStatusHistoryKeepsAScrolledViewWhenAMessageArrives is B1's other half: a
// background load recording a status snapped a user reading older messages
// back to line 1.
func TestStatusHistoryKeepsAScrolledViewWhenAMessageArrives(t *testing.T) {
	d, screen := shownStatusHistoryForTest(t, 100)
	d.Editor().GoToLine(60)
	d.Draw(screen)
	top, _ := d.Editor().ScrollPos()
	if top == 0 {
		t.Fatal("the view did not scroll to begin with")
	}

	d.Record("Loaded 12 tables")
	d.Draw(screen)

	if got, _ := d.Editor().ScrollPos(); got != top+1 {
		t.Errorf("scroll row = %d, want %d (the same text at the top of the view)", got, top+1)
	}
}

// TestStatusHistoryTrimsTheEditorAtMax pins the trim PrependLines takes over
// from the full rebuild: an open dialog never holds more than the history does.
func TestStatusHistoryTrimsTheEditorAtMax(t *testing.T) {
	d, screen := shownStatusHistoryForTest(t, maxStatusHistoryLines)
	d.Record("one more")
	d.Draw(screen)
	if got := strings.Count(d.Editor().Text(), "\n") + 1; got != maxStatusHistoryLines {
		t.Errorf("editor holds %d lines, want %d", got, maxStatusHistoryLines)
	}
}

// TestStatusHistoryCopiesTwiceInARow drives the real path: Ctrl+C through
// App.copySelection with the dialog on top records "Copied to clipboard", a
// frame is drawn, and the second Ctrl+C must copy the same text again.
// Before PrependLines the first copy worked and the second copied nothing.
func TestStatusHistoryCopiesTwiceInARow(t *testing.T) {
	a := newAppWithEveryDialog()
	screen := tuikittest.NewScreen(100, 40)
	a.setScreen(screen)
	copied := make(chan string, 2)
	old := clipboardWriter
	clipboardWriter = func(text string) bool { copied <- text; return true }
	defer func() { clipboardWriter = old }()

	d := a.statusHistoryDialog
	for i := range 5 {
		d.Record("message " + strconv.Itoa(i))
	}
	d.Show()
	// As Run's loop does between an event and its frame.
	frame := func() { a.modals.Sync(); a.modals.Draw(screen) }
	frame()
	d.Editor().SelectRange(1, 0, 3, 0)

	var got []string
	for range 2 {
		a.copySelection()
		select {
		case text := <-copied:
			got = append(got, text)
		case <-time.After(5 * time.Second):
			t.Fatalf("copy %d never reached the clipboard (selection live: %v)", len(got)+1, d.Editor().HasSelection())
		}
		frame()
	}
	if got[0] == "" || got[1] != got[0] {
		t.Errorf("copies = %q, want the same non-empty text twice", got)
	}
}
