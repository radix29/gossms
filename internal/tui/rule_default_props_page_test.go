package tui

import (
	"database/sql/driver"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Rule and Default Properties, driven through fakedb_test.go.
//
// The two families are the same sys.objects row under two type codes, and the
// responses below are matched on exactly that: "o.type = 'R'" and
// "o.type = 'D'". A match on "sys.objects" would let either answer serve the
// other page — the confusion the whole family is prone to, and the reason
// gosmo's Defaults read carries parent_object_id = 0.

func ruleResponse(definition driver.Value) fakeResponse {
	return fakeResponse{match: "o.type = 'R'", cols: 6, rows: [][]driver.Value{
		{"PhoneRule", "dbo", int64(1001), definition, propTypeDate, propTypeDate},
	}}
}

func defaultResponse(definition driver.Value) fakeResponse {
	return fakeResponse{match: "o.type = 'D'", cols: 6, rows: [][]driver.Value{
		{"TodayDefault", "dbo", int64(1002), definition, propTypeDate, propTypeDate},
	}}
}

func TestRuleGeneralShowsItsDefinition(t *testing.T) {
	const body = "CREATE RULE dbo.PhoneRule AS @value LIKE '[0-9][0-9][0-9]-[0-9][0-9][0-9][0-9]'"
	sc, inst := newTypePropConn(t, ruleResponse(body))
	form, apply := loadPage(t, rulePropPages(sc, propTypeDB, "dbo", "PhoneRule")[0], inst)

	if got := staticValue(t, form, "Name"); got != "PhoneRule" {
		t.Errorf("Name is %q", got)
	}
	if got := editorValue(t, form); got != body {
		t.Errorf("the editor holds %q, want the rule body", got)
	}
	if apply != nil {
		t.Error("the page has an apply, but CREATE RULE has no ALTER")
	}
}

func TestDefaultGeneralShowsItsDefinition(t *testing.T) {
	const body = "CREATE DEFAULT dbo.TodayDefault AS GETDATE()"
	sc, inst := newTypePropConn(t, defaultResponse(body))
	form, apply := loadPage(t, defaultPropPages(sc, propTypeDB, "dbo", "TodayDefault")[0], inst)

	if got := staticValue(t, form, "Name"); got != "TodayDefault" {
		t.Errorf("Name is %q", got)
	}
	if got := editorValue(t, form); got != body {
		t.Errorf("the editor holds %q, want the default body", got)
	}
	if apply != nil {
		t.Error("the page has an apply, but CREATE DEFAULT has no ALTER")
	}
}

// An encrypted module, and one the login cannot see into, both report an
// empty definition. An empty editor there reads as an object with an empty
// body, which is not a state the catalog can hold.
func TestRuleGeneralReportsAnUnreadableDefinition(t *testing.T) {
	sc, inst := newTypePropConn(t, ruleResponse(""))
	form, _ := loadPage(t, rulePropPages(sc, propTypeDB, "dbo", "PhoneRule")[0], inst)

	for _, r := range form.Rows() {
		if _, ok := r.(*propsheet.EditorRow); ok {
			t.Error("the page drew an editor for a definition it could not read")
		}
	}
}

// editorValue returns the text of the page's single read-only editor, and
// fails if it is writable — an edit on a page with no apply goes nowhere, so
// offering one is a lie about what the dialog can do.
func editorValue(t *testing.T, f *propsheet.Form) string {
	t.Helper()
	var ed *propsheet.EditorRow
	for _, r := range f.Rows() {
		if er, ok := r.(*propsheet.EditorRow); ok {
			if ed != nil {
				t.Fatal("this page has more than one editor — address them by label instead")
			}
			ed = er
		}
	}
	if ed == nil {
		t.Fatal("this page has no editor row")
	}
	if !ed.Editor().ReadOnly() {
		t.Error("the definition editor is writable, but the page has no apply")
	}
	return ed.Value()
}
