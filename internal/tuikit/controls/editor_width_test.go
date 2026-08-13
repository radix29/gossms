package controls

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Editor: wide (double-width) characters occupy two terminal columns
// ---------------------------------------------------------------------------
//
// Editor used to treat every rune as exactly one cell. A CJK ideograph or
// emoji renders in two, so everything after one on the same line drew a
// column left of where the editor believed it was, and the caret landed in
// the wrong place. Every test here fails against that model and passes
// against the column-aware one; each names the specific symptom.

// glyphScreen captures the runes a Draw painted, and where the caret was put,
// so a test can assert on the actual picture rather than only on styles.
type glyphScreen struct {
	tcell.Screen
	w, h   int
	runes  map[[2]int]rune
	curX   int
	curY   int
	curSet bool
}

func newGlyphScreen(w, h int) *glyphScreen {
	return &glyphScreen{w: w, h: h, runes: map[[2]int]rune{}}
}
func (s *glyphScreen) Size() (int, int) { return s.w, s.h }
func (s *glyphScreen) SetContent(x, y int, primary rune, comb []rune, style tcell.Style) {
	s.runes[[2]int{x, y}] = primary
}
func (s *glyphScreen) ShowCursor(x, y int) { s.curX, s.curY, s.curSet = x, y, true }

// row reads back a screen row as a string, from x for w columns. A wide
// rune's continuation cell is skipped rather than read: a real tcell screen
// fills it as part of drawing the pair, and this fake only records the cell
// the caller wrote to.
func (s *glyphScreen) row(x, y, w int) string {
	out := make([]rune, 0, w)
	for i := 0; i < w; {
		r, ok := s.runes[[2]int{x + i, y}]
		if !ok || r == 0 {
			r = ' '
		}
		out = append(out, r)
		i += max(1, core.RuneWidth(r))
	}
	return string(out)
}

// widthEditor returns an editor with no gutter and no highlighter, sized w×h
// at the origin, holding text.
func widthEditor(text string, w, h int) *Editor {
	e := NewEditor(nil)
	e.SetGutterVisible(false)
	e.SetText(text)
	e.SetBounds(0, 0, w, h)
	return e
}

// TestEditorDrawsWideRunesInTwoColumns is the base symptom: with a rune-per-
// column model the "OK" after two ideographs starts at column 2, over the
// second half of the second ideograph. It belongs at column 4.
func TestEditorDrawsWideRunesInTwoColumns(t *testing.T) {
	e := widthEditor("世界OK", 20, 3)
	s := newGlyphScreen(20, 3)
	e.Draw(s)

	if got := s.row(0, 0, 6); got != "世界OK" {
		t.Errorf("row 0 = %q, want %q — a wide rune must claim two columns", got, "世界OK")
	}
	if got := s.runes[[2]int{4, 0}]; got != 'O' {
		t.Errorf("column 4 holds %q, want 'O': two ideographs occupy columns 0-3", got)
	}
}

// TestEditorCaretFollowsDisplayColumns: the caret after n runes sits at the
// sum of their widths, not at n.
func TestEditorCaretFollowsDisplayColumns(t *testing.T) {
	e := widthEditor("世界OK", 20, 3)
	e.SetActive(true)
	e.cursorCol = 3 // after 世, 界 and 'O' — columns 0-1, 2-3, 4
	e.ensureCursorVisible()

	s := newGlyphScreen(20, 3)
	e.Draw(s)

	if !s.curSet {
		t.Fatal("Draw never showed a cursor")
	}
	if s.curX != 5 {
		t.Errorf("caret x = %d after 2 wide runes + 1 narrow, want 5 (rune-counting gives 3)", s.curX)
	}
}

// TestEditorClickMapsColumnsBackToRunes: clicking column 4 must select the
// rune that renders there, which is index 2 once two wide runes precede it.
// Reading the x offset as a rune index put the caret two runes early.
func TestEditorClickMapsColumnsBackToRunes(t *testing.T) {
	e := widthEditor("世界OK", 20, 3)

	e.HandleMouse(tcell.NewEventMouse(4, 0, tcell.Button1, tcell.ModNone))
	if e.cursorCol != 2 {
		t.Errorf("click at column 4 put the cursor at rune %d, want 2", e.cursorCol)
	}
	e.HandleMouse(tcell.NewEventMouse(4, 0, tcell.ButtonNone, tcell.ModNone))

	// Either half of a wide rune snaps to its start, never between its cells:
	// there is no text position there.
	e.HandleMouse(tcell.NewEventMouse(3, 0, tcell.Button1, tcell.ModNone))
	if e.cursorCol != 1 {
		t.Errorf("click on the right half of the second ideograph gave rune %d, want 1", e.cursorCol)
	}
}

// TestEditorVerticalMovementKeepsTheDisplayColumn: desiredCol is a display
// column, so moving down from past two ideographs lands under them on an
// ASCII line — column 4 — rather than at rune 2.
func TestEditorVerticalMovementKeepsTheDisplayColumn(t *testing.T) {
	e := widthEditor("世界OK\nabcdefgh", 20, 5)
	e.cursorRow, e.cursorCol = 0, 2 // display column 4
	e.desiredCol = e.cursorDisplayCol()
	if e.desiredCol != 4 {
		t.Fatalf("desiredCol = %d, want 4", e.desiredCol)
	}

	e.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if e.cursorRow != 1 {
		t.Fatalf("Down moved to row %d, want 1", e.cursorRow)
	}
	if e.cursorCol != 4 {
		t.Errorf("Down landed on rune %d of an ASCII line, want 4 (visually below the caret)", e.cursorCol)
	}
}

// TestEditorHorizontalScrollIsInColumns: with the widest line measured in
// runes, the horizontal scrollbar under-reports a CJK document by half and
// the view can't scroll far enough to reach the end of the line.
func TestEditorHorizontalScrollIsInColumns(t *testing.T) {
	e := widthEditor("世界世界世界", 20, 4)
	if got := e.doc.maxDisplayWidth(); got != 12 {
		t.Errorf("widest line = %d, want 12 columns (6 runes)", got)
	}

	// Scrolled right by an odd number of columns, the leading rune is split
	// by the left edge — its visible half must be blank, not a stray glyph
	// drawn over the cell tcell reserves for the pair.
	e.scrollCol = 1
	s := newGlyphScreen(20, 4)
	e.Draw(s)
	if got := s.runes[[2]int{0, 0}]; got != ' ' {
		t.Errorf("column 0 with a wide rune straddling the left edge = %q, want a blank", got)
	}
	if got := s.runes[[2]int{1, 0}]; got != '界' {
		t.Errorf("column 1 = %q, want the second ideograph to start there", got)
	}
}

// TestEditorClipsAWideRuneAtTheRightEdge: half a double-width glyph makes the
// terminal draw the whole character over the neighbouring cell, so a rune
// that doesn't fit is blanked instead.
func TestEditorClipsAWideRuneAtTheRightEdge(t *testing.T) {
	e := widthEditor("ab世", 3, 2) // 世 needs columns 2-3; only 0-2 exist
	s := newGlyphScreen(10, 2)
	e.Draw(s)

	if got := s.runes[[2]int{2, 0}]; got != ' ' {
		t.Errorf("column 2 = %q, want a blank — a wide rune must not be drawn half in", got)
	}
}

// TestEditorSelectionCoversBothCellsOfAWideRune: selection is resolved per
// rune and painted per column, so selecting one ideograph must highlight the
// pair of cells it renders in.
func TestEditorSelectionCoversBothCellsOfAWideRune(t *testing.T) {
	marker := tcell.StyleDefault.Foreground(color.Fuchsia).Underline(true)
	e := NewEditor(markerHighlighter(0, 1, marker))
	e.SetGutterVisible(false)
	e.SetText("世界")
	e.SetBounds(0, 0, 10, 2)

	s := newRecordingScreen(10, 2)
	e.Draw(s)

	// tcell styles the continuation cell itself, so assert on the cell the
	// editor explicitly wrote: the run covers rune 0, which starts at 0.
	if s.cells[[2]int{0, 0}] != marker {
		t.Error("the highlighter's run over rune 0 did not reach the column it renders at")
	}
	if s.cells[[2]int{2, 0}] == marker {
		t.Error("the run over rune 0 leaked onto rune 1's column — runs are rune-indexed, not column-indexed")
	}
}

// TestEditorWrapSegmentsByDisplayWidth: wrapping counted runes, so a wrapped
// row of CJK text was twice the content width and overflowed the editor.
func TestEditorWrapSegmentsByDisplayWidth(t *testing.T) {
	line := []rune("世界世界世界世界")
	segs := wrapSegments(nil, line, 6)
	for i, seg := range segs {
		if w := core.RunesWidth(line[seg.start:seg.end]); w > 6 {
			t.Errorf("segment %d spans %d columns, want at most 6", i, w)
		}
	}
	if len(segs) != 3 {
		t.Errorf("8 ideographs at 6 columns wrapped into %d segments, want 3 (2 runes = 4 cols each... )", len(segs))
	}
}

// TestWrapSegmentsTerminatesOnARuneWiderThanTheWidth guards the degenerate
// case the width-aware break introduced: a two-column rune in a one-column
// area fits nowhere, and a segmenter that only breaks where something fits
// emits a zero-length segment forever.
func TestWrapSegmentsTerminatesOnARuneWiderThanTheWidth(t *testing.T) {
	done := make(chan []wrapSegment, 1)
	go func() { done <- wrapSegments(nil, []rune("世界"), 1) }()
	select {
	case segs := <-done:
		if len(segs) != 2 {
			t.Errorf("segments = %v, want one per rune", segs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapSegments did not return — zero-length segment loop")
	}
}
