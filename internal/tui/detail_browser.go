package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// DetailBrowser shows details of the selected object-explorer node.
// It is a thin Panel wrapper around a tuikit controls.DataGrid, adding a
// title bar and the SQL-Server-specific data loading logic.
type DetailBrowser struct {
	rect  core.Rect
	title string
	grid  *controls.DataGrid
	active bool
}

// NewDetailBrowser creates a detail browser.
func NewDetailBrowser(title string) *DetailBrowser {
	return new(DetailBrowser{title: title, grid: controls.NewDataGrid()})
}

// Title returns the panel title (Panel interface).
func (db *DetailBrowser) Title() string { return db.title }

// SetBounds positions the panel, reserving the first row for the title bar.
func (db *DetailBrowser) SetBounds(x, y, w, h int) {
	db.rect = core.Rect{X: x, Y: y, W: w, H: h}
	db.grid.SetBounds(x, y+1, w, h-1)
}

// SetActive marks this panel focused (affects title bar colour).
func (db *DetailBrowser) SetActive(v bool) { db.active = v }

// ShowNodeDetails loads detail data for the given explorer node.
func (db *DetailBrowser) ShowNodeDetails(app *App, node *explorerNode) {
	if node == nil {
		db.title = "Object Explorer Details"
		db.grid.SetData([]string{"Name", "Type"}, nil)
		return
	}

	db.title = fmt.Sprintf("Object Explorer Details — %s", node.label)
	connIdx := resolveConnIdx(node)

	if connIdx < 0 || connIdx >= len(app.connections) {
		db.grid.SetData([]string{"Property", "Value"}, [][]string{{"Status", "Not connected"}})
		return
	}
	sc := app.connections[connIdx]

	switch node.data.Type {
	case NodeServer:
		info := sc.Server.Info()
		db.grid.SetData(
			[]string{"Property", "Value"},
			[][]string{
				{"Server", sc.Opts.Server},
				{"Version", info.ProductVersion},
				{"Edition", info.Edition},
				{"OS Version", info.OSVersion},
				{"Collation", info.Collation},
				{"Data Path", info.DefaultDataPath},
				{"Log Path", info.DefaultLogPath},
			},
		)

	case NodeDatabases:
		dbs, err := sc.Server.Databases()
		if err != nil {
			db.grid.SetError(err)
			return
		}
		// Size is intentionally omitted here: it requires a separate
		// SpaceUsed() query per database (gosmo has no cached size field
		// on the list result), which would mean N extra round-trips for
		// this list view. See NodeDatabase below for the single-database
		// properties view, where one extra query is reasonable.
		rows := make([][]string, 0, len(dbs))
		for _, d := range dbs {
			rows = append(rows, []string{d.Name(), d.State(), string(d.RecoveryModel())})
		}
		db.grid.SetData([]string{"Name", "State", "Recovery"}, rows)

	case NodeDatabase:
		d, err := sc.Server.DatabaseByName(node.label)
		if err != nil {
			db.grid.SetError(err)
			return
		}
		sizeStr := "N/A"
		if space, err := d.SpaceUsed(); err == nil {
			sizeStr = fmt.Sprintf("%.2f", space.TotalMB)
		}
		db.grid.SetData(
			[]string{"Property", "Value"},
			[][]string{
				{"Name", d.Name()},
				{"State", d.State()},
				{"Recovery Model", string(d.RecoveryModel())},
				{"Compatibility Level", fmt.Sprintf("%d", d.CompatibilityLevel())},
				{"Collation", d.Collation()},
				{"Create Date", formatSQLDate(d.CreateDate())},
				{"Read Only", fmt.Sprintf("%v", d.IsReadOnly())},
				{"Size (MB)", sizeStr},
			},
		)

	case NodeTables:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			db.grid.SetError(err)
			return
		}
		tables, err := dbObj.Tables()
		if err != nil {
			db.grid.SetError(err)
			return
		}
		rows := make([][]string, 0, len(tables))
		for _, t := range tables {
			rows = append(rows, []string{t.Schema + "." + t.Name, "User Table"})
		}
		db.grid.SetData([]string{"Name", "Type"}, rows)

	case NodeViews:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			db.grid.SetError(err)
			return
		}
		views, err := dbObj.Views()
		if err != nil {
			db.grid.SetError(err)
			return
		}
		rows := make([][]string, 0, len(views))
		for _, v := range views {
			rows = append(rows, []string{v.Schema + "." + v.Name, formatSQLDate(v.CreateDate)})
		}
		db.grid.SetData([]string{"Name", "Created"}, rows)

	case NodeStoredProcedures:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			db.grid.SetError(err)
			return
		}
		procs, err := dbObj.StoredProcedures()
		if err != nil {
			db.grid.SetError(err)
			return
		}
		rows := make([][]string, 0, len(procs))
		for _, p := range procs {
			rows = append(rows, []string{p.Schema + "." + p.Name, formatSQLDate(p.CreateDate), formatSQLDate(p.ModifyDate)})
		}
		db.grid.SetData([]string{"Name", "Created", "Modified"}, rows)

	default:
		db.grid.SetData(
			[]string{"Property", "Value"},
			[][]string{
				{"Name", node.label},
				{"Type", nodeTypeName(node.data.Type)},
				{"Database", node.data.DBName},
				{"Schema", node.data.Schema},
			},
		)
	}
}

// Draw renders the title bar and the data grid.
func (db *DetailBrowser) Draw(s tcell.Screen) {
	p := theme.Active()
	titleStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	if db.active {
		titleStyle = tcell.StyleDefault.Background(p.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
	}
	core.FillRect(s, core.Rect{X: db.rect.X, Y: db.rect.Y, W: db.rect.W, H: 1}, ' ', titleStyle)
	core.DrawTextClipped(s, db.rect.X+1, db.rect.Y, db.rect.W-2, titleStyle, db.title)

	db.grid.Draw(s)
}

// HandleKey delegates to the data grid.
func (db *DetailBrowser) HandleKey(ev *tcell.EventKey) bool { return db.grid.HandleKey(ev) }

// HandleMouse delegates to the data grid.
func (db *DetailBrowser) HandleMouse(ev *tcell.EventMouse) bool { return db.grid.HandleMouse(ev) }
