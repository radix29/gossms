package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
)

// ---------------------------------------------------------------------------
// Editor: grapheme clusters, measured as tcell measures them (review plan K1)
// ---------------------------------------------------------------------------
//
// These draw on tcell's own mock screen rather than glyphScreen, because the
// bug lived in the disagreement between the editor's widths and tcell's: a
// cluster tcell draws two wide, which the editor had counted as one column,
// had its second cell overwritten by the next character.

func mockScreen(t *testing.T, w, h int) tcell.Screen {
	t.Helper()
	s, err := tcell.NewTerminfoScreenFromTty(vt.NewMockTerm(vt.MockOptSize{X: vt.Col(w), Y: vt.Row(h)}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Fini)
	return s
}

// graphemeCases are clusters whose width is not the sum of their runes'.
var graphemeCases = []string{
	"❤️",    // heart + VS16: runes sum to 1, drawn 2
	"🇺🇸",    // regional indicators: runes sum to 4, drawn 2
	"👨‍👩‍👧", // ZWJ family: runes sum to 6, drawn 2
	"👍🏽",    // skin-tone modifier: runes sum to 4, drawn 2
}

func TestEditorDrawsAClusterAtTheWidthTcellGivesIt(t *testing.T) {
	for _, g := range graphemeCases {
		e := widthEditor("x"+g+"y", 20, 3)
		s := mockScreen(t, 20, 3)
		e.Draw(s)
		if str, _, w := s.Get(1, 0); str != g || w != 2 {
			t.Errorf("%q: cell 1 = %q (width %d), want the whole cluster, 2 wide", g, str, w)
		}
		if str, _, _ := s.Get(3, 0); str != "y" {
			t.Errorf("%q: cell 3 = %q, want \"y\" after the 2-wide cluster", g, str)
		}
	}
}

func TestEditorCursorStepsOverAClusterWhole(t *testing.T) {
	for _, g := range graphemeCases {
		n := len([]rune(g))
		e := widthEditor("x"+g+"y", 20, 3)
		e.SetActive(true)
		e.cursorCol = 1
		e.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
		if e.cursorCol != 1+n {
			t.Errorf("%q: Right from before it = %d, want %d", g, e.cursorCol, 1+n)
		}
		s := newGlyphScreen(20, 3)
		e.Draw(s)
		if !s.curSet || s.curX != 3 {
			t.Errorf("%q: caret after it at x=%d (shown %v), want 3", g, s.curX, s.curSet)
		}
		e.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
		if e.cursorCol != 1 {
			t.Errorf("%q: Left from after it = %d, want 1", g, e.cursorCol)
		}
		e.HandleKey(tcell.NewEventKey(tcell.KeyDelete, "", tcell.ModNone))
		if got := e.Text(); got != "xy" {
			t.Errorf("%q: Delete left %q, want \"xy\"", g, got)
		}
		e.SetText("x" + g + "y")
		e.cursorRow, e.cursorCol = 0, 1+n
		e.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, "", tcell.ModNone))
		if got := e.Text(); got != "xy" || e.cursorCol != 1 {
			t.Errorf("%q: Backspace left %q, cursor %d; want \"xy\", 1", g, got, e.cursorCol)
		}
	}
}

// TestEditorClickOnEitherHalfOfAClusterLandsBeforeIt, and the column after it
// on the next character.
func TestEditorClickOnEitherHalfOfAClusterLandsBeforeIt(t *testing.T) {
	g := "🇺🇸"
	for _, c := range []struct{ x, want int }{{1, 1}, {2, 1}, {3, 3}} {
		// A fresh editor per click: two quick clicks would be a double-click.
		e := widthEditor("x"+g+"y", 20, 3)
		e.HandleMouse(tcell.NewEventMouse(c.x, 0, tcell.Button1, tcell.ModNone))
		e.HandleMouse(tcell.NewEventMouse(c.x, 0, tcell.ButtonNone, tcell.ModNone))
		if e.cursorCol != c.want {
			t.Errorf("click at x=%d: cursor %d, want %d", c.x, e.cursorCol, c.want)
		}
	}
}

// TestWrapMeasuresClusters: "❤️" is two columns, though its runes sum to one,
// so at width 4 it cannot share a row with "abc" — rune widths fitted it there
// and the row overflowed. It moves to the next row whole.
func TestWrapMeasuresClusters(t *testing.T) {
	line := []rune("abc❤️")
	segs := wrapSegments(nil, line, 4)
	if len(segs) != 2 || segs[0] != (wrapSegment{0, 3}) || segs[1] != (wrapSegment{3, 5}) {
		t.Fatalf("segments = %v, want [{0 3} {3 5}]", segs)
	}
}
