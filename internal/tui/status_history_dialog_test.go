package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStatusHistoryRecordPrependsNewest is a regression test: the most
// recently recorded message must appear first, so a user who clicks the
// status bar right after seeing a message sees it without scrolling.
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

// TestStatusHistoryRecordCapsAtMax is a regression test: the history must
// not grow without bound during a long session — only the last
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

// TestStatusHistoryShowDoesNotResetLog is a regression test: unlike
// KeyDiagnosticsDialog, which deliberately clears its log on every open,
// the status history must accumulate across the whole session and only
// reset on process restart. A future edit that copies
// KeyDiagnosticsDialog's reset-on-Show pattern would silently break that.
func TestStatusHistoryShowDoesNotResetLog(t *testing.T) {
	d := &StatusHistoryDialog{}
	d.Record("hello")

	d.Show()

	if len(d.lines) != 1 {
		t.Fatalf("len(lines) after Show() = %d, want 1 (history must not reset on open)", len(d.lines))
	}
}

// TestStatusHistoryRecordDefersSyncWhileHidden is a regression test for the
// dirty-flag optimization: Record must not rebuild the editor's text (the
// expensive part) while the dialog is hidden, only mark it dirty; Show()
// must then catch it up before display.
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

// TestStatusHistoryRecordSyncsImmediatelyWhileVisible confirms Record still
// updates live (dirty clears right away, no waiting for the next Show())
// when the dialog is already open — messages recorded while a user has the
// dialog up must not require closing and reopening it to appear.
func TestStatusHistoryRecordSyncsImmediatelyWhileVisible(t *testing.T) {
	d := &StatusHistoryDialog{}
	d.ModalDialog.Show()

	d.Record("visible message")
	if d.dirty {
		t.Fatal("dirty must be false immediately after Record() while the dialog is visible")
	}
}
