package theme

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// Functions, not vars, so they always reflect the live palette.

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

// StyleControlDisabled is a switched-off dialog control (text field, check box,
// radio group). It drops the background entirely, not just dims text, so live
// controls are obvious at a glance.
func StyleControlDisabled() tcell.Style {
	return tcell.StyleDefault.Background(active.DialogBg).Foreground(active.TextDim)
}

// StyleInputDisabled is StyleControlDisabled for text fields.
func StyleInputDisabled() tcell.Style { return StyleControlDisabled() }

// StyleButtonDisabled keeps the button's ground — with no border, dropping it
// would leave nothing visible — and dims only the label.
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

// StyleChartPlot is a chart's plot-area background; stacked columns' partial
// top cells blend against it.
func StyleChartPlot() tcell.Style {
	return tcell.StyleDefault.Background(active.ChartPlotBg).Foreground(active.ChartAxis)
}

// StyleChartGrid is the muted `·` dot grid and `┆` time divisions.
func StyleChartGrid() tcell.Style {
	return tcell.StyleDefault.Background(active.ChartPlotBg).Foreground(active.ChartGrid)
}

// StyleChartAxis is the Y-axis value and time labels.
func StyleChartAxis() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.ChartAxis)
}

// StyleTooltip is a popup readout's body, shared by toolbar buttons.
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
