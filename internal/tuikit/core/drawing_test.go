package core

import (
	"testing"

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
