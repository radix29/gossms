package tui

import (
	"testing"

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
