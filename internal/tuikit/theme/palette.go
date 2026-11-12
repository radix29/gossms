package theme

import "github.com/gdamore/tcell/v3"

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
	// StatusBarText is text on StatusBar. Its own role, not SelectedText: the
	// status bar is an accent ground in Dark and Light but mc's plain cyan bar.
	StatusBarText tcell.Color

	// Text
	Text          tcell.Color
	TextDim       tcell.Color
	TextHighlight tcell.Color
	TextDisabled  tcell.Color
	// SelectedText is text on an accent (selection blue) ground. Not
	// TextHighlight, which also emphasises text on panel and dialog grounds: in
	// a light theme the two need opposite lightness.
	SelectedText tcell.Color

	// Tree
	TreeSelected tcell.Color
	TreeHover    tcell.Color

	// Menu
	MenuSelected tcell.Color
	MenuHover    tcell.Color
	// MenuSelectedText is text on MenuSelected: the highlighted menu row, a
	// hovered toolbar button, Activity Monitor's selected tab.
	MenuSelectedText tcell.Color
	// MenuText, MenuTextDim and MenuLine are text, hints and rules on a MenuBar
	// ground — menus, toolbars, tab strips and panel title bars. Not Text: in
	// the mc themes the bars are cyan while panels are blue, and one
	// foreground cannot read on both.
	MenuText    tcell.Color
	MenuTextDim tcell.Color
	MenuLine    tcell.Color
	// MenuTextHighlight is an emphasised word on a MenuBar ground (the
	// dashboard header's instance name). Not a chart colour: mc's cyan bar
	// leaves only its black readable in the Tango colours, so StyleMenuBarEmphasis
	// adds bold to set the word apart there.
	MenuTextHighlight tcell.Color

	// Grid
	GridHeader   tcell.Color
	GridRowAlt   tcell.Color
	GridSelected tcell.Color
	GridBorder   tcell.Color
	// GridHeaderText is column-heading text on GridHeader.
	GridHeaderText tcell.Color
	// GridStatusBg and GridStatusText are the results grids' status line and the
	// toolbar's tooltip, SSMS's yellow execution status bar.
	GridStatusBg   tcell.Color
	GridStatusText tcell.Color

	// Editor
	EditorBg      tcell.Color
	EditorCursor  tcell.Color
	EditorKeyword tcell.Color
	EditorString  tcell.Color
	EditorComment tcell.Color
	EditorNumber  tcell.Color
	EditorLineNum tcell.Color
	// EditorMatch backs every find/replace hit except the current one, which
	// wears the selection colour; the two must stay distinguishable.
	EditorMatch tcell.Color
	// EditorMatchText is text on EditorMatch.
	EditorMatchText tcell.Color

	// Dialog
	DialogBg      tcell.Color
	DialogBorder  tcell.Color
	DialogTitle   tcell.Color
	DialogOverlay tcell.Color
	// The Dialog* foregrounds are their unprefixed namesakes (Text, TextDim,
	// TextHighlight, Border, BorderActive, Error, Warning) on a DialogBg ground.
	// The mc themes pair blue panels with light-grey dialogs, so every
	// panel foreground needs a dialog twin; Dark and Light set them equal.
	DialogText          tcell.Color
	DialogTextDim       tcell.Color
	DialogTextHighlight tcell.Color
	DialogLine          tcell.Color
	DialogAccent        tcell.Color
	DialogError         tcell.Color
	DialogWarning       tcell.Color

	// Button
	ButtonBg     tcell.Color
	ButtonFg     tcell.Color
	ButtonActive tcell.Color
	ButtonHover  tcell.Color
	// ButtonActiveText is a focused button's label on ButtonActive.
	ButtonActiveText tcell.Color

	// Input
	InputBg      tcell.Color
	InputFg      tcell.Color
	InputBorder  tcell.Color
	InputFocused tcell.Color

	// Tooltip — chart pointer popups and toolbar buttons share this scheme, so
	// a control and the box it produces read as one surface.
	TooltipBg     tcell.Color
	TooltipFg     tcell.Color
	TooltipBorder tcell.Color

	// Status
	Success tcell.Color
	Error   tcell.Color
	Warning tcell.Color
	Info    tcell.Color
	// AlertBg and AlertText are a shout on a ground of its own: the results
	// status line's truncation alert. Not Error, which is a foreground on panel
	// grounds: VGA's bright red is too light under white, so the mc themes use
	// mc's own error pair, white on dark red.
	AlertBg   tcell.Color
	AlertText tcell.Color

	// Chart — the tuikit/charts series palette. These are roles, not metrics:
	// the app maps each metric to a role once, so a series keeps its colour
	// across refreshes and dashboards.
	//
	// ChartPlotBg sets the plot apart from PanelBg (darker in Default, a
	// recessed well; white in Light); a stacked column's partial top cell blends
	// against it, so it must differ from the series colours.
	//
	// ChartSectionBg backs section title strips; it contrasts with panel and
	// grid-header backgrounds so strips divide the chart wall.
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
	// ChartHeading is a chart's title and its section strip's text, on PanelBg
	// and ChartSectionBg. Not ChartCyan: a series colour bright enough to plot
	// on Light's white well is too pale to read as text there.
	ChartHeading tcell.Color
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
	StatusBarText: tcell.NewRGBColor(255, 255, 255),

	Text:          tcell.NewRGBColor(220, 220, 220),
	TextDim:       tcell.NewRGBColor(150, 150, 150),
	TextHighlight: tcell.NewRGBColor(255, 255, 255),
	TextDisabled:  tcell.NewRGBColor(100, 100, 100),
	SelectedText:  tcell.NewRGBColor(255, 255, 255),

	TreeSelected: tcell.NewRGBColor(0, 122, 204),
	TreeHover:    tcell.NewRGBColor(62, 62, 64),

	MenuSelected:      tcell.NewRGBColor(0, 122, 204),
	MenuHover:         tcell.NewRGBColor(62, 62, 64),
	MenuSelectedText:  tcell.NewRGBColor(255, 255, 255),
	MenuText:          tcell.NewRGBColor(220, 220, 220),
	MenuTextDim:       tcell.NewRGBColor(150, 150, 150),
	MenuLine:          tcell.NewRGBColor(63, 63, 70),
	MenuTextHighlight: tcell.NewRGBColor(78, 205, 196),

	GridHeader:     tcell.NewRGBColor(45, 45, 48),
	GridRowAlt:     tcell.NewRGBColor(40, 40, 42),
	GridSelected:   tcell.NewRGBColor(0, 86, 153),
	GridBorder:     tcell.NewRGBColor(63, 63, 70),
	GridHeaderText: tcell.NewRGBColor(220, 220, 220),
	GridStatusBg:   tcell.NewRGBColor(255, 255, 224),
	GridStatusText: tcell.NewRGBColor(0, 0, 0),

	EditorBg:        tcell.NewRGBColor(30, 30, 30),
	EditorCursor:    tcell.NewRGBColor(220, 220, 220),
	EditorKeyword:   tcell.NewRGBColor(86, 156, 214),
	EditorString:    tcell.NewRGBColor(206, 145, 120),
	EditorComment:   tcell.NewRGBColor(106, 153, 85),
	EditorNumber:    tcell.NewRGBColor(181, 206, 168),
	EditorLineNum:   tcell.NewRGBColor(100, 100, 100),
	EditorMatch:     tcell.NewRGBColor(88, 76, 22),
	EditorMatchText: tcell.NewRGBColor(255, 255, 255),

	DialogBg:            tcell.NewRGBColor(45, 45, 48),
	DialogBorder:        tcell.NewRGBColor(0, 122, 204),
	DialogTitle:         tcell.NewRGBColor(255, 255, 255),
	DialogOverlay:       tcell.NewRGBColor(0, 0, 0),
	DialogText:          tcell.NewRGBColor(220, 220, 220),
	DialogTextDim:       tcell.NewRGBColor(150, 150, 150),
	DialogTextHighlight: tcell.NewRGBColor(255, 255, 255),
	DialogLine:          tcell.NewRGBColor(63, 63, 70),
	DialogAccent:        tcell.NewRGBColor(0, 122, 204),
	DialogError:         tcell.NewRGBColor(220, 50, 50),
	DialogWarning:       tcell.NewRGBColor(220, 180, 50),

	ButtonBg:         tcell.NewRGBColor(63, 63, 70),
	ButtonFg:         tcell.NewRGBColor(220, 220, 220),
	ButtonActive:     tcell.NewRGBColor(0, 122, 204),
	ButtonHover:      tcell.NewRGBColor(80, 80, 85),
	ButtonActiveText: tcell.NewRGBColor(255, 255, 255),

	InputBg:      tcell.NewRGBColor(51, 51, 55),
	InputFg:      tcell.NewRGBColor(220, 220, 220),
	InputBorder:  tcell.NewRGBColor(63, 63, 70),
	InputFocused: tcell.NewRGBColor(0, 122, 204),

	TooltipBg:     tcell.NewRGBColor(58, 58, 64),
	TooltipFg:     tcell.NewRGBColor(230, 230, 230),
	TooltipBorder: tcell.NewRGBColor(95, 95, 105),

	Success:   tcell.NewRGBColor(75, 175, 75),
	Error:     tcell.NewRGBColor(220, 50, 50),
	Warning:   tcell.NewRGBColor(220, 180, 50),
	Info:      tcell.NewRGBColor(100, 160, 220),
	AlertBg:   tcell.NewRGBColor(220, 50, 50),
	AlertText: tcell.NewRGBColor(255, 255, 255),

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
	ChartHeading:   tcell.NewRGBColor(78, 205, 196),
}

// mcColors is the 16-colour set a Midnight Commander skin names, plus the two
// off-panel shades mc itself lacks. RGB, not the ANSI palette indices: DimArea
// blends colours and needs their RGB values, and a terminal palette would make
// the theme vary by terminal.
type mcColors struct {
	Black, Blue, Green, Cyan, Red, Brown, LightGray, Gray         tcell.Color
	BrightBlue, BrightGreen, BrightCyan, BrightRed, BrightMagenta tcell.Color
	Yellow, White                                                 tcell.Color
	PanelShade, RowShade                                          tcell.Color // TreeHover, GridRowAlt
}

// vgaColors are the VGA text-mode colours.
var vgaColors = mcColors{
	Black:         tcell.NewRGBColor(0, 0, 0),
	Blue:          tcell.NewRGBColor(0, 0, 170),
	Green:         tcell.NewRGBColor(0, 170, 0),
	Cyan:          tcell.NewRGBColor(0, 170, 170),
	Red:           tcell.NewRGBColor(170, 0, 0),
	Brown:         tcell.NewRGBColor(170, 85, 0),
	LightGray:     tcell.NewRGBColor(170, 170, 170),
	Gray:          tcell.NewRGBColor(85, 85, 85),
	BrightBlue:    tcell.NewRGBColor(85, 85, 255),
	BrightGreen:   tcell.NewRGBColor(85, 255, 85),
	BrightCyan:    tcell.NewRGBColor(85, 255, 255),
	BrightRed:     tcell.NewRGBColor(255, 85, 85),
	BrightMagenta: tcell.NewRGBColor(255, 85, 255),
	Yellow:        tcell.NewRGBColor(255, 255, 85),
	White:         tcell.NewRGBColor(255, 255, 255),
	PanelShade:    tcell.NewRGBColor(0, 0, 120),
	RowShade:      tcell.NewRGBColor(0, 0, 140),
}

// tangoColors are the Tango terminal palette (the xfce4-terminal and GNOME
// Terminal default), which is how mc looks in most Linux terminals today.
var tangoColors = mcColors{
	Black:         tcell.NewRGBColor(46, 52, 54),
	Blue:          tcell.NewRGBColor(52, 101, 164),
	Green:         tcell.NewRGBColor(78, 154, 6),
	Cyan:          tcell.NewRGBColor(6, 152, 154),
	Red:           tcell.NewRGBColor(204, 0, 0),
	Brown:         tcell.NewRGBColor(196, 160, 0),
	LightGray:     tcell.NewRGBColor(211, 215, 207),
	Gray:          tcell.NewRGBColor(85, 87, 83),
	BrightBlue:    tcell.NewRGBColor(114, 159, 207),
	BrightGreen:   tcell.NewRGBColor(138, 226, 52),
	BrightCyan:    tcell.NewRGBColor(52, 226, 226),
	BrightRed:     tcell.NewRGBColor(239, 41, 41),
	BrightMagenta: tcell.NewRGBColor(173, 127, 168),
	Yellow:        tcell.NewRGBColor(252, 233, 79),
	White:         tcell.NewRGBColor(238, 238, 236),
	PanelShade:    tcell.NewRGBColor(42, 84, 138),
	RowShade:      tcell.NewRGBColor(46, 91, 150),
}

// MC follows mc's default skin rendered in the Tango terminal palette: the
// softer blue, grey and teal mc shows in a modern Linux terminal.
var MC = mcSkin(tangoColors)

// MCRetro is mc's default skin in the original VGA colours.
var MCRetro = mcSkin(vgaColors)

// mcSkin follows mc's default skin (/usr/share/mc/skins/default.ini) in the
// colour set c: light grey on blue panels and editor, black on light grey
// dialogs, cyan menus, input fields and selection, yellow headers, and the
// editor colours of mc's sql.syntax. Text on the cyan grounds that stand for an
// accent (status bar, focused button, selection) is black, as mc's statusbar,
// dfocus and selected are; white there falls below 3:1 in VGA colours.
func mcSkin(c mcColors) Palette {
	return Palette{
		Background:    c.Blue,
		MenuBar:       c.Cyan,
		StatusBar:     c.Cyan,
		PanelBg:       c.Blue,
		Border:        c.LightGray,
		BorderActive:  c.BrightCyan,
		Splitter:      c.Blue,
		SplitterHover: c.BrightCyan,
		StatusBarText: c.Black,

		Text:          c.LightGray,
		TextDim:       c.Cyan,
		TextHighlight: c.White,
		TextDisabled:  c.Gray,
		SelectedText:  c.Black,

		TreeSelected: c.Cyan,
		TreeHover:    c.PanelShade,

		MenuSelected:      c.Black,
		MenuHover:         c.Black,
		MenuSelectedText:  c.White,
		MenuText:          c.Black,
		MenuTextDim:       c.Gray,
		MenuLine:          c.White,
		MenuTextHighlight: c.Black,

		// GridRowAlt is a shade off the panel blue; mc has no zebra striping, but a
		// wide result grid is unreadable without it.
		GridHeader:     c.Blue,
		GridRowAlt:     c.RowShade,
		GridSelected:   c.Cyan,
		GridBorder:     c.LightGray,
		GridHeaderText: c.Yellow,
		GridStatusBg:   c.Yellow,
		GridStatusText: c.Black,

		EditorBg:        c.Blue,
		EditorCursor:    c.White,
		EditorKeyword:   c.Yellow,
		EditorString:    c.Green,
		EditorComment:   c.Brown,
		EditorNumber:    c.BrightCyan,
		EditorLineNum:   c.Cyan,
		EditorMatch:     c.Green,
		EditorMatchText: c.Black,

		DialogBg:            c.LightGray,
		DialogBorder:        c.Black,
		DialogTitle:         c.Blue,
		DialogOverlay:       c.Black,
		DialogText:          c.Black,
		DialogTextDim:       c.Gray,
		DialogTextHighlight: c.Blue,
		DialogLine:          c.Gray,
		DialogAccent:        c.Blue,
		DialogError:         c.Red,
		DialogWarning:       c.Brown,

		ButtonBg:         c.LightGray,
		ButtonFg:         c.Black,
		ButtonActive:     c.Cyan,
		ButtonHover:      c.BrightCyan,
		ButtonActiveText: c.Black,

		InputBg:      c.Cyan,
		InputFg:      c.Black,
		InputBorder:  c.Black,
		InputFocused: c.Blue,

		TooltipBg:     c.LightGray,
		TooltipFg:     c.Black,
		TooltipBorder: c.Black,

		Success:   c.BrightGreen,
		Error:     c.BrightRed,
		Warning:   c.Yellow,
		Info:      c.BrightCyan,
		AlertBg:   c.Red,
		AlertText: c.White,

		ChartCyan:      c.BrightCyan,
		ChartGreen:     c.BrightGreen,
		ChartYellow:    c.Yellow,
		ChartBlue:      c.BrightBlue,
		ChartRed:       c.BrightRed,
		ChartPurple:    c.BrightMagenta,
		ChartNeutral:   c.LightGray,
		ChartGrid:      c.Gray,
		ChartAxis:      c.LightGray,
		ChartPlotBg:    c.Black,
		ChartSectionBg: c.Gray,
		ChartHeading:   c.BrightCyan,
	}
}

// Light is the built-in SSMS light (Blue/Light) theme. Accent backgrounds keep
// the dark theme's blue, with white text on them as in Default.
var Light = Palette{
	Background:    tcell.NewRGBColor(255, 255, 255),
	MenuBar:       tcell.NewRGBColor(238, 238, 242),
	StatusBar:     tcell.NewRGBColor(0, 122, 204),
	PanelBg:       tcell.NewRGBColor(245, 245, 245),
	Border:        tcell.NewRGBColor(204, 206, 219),
	BorderActive:  tcell.NewRGBColor(0, 122, 204),
	Splitter:      tcell.NewRGBColor(204, 206, 219),
	SplitterHover: tcell.NewRGBColor(0, 122, 204),
	StatusBarText: tcell.NewRGBColor(255, 255, 255),

	Text:          tcell.NewRGBColor(30, 30, 30),
	TextDim:       tcell.NewRGBColor(110, 110, 110),
	TextHighlight: tcell.NewRGBColor(0, 0, 0),
	TextDisabled:  tcell.NewRGBColor(160, 160, 160),
	SelectedText:  tcell.NewRGBColor(255, 255, 255),

	TreeSelected: tcell.NewRGBColor(0, 122, 204),
	TreeHover:    tcell.NewRGBColor(220, 230, 244),

	MenuSelected:      tcell.NewRGBColor(0, 122, 204),
	MenuHover:         tcell.NewRGBColor(220, 230, 244),
	MenuSelectedText:  tcell.NewRGBColor(255, 255, 255),
	MenuText:          tcell.NewRGBColor(30, 30, 30),
	MenuTextDim:       tcell.NewRGBColor(110, 110, 110),
	MenuLine:          tcell.NewRGBColor(204, 206, 219),
	MenuTextHighlight: tcell.NewRGBColor(0, 100, 100),

	GridHeader:     tcell.NewRGBColor(225, 225, 230),
	GridRowAlt:     tcell.NewRGBColor(236, 236, 240),
	GridSelected:   tcell.NewRGBColor(0, 118, 210),
	GridBorder:     tcell.NewRGBColor(204, 206, 219),
	GridHeaderText: tcell.NewRGBColor(30, 30, 30),
	GridStatusBg:   tcell.NewRGBColor(255, 255, 224),
	GridStatusText: tcell.NewRGBColor(0, 0, 0),

	EditorBg:        tcell.NewRGBColor(255, 255, 255),
	EditorCursor:    tcell.NewRGBColor(30, 30, 30),
	EditorKeyword:   tcell.NewRGBColor(0, 0, 255),
	EditorString:    tcell.NewRGBColor(163, 21, 21),
	EditorComment:   tcell.NewRGBColor(0, 128, 0),
	EditorNumber:    tcell.NewRGBColor(9, 134, 88),
	EditorLineNum:   tcell.NewRGBColor(43, 145, 175),
	EditorMatch:     tcell.NewRGBColor(255, 226, 130),
	EditorMatchText: tcell.NewRGBColor(0, 0, 0),

	// DialogOverlay is mid-grey, not black: what a modal dims is mostly light
	// ground, which a blend toward black turns muddy.
	DialogBg:            tcell.NewRGBColor(240, 240, 240),
	DialogBorder:        tcell.NewRGBColor(0, 122, 204),
	DialogTitle:         tcell.NewRGBColor(0, 0, 0),
	DialogOverlay:       tcell.NewRGBColor(128, 128, 128),
	DialogText:          tcell.NewRGBColor(30, 30, 30),
	DialogTextDim:       tcell.NewRGBColor(110, 110, 110),
	DialogTextHighlight: tcell.NewRGBColor(0, 0, 0),
	DialogLine:          tcell.NewRGBColor(204, 206, 219),
	DialogAccent:        tcell.NewRGBColor(0, 122, 204),
	DialogError:         tcell.NewRGBColor(196, 30, 30),
	DialogWarning:       tcell.NewRGBColor(170, 110, 0),

	ButtonBg:         tcell.NewRGBColor(221, 221, 221),
	ButtonFg:         tcell.NewRGBColor(30, 30, 30),
	ButtonActive:     tcell.NewRGBColor(0, 122, 204),
	ButtonHover:      tcell.NewRGBColor(229, 241, 251),
	ButtonActiveText: tcell.NewRGBColor(255, 255, 255),

	InputBg:      tcell.NewRGBColor(255, 255, 255),
	InputFg:      tcell.NewRGBColor(30, 30, 30),
	InputBorder:  tcell.NewRGBColor(171, 173, 179),
	InputFocused: tcell.NewRGBColor(0, 122, 204),

	TooltipBg:     tcell.NewRGBColor(255, 255, 225),
	TooltipFg:     tcell.NewRGBColor(30, 30, 30),
	TooltipBorder: tcell.NewRGBColor(118, 118, 118),

	Success:   tcell.NewRGBColor(16, 124, 16),
	Error:     tcell.NewRGBColor(196, 30, 30),
	Warning:   tcell.NewRGBColor(170, 110, 0),
	Info:      tcell.NewRGBColor(0, 90, 170),
	AlertBg:   tcell.NewRGBColor(196, 30, 30),
	AlertText: tcell.NewRGBColor(255, 255, 255),

	ChartCyan:      tcell.NewRGBColor(0, 140, 140),
	ChartGreen:     tcell.NewRGBColor(30, 150, 50),
	ChartYellow:    tcell.NewRGBColor(200, 140, 0),
	ChartBlue:      tcell.NewRGBColor(40, 80, 220),
	ChartRed:       tcell.NewRGBColor(210, 40, 50),
	ChartPurple:    tcell.NewRGBColor(150, 50, 170),
	ChartNeutral:   tcell.NewRGBColor(120, 120, 120),
	ChartGrid:      tcell.NewRGBColor(200, 200, 200),
	ChartAxis:      tcell.NewRGBColor(90, 90, 90),
	ChartPlotBg:    tcell.NewRGBColor(255, 255, 255),
	ChartSectionBg: tcell.NewRGBColor(218, 220, 228),
	ChartHeading:   tcell.NewRGBColor(0, 100, 100),
}

// active is the current palette. Package-level state with no lock: SetPalette
// and Active belong to the UI goroutine.
var active = Default

// SetPalette replaces the active palette. Call it on the UI goroutine, before
// the first Draw or between frames (a theme switch); every Style* function and
// every Active reader sees it from the next Draw on.
func SetPalette(p Palette) { active = p }

// Active returns the live palette, not a copy: read it, never write through it.
// A write would restyle every control behind SetPalette's back; change the
// palette with SetPalette. A pointer because ~130 call sites read it per frame
// and a Palette is large. UI goroutine only.
func Active() *Palette { return &active }
