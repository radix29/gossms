package theme

import "github.com/gdamore/tcell/v3"

// Functions, not vars, so they always reflect the live palette.

// On is the style with background bg and foreground fg: the one-off pair no
// named helper below covers, e.g. On(p.EditorBg, p.EditorKeyword).
func On(bg, fg tcell.Color) tcell.Style {
	return tcell.StyleDefault.Background(bg).Foreground(fg)
}

func StyleDefault() tcell.Style {
	return On(active.Background, active.Text)
}
func StylePanel() tcell.Style {
	return On(active.PanelBg, active.Text)
}
func StyleBorder() tcell.Style {
	return On(active.PanelBg, active.Border)
}
func StyleActiveBorder() tcell.Style {
	return On(active.PanelBg, active.BorderActive)
}
func StyleMenuBar() tcell.Style {
	return On(active.MenuBar, active.MenuText)
}

// StyleMenuDim is secondary text on a MenuBar ground: shortcut hints, a tab's
// close mark, scroll arrows.
func StyleMenuDim() tcell.Style {
	return On(active.MenuBar, active.MenuTextDim)
}

// StyleMenuLine is a popup menu's frame and separators.
func StyleMenuLine() tcell.Style {
	return On(active.MenuBar, active.MenuLine)
}

// StyleActiveBlock is selected text on a solid BorderActive block: the text
// caret, the active tab, a splitter bar being dragged.
func StyleActiveBlock() tcell.Style {
	return On(active.BorderActive, active.SelectedText)
}

// StyleScrollThumb is a scrollbar thumb: a solid BorderActive cell whatever
// glyph it draws.
func StyleScrollThumb() tcell.Style {
	return On(active.BorderActive, active.BorderActive)
}

func StyleDisabled() tcell.Style {
	return On(active.MenuBar, active.TextDisabled)
}
func StyleStatusBar() tcell.Style {
	return On(active.StatusBar, active.StatusBarText)
}
func StyleSelected() tcell.Style {
	return On(active.TreeSelected, active.SelectedText)
}

// StyleMenuSelected is the highlighted row of a list on a MenuBar ground (menu,
// dropdown list, panel switcher). Not StyleSelected: Midnight Commander's
// selection is the same cyan as its menus, so the row would vanish.
func StyleMenuSelected() tcell.Style {
	return On(active.MenuSelected, active.MenuSelectedText)
}

// StyleMenuBarEmphasis is an emphasised word on a MenuBar ground, such as the
// dashboard header's instance name. Bold, because in the mc themes the only
// readable colour on the cyan bar is MenuText's own black.
func StyleMenuBarEmphasis() tcell.Style {
	return On(active.MenuBar, active.MenuTextHighlight).Bold(true)
}

// StyleAlert is a status line that must not read as routine: the results
// status line when Max fetch rows cut a result short.
func StyleAlert() tcell.Style {
	return On(active.AlertBg, active.AlertText).Bold(true)
}
func StyleSearchMatch() tcell.Style {
	return On(active.EditorMatch, active.EditorMatchText)
}
func StyleDialog() tcell.Style {
	return On(active.DialogBg, active.DialogText)
}

// StyleDialogDim is secondary dialog text: hints, notes, unfocused values.
func StyleDialogDim() tcell.Style {
	return On(active.DialogBg, active.DialogTextDim)
}

// StyleDialogLine is a dialog's separator rules and its scrollbar track.
func StyleDialogLine() tcell.Style {
	return On(active.DialogBg, active.DialogLine)
}

// StyleDialogAccent is an accented dialog word: a checked mark, a selected
// radio, a section or key label.
func StyleDialogAccent() tcell.Style {
	return On(active.DialogBg, active.DialogAccent)
}

// StyleDialogError is an error or validation message inside a dialog.
func StyleDialogError() tcell.Style {
	return On(active.DialogBg, active.DialogError)
}
func StyleButton() tcell.Style {
	return On(active.ButtonBg, active.ButtonFg)
}
func StyleButtonActive() tcell.Style {
	return On(active.ButtonActive, active.ButtonActiveText)
}
func StyleInput() tcell.Style {
	return On(active.InputBg, active.InputFg)
}

// StyleControlDisabled is a switched-off dialog control (text field, check box,
// radio group). It drops the background entirely, not just dims text, so live
// controls are obvious at a glance.
func StyleControlDisabled() tcell.Style {
	return On(active.DialogBg, active.DialogTextDim)
}

// StyleButtonDisabled keeps the button's ground — with no border, dropping it
// would leave nothing visible — and dims only the label.
func StyleButtonDisabled() tcell.Style {
	return On(active.ButtonBg, active.TextDim)
}
func StyleGridHeader() tcell.Style {
	return On(active.GridHeader, active.GridHeaderText).Bold(true)
}
func StyleGridRow() tcell.Style {
	return On(active.PanelBg, active.Text)
}
func StyleGridRowAlt() tcell.Style {
	return On(active.GridRowAlt, active.Text)
}
func StyleGridSelected() tcell.Style {
	return On(active.GridSelected, active.SelectedText)
}

// StyleGridStatus is a results grid's status line, SSMS's yellow execution
// status bar; the toolbar's tooltip wears it too.
func StyleGridStatus() tcell.Style {
	return On(active.GridStatusBg, active.GridStatusText)
}

// StyleChartPlot is a chart's plot-area background; stacked columns' partial
// top cells blend against it.
func StyleChartPlot() tcell.Style {
	return On(active.ChartPlotBg, active.ChartAxis)
}

// StyleChartGrid is the muted `·` dot grid and `┆` time divisions.
func StyleChartGrid() tcell.Style {
	return On(active.ChartPlotBg, active.ChartGrid)
}

// StyleChartAxis is the Y-axis value and time labels.
func StyleChartAxis() tcell.Style {
	return On(active.PanelBg, active.ChartAxis)
}

// StyleTooltip is a popup readout's body, shared by toolbar buttons.
func StyleTooltip() tcell.Style {
	return On(active.TooltipBg, active.TooltipFg)
}

// StyleTooltipBorder is a tooltip's frame.
func StyleTooltipBorder() tcell.Style {
	return On(active.TooltipBg, active.TooltipBorder)
}

// StyleChartSection is a dashboard section's title strip.
func StyleChartSection() tcell.Style {
	return On(active.ChartSectionBg, active.ChartHeading)
}

// StyleChartTitle is a chart panel's heading, e.g. "SQL SERVER WAITS".
func StyleChartTitle() tcell.Style {
	return On(active.PanelBg, active.ChartHeading)
}
