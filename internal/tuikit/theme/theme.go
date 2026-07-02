// Package theme defines the SSMS-inspired dark colour palette and base
// tcell styles used throughout the tuikit library and the gossms application.
// Consumers import this package to read colours and styles; they may also
// call SetPalette to substitute a different palette at start-up.
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

	// Status
	Success tcell.Color
	Error   tcell.Color
	Warning tcell.Color
	Info    tcell.Color
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

	Success: tcell.NewRGBColor(75, 175, 75),
	Error:   tcell.NewRGBColor(220, 50, 50),
	Warning: tcell.NewRGBColor(220, 180, 50),
	Info:    tcell.NewRGBColor(100, 160, 220),
}

// active is the currently active palette (starts as Default).
var active = Default

// SetPalette replaces the active palette. Call before rendering.
func SetPalette(p Palette) { active = p }

// Active returns the currently active palette.
func Active() *Palette { return &active }

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
func StyleStatusBar() tcell.Style {
	return tcell.StyleDefault.Background(active.StatusBar).Foreground(tcell.ColorWhite)
}
func StyleSelected() tcell.Style {
	return tcell.StyleDefault.Background(active.TreeSelected).Foreground(active.TextHighlight)
}
func StyleDialog() tcell.Style {
	return tcell.StyleDefault.Background(active.DialogBg).Foreground(active.Text)
}
func StyleButton() tcell.Style {
	return tcell.StyleDefault.Background(active.ButtonBg).Foreground(active.ButtonFg)
}
func StyleButtonActive() tcell.Style {
	return tcell.StyleDefault.Background(active.ButtonActive).Foreground(tcell.ColorWhite)
}
func StyleInput() tcell.Style {
	return tcell.StyleDefault.Background(active.InputBg).Foreground(active.InputFg)
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
	return tcell.StyleDefault.Background(active.GridSelected).Foreground(tcell.ColorWhite)
}
