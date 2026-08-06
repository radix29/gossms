package theme

import "github.com/gdamore/tcell/v3"

// ---------------------------------------------------------------------------
// Colour palette
// ---------------------------------------------------------------------------

// Palette holds every named colour used by tuikit.
type Palette struct {
	// Backgrounds
	Background    tcell.Color
	MenuBar       tcell.Color
	StatusBar     tcell.Color
	PanelBg       tcell.Color
	Border        tcell.Color
	BorderActive  tcell.Color
	Splitter      tcell.Color
	SplitterHover tcell.Color

	// Text
	Text          tcell.Color
	TextDim       tcell.Color
	TextHighlight tcell.Color
	TextDisabled  tcell.Color

	// Tree
	TreeSelected tcell.Color
	TreeHover    tcell.Color

	// Menu
	MenuSelected tcell.Color
	MenuHover    tcell.Color

	// Grid
	GridHeader   tcell.Color
	GridRowAlt   tcell.Color
	GridSelected tcell.Color
	GridBorder   tcell.Color

	// Editor
	EditorBg      tcell.Color
	EditorCursor  tcell.Color
	EditorKeyword tcell.Color
	EditorString  tcell.Color
	EditorComment tcell.Color
	EditorNumber  tcell.Color
	EditorLineNum tcell.Color
	// EditorMatch backs every find/replace hit *except* the current one,
	// which is the editor's selection and so wears the selection colour —
	// the two have to stay distinguishable at a glance.
	EditorMatch tcell.Color

	// Dialog
	DialogBg      tcell.Color
	DialogBorder  tcell.Color
	DialogTitle   tcell.Color
	DialogOverlay tcell.Color

	// Button
	ButtonBg     tcell.Color
	ButtonFg     tcell.Color
	ButtonActive tcell.Color
	ButtonHover  tcell.Color

	// Input
	InputBg      tcell.Color
	InputFg      tcell.Color
	InputBorder  tcell.Color
	InputFocused tcell.Color

	// Tooltip — the popup a chart shows for the sample under the pointer,
	// and the toolbar buttons, which wear the same scheme so a clickable
	// control and the box it produces read as the same surface.
	TooltipBg     tcell.Color
	TooltipFg     tcell.Color
	TooltipBorder tcell.Color

	// Status
	Success tcell.Color
	Error   tcell.Color
	Warning tcell.Color
	Info    tcell.Color

	// Chart — the series palette used by tuikit/charts. These are roles,
	// not metric names: the application layer maps each metric to a role
	// once and keeps that mapping fixed, so a series wears the same colour
	// across refreshes and across dashboards.
	//
	// ChartPlotBg is deliberately darker than PanelBg: a chart's plot area
	// reads as a recessed well against the panel it sits in, and a stacked
	// column's topmost partial cell blends its series colour against this,
	// so it has to differ from the series colours themselves.
	//
	// ChartSectionBg backs a dashboard section's title strip. It is lighter
	// than the panel and grid-header backgrounds so the strips read as the
	// dividers between sections on a screen that is otherwise wall-to-wall
	// charts.
	ChartCyan      tcell.Color
	ChartGreen     tcell.Color
	ChartYellow    tcell.Color
	ChartBlue      tcell.Color
	ChartRed       tcell.Color
	ChartPurple    tcell.Color
	ChartNeutral   tcell.Color
	ChartGrid      tcell.Color
	ChartAxis      tcell.Color
	ChartPlotBg    tcell.Color
	ChartSectionBg tcell.Color
}

// Default is the built-in SSMS dark theme.
var Default = Palette{
	Background:    tcell.NewRGBColor(30, 30, 30),
	MenuBar:       tcell.NewRGBColor(45, 45, 48),
	StatusBar:     tcell.NewRGBColor(0, 122, 204),
	PanelBg:       tcell.NewRGBColor(37, 37, 38),
	Border:        tcell.NewRGBColor(63, 63, 70),
	BorderActive:  tcell.NewRGBColor(0, 122, 204),
	Splitter:      tcell.NewRGBColor(63, 63, 70),
	SplitterHover: tcell.NewRGBColor(0, 122, 204),

	Text:          tcell.NewRGBColor(220, 220, 220),
	TextDim:       tcell.NewRGBColor(150, 150, 150),
	TextHighlight: tcell.NewRGBColor(255, 255, 255),
	TextDisabled:  tcell.NewRGBColor(100, 100, 100),

	TreeSelected: tcell.NewRGBColor(0, 122, 204),
	TreeHover:    tcell.NewRGBColor(62, 62, 64),

	MenuSelected: tcell.NewRGBColor(0, 122, 204),
	MenuHover:    tcell.NewRGBColor(62, 62, 64),

	GridHeader:   tcell.NewRGBColor(45, 45, 48),
	GridRowAlt:   tcell.NewRGBColor(40, 40, 42),
	GridSelected: tcell.NewRGBColor(0, 86, 153),
	GridBorder:   tcell.NewRGBColor(63, 63, 70),

	EditorBg:      tcell.NewRGBColor(30, 30, 30),
	EditorCursor:  tcell.NewRGBColor(220, 220, 220),
	EditorKeyword: tcell.NewRGBColor(86, 156, 214),
	EditorString:  tcell.NewRGBColor(206, 145, 120),
	EditorComment: tcell.NewRGBColor(106, 153, 85),
	EditorNumber:  tcell.NewRGBColor(181, 206, 168),
	EditorLineNum: tcell.NewRGBColor(100, 100, 100),
	EditorMatch:   tcell.NewRGBColor(88, 76, 22),

	DialogBg:      tcell.NewRGBColor(45, 45, 48),
	DialogBorder:  tcell.NewRGBColor(0, 122, 204),
	DialogTitle:   tcell.NewRGBColor(255, 255, 255),
	DialogOverlay: tcell.NewRGBColor(0, 0, 0),

	ButtonBg:     tcell.NewRGBColor(63, 63, 70),
	ButtonFg:     tcell.NewRGBColor(220, 220, 220),
	ButtonActive: tcell.NewRGBColor(0, 122, 204),
	ButtonHover:  tcell.NewRGBColor(80, 80, 85),

	InputBg:      tcell.NewRGBColor(51, 51, 55),
	InputFg:      tcell.NewRGBColor(220, 220, 220),
	InputBorder:  tcell.NewRGBColor(63, 63, 70),
	InputFocused: tcell.NewRGBColor(0, 122, 204),

	TooltipBg:     tcell.NewRGBColor(58, 58, 64),
	TooltipFg:     tcell.NewRGBColor(230, 230, 230),
	TooltipBorder: tcell.NewRGBColor(95, 95, 105),

	Success: tcell.NewRGBColor(75, 175, 75),
	Error:   tcell.NewRGBColor(220, 50, 50),
	Warning: tcell.NewRGBColor(220, 180, 50),
	Info:    tcell.NewRGBColor(100, 160, 220),

	ChartCyan:      tcell.NewRGBColor(78, 205, 196),
	ChartGreen:     tcell.NewRGBColor(60, 200, 80),
	ChartYellow:    tcell.NewRGBColor(212, 160, 23),
	ChartBlue:      tcell.NewRGBColor(66, 110, 240),
	ChartRed:       tcell.NewRGBColor(230, 60, 70),
	ChartPurple:    tcell.NewRGBColor(170, 60, 190),
	ChartNeutral:   tcell.NewRGBColor(200, 200, 200),
	ChartGrid:      tcell.NewRGBColor(70, 70, 70),
	ChartAxis:      tcell.NewRGBColor(150, 150, 150),
	ChartPlotBg:    tcell.NewRGBColor(0, 0, 0),
	ChartSectionBg: tcell.NewRGBColor(66, 66, 72),
}

// active is the currently active palette (starts as Default).
var active = Default

// SetPalette replaces the active palette. Call before rendering.
func SetPalette(p Palette) { active = p }

// Active returns the currently active palette.
func Active() *Palette { return &active }
