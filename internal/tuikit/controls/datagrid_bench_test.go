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

// constRows is a RowSource of n identical rows that holds none of them: the
// selection benchmarks measure the selection, not a million-row slice.
type constRows int

func (n constRows) Len() int       { return int(n) }
func (constRows) Row(int) []string { return []string{"42", "name"} }

// newBigBlockGrid is a read-only cell-cursor grid of n rows with all of them
// Shift-selected — the state a Ctrl+click then folds into a discontiguous
// selection.
func newBigBlockGrid(n int) *DataGrid {
	g := newTestDataGrid()
	g.SetSource([]string{"id", "name"}, constRows(n))
	g.SetCellCursor(true)
	g.selAnchorRow, g.selRow, g.blockSelecting = 0, n-1, true
	return g
}

// A Ctrl+click after selecting every row of a large result runs on the UI
// goroutine. It used to copy the whole run into a map first.
func BenchmarkMarkRowAfterSelectAll(b *testing.B) {
	g := newBigBlockGrid(1_000_000)
	for b.Loop() {
		g.ClearMarkedRows() // back to the Shift-selected run, so each click is the first
		g.markRow(500_000)
	}
}

// Copy, and the context menu's row count, read selectedRows after such a
// Ctrl+click: it used to sort the map's million keys.
func BenchmarkSelectedRowsAfterCtrlClick(b *testing.B) {
	g := newBigBlockGrid(1_000_000)
	g.markRow(500_000)
	for b.Loop() {
		g.SelectedRows()
	}
}
