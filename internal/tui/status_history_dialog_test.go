package tui

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// TestStatusHistoryRecordPrependsNewest pins down newest-first ordering, so
// clicking the status bar right after a message shows it without
// scrolling.
func TestStatusHistoryRecordPrependsNewest(t *testing.T) {
	d := &StatusHistoryDialog{}
	d.Record("first message")
	d.Record("second message")

	if len(d.lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(d.lines))
	}
	if !strings.Contains(d.lines[0], "second message") {
		t.Fatalf("lines[0] = %q, want the most recent (second message) first", d.lines[0])
	}
	if !strings.Contains(d.lines[1], "first message") {
		t.Fatalf("lines[1] = %q, want the older (first message) second", d.lines[1])
	}
}

// TestStatusHistoryRecordCapsAtMax pins down that the history doesn't grow
// without bound during a long session — only the last
// maxStatusHistoryLines messages are kept.
func TestStatusHistoryRecordCapsAtMax(t *testing.T) {
	d := &StatusHistoryDialog{}
	for i := 0; i < maxStatusHistoryLines+10; i++ {
		d.Record("message " + strconv.Itoa(i))
	}
	if len(d.lines) != maxStatusHistoryLines {
		t.Fatalf("len(lines) = %d, want %d (capped)", len(d.lines), maxStatusHistoryLines)
	}
	if !strings.Contains(d.lines[0], "message "+strconv.Itoa(maxStatusHistoryLines+9)) {
		t.Fatalf("lines[0] = %q, want the very last recorded message", d.lines[0])
	}
}

// TestStatusHistoryRecordIncludesTimestamp confirms each line begins with
// an HH:MM:SS timestamp of when the message was recorded, per the todo.
func TestStatusHistoryRecordIncludesTimestamp(t *testing.T) {
	d := &StatusHistoryDialog{}
	d.Record("hello")

	if len(d.lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(d.lines))
	}
	line := d.lines[0]
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
	d := &StatusHistoryDialog{}
	d.Record("hello")

	d.Show()

	if len(d.lines) != 1 {
		t.Fatalf("len(lines) after Show() = %d, want 1 (history must not reset on open)", len(d.lines))
	}
}

// TestStatusHistoryRecordDefersSyncWhileHidden pins down the dirty flag:
// Record must not rebuild the editor's text (the expensive part) while the
// dialog is hidden, only mark it dirty; Show() then catches it up before
// display.
func TestStatusHistoryRecordDefersSyncWhileHidden(t *testing.T) {
	d := &StatusHistoryDialog{}
	if d.Visible() {
		t.Fatal("zero-value dialog must start hidden")
	}

	d.Record("hidden message")
	if !d.dirty {
		t.Fatal("dirty must be true after Record() while hidden")
	}

	d.Show()
	if d.dirty {
		t.Fatal("dirty must be false after Show() rebuilds the editor text")
	}
}

// TestStatusHistoryDrawSyncsWhatWasRecordedWhileVisible confirms a message
// recorded while the dialog is open reaches the editor on the next frame —
// the rebuild moved out of Record (which may be on a background goroutine)
// into Draw, so a user watching the dialog must not have to close and
// reopen it to see new messages.
func TestStatusHistoryDrawSyncsWhatWasRecordedWhileVisible(t *testing.T) {
	d := &StatusHistoryDialog{}
	d.InitModal(nil, "Status History", 90, 26)
	d.editor = controls.NewEditor(nil)
	d.editor.SetReadOnly(true)
	d.ModalDialog.Show()

	d.Record("visible message")
	if !d.dirty {
		t.Fatal("dirty must still be set after Record(): the editor is the UI goroutine's to write")
	}

	d.Draw(&fakeSizedScreen{w: 100, h: 40})

	if d.dirty {
		t.Fatal("dirty must be false after Draw() rebuilt the editor text")
	}
	if !strings.Contains(d.editor.Text(), "visible message") {
		t.Fatalf("editor text = %q, want it to contain the message recorded while visible", d.editor.Text())
	}
}

// TestStatusHistoryRecordIsSafeFromBackgroundGoroutines pins the ring down as
// concurrency-safe: App.logStatus reaches Record from the Object Explorer's
// loader goroutine while the UI goroutine draws the dialog, which was an
// unsynchronized append to (and read of) d.lines. Fails under -race on the
// version before the mutex.
func TestStatusHistoryRecordIsSafeFromBackgroundGoroutines(t *testing.T) {
	d := &StatusHistoryDialog{}
	d.InitModal(nil, "Status History", 90, 26)
	d.editor = controls.NewEditor(nil)
	d.editor.SetReadOnly(true)
	d.ModalDialog.Show()

	screen := &fakeSizedScreen{w: 100, h: 40}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				d.Record("worker " + strconv.Itoa(w) + " message " + strconv.Itoa(i))
			}
		}(w)
	}
	for i := 0; i < 50; i++ {
		d.Draw(screen)
	}
	wg.Wait()
	d.Draw(screen)

	if got := d.lineCount(); got != maxStatusHistoryLines {
		t.Fatalf("lineCount() = %d, want %d (400 messages recorded, capped)", got, maxStatusHistoryLines)
	}
	if lines := strings.Count(d.editor.Text(), "\n") + 1; lines != maxStatusHistoryLines {
		t.Fatalf("editor holds %d lines, want %d — the final Draw must show every retained message", lines, maxStatusHistoryLines)
	}
}
