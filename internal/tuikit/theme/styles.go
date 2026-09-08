package theme

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// ---------------------------------------------------------------------------
// Pre-built styles derived from the active palette
// ---------------------------------------------------------------------------
// These are functions (not vars) so they always reflect the live palette.

func StyleDefault() tcell.Style {
	return tcell.StyleDefault.Background(active.Background).Foreground(active.Text)
}
func StylePanel() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.Text)
}
func StyleBorder() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.Border)
}
func StyleActiveBorder() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.BorderActive)
}
func StyleMenuBar() tcell.Style {
	return tcell.StyleDefault.Background(active.MenuBar).Foreground(active.Text)
}
func StyleDisabled() tcell.Style {
	return tcell.StyleDefault.Background(active.MenuBar).Foreground(active.TextDisabled)
}
func StyleStatusBar() tcell.Style {
	return tcell.StyleDefault.Background(active.StatusBar).Foreground(color.White)
}
func StyleSelected() tcell.Style {
	return tcell.StyleDefault.Background(active.TreeSelected).Foreground(active.TextHighlight)
}
func StyleSearchMatch() tcell.Style {
	return tcell.StyleDefault.Background(active.EditorMatch).Foreground(active.TextHighlight)
}
func StyleDialog() tcell.Style {
	return tcell.StyleDefault.Background(active.DialogBg).Foreground(active.Text)
}
func StyleButton() tcell.Style {
	return tcell.StyleDefault.Background(active.ButtonBg).Foreground(active.ButtonFg)
}
func StyleButtonActive() tcell.Style {
	return tcell.StyleDefault.Background(active.ButtonActive).Foreground(color.White)
}
func StyleInput() tcell.Style {
	return tcell.StyleDefault.Background(active.InputBg).Foreground(active.InputFg)
}

// StyleControlDisabled is a dialog control the page has switched off — a text
// field, check box or radio group that still draws but refuses keys and
// clicks. It drops the control's own background entirely rather than only
// dimming its text: a disabled control accepts nothing, so it has to be
// obvious at a glance which ones are live.
func StyleControlDisabled() tcell.Style {
	return tcell.StyleDefault.Background(active.DialogBg).Foreground(active.TextDim)
}

// StyleInputDisabled is StyleControlDisabled applied to a text field, named
// for the caller that reads best at the InputField.Draw site.
func StyleInputDisabled() tcell.Style { return StyleControlDisabled() }

// StyleButtonDisabled is a button the page has switched off. A button keeps
// its own ground — unlike a field, it has no border to lose, so dropping the
// background would leave nothing on screen to click at all — and dims only
// its label.
func StyleButtonDisabled() tcell.Style {
	return tcell.StyleDefault.Background(active.ButtonBg).Foreground(active.TextDim)
}
func StyleGridHeader() tcell.Style {
	return tcell.StyleDefault.Background(active.GridHeader).Foreground(active.Text).Bold(true)
}
func StyleGridRow() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.Text)
}
func StyleGridRowAlt() tcell.Style {
	return tcell.StyleDefault.Background(active.GridRowAlt).Foreground(active.Text)
}
func StyleGridSelected() tcell.Style {
	return tcell.StyleDefault.Background(active.GridSelected).Foreground(color.White)
}
func StyleGridStatus() tcell.Style {
	return tcell.StyleDefault.Background(color.LightYellow).Foreground(color.Black)
}

// StyleChartPlot is the background of a chart's plot area — every chart
// clears its plot to this before drawing, and a stacked column's topmost
// partial cell blends against it.
func StyleChartPlot() tcell.Style {
	return tcell.StyleDefault.Background(active.ChartPlotBg).Foreground(active.ChartAxis)
}

// StyleChartGrid is the muted `·` dot grid and `┆` time divisions drawn
// inside the plot area.
func StyleChartGrid() tcell.Style {
	return tcell.StyleDefault.Background(active.ChartPlotBg).Foreground(active.ChartGrid)
}

// StyleChartAxis is the Y-axis value labels and time labels around the plot.
func StyleChartAxis() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.ChartAxis)
}

// StyleTooltip is the body of a popup readout, and of the toolbar buttons
// that share its scheme.
func StyleTooltip() tcell.Style {
	return tcell.StyleDefault.Background(active.TooltipBg).Foreground(active.TooltipFg)
}

// StyleTooltipBorder is a tooltip's frame.
func StyleTooltipBorder() tcell.Style {
	return tcell.StyleDefault.Background(active.TooltipBg).Foreground(active.TooltipBorder)
}

// StyleChartSection is a dashboard section's title strip.
func StyleChartSection() tcell.Style {
	return tcell.StyleDefault.Background(active.ChartSectionBg).Foreground(active.ChartCyan)
}

// StyleChartTitle is a chart panel's heading, e.g. "SQL SERVER WAITS".
func StyleChartTitle() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.ChartCyan)
}
