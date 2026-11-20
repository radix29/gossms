package tui

import (
	"strings"
	"testing"

	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
	"github.com/radix29/tuikit/tuikittest"
)

// drawPane draws db and returns the screen.
func drawPane(db *DetailBrowser) *tuikittest.Screen {
	s := tuikittest.NewScreen(100, 30)
	db.Draw(s)
	return s
}

// listAutoAlign exempts a Property/Value record and gives a list
// tuikit's text-cell classifier (its cases are tuikit's TestClassifyTextCell).
func TestListAutoAlignExemptsPropertyValue(t *testing.T) {
	if listAutoAlign([]string{"Property", "Value"}) != nil {
		t.Error("a Property/Value grid should get no classifier")
	}
	if listAutoAlign([]string{"Name", "Row Count"}) == nil {
		t.Error("a list grid should get the classifier")
	}
}

// On screen: the Tables list's figures end at their column's separator, the
// names start at theirs, and a column of "…" placeholders turns right-aligned
// once a backfill writes its figures in place.
func TestDetailBrowserRightAlignsNumbersAfterBackfill(t *testing.T) {
	db, _ := sortTestPane(t)
	snap := drawPane(db).Snapshot()
	for _, want := range []string{"    1,200|", "       35|", "1,000,000|", " dbo.beta "} {
		if !strings.Contains(snap, want) {
			t.Errorf("screen lacks %q:\n%s", want, snap)
		}
	}

	db = NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.currentNode = &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: &dbconn.ServerConn{}}}
	rows := [][]string{{"dbo.a", "…"}, {"dbo.b", "…"}}
	db.applyResult(&detailResult{cols: []string{"Name", "Row Count"}, rows: rows})
	if snap := drawPane(db).Snapshot(); !strings.Contains(snap, "| …    ") {
		t.Errorf("placeholders before the backfill should stay left:\n%s", snap)
	}
	rows[0][1], rows[1][1] = "7", "1,234,567"
	db.grid.RefreshColumnWidths()
	if snap := drawPane(db).Snapshot(); !strings.Contains(snap, "        7|") {
		t.Errorf("backfilled figures should right-align:\n%s", snap)
	}
}

// A Property/Value view keeps its Value column left-aligned, numbers included.
func TestDetailBrowserPropertyValueStaysLeft(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.currentNode = &explorerNode{label: "win10cli", data: nodeData{Type: NodeServer, conn: &dbconn.ServerConn{}}}
	db.applyResult(&detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"CPU Count", "4"}, {"Memory (MB)", "65,536"}}})
	s := drawPane(db)
	x4, _, ok4 := s.Find("| 4 ")
	x6, _, ok6 := s.Find("| 65,536")
	if !ok4 || !ok6 || x4 != x6 {
		t.Errorf("Value cells should start in the same column (4 at %d/%v, 65,536 at %d/%v):\n%s", x4, ok4, x6, ok6, s.Snapshot())
	}
}

// autoAlignFormGrids reaches a page's grids: a Files-page-shaped grid
// right-aligns its sizes, its names stay left, and a grid filled after the
// form was installed (as the pages' own redraws do) follows too, and so does
// a ToggleGridRow's.
func TestAutoAlignFormGridsRightAlignsPageGrids(t *testing.T) {
	grid := controls.NewDataGrid()
	toggle := propsheet.NewToggleGrid([]string{"Name", "Files", "Default"}, []int{2}, 6)
	form := propsheet.NewForm(propsheet.NewGridRow(grid, 8), toggle)
	autoAlignFormGrids(form)
	toggle.SetRows([][]string{{"PRIMARY", "1"}, {"FG2", "12"}}, [][]bool{{true}, {false}})
	toggle.Grid.SetBounds(0, 0, 40, 6)
	ts := tuikittest.NewScreen(40, 6)
	toggle.Grid.Draw(ts)
	if !strings.Contains(ts.Snapshot(), "    1|") {
		t.Errorf("a toggle grid's numeric column should right-align:\n%s", ts.Snapshot())
	}
	grid.SetBounds(0, 0, 60, 8)
	grid.SetData([]string{"Logical Name", "Size (MB)", "Path"}, [][]string{
		{"Clinic_data", "1,024", `C:\data`}, {"Clinic_log", "8", `C:\log`},
	})
	s := tuikittest.NewScreen(60, 8)
	grid.Draw(s)
	snap := s.Snapshot()
	for _, want := range []string{"    1,024|", "        8|", " Clinic_log ", ` C:\log `} {
		if !strings.Contains(snap, want) {
			t.Errorf("screen lacks %q:\n%s", want, snap)
		}
	}
}
