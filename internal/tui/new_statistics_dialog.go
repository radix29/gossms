package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// new_statistics_dialog.go is the New Statistics creation dialog (a table's
// Statistics folder, "New Statistics..."). Built on newObjectDialog like
// every other create dialog; what it adds is the table it creates the
// statistic on, the way New Column Master Key adds a database.
//
// The column list is ordered on purpose: the leading column is the one the
// histogram is built on, and only that one. The rest contribute to the
// density vector, so a statistic on (b, a) answers a different set of
// estimates than one on (a, b) — hence the grid with Move Up/Move Down
// rather than a set of checkboxes.

// nstatSamplingItems is the sampling choice, in the order the request reads
// it back: index 0 leaves the server to pick, 1 is a full scan, 2 takes the
// percentage from the row below.
var nstatSamplingItems = []string{"Server default", "Full scan", "Sample percent"}

// nstatPrefetch is the one fetch this dialog needs: the table's columns for
// the picker, and its existing statistic names for the uniqueness preflight.
type nstatPrefetch struct {
	columns       []*gosmo.Column
	existingNames map[string]bool
}

// NewStatisticsDialog is the New Statistics creation dialog.
type NewStatisticsDialog struct {
	newObjectDialog[nstatPrefetch]

	dbName string
	schema string
	table  string
	node   *explorerNode

	name     *propsheet.TextRow
	sampling *propsheet.SelectRow
	samplePc *propsheet.TextRow
	filter   *propsheet.TextRow
	noRecomp *propsheet.CheckRow
	incr     *propsheet.CheckRow

	// columns is the ordered column list the grid edits.
	columns []string
}

// NewNewStatisticsDialog creates the dialog and wires its callbacks.
func NewNewStatisticsDialog(app *App) *NewStatisticsDialog {
	d := &NewStatisticsDialog{}
	d.init(app, newObjectConfig[nstatPrefetch]{
		title:   "New Statistics",
		noun:    "Statistics",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
	})
	return d
}

// show opens the dialog for one table's Statistics folder.
func (d *NewStatisticsDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.node = node
	d.dbName, d.schema, d.table = node.data.DBName, node.data.Schema, node.data.Name
	// Script Changes opens its query window in the database the statement
	// runs in, not the connection's default.
	d.scriptDatabase = d.dbName
	d.columns = nil
	d.newObjectDialog.show(sc)
	d.SetHeader("Database: "+d.dbName, "Table: "+fqn(d.schema, d.table))
}

func (d *NewStatisticsDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*nstatPrefetch, error) {
	t, err := findTable(ctx, sc, d.dbName, d.schema, d.table)
	if err != nil {
		return nil, err
	}
	cols, err := t.ColumnsContext(ctx)
	if err != nil {
		return nil, err
	}
	stats, err := t.StatisticsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(stats))
	for _, st := range stats {
		existing[strings.ToLower(st.Name)] = true
	}
	return &nstatPrefetch{columns: cols, existingNames: existing}, nil
}

func (d *NewStatisticsDialog) buildPages(pf *nstatPrefetch) {
	d.name = propsheet.Text("Name", "ST_"+d.table, 40)
	d.sampling = propsheet.Select("Sampling", nstatSamplingItems, 0)
	d.samplePc = propsheet.Int("Sample percent", 50, 1, 100, "%")
	d.filter = propsheet.Text("Filter predicate", "", 60)
	d.noRecomp = propsheet.Check("Do not recompute automatically", false)
	d.incr = propsheet.Check("Incremental (per partition)", false)

	rows := []propsheet.Row{
		propsheet.Section("Statistics"),
		d.name,
		propsheet.Static("Table", fqn(d.schema, d.table)),
	}
	rows = append(rows, orderedColumnRows(&d.columns, nidxColumnNames(pf.columns),
		"Columns", "This table has no columns.")...)
	rows = append(rows,
		propsheet.Section("Sampling"),
		d.sampling, d.samplePc,
		propsheet.Section("Filter"),
		d.filter,
		propsheet.Section("Options"),
		d.noRecomp, d.incr,
		propsheet.Note("The first column is the one the histogram is built on; the rest only contribute to the density vector, so the order matters. Sample percent applies only when Sampling is set to it."),
		propsheet.Note("Incremental builds the statistic per partition and requires a partitioned table. Do not recompute leaves the statistic unchanged until UPDATE STATISTICS is run against it."),
	)

	d.forms[0] = propsheet.NewForm(rows...)
	d.applyFns[0] = d.createStatistic
	d.objectName = func() string { return strings.TrimSpace(d.name.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("statistics name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a statistics object named %q already exists on %s", name, fqn(d.schema, d.table))
		}
		if len(d.columns) == 0 {
			return fmt.Errorf("add at least one column")
		}
		return nil
	}
}

func (d *NewStatisticsDialog) createStatistic(ctx context.Context) error {
	t, err := findTable(ctx, d.sc, d.dbName, d.schema, d.table)
	if err != nil {
		return err
	}
	return t.CreateStatisticWithOptionsContext(ctx, d.request())
}

// request assembles the CreateStatisticRequest from the form.
func (d *NewStatisticsDialog) request() gosmo.CreateStatisticRequest {
	req := gosmo.CreateStatisticRequest{
		Name:             d.objectName(),
		Columns:          d.columns,
		FilterDefinition: strings.TrimSpace(d.filter.Value()),
		NoRecompute:      d.noRecomp.Checked(),
		Incremental:      d.incr.Checked(),
	}
	switch d.sampling.Selected() {
	case 1:
		req.FullScan = true
	case 2:
		if n, err := d.samplePc.IntValue(); err == nil {
			req.SamplePercent = int(n)
		}
	}
	return req
}

// orderedColumnRows is an ordered column picker: a grid of what has been
// chosen, in order, plus Add/Remove/Move Up/Move Down over the available
// names. list is the model the buttons edit — the caller's own slice, so it
// reads the result back without a callback.
//
// The order is the point. A statistics object's leading column is the only
// one that gets a histogram, so a picker that lost the order would quietly
// build a different statistic from the one the user described.
func orderedColumnRows(list *[]string, available []string, section, emptyNote string) []propsheet.Row {
	headers := []string{"Ord", "Column name"}
	rowsFor := func() [][]string {
		out := make([][]string, len(*list))
		for i, name := range *list {
			out[i] = []string{strconv.Itoa(i + 1), name}
		}
		return out
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)

	addRow := propsheet.Select("Column to add", available, 0)
	hint := propsheet.Hint()

	current := -1
	noCommit := func() {}
	sync := func() { current = grid.SelectedRow() }
	reload := wireGridEditor(grid, headers, rowsFor, noCommit, sync)

	move := func(delta int) {
		to := current + delta
		if current < 0 || current >= len(*list) || to < 0 || to >= len(*list) {
			return
		}
		(*list)[current], (*list)[to] = (*list)[to], (*list)[current]
		reload()
		grid.SetSelectedCell(to, 0)
		sync()
	}

	addBtn := widgets.NewButton("Add", func() {
		name := addRow.Value()
		if name == "" {
			return
		}
		if slices.Contains(*list, name) {
			hint.SetError(name + " is already in the list.")
			return
		}
		hint.Clear()
		*list = append(*list, name)
		reload()
	})
	removeBtn := widgets.NewButton("Remove", func() {
		if current < 0 || current >= len(*list) {
			hint.Set("Select a column to remove.")
			return
		}
		hint.Clear()
		*list = slices.Delete(*list, current, current+1)
		current = -1
		reload()
	})

	gridRow := propsheet.NewGridRow(grid, 6)
	// The grid mirrors a list the buttons own, so its dirty state is that
	// list's and reverting restores the list rather than the rows.
	gridRow.DirtyFn = func() bool { return len(*list) > 0 }
	gridRow.RevertFn = func() {
		*list = nil
		current = -1
		reload()
	}

	rows := []propsheet.Row{
		propsheet.Section(section),
		gridRow,
		addRow,
		propsheet.Buttons(addBtn, removeBtn,
			widgets.NewButton("Move Up", func() { move(-1) }),
			widgets.NewButton("Move Down", func() { move(1) })),
		hint,
	}
	if len(available) == 0 {
		rows = append(rows, propsheet.Note(emptyNote))
	}
	return rows
}
