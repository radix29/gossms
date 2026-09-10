package controls

import (
	"strings"
	"testing"
)

// Sizing columns is on the UI goroutine at every SetSource, SetBounds and
// column drag. One varchar(max)/XML column of large values used to be measured
// in full, cell by cell, though no width past the clamp can matter.
func BenchmarkComputeColWidthsLargeCells(b *testing.B) {
	big := strings.Repeat("<row id=\"1\">你好</row>", 256<<10/24) // ~256 KB, mixed widths
	rows := make([][]string, colWidthSampleRows)
	for i := range rows {
		rows[i] = []string{"42", "some name", big}
	}
	benchmarkColWidths(b, []string{"id", "name", "doc"}, rows)
}

// A grid of ordinary short cells — the common case must not get slower.
func BenchmarkComputeColWidthsShortCells(b *testing.B) {
	rows := make([][]string, colWidthSampleRows)
	for i := range rows {
		rows[i] = []string{"42", "dbo.SomeTable", "2026-09-10 12:34:56.789", "ONLINE", "你好吗"}
	}
	benchmarkColWidths(b, []string{"id", "name", "created", "state", "note"}, rows)
}

func benchmarkColWidths(b *testing.B, cols []string, rows [][]string) {
	g := NewDataGrid()
	g.SetMaxCellWidth(26)
	g.SetData(cols, rows)
	for b.Loop() {
		g.computeColWidths()
	}
}
