package charts

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// KPI is a compact "label: value" readout — the numeric boxes along a
// dashboard's section headers (User Connections, Blocked Processes, Page
// Life Expectancy).
//
// The value is drawn on a filled background so it reads as a figure rather
// than as more label text, which is the whole point of a KPI next to a wall
// of charts.
type KPI struct {
	Label string
	Value string

	// ValueBg fills the value box; the zero colour uses the theme's
	// selection blue, matching the mockups.
	ValueBg tcell.Color

	// ValueFg draws the value text; the zero colour uses the theme's
	// highlight text.
	ValueFg tcell.Color

	// LabelBg backs the label text; the zero colour uses the panel
	// background. A KPI drawn onto a filled strip has to be told what that
	// strip's colour is, or its label punches a panel-coloured hole in it.
	LabelBg tcell.Color
}

// Width is the number of columns Draw needs to show the KPI in full.
func (k KPI) Width() int {
	return core.DisplayWidth(k.Label) + core.DisplayWidth(k.Value) + 3
}

// Draw renders the KPI right-aligned within r's first row — dashboards
// place these at the right end of a section header, so growing leftward
// keeps them anchored as the value's width changes.
//
// Too little room drops the label before the value: the number is the
// point, and the surrounding section already gives it context.
func (k KPI) Draw(s tcell.Screen, r core.Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	pal := theme.Active()
	valueBg, valueFg := k.ValueBg, k.ValueFg
	if valueBg == tcell.ColorDefault {
		valueBg = pal.GridSelected
	}
	if valueFg == tcell.ColorDefault {
		valueFg = pal.TextHighlight
	}

	value := " " + k.Value + " "
	valueW := core.DisplayWidth(value)
	if valueW > r.W {
		value = core.Truncate(value, r.W)
		valueW = core.DisplayWidth(value)
	}
	valueX := r.Right() - valueW
	core.DrawText(s, valueX, r.Y, tcell.StyleDefault.Background(valueBg).Foreground(valueFg), value)

	labelW := valueX - r.X
	if labelW <= 0 || k.Label == "" {
		return
	}
	labelStyle := theme.StyleChartAxis()
	if k.LabelBg != tcell.ColorDefault {
		labelStyle = labelStyle.Background(k.LabelBg)
	}
	core.DrawTextRight(s, r.X, r.Y, labelW, labelStyle, " "+k.Label)
}
