package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
)

// shrink_database_dialog.go is the database node's Shrink > Database... —
// SSMS's Tasks > Shrink > Database — plus what it shares with Shrink > Files...
// (shrink_files_dialog.go): the menu cascade, the size wording and the
// WAIT_AT_LOW_PRIORITY row.
//
// Both dialogs only script. A shrink moves pages, fragments every index it
// touches and can run for hours, so the statement goes to a query window for
// the user to run (or schedule) themselves; the newObjectDialog shell's
// scriptOnly mode gives them Script and Cancel, and nothing here executes.

// shrinkMenuItem is the database node's Shrink cascade. Script-only, so it asks
// no right: generating a statement is what a login without the right to run
// it can still use, as Script Changes on a read-only page is. Offered on an
// ONLINE, writable database only — DBCC SHRINK* needs the database open (the
// dialogs' size reads go through a USE) and fails on a read-only one, so the
// script could only fail.
func shrinkMenuItem(a *App, sc *db.ServerConn, node *explorerNode) (controls.MenuItem, bool) {
	if node.data.DBState != gosmo.DatabaseOnline || node.data.DBReadOnly {
		return controls.MenuItem{}, false
	}
	dbName := node.data.DBName
	return controls.MenuItem{Label: "Shrink", Sub: []controls.MenuItem{
		{Label: "Database...", Action: func() { a.showShrinkDatabaseDialog(sc, dbName) }},
		{Label: "Files...", Action: func() { a.showShrinkFilesDialog(sc, dbName) }},
	}}, true
}

// shrinkMB renders a size in KB as SSMS's dialogs do: MB with two decimals.
func shrinkMB(kb int64) string {
	return strconv.FormatFloat(float64(kb)/1024, 'f', 2, 64) + " MB"
}

// shrinkFreeText is "52.90 MB (36%)": free space and its share of allocated,
// the percentage truncated as SSMS truncates it.
func shrinkFreeText(freeKB, allocatedKB int64) string {
	pct := int64(0)
	if allocatedKB > 0 {
		pct = freeKB * 100 / allocatedKB
	}
	return fmt.Sprintf("%s (%d%%)", shrinkMB(freeKB), pct)
}

// shrinkWaitChoices are the Wait at low priority row's items, in the order
// shrinkWaitValues maps them.
var shrinkWaitChoices = []string{
	"No",
	"Yes, then stop the shrink",
	"Yes, then kill the blockers",
}

var shrinkWaitValues = []gosmo.ShrinkWait{
	gosmo.ShrinkWaitNormal,
	gosmo.ShrinkWaitLowPrioritySelf,
	gosmo.ShrinkWaitLowPriorityBlockers,
}

// shrinkWaitRows is the WAIT_AT_LOW_PRIORITY choice, and how to read it. On an
// instance before SQL Server 2022, which refuses the clause, there are no rows
// and the choice reads as the ordinary wait.
func shrinkWaitRows(sc *db.ServerConn) ([]propsheet.Row, func() gosmo.ShrinkWait) {
	if !sc.Server.Info().SupportsShrinkLowPriority() {
		return nil, func() gosmo.ShrinkWait { return gosmo.ShrinkWaitNormal }
	}
	row := propsheet.Select("Wait at low priority", shrinkWaitChoices, 0)
	row.SetFitItems(true) // the fixed width cuts "Yes, then kill the blockers"
	rows := []propsheet.Row{
		propsheet.Section("Locking"),
		row,
		propsheet.Note("WAIT_AT_LOW_PRIORITY: the shrink queues for its locks behind other work instead of ahead of it, for one minute, then stops itself or kills what blocks it. Without it a shrink waiting on a long query blocks every query that arrives after it."),
	}
	return rows, func() gosmo.ShrinkWait { return shrinkWaitValues[row.Selected()] }
}

// shrinkDatabasePrefetch is the space figures the page shows.
type shrinkDatabasePrefetch struct {
	allocatedKB, freeKB int64
}

// ShrinkDatabaseDialog is the Shrink Database dialog.
type ShrinkDatabaseDialog struct {
	newObjectDialog[shrinkDatabasePrefetch]

	// dbName is set by show, before the shell's own show runs.
	dbName string
}

// NewShrinkDatabaseDialog creates the dialog and wires its callbacks.
func NewShrinkDatabaseDialog(app *App) *ShrinkDatabaseDialog {
	d := &ShrinkDatabaseDialog{}
	d.init(app, newObjectConfig[shrinkDatabasePrefetch]{
		title:      "Shrink Database",
		noun:       "Database",
		pages:      []string{"General"},
		fetch:      d.fetchPrefetch,
		build:      d.buildPages,
		refresh:    func(*db.ServerConn) {},
		scriptOnly: true,
	})
	return d
}

func (d *ShrinkDatabaseDialog) show(sc *db.ServerConn, dbName string) {
	d.dbName = dbName
	d.scriptDatabase = dbName
	d.newObjectDialog.show(sc)
	d.SetTitle("Shrink Database - " + dbName)
	d.SetHeader("Database: "+dbName, "Server: "+sc.Opts.Server)
}

// fetchPrefetch reads the figures SSMS shows: allocated is every file, data
// and log; free is unallocated space in the data files only — SSMS's
// 144.00 MB / 52.90 MB (36%) on HealthClinic matches that and not data plus
// log free (checked against the live server, 2026-10-21).
func (d *ShrinkDatabaseDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*shrinkDatabasePrefetch, error) {
	si, err := sc.Server.DatabaseRef(d.dbName).SpaceUsed(ctx)
	if err != nil {
		return nil, err
	}
	return &shrinkDatabasePrefetch{
		allocatedKB: int64(si.TotalMB * 1024),
		freeKB:      int64(si.UnallocatedMB * 1024),
	}, nil
}

func (d *ShrinkDatabaseDialog) buildPages(pf *shrinkDatabasePrefetch) {
	sc := d.sc
	dbName := d.dbName

	reorganize := propsheet.Check("Reorganize files before releasing unused space", false)
	percent := propsheet.Int("Max free space after shrinking", 0, 0, 99, "%")
	percent.SetEnabled(false)
	reorganize.SetOnChange(percent.SetEnabled)
	waitRows, wait := shrinkWaitRows(sc)

	rows := []propsheet.Row{
		propsheet.Note("The size of a database is reduced by collectively shrinking the database files, releasing unused space. To shrink individual database files, use Shrink > Files instead."),
		propsheet.Static("Database", dbName),
		propsheet.Section("Database size"),
		propsheet.Static("Currently allocated space", shrinkMB(pf.allocatedKB)),
		propsheet.Static("Available free space", shrinkFreeText(pf.freeKB, pf.allocatedKB)),
		propsheet.Section("Shrink action"),
		reorganize,
		propsheet.Note("Ticked or not, the shrink moves pages toward the start of each file, then releases the free space at the end. Ticking only adds the free-space target below."),
		percent,
		propsheet.Note("The free space, as a percentage of each file, left in it once shrunk. Unticked, the database shrinks as far as its contents allow."),
	}
	rows = append(rows, waitRows...)
	rows = append(rows, propsheet.Note("Script opens the DBCC SHRINKDATABASE statement in a new query window; nothing runs until you execute it there. A shrink moves pages and fragments the indexes it touches."))

	d.forms[0] = propsheet.NewForm(rows...)
	d.objectName = func() string { return dbName }
	opts := func() (gosmo.ShrinkDatabaseOptions, error) {
		o := gosmo.ShrinkDatabaseOptions{Wait: wait()}
		if reorganize.Checked() {
			p, err := percent.IntValue()
			if err != nil || p < 0 || p > 99 {
				return o, fmt.Errorf("maximum free space must be a whole number from 0 to 99, not %q", strings.TrimSpace(percent.Value()))
			}
			o.TargetFreePercent = new(int(p))
		}
		return o, nil
	}
	// preflight reads the rows on the UI goroutine and leaves the request for
	// the apply, which runs on the pipeline's.
	var req gosmo.ShrinkDatabaseOptions
	d.preflight = func() error {
		var err error
		req, err = opts()
		return err
	}
	d.applyFns[0] = func(ctx context.Context) error {
		return sc.Server.DatabaseRef(dbName).Shrink(ctx, req)
	}
}

// showShrinkDatabaseDialog opens Shrink Database for one database.
func (a *App) showShrinkDatabaseDialog(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.shrinkDatabaseDialog.show(sc, dbName)
}
