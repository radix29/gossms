package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// The query editor toggles wrap at runtime (Alt+Z), usually far from line 1.
// SetWrapMode resets scrollRow because it means different things in the two
// modes, but must then scroll back to the cursor rather than leave it off-screen.
func TestSetWrapModeKeepsCursorVisible(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "select 1"
	}
	e := newTestEditor(strings.Join(lines, "\n"))
	e.SetBounds(0, 0, 40, 5)
	e.cursorRow, e.cursorCol = 50, 3
	e.ensureCursorVisible()

	for _, on := range []bool{true, false} {
		e.SetWrapMode(on)
		if e.cursorRow != 50 || e.cursorCol != 3 {
			t.Fatalf("wrap=%v: cursor = (%d,%d), want (50,3) kept", on, e.cursorRow, e.cursorCol)
		}
		if _, y := e.cursorScreenPos(); y < 0 || y >= 5 {
			t.Fatalf("wrap=%v: cursor screen row %d, want inside the 5-row editor", on, y)
		}
	}
}

// In wrap mode a wrapped line above the cursor pushes it down a screen row per
// extra segment, and x restarts at each segment. The completion popup and the
// Ctrl+Space menu anchor on cursorScreenPos, so the unwrapped arithmetic put
// both one row too high and past the right edge.
func TestCursorScreenPosWrapMode(t *testing.T) {
	// Content width 10 once the gutter is hidden: line 0 wraps as
	// "aaaa bbbb " / "cccc", so line 1 is on screen row 2.
	e := newWrappedTestEditor("aaaa bbbb cccc\nselect x", 10, 6)
	e.cursorRow, e.cursorCol = 1, 7

	if x, y := e.cursorScreenPos(); x != 7 || y != 2 {
		t.Fatalf("cursorScreenPos() = (%d,%d), want (7,2)", x, y)
	}

	// On the continuation row of line 0, x counts from the segment's start.
	e.cursorRow, e.cursorCol = 0, 12
	if x, y := e.cursorScreenPos(); x != 2 || y != 1 {
		t.Fatalf("cursorScreenPos() on a continuation row = (%d,%d), want (2,1)", x, y)
	}
}

func TestCompletionRectFollowsWrappedCursorRow(t *testing.T) {
	e := newWrappedTestEditor("aaaa bbbb cccc\nselect x", 10, 6)
	e.cursorRow, e.cursorCol = 1, 8
	e.completionOpen, e.completionFrom = true, 7
	e.completionItems = []CompletionItem{{Label: "xyz", Text: "xyz"}}

	r := e.completionRect()
	if r.Y != 3 {
		t.Fatalf("completionRect().Y = %d, want 3 (the row under line 1's visual row 2)", r.Y)
	}
}

// SetCursorFromScreen places the caret for an Object Explorer drop. In wrap
// mode the screen row is a visual row, not a logical line.
func TestSetCursorFromScreenWrapMode(t *testing.T) {
	e := newWrappedTestEditor("aaaa bbbb cccc\nselect x", 10, 6)

	e.SetCursorFromScreen(3, 2)
	if e.cursorRow != 1 || e.cursorCol != 3 {
		t.Fatalf("drop on row 2 col 3: cursor = (%d,%d), want (1,3)", e.cursorRow, e.cursorCol)
	}
	e.SetCursorFromScreen(1, 1)
	if e.cursorRow != 0 || e.cursorCol != 11 {
		t.Fatalf("drop on continuation row: cursor = (%d,%d), want (0,11)", e.cursorRow, e.cursorCol)
	}
}

// runsInSpan must keep overlap exactly and order intact — styleAt's later
// run wins, so a reordered subset would recolour overlapping runs.
func TestRunsInSpan(t *testing.T) {
	runs := []ColorRun{{Start: 0, Len: 3}, {Start: 3, Len: 4}, {Start: 5, Len: 10}, {Start: 20, Len: 2}}
	got := runsInSpan(nil, runs, 3, 10)
	if len(got) != 2 || got[0].Start != 3 || got[1].Start != 5 {
		t.Fatalf("runsInSpan(3,10) = %+v, want the runs starting at 3 and 5, in order", got)
	}
	if got := runsInSpan(nil, runs, 15, 20); len(got) != 0 {
		t.Fatalf("runsInSpan(15,20) = %+v, want none (15 and 20 are both exclusive edges)", got)
	}
}

// With word wrap on, Down/Up walk visual rows: a wrapped line's continuation
// rows are stops of their own, not skipped on the way to the next newline.
func TestWrapModeUpDownMoveByVisualRow(t *testing.T) {
	// Width 10: line 0 is "aaaa bbbb " / "cccc dddd " / "ee", line 1 "xyz".
	e := newWrappedTestEditor("aaaa bbbb cccc dddd ee\nxyz", 10, 6)
	e.cursorRow, e.cursorCol = 0, 2

	steps := []struct {
		k        tcell.Key
		row, col int
	}{
		{tcell.KeyDown, 0, 12}, // second visual row of line 0, same x
		{tcell.KeyDown, 0, 22}, // "ee": x 2 is its end
		{tcell.KeyDown, 1, 2},  // into line 1
		{tcell.KeyDown, 1, 2},  // last row: stays put
		{tcell.KeyUp, 0, 22},
		{tcell.KeyUp, 0, 12},
		{tcell.KeyUp, 0, 2},
		{tcell.KeyUp, 0, 2}, // first row: stays put
	}
	for i, st := range steps {
		e.HandleKey(key(st.k, tcell.ModNone))
		if e.cursorRow != st.row || e.cursorCol != st.col {
			t.Fatalf("step %d (%v): cursor = (%d,%d), want (%d,%d)", i, st.k, e.cursorRow, e.cursorCol, st.row, st.col)
		}
	}
}

// The goal column survives a short row, like desiredCol outside wrap mode:
// Down from x 8 across "ee" (clamped to x 2) lands back on x 8 below it. A
// non-final row never takes the caret at its end index, which belongs to the
// next row and would draw the caret one row too far.
func TestWrapModeVerticalGoalColumnIsSticky(t *testing.T) {
	e := newWrappedTestEditor("aaaa bbbb cccc dddd ee\nlonger_line_here", 10, 6)
	e.cursorRow, e.cursorCol = 0, 8

	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	if e.cursorRow != 0 || e.cursorCol != 22 {
		t.Fatalf("on the short row: cursor = (%d,%d), want (0,22)", e.cursorRow, e.cursorCol)
	}
	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	if e.cursorRow != 1 || e.cursorCol != 8 {
		t.Fatalf("past the short row: cursor = (%d,%d), want (1,8) — goal column lost", e.cursorRow, e.cursorCol)
	}

	// Any other move resets the goal: Right on line 1, then Up aims for
	// that new x.
	e.cursorRow, e.cursorCol = 1, 0
	e.HandleKey(key(tcell.KeyRight, tcell.ModNone))
	e.HandleKey(key(tcell.KeyUp, tcell.ModNone))
	if e.cursorRow != 0 || e.cursorCol != 21 {
		t.Fatalf("Up after Right: cursor = (%d,%d), want (0,21) at x 1 of \"ee\"", e.cursorRow, e.cursorCol)
	}

	// "aaaa bbbb " is 10 wide; goal x 10 (the end of a full-width line) on it
	// clamps to index 9, not 10.
	e.SetText("aaaa bbbb cccc\nxxxxxxxxxx")
	e.cursorRow, e.cursorCol = 1, 10
	e.HandleKey(key(tcell.KeyUp, tcell.ModNone)) // onto "cccc", clamps to its end
	e.HandleKey(key(tcell.KeyUp, tcell.ModNone)) // onto "aaaa bbbb ", non-final
	if e.cursorRow != 0 || e.cursorCol != 9 {
		t.Fatalf("on a non-final row: cursor = (%d,%d), want (0,9)", e.cursorRow, e.cursorCol)
	}
}

// PgDn/PgUp page by visual rows, and Shift+Down extends the selection one
// visual row rather than a whole wrapped line.
func TestWrapModePageAndShiftDownAreVisual(t *testing.T) {
	e := newWrappedTestEditor("aaaa bbbb cccc dddd eeee ffff\nnext", 10, 2)
	e.HandleKey(key(tcell.KeyPgDn, tcell.ModNone))
	if e.cursorRow != 0 || e.cursorCol != 20 {
		t.Fatalf("PgDn: cursor = (%d,%d), want (0,20), two visual rows down", e.cursorRow, e.cursorCol)
	}
	e.HandleKey(key(tcell.KeyPgUp, tcell.ModNone))
	if e.cursorRow != 0 || e.cursorCol != 0 {
		t.Fatalf("PgUp: cursor = (%d,%d), want (0,0)", e.cursorRow, e.cursorCol)
	}

	e.HandleKey(key(tcell.KeyDown, tcell.ModShift))
	if got := e.SelectedText(); got != "aaaa bbbb " {
		t.Fatalf("Shift+Down selected %q, want the first visual row", got)
	}
}
