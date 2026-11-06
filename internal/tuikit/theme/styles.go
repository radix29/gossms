package theme

import "github.com/gdamore/tcell/v3"

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
	return tcell.StyleDefault.Background(active.MenuBar).Foreground(active.MenuText)
}
func StyleDisabled() tcell.Style {
	return tcell.StyleDefault.Background(active.MenuBar).Foreground(active.TextDisabled)
}
func StyleStatusBar() tcell.Style {
	return tcell.StyleDefault.Background(active.StatusBar).Foreground(active.StatusBarText)
}
func StyleSelected() tcell.Style {
	return tcell.StyleDefault.Background(active.TreeSelected).Foreground(active.SelectedText)
}

// StyleMenuSelected is the highlighted row of a list on a MenuBar ground (menu,
// dropdown list, panel switcher). Not StyleSelected: Midnight Commander's
// selection is the same cyan as its menus, so the row would vanish.
func StyleMenuSelected() tcell.Style {
	return tcell.StyleDefault.Background(active.MenuSelected).Foreground(active.MenuSelectedText)
}

// StyleMenuBarEmphasis is an emphasised word on a MenuBar ground, such as the
// dashboard header's instance name. Bold, because in the mc themes the only
// readable colour on the cyan bar is MenuText's own black.
func StyleMenuBarEmphasis() tcell.Style {
	return tcell.StyleDefault.Background(active.MenuBar).Foreground(active.MenuTextHighlight).Bold(true)
}

// StyleAlert is a status line that must not read as routine: the results
// status line when Max fetch rows cut a result short.
func StyleAlert() tcell.Style {
	return tcell.StyleDefault.Background(active.AlertBg).Foreground(active.AlertText).Bold(true)
}
func StyleSearchMatch() tcell.Style {
	return tcell.StyleDefault.Background(active.EditorMatch).Foreground(active.EditorMatchText)
}
func StyleDialog() tcell.Style {
	return tcell.StyleDefault.Background(active.DialogBg).Foreground(active.DialogText)
}
func StyleButton() tcell.Style {
	return tcell.StyleDefault.Background(active.ButtonBg).Foreground(active.ButtonFg)
}
func StyleButtonActive() tcell.Style {
	return tcell.StyleDefault.Background(active.ButtonActive).Foreground(active.ButtonActiveText)
}
func StyleInput() tcell.Style {
	return tcell.StyleDefault.Background(active.InputBg).Foreground(active.InputFg)
}

// StyleControlDisabled is a switched-off dialog control (text field, check box,
// radio group). It drops the background entirely, not just dims text, so live
// controls are obvious at a glance.
func StyleControlDisabled() tcell.Style {
	return tcell.StyleDefault.Background(active.DialogBg).Foreground(active.DialogTextDim)
}

// StyleInputDisabled is StyleControlDisabled for text fields.
func StyleInputDisabled() tcell.Style { return StyleControlDisabled() }

// StyleButtonDisabled keeps the button's ground — with no border, dropping it
// would leave nothing visible — and dims only the label.
func StyleButtonDisabled() tcell.Style {
	return tcell.StyleDefault.Background(active.ButtonBg).Foreground(active.TextDim)
}
func StyleGridHeader() tcell.Style {
	return tcell.StyleDefault.Background(active.GridHeader).Foreground(active.GridHeaderText).Bold(true)
}
func StyleGridRow() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.Text)
}
func StyleGridRowAlt() tcell.Style {
	return tcell.StyleDefault.Background(active.GridRowAlt).Foreground(active.Text)
}
func StyleGridSelected() tcell.Style {
	return tcell.StyleDefault.Background(active.GridSelected).Foreground(active.SelectedText)
}

// StyleGridStatus is a results grid's status line, SSMS's yellow execution
// status bar; the toolbar's tooltip wears it too.
func StyleGridStatus() tcell.Style {
	return tcell.StyleDefault.Background(active.GridStatusBg).Foreground(active.GridStatusText)
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
	return tcell.StyleDefault.Background(active.ChartSectionBg).Foreground(active.ChartHeading)
}

// StyleChartTitle is a chart panel's heading, e.g. "SQL SERVER WAITS".
func StyleChartTitle() tcell.Style {
	return tcell.StyleDefault.Background(active.PanelBg).Foreground(active.ChartHeading)
}
