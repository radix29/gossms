package cmd

import (
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// Each type family gets the aggregates SQL Server accepts on it (verified
// live on 17: MIN(bit), COUNT(DISTINCT xml/text/geography), MIN(xml) and
// LEN(text/sql_variant) are Msg 8117/8116), pinned by type name.
func TestProfileAggForTypeFamilies(t *testing.T) {
	full := profileAgg{distinct: true, minMax: "%s"}
	for _, tc := range []struct {
		types []string
		want  profileAgg
	}{
		{[]string{"bit", "BIT"}, profileAgg{distinct: true, minMax: "CAST(%s AS tinyint)"}},
		{[]string{"char", "varchar", "nchar", "NVarChar", "sysname"}, profileAgg{distinct: true, minMax: "%s", length: "LEN"}},
		{[]string{"binary", "varbinary"}, profileAgg{distinct: true, minMax: "%s", length: "DATALENGTH"}},
		{[]string{"tinyint", "smallint", "int", "bigint", "decimal", "numeric", "money", "smallmoney", "float", "real"}, full},
		{[]string{"date", "time", "datetime", "datetime2", "smalldatetime", "datetimeoffset"}, full},
		{[]string{"uniqueidentifier", "sql_variant", "hierarchyid", "rowversion", "timestamp"}, full},
		{[]string{"xml", "text", "ntext", "image", "geography", "geometry", "vector", "json", ""}, profileAgg{}},
	} {
		for _, typ := range tc.types {
			if got := profileAggFor(typ); got != tc.want {
				t.Errorf("%q: %+v, want %+v", typ, got, tc.want)
			}
		}
	}
}

func TestProfiledColumnsResolveTheBaseType(t *testing.T) {
	cols := []*gosmo.Column{
		// An alias type: its base type's aggregates.
		{Name: "phone", DataType: "Phone", TypeSchema: "dbo", IsUserDefinedType: true},
		// A user-defined type that is no alias: a CLR type.
		{Name: "pt", DataType: "Point", TypeSchema: "dbo", IsUserDefinedType: true},
		// A type the login cannot see: DataType is its base type.
		{Name: "hidden", DataType: "int", IsUserDefinedType: true, IsTypeHidden: true},
		// Ciphertext has no order.
		{Name: "ssn", DataType: "char", ColumnEncryptionKey: "CEK1"},
		{Name: "id", DataType: "int"},
	}
	got := profiledColumns(cols, map[string]string{"dbo.Phone": "varchar"})
	want := []profileAgg{
		{distinct: true, minMax: "%s", length: "LEN"}, {}, {distinct: true, minMax: "%s"}, {}, {distinct: true, minMax: "%s"},
	}
	for i := range want {
		if got[i].col != cols[i] || got[i].agg != want[i] {
			t.Errorf("%s: %+v, want %+v", cols[i].Name, got[i].agg, want[i])
		}
	}
}

func TestProfileableLeavesOutInternalColumns(t *testing.T) {
	for _, tc := range []struct {
		c    gosmo.Column
		want bool
	}{
		{gosmo.Column{Name: "id"}, true},
		{gosmo.Column{Name: "ValidFrom", IsHidden: true, GeneratedAlwaysType: 1}, true},
		{gosmo.Column{Name: "graph_id_X", IsHidden: true, GraphType: gosmo.GraphColumnID}, false},
		{gosmo.Column{Name: "$node_id_X", GraphType: gosmo.GraphColumnIDComputed}, false},
		{gosmo.Column{Name: "MSSQL_DroppedLedgerColumn_x", IsDroppedLedgerColumn: true}, false},
	} {
		if got := profileable(&tc.c); got != tc.want {
			t.Errorf("%s: %v", tc.c.Name, got)
		}
	}
}

func TestProfileStatementsChunk(t *testing.T) {
	var cols []profiledColumn
	for i := range 2*maxProfileColumns + 1 {
		cols = append(cols, profiledColumn{col: &gosmo.Column{Name: "c" + string(rune('a'+i%26))}, agg: profileAgg{}})
	}
	stmts := profileStatements(cols, "[dbo].[t]", fragments{where: "x = 1"})
	if len(stmts) != 3 {
		t.Fatalf("%d statements", len(stmts))
	}
	for i, s := range stmts {
		if !strings.HasPrefix(s, "SELECT COUNT_BIG(*) AS [rows],") || !strings.HasSuffix(s, "\nFROM [dbo].[t]\nWHERE (x = 1\n)") {
			t.Errorf("statement %d:\n%s", i, s)
		}
	}
	// The aliases restart in each statement: index within the chunk.
	if !strings.Contains(stmts[2], "[nulls_0]") || strings.Contains(stmts[2], "[nulls_1]") {
		t.Errorf("last chunk:\n%s", stmts[2])
	}
	if got := profileStatements(nil, "[dbo].[t]", fragments{}); len(got) != 1 || got[0] != "SELECT COUNT_BIG(*) AS [rows]\nFROM [dbo].[t]" {
		t.Errorf("no columns: %q", got)
	}
}

func TestNullPct(t *testing.T) {
	s := func(v string) *string { return &v }
	for _, tc := range []struct {
		nulls, rows *string
		want        any
	}{
		{s("1"), s("3"), "33.33"}, {s("0"), s("5"), "0.00"}, {s("2"), s("2"), "100.00"},
		{s("0"), s("0"), nil}, {nil, s("1"), nil}, {s("1"), nil, nil},
	} {
		got := nullPct(tc.nulls, tc.rows)
		if (got == nil) != (tc.want == nil) || got != nil && *got != tc.want {
			t.Errorf("%v/%v: %v, want %v", tc.nulls, tc.rows, got, tc.want)
		}
	}
}
