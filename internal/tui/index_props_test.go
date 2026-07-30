package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// A columnstore index reports data_compression_desc = "COLUMNSTORE", which
// isn't one of the three editable values. Selecting into the dropdown with
// indexOf's not-found 0 rendered it as "NONE" — a real compression setting,
// so it read as fact rather than as a value the row can't represent.
func TestDataCompressionRowShowsAnUneditableValueAsIs(t *testing.T) {
	for _, current := range []string{"COLUMNSTORE", "COLUMNSTORE_ARCHIVE"} {
		row, sel := dataCompressionRow(current)
		if sel != nil {
			t.Errorf("%s: got an editable Select row, want none", current)
		}
		static, ok := row.(*propsheet.StaticRow)
		if !ok {
			t.Fatalf("%s: row is %T, want *propsheet.StaticRow", current, row)
		}
		if !strings.Contains(static.CopyText(), current) {
			t.Errorf("%s: row shows %q, want it to contain the server's own value",
				current, static.CopyText())
		}
		// The wrong answer this replaced.
		if strings.Contains(static.CopyText(), "NONE") {
			t.Errorf("%s: row shows %q — must not read as NONE", current, static.CopyText())
		}
	}
}

// The three editable values still get a real dropdown, selected on the value
// the server reported.
func TestDataCompressionRowEditableValues(t *testing.T) {
	for want, current := range indexDataCompressionOptions {
		row, sel := dataCompressionRow(current)
		if sel == nil {
			t.Fatalf("%s: got no editable Select row, want one", current)
		}
		if row != propsheet.Row(sel) {
			t.Errorf("%s: layout row and Select row should be the same row", current)
		}
		if got := sel.Selected(); got != want {
			t.Errorf("%s: Selected() = %d, want %d", current, got, want)
		}
		if sel.Dirty() {
			t.Errorf("%s: a freshly built row must not be dirty", current)
		}
	}
}

// gosmo carries a type_desc it has no constant for through verbatim, so the
// display must fall back to it. The bare map lookup drew an empty row.
func TestIndexTypeNameFallsBackToTheServersText(t *testing.T) {
	if got := indexTypeName(gosmo.IndexType("NONCLUSTERED HASH")); got != "NONCLUSTERED HASH" {
		t.Errorf("indexTypeName(NONCLUSTERED HASH) = %q, want the server's own text", got)
	}
	for _, tc := range []struct {
		typ  gosmo.IndexType
		want string
	}{
		{gosmo.IndexTypeClustered, "Clustered"},
		{gosmo.IndexTypeNonClustered, "Nonclustered"},
		{gosmo.IndexTypeColumnStore, "Nonclustered columnstore"},
		{gosmo.IndexTypeClusteredColumnStore, "Clustered columnstore"},
	} {
		if got := indexTypeName(tc.typ); got != tc.want {
			t.Errorf("indexTypeName(%s) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}

// The two columnstore types must render differently — the whole point of
// splitting them is that Object Explorer stops labelling a clustered
// columnstore index "Nonclustered".
func TestColumnStoreIndexTypesRenderDistinctly(t *testing.T) {
	if indexTypeName(gosmo.IndexTypeColumnStore) == indexTypeName(gosmo.IndexTypeClusteredColumnStore) {
		t.Fatal("clustered and nonclustered columnstore must not render identically")
	}
}
