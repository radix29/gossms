package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/dialogs"
)

// exportSub returns the Export Results cascade's item for label.
func exportSub(t *testing.T, item controls.MenuItem, label string) controls.MenuItem {
	t.Helper()
	if item.Label != "Export Results" {
		t.Fatalf("item = %q, want Export Results", item.Label)
	}
	for _, s := range item.Sub {
		if s.Label == label {
			return s
		}
	}
	t.Fatalf("Export Results has no %q: %+v", label, item.Sub)
	return controls.MenuItem{}
}

// Export writes the active set whole, in the order the grid shows it (a column
// sort included), with NULL distinct from the string 'NULL', to the file chosen.
func TestExportResultsWritesTheActiveSetAsShown(t *testing.T) {
	qp, _ := sortTestQueryPanel(t)
	a := qp.app
	a.fileDialog = dialogs.NewFileDialog(nil)
	qp.setResultSort(1, false) // n ascending, NULL last

	items := qp.gridExportMenuItems()
	if len(items) != 1 {
		t.Fatalf("grid menu items = %+v, want Export Results", items)
	}
	exportSub(t, items[0], "To JSON File...").Action()
	path := filepath.Join(t.TempDir(), "out.json")
	a.fileDialog.OnChoose(path)
	drainUntil(t, a, func() bool { _, err := os.Stat(path); return err == nil }, "the export file")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `[
{"name":"beta","n":9,"amount":"10.50"},
{"name":null,"n":100,"amount":"2.00"},
{"name":"delta","n":9007199254740992,"amount":"0.10"},
{"name":"gamma","n":9007199254740993,"amount":null},
{"name":"Alpha","n":null,"amount":"-3.25"}
]
`
	if string(got) != want {
		t.Errorf("export =\n%s\nwant\n%s", got, want)
	}
}

// The cascade is offered on a result set's tab only — in the grid's menus and
// greyed in the File menu — and the cell menu keeps its sort items above it.
func TestExportResultsIsOfferedOnlyOnAResultSetTab(t *testing.T) {
	qp, _ := sortTestQueryPanel(t)
	a := qp.app
	a.panels.AddPanel(qp)
	fileItem := exportResultsMenu(a.activeQueryPanel)
	if !fileItem.Enabled() {
		t.Fatal("File > Export Results disabled on a result set's tab")
	}
	if a.activeQueryPanel() != qp {
		t.Fatal("the test panel is not the active one")
	}
	qp.results.SetSelectedCell(0, 0)
	cell := qp.resultMenuItems()
	if n := len(cell); n != 4 || cell[0].Label != "Sort Ascending" || !cell[2].Divider || cell[3].Label != "Export Results" {
		t.Fatalf("cell menu = %+v, want sort items, divider, Export Results", cell)
	}

	qp.setActiveTab(qp.messagesTabIndex())
	if fileItem.Enabled() {
		t.Error("File > Export Results enabled on the Messages tab")
	}
	if items := qp.gridExportMenuItems(); len(items) != 0 {
		t.Errorf("grid menu offers %+v on the Messages tab", items)
	}
}

// The suggested name is the script's, numbered by set when there are several.
func TestExportFileName(t *testing.T) {
	qp, _ := sortTestQueryPanel(t)
	if got := qp.exportFileName("csv"); got != "results-1.csv" {
		t.Errorf("name = %q, want results-1.csv", got)
	}
	qp.filePath = filepath.Join("dir", "report.sql")
	qp.setActiveTab(1)
	if got := qp.exportFileName("xml"); got != "report-2.xml" {
		t.Errorf("name = %q, want report-2.xml", got)
	}
}
