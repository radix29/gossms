package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

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

// TestKeyDiagnosticsRecordKeyCapsAtMax pins down that the log doesn't grow
// without bound during a long diagnostic session.
func TestKeyDiagnosticsRecordKeyCapsAtMax(t *testing.T) {
	d := &KeyDiagnosticsDialog{}
	for i := 0; i < maxKeyDiagLines+10; i++ {
		d.RecordKey(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone))
	}
	if len(d.lines) != maxKeyDiagLines {
		t.Fatalf("len(lines) = %d, want %d (capped)", len(d.lines), maxKeyDiagLines)
	}
}

// TestKeyDiagnosticsShowResetsLog confirms each Show() starts a fresh
// diagnostic session rather than accumulating across unrelated opens.
func TestKeyDiagnosticsShowResetsLog(t *testing.T) {
	d := &KeyDiagnosticsDialog{}
	d.RecordKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	d.scroll = 3

	d.Show()

	if len(d.lines) != 0 {
		t.Fatalf("len(lines) after Show() = %d, want 0", len(d.lines))
	}
	if d.scroll != 0 {
		t.Fatalf("scroll after Show() = %d, want 0", d.scroll)
	}
}
