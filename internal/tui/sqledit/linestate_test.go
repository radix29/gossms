package sqledit

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/tuikit/controls/editor"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/tuikittest"
)

// LineState reads the cache a Draw with Highlighter filled, and an edit typed
// afterwards is reflected on the next ask: closing an open comment puts the
// lines below it back in code. (controls' LineStateAt tests cover the cache
// itself; this pins that Highlighter and LineState share it and stay current.)
func TestLineStateFollowsATypedEdit(t *testing.T) {
	e := editor.NewEditor(Highlighter(&theme.Default))
	e.SetBounds(0, 0, 60, 10)
	e.SetText("SELECT 1 /* open\nstill inside\nSELECT 2")
	e.Draw(tuikittest.NewScreen(60, 10))
	if got := LineState(e.Document(), 2); got.Mode != sqltext.ModeBlockComment {
		t.Fatalf("line 2 before the edit = %v, want inside the block comment", got)
	}

	setCursor(e, 0, len("SELECT 1 /* open"))
	e.HandleKey(tuikittest.Rune('*', tcell.ModNone))
	e.HandleKey(tuikittest.Rune('/', tcell.ModNone))
	e.Draw(tuikittest.NewScreen(60, 10))
	if got := LineState(e.Document(), 2); got.Mode != sqltext.ModeNormal {
		t.Errorf("line 2 after closing the comment = %v, want ModeNormal", got)
	}
}
