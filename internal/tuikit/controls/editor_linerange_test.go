package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// selectLinesByKeys selects whole lines the way a user does: cursor at column 0
// of from, then Shift+Down (or Shift+Up) until it reaches column 0 of to. The
// selection therefore ends at column 0 of the row after the last selected line
// (forwards) or starts there (backwards, anchor below).
func selectLinesByKeys(t *testing.T, e *Editor, from, to int) {
	t.Helper()
	e.cursorRow, e.cursorCol = from, 0
	e.selecting = false
	k := tcell.KeyDown
	if to < from {
		k = tcell.KeyUp
	}
	for e.cursorRow != to {
		e.HandleKey(tuikittest.Key(k, tcell.ModShift))
	}
	if e.cursorCol != 0 || !e.HasSelection() {
		t.Fatalf("setup: cursor (%d,%d) selection %v, want column 0 with a selection", e.cursorRow, e.cursorCol, e.HasSelection())
	}
}

type pos struct{ row, col int }

// TestLineActionsExcludeRowAColumnZeroSelectionEndsOn pins review X4: a
// selection made with Shift+Down from a line start ends at column 0 of the next
// row, and every line action must leave that row alone — it used to be
// commented, indented, duplicated, moved and even deleted with the rest.
// Each case runs forwards (anchor above) and backwards (anchor below).
func TestLineActionsExcludeRowAColumnZeroSelectionEndsOn(t *testing.T) {
	type want struct {
		text           string
		sel            bool // selection still active
		anchor, cursor pos  // checked when sel
		cursorOnly     pos  // checked when !sel
		selected       string
	}
	cases := []struct {
		name     string
		text     string
		top, bot int // selected lines are top..bot-1; the selection's far end is column 0 of bot
		act      func(*Editor)
		fwd, bwd want
	}{
		{
			name: "toggle comment", text: "a\nb\nc\nd", top: 0, bot: 2,
			act: (*Editor).ToggleLineComments,
			fwd: want{text: "-- a\n-- b\nc\nd", sel: true, anchor: pos{0, 3}, cursor: pos{2, 0}, selected: "a\n-- b\n"},
			bwd: want{text: "-- a\n-- b\nc\nd", sel: true, anchor: pos{2, 0}, cursor: pos{0, 3}, selected: "a\n-- b\n"},
		},
		{
			name: "indent", text: "a\nb\nc\nd", top: 0, bot: 2,
			act: (*Editor).IndentLines,
			fwd: want{text: "    a\n    b\nc\nd", sel: true, anchor: pos{0, 4}, cursor: pos{2, 0}, selected: "a\n    b\n"},
			bwd: want{text: "    a\n    b\nc\nd", sel: true, anchor: pos{2, 0}, cursor: pos{0, 4}, selected: "a\n    b\n"},
		},
		{
			name: "dedent", text: "    a\n    b\n    c\nd", top: 0, bot: 2,
			act: (*Editor).DedentLines,
			fwd: want{text: "a\nb\n    c\nd", sel: true, anchor: pos{0, 0}, cursor: pos{2, 0}, selected: "a\nb\n"},
			bwd: want{text: "a\nb\n    c\nd", sel: true, anchor: pos{2, 0}, cursor: pos{0, 0}, selected: "a\nb\n"},
		},
		{
			name: "duplicate", text: "a\nb\nc\nd", top: 0, bot: 2,
			act: (*Editor).DuplicateLines,
			fwd: want{text: "a\nb\na\nb\nc\nd", cursorOnly: pos{3, 0}},
			bwd: want{text: "a\nb\na\nb\nc\nd", cursorOnly: pos{2, 0}},
		},
		{
			name: "delete", text: "a\nb\nc\nd", top: 0, bot: 2,
			act: (*Editor).DeleteLines,
			fwd: want{text: "c\nd", cursorOnly: pos{0, 0}},
			bwd: want{text: "c\nd", cursorOnly: pos{0, 0}},
		},
		{
			name: "move up", text: "a\nb\nc\nd", top: 1, bot: 3,
			act: (*Editor).MoveLinesUp,
			fwd: want{text: "b\nc\na\nd", sel: true, anchor: pos{0, 0}, cursor: pos{2, 0}, selected: "b\nc\n"},
			bwd: want{text: "b\nc\na\nd", sel: true, anchor: pos{2, 0}, cursor: pos{0, 0}, selected: "b\nc\n"},
		},
		{
			name: "move down", text: "a\nb\nc\nd", top: 0, bot: 2,
			act: (*Editor).MoveLinesDown,
			fwd: want{text: "c\na\nb\nd", sel: true, anchor: pos{1, 0}, cursor: pos{3, 0}, selected: "a\nb\n"},
			bwd: want{text: "c\na\nb\nd", sel: true, anchor: pos{3, 0}, cursor: pos{1, 0}, selected: "a\nb\n"},
		},
		{
			// The block reaches the bottom, so the end that sat on the excluded row
			// would land past the buffer; it is pinned to the block's last line end.
			name: "move down to bottom", text: "a\nb\nc", top: 0, bot: 2,
			act: (*Editor).MoveLinesDown,
			fwd: want{text: "c\na\nb", sel: true, anchor: pos{1, 0}, cursor: pos{2, 1}, selected: "a\nb"},
			bwd: want{text: "c\na\nb", sel: true, anchor: pos{2, 1}, cursor: pos{1, 0}, selected: "a\nb"},
		},
	}
	for _, tc := range cases {
		for _, dir := range []string{"forwards", "backwards"} {
			t.Run(tc.name+"/"+dir, func(t *testing.T) {
				e := newTestEditor(tc.text)
				w := tc.fwd
				if dir == "forwards" {
					selectLinesByKeys(t, e, tc.top, tc.bot)
				} else {
					w = tc.bwd
					selectLinesByKeys(t, e, tc.bot, tc.top)
				}
				tc.act(e)
				if got := e.Text(); got != w.text {
					t.Fatalf("text = %q, want %q", got, w.text)
				}
				if e.HasSelection() != w.sel {
					t.Fatalf("HasSelection() = %v, want %v", e.HasSelection(), w.sel)
				}
				cur := pos{e.cursorRow, e.cursorCol}
				if !w.sel {
					if cur != w.cursorOnly {
						t.Fatalf("cursor = %v, want %v", cur, w.cursorOnly)
					}
					return
				}
				if anc := (pos{e.selAnchorRow, e.selAnchorCol}); anc != w.anchor || cur != w.cursor {
					t.Fatalf("anchor %v cursor %v, want anchor %v cursor %v", anc, cur, w.anchor, w.cursor)
				}
				if got := e.SelectedText(); got != w.selected {
					t.Fatalf("SelectedText() = %q, want %q", got, w.selected)
				}
			})
		}
	}
}

// TestLineActionsKeepRowTheSelectionReaches is the other side of X4: a
// selection that reaches past column 0 of its last row, a single-row selection
// ending at column 0, and a block selection all still include that row.
func TestLineActionsKeepRowTheSelectionReaches(t *testing.T) {
	t.Run("past column 0", func(t *testing.T) {
		e := newTestEditor("a\nb\nc\nd")
		selectLinesByKeys(t, e, 0, 2)
		e.HandleKey(tuikittest.Key(tcell.KeyRight, tcell.ModShift))
		e.ToggleLineComments()
		if got, want := e.Text(), "-- a\n-- b\n-- c\nd"; got != want {
			t.Fatalf("text = %q, want %q", got, want)
		}
	})
	t.Run("single row", func(t *testing.T) {
		e := newTestEditor("abc\nd")
		e.cursorCol = 2
		e.HandleKey(tuikittest.Key(tcell.KeyLeft, tcell.ModShift))
		e.HandleKey(tuikittest.Key(tcell.KeyLeft, tcell.ModShift))
		e.IndentLines()
		if got, want := e.Text(), "    abc\nd"; got != want {
			t.Fatalf("text = %q, want %q", got, want)
		}
	})
	t.Run("block", func(t *testing.T) {
		e := newTestEditor("a\nb\nc\nd")
		e.selecting, e.selBlock = true, true
		e.selAnchorRow, e.selAnchorCol = 0, 1
		e.cursorRow, e.cursorCol = 2, 0
		e.IndentLines()
		if got, want := e.Text(), "    a\n    b\n    c\nd"; got != want {
			t.Fatalf("text = %q, want %q", got, want)
		}
	})
}
