package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// attach_database_dialog.go is "Attach Database..." on the Databases folder —
// SSMS's Attach Databases, and the other half of Detach.
//
// The dialog exists because of one case: files that have moved. SQL Server
// finds a database's secondary and log files by itself only at the paths
// recorded inside the primary file, which is exactly what stops being true
// once someone copies the files elsewhere. Reading that recorded list back is
// what makes the paths correctable — see gosmo's DetachedDatabaseInfoContext,
// which goes through the undocumented DBCC CHECKPRIMARYFILE.

// attachPrefetch is what the page needs before anything is typed.
type attachPrefetch struct {
	// dataPath is the instance's default data directory, where Browse starts.
	dataPath string
	// existing is every database name already on the instance, lower-cased. A
	// collision is rejected here rather than by CREATE DATABASE, which reports
	// it after the file list has been typed out.
	existing map[string]bool
}

// AttachDatabaseDialog is the Attach Database dialog.
type AttachDatabaseDialog struct {
	newObjectDialog[attachPrefetch]

	// files is the detached database's file list as read out of its primary
	// file, with PhysicalName editable: an attach whose files have moved is
	// the case this dialog exists for. Empty until a primary file has been
	// read, and legitimately still empty after a read that was refused — see
	// attachFilePaths.
	files []*gosmo.DetachedFile

	// reading latches the one round trip to DBCC CHECKPRIMARYFILE, so a second
	// press cannot start another over the top of it. Released by the callback
	// the goroutine posts, and by readPanicked if it panics instead.
	reading bool
}

// NewAttachDatabaseDialog creates the dialog and wires its callbacks.
func NewAttachDatabaseDialog(app *App) *AttachDatabaseDialog {
	d := &AttachDatabaseDialog{}
	d.init(app, newObjectConfig[attachPrefetch]{
		title:   "Attach Database",
		noun:    "Database",
		verb:    "attached",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.explorer.RefreshDatabasesFolder(sc) },
	})
	return d
}

func (d *AttachDatabaseDialog) show(sc *db.ServerConn) {
	d.files = nil
	d.reading = false
	d.newObjectDialog.show(sc)
	d.SetHeader("Server: "+sc.Opts.Server, "Attaching files already on that host")
}

func (d *AttachDatabaseDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*attachPrefetch, error) {
	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	pf := &attachPrefetch{existing: make(map[string]bool, len(dbs))}
	for _, db := range dbs {
		pf.existing[strings.ToLower(db.Name())] = true
	}
	if info := sc.Server.Info(); info != nil {
		pf.dataPath = info.DefaultDataPath
	}
	return pf, nil
}

func (d *AttachDatabaseDialog) buildPages(pf *attachPrefetch) {
	sc := d.sc

	mdfRow := propsheet.Text("Primary data file", "", 48)
	nameRow := propsheet.Text("Attach as", "", 40)
	ownerRow := propsheet.Text("Owner", "", 40)
	rebuildRow := propsheet.Check("Build a new log file", false)
	pathRow := propsheet.Text("Path of selected file", "", 48)
	hint := propsheet.Hint()

	headers := []string{"File", "Type", "Path on the server"}
	fileRows := func() [][]string {
		rows := make([][]string, len(d.files))
		for i, f := range d.files {
			rows[i] = []string{f.Name, attachFileType(f), f.PhysicalName}
		}
		return rows
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, fileRows())
	grid.SetCellCursor(true)

	selected := func() *gosmo.DetachedFile {
		i := grid.SelectedRow()
		if i < 0 || i >= len(d.files) {
			return nil
		}
		return d.files[i]
	}
	var current *gosmo.DetachedFile
	commitCurrent := func() {
		if current != nil {
			current.PhysicalName = strings.TrimSpace(pathRow.Value())
		}
	}
	syncFromSelection := func() {
		current = selected()
		if current == nil {
			pathRow.SetValue("")
			return
		}
		pathRow.SetValue(current.PhysicalName)
	}
	reloadGrid := wireGridEditor(grid, headers, fileRows, commitCurrent, syncFromSelection)

	// readFiles is both Browse's continuation and the Read File List button:
	// a path can be typed as well as picked, and either way the file list has
	// to come out of it.
	readFiles := func() {
		path := strings.TrimSpace(mdfRow.Value())
		if path == "" {
			hint.SetError("Type or browse to the database's primary data file first.")
			return
		}
		if d.reading {
			return
		}
		d.reading = true
		hint.Set("Reading the file list from " + serverPathBase(path) + "...")
		sessionCtx := d.ctx
		d.app.safegoRepair("reading a detached database's file list", d.readPanicked, func() {
			ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
			defer cancel()
			info, err := sc.Server.DetachedDatabaseInfoContext(ctx, path)
			d.app.postAndWake(func() {
				if d.ctx != sessionCtx {
					return // the dialog was closed and reopened while this was out
				}
				d.reading = false
				if err != nil {
					// Not fatal: CREATE DATABASE ... FOR ATTACH still works
					// from the primary file alone as long as the other files
					// are where it recorded them. Only the correcting is lost.
					d.files = nil
					hint.SetError(displayError(err).Error() +
						" — the attach can still go ahead from the primary file alone, if the other files have not moved.")
				} else {
					d.files = attachEditableFiles(info, path)
					if strings.TrimSpace(nameRow.Value()) == "" {
						nameRow.SetValue(info.Name)
					}
					hint.Set(fmt.Sprintf("%q, %d files. Correct any path that has changed.", info.Name, len(info.Files)))
				}
				current = nil
				reloadGrid()
			})
		})
	}

	browseBtn := widgets.NewButton("Browse...", func() {
		fs, ok := newServerFS(sc)
		if !ok {
			hint.SetError("Not connected — cannot browse the server's filesystem.")
			return
		}
		start := strings.TrimSpace(mdfRow.Value())
		if start == "" {
			start = joinServerPath(pf.dataPath, "")
		}
		d.app.fileDialog.ShowOpenOn(fs, "Select Primary Data File", start, func(path string) {
			mdfRow.SetValue(path)
			readFiles()
		})
	})
	readBtn := widgets.NewButton("Read File List", readFiles)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Primary data file"),
		mdfRow,
		propsheet.Buttons(browseBtn, readBtn),
		hint,
		propsheet.Note("The path is on the SQL Server host, not on this machine — Browse lists that host's disks."),
		propsheet.Section("Attach as"),
		nameRow, ownerRow, rebuildRow,
		propsheet.Note("The name need not be the one it was detached under. Leave Owner empty to own it yourself."),
		propsheet.Note("Build a new log file attaches without the log (ATTACH_REBUILD_LOG). Only for a log that was lost — a database that was not shut down cleanly cannot be recovered without it, and the attach fails rather than losing the transactions in it."),
		propsheet.Section("Files"),
		propsheet.NewGridRow(grid, 8),
		pathRow,
		propsheet.Note("Every file must be listed. SQL Server finds the others by itself only at the paths recorded inside the primary file, which is what stops being true once they are moved."),
	)

	d.objectName = func() string { return strings.TrimSpace(nameRow.Value()) }
	d.preflight = func() error {
		if strings.TrimSpace(mdfRow.Value()) == "" {
			return fmt.Errorf("the database's primary data file is required")
		}
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("a name to attach the database as is required")
		}
		if pf.existing[strings.ToLower(name)] {
			return fmt.Errorf("this instance already has a database called %q — attach it under another name", name)
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		commitCurrent()
		paths := attachFilePaths(d.files, strings.TrimSpace(mdfRow.Value()), rebuildRow.Checked())
		// Scripting deliberately skips the check: scripting the attach now
		// and copying the files later is a legitimate order to do this in.
		if !gosmo.Scripting(ctx) {
			if err := checkAttachFiles(ctx, sc.Server, paths); err != nil {
				return err
			}
		}
		return sc.Server.AttachDatabaseContext(ctx, gosmo.AttachSpec{
			Name:       d.objectName(),
			Files:      paths,
			Owner:      strings.TrimSpace(ownerRow.Value()),
			RebuildLog: rebuildRow.Checked(),
		})
	}
}

// readPanicked releases the read latch after a panic in readFiles' goroutine —
// its App.safegoRepair step. Without it Browse and Read File List are dead for
// the rest of the dialog's life, and the hint sits on "Reading..." forever.
func (d *AttachDatabaseDialog) readPanicked() {
	d.reading = false
}

// attachFileType labels a file for the grid. DBCC CHECKPRIMARYFILE reports
// only "is this the log", so a secondary data file and the primary read alike.
func attachFileType(f *gosmo.DetachedFile) string {
	if f.IsLog {
		return "LOG"
	}
	return "Data"
}

// attachEditableFiles copies info's file list for editing, with the primary
// data file's path replaced by the one the user actually pointed at.
//
// The list inside the file records where the files were when it was detached.
// Following it blindly would send the attach back to the old location for the
// very file just browsed to somewhere else — and the moved-files case is the
// only reason this dialog exists.
func attachEditableFiles(info *gosmo.DetachedDatabase, primaryPath string) []*gosmo.DetachedFile {
	// Ask gosmo which file is the primary rather than taking the first data
	// file in the list: DBCC CHECKPRIMARYFILE's row order is undocumented, and
	// on a list that comes back secondary-first the browsed path lands on the
	// secondary while the primary keeps its stale recorded path — an attach
	// with both data files pointed at the wrong place.
	primary := info.PrimaryFile()
	out := make([]*gosmo.DetachedFile, 0, len(info.Files))
	for _, f := range info.Files {
		c := *f
		if f == primary {
			c.PhysicalName = primaryPath
		}
		out = append(out, &c)
	}
	return out
}

// attachFilePaths builds the file list for CREATE DATABASE ... FOR ATTACH.
//
// With no file list read — DBCC refused, or nothing was read yet — the primary
// file alone is the whole request, which SQL Server accepts as long as the
// other files are still at their recorded paths. rebuildLog drops the log
// files: ATTACH_REBUILD_LOG builds a new one and rejects a statement that
// names the old.
func attachFilePaths(files []*gosmo.DetachedFile, primaryPath string, rebuildLog bool) []string {
	if len(files) == 0 {
		if primaryPath == "" {
			return nil
		}
		return []string{primaryPath}
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if rebuildLog && f.IsLog {
			continue
		}
		if p := strings.TrimSpace(f.PhysicalName); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// checkAttachFiles refuses an attach whose files are not where the request
// says they are, naming them.
//
// This is the moved-files case landing one path short, and the server's own
// answer to it is a single sentence built around the full path — "Unable to
// open the physical file \"C:\\Program Files\\Microsoft SQL
// Server\\...\\AppDB_2.ndf\". Operating system error 2..." — which the
// dialog's one-line message clips mid-path, exactly where the information is.
// Naming the files instead keeps the answer inside the line.
//
// A probe that cannot run is not a refusal: xp_fileexist may be denied where
// the attach itself is not, and the attach then reports whatever it finds.
func checkAttachFiles(ctx context.Context, srv *gosmo.Server, paths []string) error {
	var missing []string
	for _, p := range paths {
		exists, _, err := srv.FileSystemExistsContext(ctx, p)
		if err != nil {
			return nil
		}
		if !exists {
			missing = append(missing, serverPathBase(p))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("not on the server: %s — correct the paths in the Files grid",
		strings.Join(missing, ", "))
}

// showAttachDatabaseDialog opens Attach Database — the Databases folder's
// context menu entry.
func (a *App) showAttachDatabaseDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.attachDatabaseDialog.show(sc)
}
