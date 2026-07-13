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
