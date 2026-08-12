package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// A SQL Server restore failure, verbatim — the message wrapMessage exists for.
const restoreFailure = "The backup set holds a backup of a database other " +
	"than the existing 'AppDB' database. RESTORE DATABASE is terminating " +
	"abnormally."

// TestWrapMessageFitsWithinBudget pins the ordinary case: a message that fits
// is laid out over as many lines as it needs and no more, and no line is
// wider than the column budget.
func TestWrapMessageFitsWithinBudget(t *testing.T) {
	lines := wrapMessage(restoreFailure, 40, 6)
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want the message spread over several", len(lines))
	}
	if len(lines) > 6 {
		t.Fatalf("got %d lines, want at most 6", len(lines))
	}
	for i, ln := range lines {
		if w := core.DisplayWidth(ln); w > 40 {
			t.Errorf("line %d is %d columns wide, want at most 40: %q", i, w, ln)
		}
	}
	// Nothing was dropped: every word survives somewhere in the layout.
	joined := strings.Join(lines, " ")
	for _, word := range strings.Fields(restoreFailure) {
		if !strings.Contains(joined, word) {
			t.Errorf("word %q went missing from a layout with room to spare", word)
		}
	}
}

// TestWrapMessageClipsTheLastLine pins the budget-exhaustion branch: when the
// text needs more lines than it may have, the overflow is folded into the
// last line and clipped there. Truncating the *slice* instead would leave the
// clipped-away part unmarked, so a two-line status would read as a complete
// sentence that happens to stop early — the failure this whole helper exists
// to prevent.
func TestWrapMessageClipsTheLastLine(t *testing.T) {
	lines := wrapMessage(restoreFailure, 30, 2)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want exactly the 2 allowed", len(lines))
	}
	for i, ln := range lines {
		if w := core.DisplayWidth(ln); w > 30 {
			t.Errorf("line %d is %d columns wide, want at most 30: %q", i, w, ln)
		}
	}
	if !strings.HasSuffix(lines[1], "…") {
		t.Errorf("last line = %q, want it to end in the ellipsis that marks the clip", lines[1])
	}
	// The clipped line carries the overflow, not just the words that already
	// fit on line 2 of an unclipped layout.
	full := core.WrapText(restoreFailure, 30)
	if len(full) <= 2 {
		t.Fatalf("test text no longer overflows 2 lines of 30 columns (%d lines)", len(full))
	}
	if lines[1] == full[1] {
		t.Errorf("last line = %q, unchanged from the unclipped wrap — the tail was dropped, not folded in", lines[1])
	}
}

// A single line of budget is the Files view's status row (see drawStatus's
// maxLines=1), so the whole message has to collapse into one clipped line.
func TestWrapMessageWithOneLineOfBudget(t *testing.T) {
	lines := wrapMessage(restoreFailure, 30, 1)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if !strings.HasSuffix(lines[0], "…") {
		t.Errorf("line = %q, want it clipped with an ellipsis", lines[0])
	}
	if !strings.HasPrefix(lines[0], "The backup set") {
		t.Errorf("line = %q, want it to start at the beginning of the message", lines[0])
	}
}

// No room at all returns nothing rather than a line the caller would draw
// outside its own rectangle.
func TestWrapMessageWithNoRoom(t *testing.T) {
	for _, tc := range []struct{ w, maxLines int }{{0, 4}, {40, 0}, {-1, -1}} {
		if got := wrapMessage(restoreFailure, tc.w, tc.maxLines); got != nil {
			t.Errorf("wrapMessage(w=%d, maxLines=%d) = %q, want nil", tc.w, tc.maxLines, got)
		}
	}
}
