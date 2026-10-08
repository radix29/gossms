package core

import (
	"fmt"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/clipperhouse/displaywidth"

	"github.com/gdamore/tcell/v3"
)

func TestBlendColor(t *testing.T) {
	black := tcell.NewRGBColor(0, 0, 0)
	white := tcell.NewRGBColor(255, 255, 255)

	tests := []struct {
		name                string
		a, b                tcell.Color
		num, den            int
		wantR, wantG, wantB int32
	}{
		{"num 0 keeps a unchanged", white, black, 0, 5, 255, 255, 255},
		{"num == den becomes b", white, black, 5, 5, 0, 0, 0},
		{"3/5 toward black leaves 40%", white, black, 3, 5, 102, 102, 102},
		{"blends each channel independently", tcell.NewRGBColor(200, 100, 50), black, 1, 2, 100, 50, 25},
		{"blends toward a non-black target", black, white, 1, 4, 63, 63, 63},
	}
	for _, tt := range tests {
		r, g, b := BlendColor(tt.a, tt.b, tt.num, tt.den).RGB()
		if r != tt.wantR || g != tt.wantG || b != tt.wantB {
			t.Errorf("%s: got (%d,%d,%d), want (%d,%d,%d)", tt.name, r, g, b, tt.wantR, tt.wantG, tt.wantB)
		}
	}
}

// An unset/default colour has no RGB value to fade, so it must pass through
// untouched rather than being coerced to black.
func TestBlendColorLeavesUnsetColorAlone(t *testing.T) {
	var unset tcell.Color // zero value — not Valid()
	if got := BlendColor(unset, tcell.NewRGBColor(0, 0, 0), 3, 5); got != unset {
		t.Errorf("BlendColor(unset) = %v, want unchanged", got)
	}
}

func TestHandleScrollbarDrag(t *testing.T) {
	var dragging bool
	var scroll int

	// A Button1 press elsewhere in the widget (not on the bar's column)
	// must not start a drag.
	miss := tcell.NewEventMouse(5, 5, tcell.Button1, tcell.ModNone)
	if HandleScrollbarDrag(miss, 10, 0, 8, 100, &dragging, &scroll) {
		t.Fatal("press off the bar's column should not be handled")
	}
	if dragging {
		t.Fatal("dragging should still be false")
	}

	// A press on the bar's column, within the track's row range, jumps and
	// latches dragging.
	press := tcell.NewEventMouse(10, 4, tcell.Button1, tcell.ModNone)
	if !HandleScrollbarDrag(press, 10, 0, 8, 100, &dragging, &scroll) {
		t.Fatal("press on the bar should be handled")
	}
	if !dragging {
		t.Fatal("dragging should now be true")
	}
	if scroll == 0 {
		t.Error("scroll should have jumped to roughly the middle of the range")
	}

	// Once dragging, a Button1 event with x drifted off the bar's column
	// still keeps controlling scroll.
	drift := tcell.NewEventMouse(2, 7, tcell.Button1, tcell.ModNone)
	if !HandleScrollbarDrag(drift, 10, 0, 8, 100, &dragging, &scroll) {
		t.Fatal("a continued drag with x off-column should still be handled")
	}

	// Wheel/other buttons never start or continue a drag.
	dragging = false
	wheel := tcell.NewEventMouse(10, 4, tcell.WheelDown, tcell.ModNone)
	if HandleScrollbarDrag(wheel, 10, 0, 8, 100, &dragging, &scroll) {
		t.Fatal("a wheel event must never be treated as a scrollbar drag")
	}

	// Content that fits entirely (total <= visible) never engages.
	dragging = false
	if HandleScrollbarDrag(press, 10, 0, 8, 8, &dragging, &scroll) {
		t.Fatal("no scrollbar is shown when total <= visible, so it must not engage")
	}
}

func TestHandleScrollbarDragH(t *testing.T) {
	var dragging bool
	var scroll int

	miss := tcell.NewEventMouse(5, 5, tcell.Button1, tcell.ModNone)
	if HandleScrollbarDragH(miss, 0, 20, 8, 100, &dragging, &scroll) {
		t.Fatal("press off the bar's row should not be handled")
	}

	press := tcell.NewEventMouse(4, 20, tcell.Button1, tcell.ModNone)
	if !HandleScrollbarDragH(press, 0, 20, 8, 100, &dragging, &scroll) {
		t.Fatal("press on the horizontal bar should be handled")
	}
	if !dragging {
		t.Fatal("dragging should now be true")
	}

	// Once dragging, y drifted off the bar's row still keeps controlling
	// scroll.
	drift := tcell.NewEventMouse(7, 2, tcell.Button1, tcell.ModNone)
	if !HandleScrollbarDragH(drift, 0, 20, 8, 100, &dragging, &scroll) {
		t.Fatal("a continued drag with y off-row should still be handled")
	}
}

func TestScrollOffsetForDrag(t *testing.T) {
	tests := []struct {
		name                 string
		y, h, total, visible int
		want                 int
	}{
		{"top of track jumps to 0", 0, 10, 100, 10, 0},
		{"bottom of track jumps near the end", 9, 10, 100, 10, 90},
		{"midway lands proportionally", 5, 10, 100, 10, 50},
		{"y clamped below 0", -5, 10, 100, 10, 0},
		{"y clamped past track height", 50, 10, 100, 10, 90},
		{"content fits entirely: always 0", 3, 10, 8, 10, 0},
		{"zero track height: always 0", 3, 0, 100, 10, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScrollOffsetForDrag(tt.y, tt.h, tt.total, tt.visible); got != tt.want {
				t.Errorf("ScrollOffsetForDrag(%d,%d,%d,%d) = %d, want %d", tt.y, tt.h, tt.total, tt.visible, got, tt.want)
			}
		})
	}
}

// The thumb reaches both ends of the track (T64): at offset*h/total it stopped
// a row short of the bottom whenever the rounding of length and start
// disagreed. The drag's two ends agree with it.
func TestScrollbarThumbSpansTheWholeTrack(t *testing.T) {
	for _, c := range []struct{ h, total, visible int }{
		{10, 30, 10}, {10, 1000, 5}, {7, 13, 6}, {20, 21, 20}, {3, 100, 3},
	} {
		length, top := scrollThumb(c.h, c.total, c.visible, ScrollOffsetForDrag(0, c.h, c.total, c.visible))
		if top != 0 {
			t.Errorf("%+v: thumb after a drag to the first row starts at %d, want 0", c, top)
		}
		last := ScrollOffsetForDrag(c.h-1, c.h, c.total, c.visible)
		if last != c.total-c.visible {
			t.Errorf("%+v: a drag to the last row gives offset %d, want %d", c, last, c.total-c.visible)
		}
		if _, start := scrollThumb(c.h, c.total, c.visible, last); start+length != c.h {
			t.Errorf("%+v: thumb at the last offset ends at %d, want the track's end %d", c, start+length, c.h)
		}
	}
}

// TestRuneStringIsStringOfRune pins RuneString to string(r) across both
// tables, their edges and past them, and pins the tables to costing nothing:
// they are why a drawn cell no longer allocates.
func TestRuneStringIsStringOfRune(t *testing.T) {
	for _, r := range []rune{0, ' ', 'A', '~', 0x7f, 0x80, 'é', boxRunesFirst - 1, boxRunesFirst, '─', '│', '█', boxRunesLast, boxRunesLast + 1, '你', -1} {
		if got, want := RuneString(r), string(r); got != want {
			t.Errorf("RuneString(%U) = %q, want %q", r, got, want)
		}
	}
	if n := testing.AllocsPerRun(100, func() {
		_ = RuneString('x')
		_ = RuneString('─')
	}); n != 0 {
		t.Errorf("RuneString of a table rune allocated %v times, want 0", n)
	}
}

// putLog is a tcell.Screen fake recording each Put as "x,y:str".
type putLog struct {
	tcell.Screen
	puts []string
}

func (s *putLog) Put(x, y int, str string, _ tcell.Style) (string, int) {
	s.puts = append(s.puts, fmt.Sprintf("%d,%d:%s", x, y, str))
	return "", 1
}

// drawTextLineCases are cells chosen to land the cut on every kind of
// grapheme: wide, combining, a ZWJ sequence, a line break, a tab.
var drawTextLineCases = []string{
	"", "a", "abc", "abcdef", "日本語テキスト", "a日b本c", "éééé",
	"👨‍👩‍👧x👍🏽y", "line1\nline2", "a\r\nb\tc", "\t\t\t\t", "ab日", "abc日", "abcd日",
	"x́", "́abc", "🇺🇸🇺🇸🇺🇸",
}

// TestDrawTextLineMatchesTruncateLine pins DrawTextLine to the composition
// it replaces, Put for Put, at every width from 0 past each string's own.
func TestDrawTextLineMatchesTruncateLine(t *testing.T) {
	for _, text := range drawTextLineCases {
		for w := 0; w <= DisplayWidth(text)+3; w++ {
			want, got := &putLog{}, &putLog{}
			DrawTextClipped(want, 2, 1, w, tcell.StyleDefault, TruncateLine(text, w))
			DrawTextLine(got, 2, 1, w, tcell.StyleDefault, text)
			if !slices.Equal(got.puts, want.puts) {
				t.Errorf("DrawTextLine(%q, %d) = %v, want %v", text, w, got.puts, want.puts)
			}
		}
	}
}

func FuzzDrawTextLineMatchesTruncateLine(f *testing.F) {
	for _, text := range drawTextLineCases {
		f.Add(text, 5)
	}
	f.Fuzz(func(t *testing.T, text string, w int) {
		if w < -1 || w > 200 || !utf8.ValidString(text) {
			t.Skip()
		}
		// TruncateLine maps a break to a space and the draw re-segments its
		// output, so a mark or modifier after a break joins the space; it is
		// the one place the two differ (TestDrawTextLineKeepsAMarkAfterABreakApart).
		if graphemeCount(TruncateLine(text, 1<<30)) != graphemeCount(text) {
			t.Skip()
		}
		want, got := &putLog{}, &putLog{}
		DrawTextClipped(want, 0, 0, w, tcell.StyleDefault, TruncateLine(text, w))
		DrawTextLine(got, 0, 0, w, tcell.StyleDefault, text)
		if !slices.Equal(got.puts, want.puts) {
			t.Errorf("DrawTextLine(%q, %d) = %v, want %v", text, w, got.puts, want.puts)
		}
	})
}

func graphemeCount(s string) int {
	n := 0
	for g := displaywidth.StringGraphemes(s); g.Next(); {
		n++
	}
	return n
}

// TestDrawTextLineKeepsAMarkAfterABreakApart is where DrawTextLine and the
// TruncateLine composition differ, on purpose: a skin-tone modifier after a
// line break. Re-segmented after the break becomes a space, the modifier
// joined it and drew as one cell; DrawTextLine segments the cell once, so the
// space stays a space and the modifier draws as itself.
func TestDrawTextLineKeepsAMarkAfterABreakApart(t *testing.T) {
	s := &putLog{}
	DrawTextLine(s, 0, 0, 10, tcell.StyleDefault, "a\r🏽b")
	want := []string{"0,0:a", "1,0: ", "2,0:🏽", "4,0:b"}
	if !slices.Equal(s.puts, want) {
		t.Errorf("puts = %v, want %v", s.puts, want)
	}
}

func TestDrawTextLineDoesNotAllocate(t *testing.T) {
	text := "a long cell value\nwith a line break that is clipped"
	if n := testing.AllocsPerRun(100, func() {
		DrawTextLine(quietScreen{}, 0, 0, 12, tcell.StyleDefault, text)
	}); n != 0 {
		t.Errorf("DrawTextLine allocated %v times per call, want 0", n)
	}
}

// quietScreen is a tcell.Screen fake whose Put records nothing, so an
// allocation count is the drawing code's alone.
type quietScreen struct{ tcell.Screen }

func (quietScreen) Put(int, int, string, tcell.Style) (string, int) { return "", 1 }
