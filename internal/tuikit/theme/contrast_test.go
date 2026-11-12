package theme

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// minTextContrast is WCAG AA's floor for body text.
const minTextContrast = 4.5

// paletteFloor lowers the floor for a palette that reproduces a terminal's own
// colours. MC is mc in the Tango palette, where nothing dark is black: Tango's
// black on its green (mc's found-match pair) is 3.58:1 and on its cyan 3.59:1,
// so 4.5 is out of reach for any faithful mc skin there. The floor is still
// that pair, so a style reading worse than mc itself does — white on the cyan
// status bar, 3.52:1 — fails.
var paletteFloor = map[string]float64{
	"MC": contrastRatio(tangoColors.Black, tangoColors.Green),
}

// contrastExempt names the styles whose foreground is not text to be read, so
// the floor does not apply: disabled controls (WCAG exempts inactive UI),
// frame lines and the chart's dot grid, which recede on purpose.
var contrastExempt = map[string]bool{
	"StyleBorder":          true,
	"StyleActiveBorder":    true,
	"StyleDisabled":        true,
	"StyleControlDisabled": true,
	"StyleDialogLine":      true,
	"StyleMenuLine":        true,
	"StyleScrollThumb":     true,
	"StyleButtonDisabled":  true,
	"StyleChartGrid":       true,
	"StyleTooltipBorder":   true,
}

// knownLowContrast is palette/style pairs measured below the floor when the
// pair got a named helper (the colours were already drawn inline). Recolouring
// a palette is a visible change of its own, tracked in docs/open-threads.md
// § Theme: what is left. An entry that starts passing fails the test, so the
// list only shrinks.
var knownLowContrast = map[string]bool{
	"Default/StyleDialogAccent": true,
	"Default/StyleDialogError":  true,
	"Light/StyleDialogAccent":   true,
	"Light/StyleDialogDim":      true,
	"Light/StyleMenuDim":        true,
	"MC/StyleMenuDim":           true,
	"MCRetro/StyleMenuDim":      true,
	"MCRetro/StyleDialogDim":    true,
	"MCRetro/StyleDialogError":  true,
}

// styleFuncs is every Style* function, by name. A new one belongs here: the
// contrast test only checks what it is given.
var styleFuncs = map[string]func() tcell.Style{
	"StyleDefault":         StyleDefault,
	"StylePanel":           StylePanel,
	"StyleBorder":          StyleBorder,
	"StyleActiveBorder":    StyleActiveBorder,
	"StyleMenuBar":         StyleMenuBar,
	"StyleDisabled":        StyleDisabled,
	"StyleStatusBar":       StyleStatusBar,
	"StyleSelected":        StyleSelected,
	"StyleMenuSelected":    StyleMenuSelected,
	"StyleMenuBarEmphasis": StyleMenuBarEmphasis,
	"StyleAlert":           StyleAlert,
	"StyleSearchMatch":     StyleSearchMatch,
	"StyleDialog":          StyleDialog,
	"StyleButton":          StyleButton,
	"StyleButtonActive":    StyleButtonActive,
	"StyleInput":           StyleInput,
	"StyleControlDisabled": StyleControlDisabled,
	"StyleDialogDim":       StyleDialogDim,
	"StyleDialogLine":      StyleDialogLine,
	"StyleDialogAccent":    StyleDialogAccent,
	"StyleDialogError":     StyleDialogError,
	"StyleMenuDim":         StyleMenuDim,
	"StyleMenuLine":        StyleMenuLine,
	"StyleActiveBlock":     StyleActiveBlock,
	"StyleScrollThumb":     StyleScrollThumb,
	"StyleButtonDisabled":  StyleButtonDisabled,
	"StyleGridHeader":      StyleGridHeader,
	"StyleGridRow":         StyleGridRow,
	"StyleGridRowAlt":      StyleGridRowAlt,
	"StyleGridSelected":    StyleGridSelected,
	"StyleGridStatus":      StyleGridStatus,
	"StyleChartPlot":       StyleChartPlot,
	"StyleChartGrid":       StyleChartGrid,
	"StyleChartAxis":       StyleChartAxis,
	"StyleTooltip":         StyleTooltip,
	"StyleTooltipBorder":   StyleTooltipBorder,
	"StyleChartSection":    StyleChartSection,
	"StyleChartTitle":      StyleChartTitle,
}

var palettes = map[string]Palette{"Default": Default, "Light": Light, "MC": MC, "MCRetro": MCRetro}

// relativeLuminance is WCAG 2's relative luminance of an RGB colour.
func relativeLuminance(c color.Color) float64 {
	r, g, b := c.RGB()
	lin := func(v int32) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrastRatio is WCAG 2's contrast ratio of two colours, 1 to 21.
func contrastRatio(a, b color.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

func TestContrastRatioMatchesWCAG(t *testing.T) {
	black, white := tcell.NewRGBColor(0, 0, 0), tcell.NewRGBColor(255, 255, 255)
	if got := contrastRatio(black, white); math.Abs(got-21) > 0.01 {
		t.Errorf("black on white = %.2f, want 21", got)
	}
	// VGA cyan under white: B1 of the 2026-11-04 review.
	if got := contrastRatio(white, tcell.NewRGBColor(0, 170, 170)); math.Abs(got-2.9) > 0.05 {
		t.Errorf("white on VGA cyan = %.2f, want about 2.9", got)
	}
}

// Every text style, in every palette, must read: a foreground hard-coded
// instead of taken from the palette (white on the mc themes' cyan status bar,
// grid selection and active button) passes in Default and fails here.
func TestEveryStyleMeetsTheContrastFloor(t *testing.T) {
	saved := *Active()
	defer SetPalette(saved)

	for pname, p := range palettes {
		SetPalette(p)
		for sname, f := range styleFuncs {
			st := f()
			fg, bg := st.GetForeground(), st.GetBackground()
			if r, _, _ := fg.RGB(); r < 0 {
				t.Errorf("%s/%s: foreground %v has no RGB value", pname, sname, fg)
				continue
			}
			if r, _, _ := bg.RGB(); r < 0 {
				t.Errorf("%s/%s: background %v has no RGB value", pname, sname, bg)
				continue
			}
			if contrastExempt[sname] {
				continue
			}
			floor := minTextContrast
			if f, ok := paletteFloor[pname]; ok {
				floor = f
			}
			got := contrastRatio(fg, bg)
			if known := knownLowContrast[pname+"/"+sname]; known {
				if got >= floor {
					t.Errorf("%s/%s: contrast %.3f:1 now meets the floor; drop it from knownLowContrast", pname, sname, got)
				}
				continue
			}
			if got < floor {
				t.Errorf("%s/%s: contrast %.3f:1, want at least %.3f:1", pname, sname, got, floor)
			}
		}
	}
}
