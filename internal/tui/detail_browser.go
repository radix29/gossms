package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// DetailBrowser shows details of the selected object-explorer node.
// It is a thin Panel wrapper around a tuikit controls.DataGrid, adding a
// title bar and the SQL-Server-specific data loading logic.
type DetailBrowser struct {
	rect   core.Rect
	title  string
	grid   *controls.DataGrid
	active bool

	// seq guards against a slow, superseded fetch (see ShowNodeDetails)
	// overwriting the grid with results for a node that's no longer
	// selected — incremented on every call, and the async result is only
	// applied if it's still the most recent one requested.
	seq int
}

// NewDetailBrowser creates a detail browser.
func NewDetailBrowser(title string) *DetailBrowser {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	return new(DetailBrowser{title: title, grid: grid})
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

// Closable reports false: Object Explorer Details is a fixed, always-
// present panel (there is exactly one, added once in App.buildUI) and
// can't be closed via the tab bar's [x] or Ctrl+W — see layout.Closable.
func (db *DetailBrowser) Closable() bool { return false }

// ShowNodeDetails loads detail data for the given explorer node,
// asynchronously — every case in fetchNodeDetails is a real network round
// trip, and this fires on every tree-selection change, so running it
// inline on the UI goroutine would freeze the whole app on each arrow-key
// press against a slow or remote server.
func (db *DetailBrowser) ShowNodeDetails(app *App, node *explorerNode) {
	db.seq++
	seq := db.seq

	if node == nil {
		db.title = "Object Explorer Details"
		db.grid.SetData([]string{"Name", "Type"}, nil)
		return
	}

	db.title = fmt.Sprintf("Object Explorer Details — %s", node.label)
	sc := resolveConn(node)

	if !app.isConnected(sc) {
		db.grid.SetData([]string{"Property", "Value"}, [][]string{{"Status", "Not connected"}})
		return
	}
	db.grid.SetStatus("Loading...")

	go func() {
		cols, rows, err := fetchNodeDetails(sc, node)
		app.postEvent(func() {
			if seq != db.seq {
				return // a newer selection superseded this fetch
			}
			if err != nil {
				db.grid.SetError(err)
				return
			}
			db.grid.SetData(cols, rows)
		})
		app.wakeEventLoop()
	}()
}

// fetchNodeDetails runs the gosmo queries for a node's detail grid. Called
// from a background goroutine (see ShowNodeDetails) — it must not touch
// DetailBrowser or any other UI state directly, only return data for the
// caller to apply via postEvent.
func fetchNodeDetails(sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	switch node.data.Type {
	case NodeServer:
		info := sc.Server.Info()
		return []string{"Property", "Value"}, [][]string{
			{"Server", sc.Opts.Server},
			{"Version", info.ProductVersion},
			{"Edition", info.Edition},
			{"OS Version", info.OSVersion},
			{"Collation", info.Collation},
			{"Data Path", info.DefaultDataPath},
			{"Log Path", info.DefaultLogPath},
		}, nil

	case NodeDatabases:
		dbs, err := sc.Server.Databases()
		if err != nil {
			return nil, nil, err
		}
		// Size is omitted from this list view (it would need a separate
		// SpaceUsed() query per database). See NodeDatabase below for the
		// single-database properties view, which includes it. System
		// databases are excluded here — they're listed under the System
		// Databases node instead, mirroring the tree.
		rows := make([][]string, 0, len(dbs))
		for _, d := range dbs {
			if d.IsSystem() {
				continue
			}
			rows = append(rows, []string{d.Name(), d.State(), string(d.RecoveryModel())})
		}
		return []string{"Name", "State", "Recovery"}, rows, nil

	case NodeSystemDatabases:
		dbs, err := sc.Server.Databases()
		if err != nil {
			return nil, nil, err
		}
		rows := make([][]string, 0, 4)
		for _, d := range dbs {
			if d.IsSystem() {
				rows = append(rows, []string{d.Name(), d.State(), string(d.RecoveryModel())})
			}
		}
		return []string{"Name", "State", "Recovery"}, rows, nil

	case NodeDatabase:
		d, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return nil, nil, err
		}
		sizeStr := "N/A"
		if space, err := d.SpaceUsed(); err == nil {
			sizeStr = fmt.Sprintf("%.2f", space.TotalMB)
		}
		return []string{"Property", "Value"}, [][]string{
			{"Name", d.Name()},
			{"State", d.State()},
			{"Recovery Model", string(d.RecoveryModel())},
			{"Compatibility Level", fmt.Sprintf("%d", d.CompatibilityLevel())},
			{"Collation", d.Collation()},
			{"Create Date", formatSQLDate(d.CreateDate())},
			{"Read Only", fmt.Sprintf("%v", d.IsReadOnly())},
			{"Size (MB)", sizeStr},
		}, nil

	case NodeTables:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return nil, nil, err
		}
		tables, err := dbObj.Tables()
		if err != nil {
			return nil, nil, err
		}
		rows := make([][]string, 0, len(tables))
		for _, t := range tables {
			rows = append(rows, []string{t.Schema + "." + t.Name, "User Table"})
		}
		return []string{"Name", "Type"}, rows, nil

	case NodeViews:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return nil, nil, err
		}
		views, err := dbObj.Views()
		if err != nil {
			return nil, nil, err
		}
		rows := make([][]string, 0, len(views))
		for _, v := range views {
			rows = append(rows, []string{v.Schema + "." + v.Name, formatSQLDate(v.CreateDate)})
		}
		return []string{"Name", "Created"}, rows, nil

	case NodeStoredProcedures:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return nil, nil, err
		}
		procs, err := dbObj.StoredProcedures()
		if err != nil {
			return nil, nil, err
		}
		rows := make([][]string, 0, len(procs))
		for _, p := range procs {
			rows = append(rows, []string{p.Schema + "." + p.Name, formatSQLDate(p.CreateDate), formatSQLDate(p.ModifyDate)})
		}
		return []string{"Name", "Created", "Modified"}, rows, nil

	default:
		return []string{"Property", "Value"}, [][]string{
			{"Name", node.label},
			{"Type", nodeTypeName(node.data.Type)},
			{"Database", node.data.DBName},
			{"Schema", node.data.Schema},
		}, nil
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
	db.grid.DrawOverlay(s)
}

// HandleKey delegates to the data grid.
func (db *DetailBrowser) HandleKey(ev *tcell.EventKey) bool { return db.grid.HandleKey(ev) }

// HandleMouse delegates to the data grid.
func (db *DetailBrowser) HandleMouse(ev *tcell.EventMouse) bool { return db.grid.HandleMouse(ev) }

// HasSelection, SelectedText, Cut, Paste, and SelectAll implement
// clipboardTarget (see internal/tui/clipboard.go) by forwarding to the
// grid, which is itself only a real clipboard target while its built-in
// "Show Value" content viewer is open (see controls.DataGrid.HasSelection).
func (db *DetailBrowser) HasSelection() bool   { return db.grid.HasSelection() }
func (db *DetailBrowser) SelectedText() string { return db.grid.SelectedText() }
func (db *DetailBrowser) Cut() string          { return db.grid.Cut() }
func (db *DetailBrowser) Paste(text string)    { db.grid.Paste(text) }
func (db *DetailBrowser) SelectAll()           { db.grid.SelectAll() }
