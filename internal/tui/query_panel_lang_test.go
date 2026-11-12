package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/radix29/gossms/internal/format"
	"github.com/radix29/tuikit/dialogs"
)

// File > Open takes the panel's language from the extension. A .json file
// used to open SQL-highlighted; anything unknown (.txt included) stays SQL.
func TestOpenQueryFilePicksLanguageByExtension(t *testing.T) {
	for _, tt := range []struct {
		name string
		want format.Language
	}{
		{"q.sql", format.LangSQL},
		{"plan.XML", format.LangXML},
		{"cfg.json", format.LangJSON},
		{"notes.txt", format.LangSQL},
		{"noext", format.LangSQL},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp()
			a.fileDialog = dialogs.NewFileDialog(nil)
			path := filepath.Join(t.TempDir(), tt.name)
			if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			a.openQueryFile()
			a.fileDialog.OnChoose(path)
			qp := a.activeQueryPanel()
			if qp == nil {
				t.Fatal("no query panel opened")
			}
			if qp.lang != tt.want {
				t.Errorf("lang = %v, want %v", qp.lang, tt.want)
			}
		})
	}
}

// A cell value panel's language is the value's kind, and its title carries the
// matching extension.
func TestValuePanelLanguage(t *testing.T) {
	for _, tt := range []struct {
		sqlType, value string
		want           format.Language
		title          string
	}{
		{"xml", "<a/>", format.LangXML, "c.xml"},
		{"nvarchar", `{"a":1}`, format.LangJSON, "c.json"},
	} {
		a := newTestApp()
		if !a.openCellValuePanel(tt.sqlType, "c", tt.value) {
			t.Fatalf("%s value not opened", tt.sqlType)
		}
		qp := a.activeQueryPanel()
		if qp.lang != tt.want || qp.Title() != tt.title {
			t.Errorf("%s: lang = %v, title = %q; want %v, %q", tt.sqlType, qp.lang, qp.Title(), tt.want, tt.title)
		}
	}
	a := newTestApp()
	a.openValuePanel("", format.LangText, "plain")
	if qp := a.activeQueryPanel(); qp.lang != format.LangText || qp.Title() != "TXT" {
		t.Errorf("text panel: lang = %v, title = %q", qp.lang, qp.Title())
	}
}

// Save As to a known extension re-derives the language; to any other leaves
// it. And a value panel offers its own extension, not query.sql — which the
// save would otherwise turn into a SQL panel.
func TestSaveAsRederivesLanguage(t *testing.T) {
	a := newTestApp()
	a.fileDialog = dialogs.NewFileDialog(nil)
	a.openValuePanel("c", format.LangJSON, `{"a":1}`)
	qp := a.activeQueryPanel()
	dir := t.TempDir()

	a.saveQueryPanel(qp, true, nil)
	if got := a.fileDialog.FocusedField().Value(); got != "query.json" {
		t.Errorf("Save As offers %q, want query.json", got)
	}

	for _, step := range []struct {
		name string
		want format.Language
	}{
		{"a.xml", format.LangXML},
		{"a.txt", format.LangXML}, // unknown: unchanged
		{"a.SQL", format.LangSQL},
		{"a.json", format.LangJSON},
	} {
		if !a.writeQueryFile(qp, filepath.Join(dir, step.name)) {
			t.Fatalf("save to %s failed", step.name)
		}
		if qp.lang != step.want {
			t.Errorf("after saving as %s: lang = %v, want %v", step.name, qp.lang, step.want)
		}
	}
}
