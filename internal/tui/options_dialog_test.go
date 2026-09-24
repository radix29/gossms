package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// internal/config can't import tuikit, so it carries its own copy of the
// indent-width default and ceiling. This package imports both and is where
// they are held together.
func TestIndentWidthConstantsAgree(t *testing.T) {
	if config.DefaultIndentWidth != controls.DefaultIndentWidth {
		t.Errorf("config.DefaultIndentWidth = %d, controls.DefaultIndentWidth = %d — they must agree",
			config.DefaultIndentWidth, controls.DefaultIndentWidth)
	}
	if config.MaxIndentWidth != controls.MaxIndentWidth {
		t.Errorf("config.MaxIndentWidth = %d, controls.MaxIndentWidth = %d — they must agree",
			config.MaxIndentWidth, controls.MaxIndentWidth)
	}
}

// A new query panel starts at the configured width.
func TestQueryPanelEditorTakesConfiguredIndentWidth(t *testing.T) {
	a := newTestApp()
	a.cfg.IndentWidth = 2
	qp := NewQueryPanel(a, "Query 1")
	if got := qp.editor.IndentWidth(); got != 2 {
		t.Fatalf("editor IndentWidth() = %d, want 2", got)
	}
}

// OK pushes the new width into every open query panel — unlike the other
// Options settings, this one lives in the Editor, not read from cfg at use
// time — and into the default new editors are seeded from.
func TestOptionsApplyPushesIndentWidthToOpenPanels(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Cleanup(func() { controls.SetDefaultIndentWidth(controls.DefaultIndentWidth) })

	a := newTestApp()
	a.cfg.IndentWidth = config.DefaultIndentWidth
	qp := NewQueryPanel(a, "Query 1")
	a.panels.AddPanel(qp)

	d := NewOptionsDialog(a)
	d.Show()
	if got := d.fIndentWidth.Value(); got != "4" {
		t.Fatalf("Show() pre-filled the field with %q, want \"4\"", got)
	}
	d.fIndentWidth.SetValue("2")
	d.apply()

	if got := a.cfg.IndentWidth; got != 2 {
		t.Errorf("cfg.IndentWidth = %d, want 2", got)
	}
	if got := qp.editor.IndentWidth(); got != 2 {
		t.Errorf("open panel's editor IndentWidth() = %d, want 2 — apply must push it", got)
	}
	if got := controls.NewEditor(nil).IndentWidth(); got != 2 {
		t.Errorf("new editor IndentWidth() = %d, want 2 — apply must move the default too", got)
	}
}

// Anything unparseable or out of range falls back to the default rather than
// storing a width no editor would accept.
func TestOptionsApplyClampsIndentWidth(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Cleanup(func() { controls.SetDefaultIndentWidth(controls.DefaultIndentWidth) })

	for _, in := range []string{"", "0", "-1", "99", "two"} {
		a := newTestApp()
		d := NewOptionsDialog(a)
		d.Show()
		d.fIndentWidth.SetValue(in)
		d.apply()
		if got := a.cfg.IndentWidth; got != config.DefaultIndentWidth {
			t.Errorf("apply(%q): cfg.IndentWidth = %d, want the default %d", in, got, config.DefaultIndentWidth)
		}
	}
}

// The Agent job-step Command box is the one writable editor that is not a
// QueryPanel — it is an EditorRow on a property-sheet page — so apply has to
// reach it through the open dialogs, not through a.panels.
func TestOptionsApplyPushesIndentWidthToOpenSheetEditors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Cleanup(func() { controls.SetDefaultIndentWidth(controls.DefaultIndentWidth) })

	a := newTestApp()
	a.cfg.IndentWidth = config.DefaultIndentWidth

	panel := newJobStepPanel(unchangedDatabaseItem, []string{"master"}, a.cfg.IndentWidth)
	sheet := propsheet.NewPropertySheet(&fakeSizedScreen{w: 120, h: 40}, "Job Properties")
	pd := registerDialog(a, &PropDialog{PropertySheet: sheet, app: a, pages: []propPage{{title: "Steps"}}})
	pd.SetPages([]string{"Steps"})
	pd.OnLoadPage = func(page, seq int) {
		pd.SetPageForm(page, seq, propsheet.NewForm(panel.rows()...))
	}
	pd.Show()
	pd.SelectPage(0)
	if got := panel.commandEditor.IndentWidth(); got != config.DefaultIndentWidth {
		t.Fatalf("setup: command editor IndentWidth() = %d, want %d", got, config.DefaultIndentWidth)
	}

	d := NewOptionsDialog(a)
	d.Show()
	d.fIndentWidth.SetValue("2")
	d.apply()

	if got := panel.commandEditor.IndentWidth(); got != 2 {
		t.Errorf("open job-step editor IndentWidth() = %d, want 2 — apply must reach the open sheets", got)
	}
}

// Options pre-fills the text-column cap and applies it, falling back to SSMS's
// default for anything that is not a positive number.
func TestOptionsApplyMaxTextColumnLength(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Cleanup(func() { controls.SetDefaultIndentWidth(controls.DefaultIndentWidth) })

	for _, tc := range []struct {
		in   string
		want int
	}{
		{"1024", 1024},
		{"", config.DefaultMaxTextColumnLength},
		{"0", config.DefaultMaxTextColumnLength},
		{"-3", config.DefaultMaxTextColumnLength},
		{"wide", config.DefaultMaxTextColumnLength},
	} {
		a := newTestApp()
		a.cfg.MaxTextColumnLength = 300
		d := NewOptionsDialog(a)
		d.Show()
		if got := d.fMaxTextLen.Value(); got != "300" {
			t.Fatalf("Show() pre-filled %q, want \"300\"", got)
		}
		d.fMaxTextLen.SetValue(tc.in)
		d.apply()
		if got := a.cfg.MaxTextColumnLength; got != tc.want {
			t.Errorf("apply(%q): MaxTextColumnLength = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// optionsFocusName names what has keyboard focus on the Options dialog.
func optionsFocusName(d *OptionsDialog) string {
	if d.onButtons() {
		return fmt.Sprintf("button%d", d.btnFocus)
	}
	switch d.focusable[d.focusIdx] {
	case d.rbIconStyle:
		return "icons"
	case d.fMaxCellLen:
		return "cell"
	case d.fMaxTextLen:
		return "text"
	case d.fIndentWidth:
		return "indent"
	case d.cbIntelliSense:
		return "intellisense"
	}
	return "?"
}

// The keyboard walk through the Options dialog, recorded from the per-zone
// switch the focus slice replaced: after each key, what has focus, whether
// the dialog consumed the key, and whether Copy/Paste has a target. Pins the
// quirks as well as the order — Backtab does not wrap off the first control,
// the radio box swallows the keys it does not use while the fields pass them
// on, Down moves the radio selection before it moves focus, and Up from
// either button returns to the checkbox.
func TestOptionsDialogKeyboardWalk(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewOptionsDialog(a)
	d.Show()
	keys := []struct {
		n string
		k tcell.Key
	}{
		{"Tab", tcell.KeyTab}, {"Tab", tcell.KeyTab}, {"Tab", tcell.KeyTab}, {"Tab", tcell.KeyTab},
		{"Tab", tcell.KeyTab}, {"Tab", tcell.KeyTab}, {"Tab", tcell.KeyTab},
		{"Right", tcell.KeyRight}, {"Left", tcell.KeyLeft}, {"Left", tcell.KeyLeft},
		{"Tab", tcell.KeyTab}, {"Up", tcell.KeyUp},
		{"Backtab", tcell.KeyBacktab}, {"Up", tcell.KeyUp}, {"Backtab", tcell.KeyBacktab},
		{"Up", tcell.KeyUp}, {"Backtab", tcell.KeyBacktab},
		{"Down", tcell.KeyDown}, {"Up", tcell.KeyUp}, {"Down", tcell.KeyDown}, {"Down", tcell.KeyDown},
		{"Down", tcell.KeyDown}, {"Down", tcell.KeyDown}, {"Down", tcell.KeyDown},
		{"F5", tcell.KeyF5}, {"Up", tcell.KeyUp}, {"F5", tcell.KeyF5}, {"Up", tcell.KeyUp},
		{"F5", tcell.KeyF5}, {"Up", tcell.KeyUp}, {"Up", tcell.KeyUp}, {"Up", tcell.KeyUp},
		{"F5", tcell.KeyF5},
	}
	var b strings.Builder
	b.WriteString(optionsFocusName(d))
	for _, k := range keys {
		ok := d.HandleKey(tcell.NewEventKey(k.k, "", tcell.ModNone))
		fmt.Fprintf(&b, " %s>%s(%v,%v)", k.n, optionsFocusName(d), ok, d.FocusedClipboardTarget() != nil)
	}
	const want = "icons Tab>cell(true,true) Tab>text(true,true) Tab>indent(true,true) " +
		"Tab>intellisense(true,false) Tab>button0(true,false) Tab>button1(true,false) " +
		"Tab>button0(true,false) Right>button1(true,false) Left>button0(true,false) " +
		"Left>intellisense(true,false) Tab>button0(true,false) Up>intellisense(true,false) " +
		"Backtab>indent(true,true) Up>text(true,true) Backtab>cell(true,true) Up>icons(true,false) " +
		"Backtab>icons(true,false) Down>icons(true,false) Up>icons(true,false) Down>icons(true,false) " +
		"Down>icons(true,false) Down>icons(true,false) Down>cell(true,true) Down>text(true,true) " +
		"F5>text(false,true) Up>cell(true,true) F5>cell(false,true) Up>icons(true,false) " +
		"F5>icons(true,false) Up>icons(true,false) Up>icons(true,false) Up>icons(true,false) " +
		"F5>icons(true,false)"
	if got := b.String(); got != want {
		t.Errorf("keyboard walk:\n got %s\nwant %s", got, want)
	}
}

// A press focuses whatever it lands on, found through the focus slice: each
// input field, and the checkbox, which takes the press itself.
func TestOptionsDialogPressFocusesTheControlUnderIt(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewOptionsDialog(a)
	d.Show()
	// Draw's positions, which only Draw applies.
	inner := d.InnerRect()
	d.fMaxCellLen.SetBounds(inner.X+1, inner.Y+6)
	d.fMaxTextLen.SetBounds(inner.X+1, inner.Y+8)
	d.fIndentWidth.SetBounds(inner.X+1, inner.Y+10)
	d.cbIntelliSense.SetBounds(inner.X+1, inner.Y+12)

	press := func(x, y int) {
		d.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
		d.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	}
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"indent", d.fIndentWidth.InputX() + 1, d.fIndentWidth.RectY()},
		{"text", d.fMaxTextLen.InputX() + 1, d.fMaxTextLen.RectY()},
		{"intellisense", inner.X + 2, inner.Y + 12},
		{"cell", d.fMaxCellLen.InputX() + 1, d.fMaxCellLen.RectY()},
	} {
		press(tc.x, tc.y)
		if got := optionsFocusName(d); got != tc.name {
			t.Errorf("a press on %s focused %s", tc.name, got)
		}
	}
}
