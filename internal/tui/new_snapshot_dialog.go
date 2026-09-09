package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// new_snapshot_dialog.go is "New Snapshot..." — CREATE DATABASE … AS SNAPSHOT
// OF, on the Database Snapshots folder and on a database.
//
// The file grid is empty until it is asked for, and an empty grid is a
// complete request: gosmo derives one sparse file per ROWS file of the source
// when none is given, which is also the only way to get paths that match the
// name currently typed. Pressing Default File Paths freezes them, so the
// button re-runs whenever the source or the name changes rather than leaving
// a list built for a different snapshot.
//
// Only data files are ever listed. A snapshot has no transaction log and no
// FILESTREAM container, and naming either is the usual way a hand-written
// CREATE DATABASE … AS SNAPSHOT OF fails.

// snapshotPrefetch is what the dialog needs before anything is typed: which
// databases can be snapshotted, and every name already taken.
type snapshotPrefetch struct {
	// sources are the user databases that are online and not themselves
	// snapshots — a snapshot of a snapshot is refused by the server, and an
	// offline database cannot be read at all.
	sources []string
	// existing is every database name on the instance, lower-cased, snapshots
	// included: the new snapshot is a database and collides with all of them.
	existing map[string]bool
}

// NewSnapshotDialog is the New Database Snapshot dialog.
type NewSnapshotDialog struct {
	newObjectDialog[snapshotPrefetch]

	// source is the database the dialog was opened on, empty when it was
	// opened from the folder and the source is still to be picked.
	source string

	// files is the sparse-file list, empty until Default File Paths has been
	// pressed. Empty is a complete request — see the file comment.
	files []gosmo.SnapshotFileSpec

	// reading latches the one round trip to SnapshotFileDefaults, so a second
	// press cannot start another over the top of it.
	reading bool
}

// NewNewSnapshotDialog creates the dialog and wires its callbacks.
func NewNewSnapshotDialog(app *App) *NewSnapshotDialog {
	d := &NewSnapshotDialog{}
	d.init(app, newObjectConfig[snapshotPrefetch]{
		title:   "New Database Snapshot",
		noun:    "Database snapshot",
		verb:    "created",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.explorer.RefreshDatabasesFolder(sc) },
	})
	return d
}

func (d *NewSnapshotDialog) show(sc *db.ServerConn, source string) {
	d.source = source
	d.files = nil
	d.reading = false
	d.newObjectDialog.show(sc)
	subtitle := "Read-only, point-in-time"
	if source != "" {
		subtitle = "Of " + source
	}
	d.SetHeader("Server: "+sc.Opts.Server, subtitle)
}

func (d *NewSnapshotDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*snapshotPrefetch, error) {
	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	pf := &snapshotPrefetch{existing: make(map[string]bool, len(dbs))}
	for _, dbObj := range dbs {
		pf.existing[strings.ToLower(dbObj.Name())] = true
		if dbObj.IsSystem() || dbObj.IsSnapshot() || dbObj.State() != "ONLINE" {
			continue
		}
		pf.sources = append(pf.sources, dbObj.Name())
	}
	return pf, nil
}

func (d *NewSnapshotDialog) buildPages(pf *snapshotPrefetch) {
	sc := d.sc

	sources := pf.sources
	if len(sources) == 0 {
		// Nothing to snapshot. The dialog still opens — preflight is what
		// refuses, with a sentence rather than an empty picker.
		sources = []string{""}
	}
	selected := slices.Index(sources, d.source)
	if selected < 0 {
		selected = 0
	}
	sourceRow := propsheet.Select("Source database", sources, selected)
	nameRow := propsheet.Text("Snapshot name", defaultSnapshotName(sources[selected]), 40)
	pathRow := propsheet.Text("Path of selected file", "", 48)
	hint := propsheet.Hint()

	headers := []string{"Source file", "Sparse file on the server"}
	fileRows := func() [][]string {
		rows := make([][]string, len(d.files))
		for i, f := range d.files {
			rows[i] = []string{f.LogicalName, f.FileName}
		}
		return rows
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, fileRows())
	grid.SetCellCursor(true)

	current := -1
	commitCurrent := func() {
		if current >= 0 && current < len(d.files) {
			d.files[current].FileName = strings.TrimSpace(pathRow.Value())
		}
	}
	syncFromSelection := func() {
		current = grid.SelectedRow()
		if current < 0 || current >= len(d.files) {
			pathRow.SetValue("")
			return
		}
		pathRow.SetValue(d.files[current].FileName)
	}
	reloadGrid := wireGridEditor(grid, headers, fileRows, commitCurrent, syncFromSelection)

	// A source or name change invalidates paths that were derived from the
	// old ones. Dropping them is what puts the request back on gosmo's
	// defaults, which are always in step with what is typed.
	invalidateFiles := func() {
		if len(d.files) == 0 {
			return
		}
		d.files = nil
		current = -1
		pathRow.SetValue("")
		reloadGrid()
		hint.Set("The file paths were built for a different snapshot and have been cleared. They will be defaulted from the source.")
	}
	sourceRow.SetOnChange(func(name string) {
		if strings.TrimSpace(nameRow.Value()) == "" || isDefaultSnapshotName(nameRow.Value(), sources) {
			nameRow.SetValue(defaultSnapshotName(name))
		}
		invalidateFiles()
	})
	nameRow.SetOnChange(func(string) { invalidateFiles() })

	defaultsBtn := widgets.NewButton("Default File Paths", func() {
		source := sourceRow.Value()
		name := strings.TrimSpace(nameRow.Value())
		if source == "" || name == "" {
			hint.SetError("Pick a source database and type a snapshot name first.")
			return
		}
		if d.reading {
			return
		}
		d.reading = true
		hint.Set("Reading " + source + "'s data files...")
		sessionCtx := d.ctx
		d.app.safegoRepair("reading a snapshot's default file paths", d.readPanicked, func() {
			ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
			defer cancel()
			specs, err := sc.Server.SnapshotFileDefaultsContext(ctx, source, name)
			d.app.postAndWake(func() {
				if d.ctx != sessionCtx {
					return // the dialog was closed and reopened while this was out
				}
				d.reading = false
				if err != nil {
					d.files = nil
					hint.SetError(displayError(err).Error())
				} else {
					d.files = specs
					hint.Set(fmt.Sprintf("%d data file(s). Edit any path that should go elsewhere.", len(specs)))
				}
				current = -1
				pathRow.SetValue("")
				reloadGrid()
			})
		})
	})

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Snapshot"),
		sourceRow,
		nameRow,
		propsheet.Note("A snapshot is read-only and cannot be renamed or altered. Reverting the source to it needs it to be the source's only snapshot."),
		propsheet.Section("Sparse files"),
		propsheet.Buttons(defaultsBtn),
		hint,
		propsheet.NewGridRow(grid, 8),
		pathRow,
		propsheet.Note("Left empty, one sparse file is placed beside each of the source's data files. The paths are on the SQL Server host, and each directory must already exist and be writable by the service account."),
		propsheet.Note("Only data files are listed: a snapshot has no transaction log, and naming one is refused."),
	)

	d.objectName = func() string { return strings.TrimSpace(nameRow.Value()) }
	d.preflight = func() error {
		if len(pf.sources) == 0 {
			return fmt.Errorf("this instance has no online user database to snapshot")
		}
		if sourceRow.Value() == "" {
			return fmt.Errorf("a source database is required")
		}
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("a name for the snapshot is required")
		}
		if pf.existing[strings.ToLower(name)] {
			return fmt.Errorf("this instance already has a database called %q — a snapshot is a database and needs a free name", name)
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		commitCurrent()
		_, err := sc.Server.CreateDatabaseSnapshotContext(ctx, gosmo.CreateDatabaseSnapshotRequest{
			Name:           d.objectName(),
			SourceDatabase: sourceRow.Value(),
			Files:          d.files,
		})
		return err
	}
}

// readPanicked releases the read latch after a panic in the defaults
// goroutine — its App.safegoRepair step. Without it the button is dead for
// the rest of the dialog's life and the hint sits on "Reading..." forever.
func (d *NewSnapshotDialog) readPanicked() { d.reading = false }

// defaultSnapshotName is the name offered for a snapshot of source. SSMS
// offers <source>_snapshot_<timestamp>; the timestamp is left off here
// because the name is the one thing the user is certain to retype, and a
// second snapshot of the same source only needs the suffix changed.
func defaultSnapshotName(source string) string {
	if source == "" {
		return ""
	}
	return source + "_snapshot"
}

// isDefaultSnapshotName reports whether name is still one of the offered
// defaults — what decides whether changing the source may rewrite it. A name
// the user typed is never overwritten.
func isDefaultSnapshotName(name string, sources []string) bool {
	name = strings.TrimSpace(name)
	for _, s := range sources {
		if name == defaultSnapshotName(s) {
			return true
		}
	}
	return false
}

// showNewSnapshotDialog opens New Database Snapshot. source is the database
// to snapshot, empty when the dialog was opened from the folder.
func (a *App) showNewSnapshotDialog(sc *db.ServerConn, source string) {
	if !a.requireConn(sc) {
		return
	}
	a.newSnapshotDialog.show(sc, source)
}
