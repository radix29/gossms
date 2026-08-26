package tui

import (
	"context"
	"fmt"
	"sort"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// detach_database_dialog.go is "Detach Database..." on a database — SSMS's
// Tasks > Detach, on the newObjectDialog shell so OK/Apply/Script Changes
// behave like every other write dialog. What it produces is the removal of a
// database rather than an object, which is why it overrides the shell's
// success verb.
//
// The file grid is not decoration. Detaching leaves the files on disk and
// takes away the only place their paths are recorded that a client can read:
// sys.database_files is behind a USE, and the detached database has none. The
// paths shown here are what an attach needs afterwards, primary data file
// first.

// detachPrefetch is what the page needs to describe a detach before it runs.
type detachPrefetch struct {
	state string

	// files are the database's files, primary data file first. Empty only
	// when neither catalog would answer — see fetchPrefetch.
	files []*gosmo.DatabaseFileInfo

	// sessions counts the other sessions inside the database. -1 when the
	// login cannot read sys.dm_exec_sessions — an unmeasured count is not a
	// reason to refuse the detach, only a reason not to promise it will work.
	sessions int
}

// DetachDatabaseDialog is the Detach Database dialog.
type DetachDatabaseDialog struct {
	newObjectDialog[detachPrefetch]

	// dbName is set by show, before the shell's own show runs.
	dbName string
}

// NewDetachDatabaseDialog creates the dialog and wires its callbacks.
func NewDetachDatabaseDialog(app *App) *DetachDatabaseDialog {
	d := &DetachDatabaseDialog{}
	d.init(app, newObjectConfig[detachPrefetch]{
		title:   "Detach Database",
		noun:    "Database",
		verb:    "detached",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.explorer.RefreshDatabasesFolder(sc) },
	})
	return d
}

func (d *DetachDatabaseDialog) show(sc *db.ServerConn, dbName string) {
	d.dbName = dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Database: "+dbName, "Server: "+sc.Opts.Server)
}

// fetchPrefetch reads the state, the file list and the session count. Only the
// state read is allowed to fail the dialog: a detach can proceed without
// either of the other two, and refusing to open over a missing VIEW SERVER
// STATE would withhold the action from a db_owner who can perform it.
func (d *DetachDatabaseDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*detachPrefetch, error) {
	dbase, err := sc.Server.DatabaseByNameContext(ctx, d.dbName)
	if err != nil {
		return nil, err
	}
	pf := &detachPrefetch{state: dbase.State(), sessions: -1}
	if pf.state == "ONLINE" {
		if files, err := dbase.FilesContext(ctx); err == nil {
			pf.files = sortDatabaseFiles(files)
		}
	}
	// sys.database_files is read through a USE, which an OFFLINE, SUSPECT or
	// RECOVERY_PENDING database refuses — the states someone is most likely
	// to be detaching from, and the paths are what the detach destroys. The
	// server catalog answers in any state.
	if len(pf.files) == 0 {
		if files, err := sc.Server.DatabaseFilesContext(ctx, d.dbName); err == nil {
			pf.files = sortDatabaseFiles(files)
		}
	}
	if sessions, err := sc.Server.ActiveSessionsContext(ctx, false); err == nil {
		pf.sessions = 0
		for _, s := range sessions {
			if s.DatabaseName == d.dbName {
				pf.sessions++
			}
		}
	}
	return pf, nil
}

// sortDatabaseFiles puts the data files ahead of the log, each by file ID, so
// the first row is the primary data file an attach starts from. gosmo's own
// order is by type_desc, which sorts LOG first.
func sortDatabaseFiles(files []*gosmo.DatabaseFileInfo) []*gosmo.DatabaseFileInfo {
	out := append([]*gosmo.DatabaseFileInfo(nil), files...)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].Type == "LOG", out[j].Type == "LOG"; a != b {
			return b
		}
		return out[i].FileID < out[j].FileID
	})
	return out
}

// detachSessionsText renders the session count for the read-only row.
func detachSessionsText(n int) string {
	switch {
	case n < 0:
		return "unknown — needs VIEW SERVER STATE"
	case n == 0:
		return "none"
	case n == 1:
		return "1 session is using the database"
	default:
		return fmt.Sprintf("%d sessions are using the database", n)
	}
}

func (d *DetachDatabaseDialog) buildPages(pf *detachPrefetch) {
	sc := d.sc
	dbName := d.dbName

	dropConns := propsheet.Check("Drop connections", false)
	updateStats := propsheet.Check("Update statistics", false)
	dropFullText := propsheet.Check("Drop full-text index files", false)

	rows := []propsheet.Row{
		propsheet.Section("Database to detach"),
		propsheet.Static("Database", dbName),
		propsheet.Static("State", pf.state),
		propsheet.Static("Sessions", detachSessionsText(pf.sessions)),
		propsheet.Section("Options"),
		dropConns,
		propsheet.Note("Rolls back and closes every other session in the database first. Without it, a database anything else is connected to refuses to detach — and that includes the pooled connections goSSMS itself leaves behind after browsing it."),
		updateStats,
		propsheet.Note("Rescans every statistics object before detaching, so the statistics survive into whatever attaches the files next. On a large database this is the slow part."),
		dropFullText,
		propsheet.Note("Deletes the full-text index files instead of leaving them beside the data files."),
		propsheet.Section("Files left on disk"),
	}
	if len(pf.files) == 0 {
		rows = append(rows, propsheet.Note(fmt.Sprintf(
			"The file list of %q (%s) could not be read — reading it from the server catalog needs one of CREATE ANY DATABASE, ALTER ANY DATABASE or VIEW ANY DEFINITION. Note the paths before detaching — nothing reports them afterwards, and attaching the database again starts from its primary data file's path.",
			dbName, pf.state)))
	} else {
		grid := controls.NewDataGrid()
		grid.SetData([]string{"File", "Type", "Path on the server"}, detachFileRows(pf.files))
		grid.SetCellCursor(true)
		rows = append(rows,
			// +3 for the grid's own header, rule and row-count footer: a
			// GridRow sized to the file count alone draws no files at all.
			propsheet.NewGridRow(grid, min(len(pf.files)+3, 11)),
			propsheet.Note("Nothing on disk is deleted. These paths are the whole record of where the database went once it is detached — the first row is the primary data file an attach starts from."),
		)
	}

	d.forms[0] = propsheet.NewForm(rows...)
	d.objectName = func() string { return dbName }
	d.preflight = func() error {
		if pf.sessions > 0 && !dropConns.Checked() {
			return fmt.Errorf("%s — tick Drop connections to close them, or close them yourself first",
				detachSessionsText(pf.sessions))
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		return sc.Server.DetachDatabaseContext(ctx, dbName, gosmo.DetachOptions{
			DropConnections:       dropConns.Checked(),
			UpdateStatistics:      updateStats.Checked(),
			DropFullTextIndexFile: dropFullText.Checked(),
		})
	}
}

// detachFileRows renders the file list as grid rows.
func detachFileRows(files []*gosmo.DatabaseFileInfo) [][]string {
	rows := make([][]string, len(files))
	for i, f := range files {
		rows[i] = []string{f.Name, f.Type, f.PhysicalName}
	}
	return rows
}

// showDetachDatabaseDialog opens Detach Database for one database — the Object
// Explorer context menu's entry point. The detached database is gone from the
// folder rather than merely changed, so the dialog reloads the whole Databases
// folder and needs no tree node.
func (a *App) showDetachDatabaseDialog(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.detachDatabaseDialog.show(sc, dbName)
}
