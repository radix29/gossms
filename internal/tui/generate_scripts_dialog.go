package tui

import (
	"fmt"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/scripting"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// generate_scripts_dialog.go is a database's "Generate Scripts...": every user
// object as one script that replays into an empty database, in dependency
// order (gosmo's ScriptDatabaseObjectList, through scripting.DatabaseObjects —
// the same call as `gossms script db --objects`). A one-page script-only
// sheet: the choices are whether to add the database's security, and whether
// the script opens in a query window or is saved to a file. The script is
// opened or written, never executed.
//
// The script is at least one round trip per object, so a large database
// takes minutes (~25 ms an object on a LAN). The dialog closes at Script and
// the run is a Background Task with progress and Cancel, under no timeout:
// it went through generateScript once, whose 30 s childFetchTimeout failed
// any database past about 1,200 modules and threw the work away.

// Output choices of the Output row, in row order.
const (
	genScriptsToQuery = iota
	genScriptsToFile
)

// GenerateScriptsDialog is the Generate Scripts dialog.
type GenerateScriptsDialog struct {
	*propsheet.PropertySheet

	app    *App
	sc     *db.ServerConn
	dbName string

	// perms and output are the page's rows; nil until it has built.
	perms  *propsheet.CheckRow
	output *propsheet.RadioRow
}

// NewGenerateScriptsDialog creates the dialog and wires its callbacks.
func NewGenerateScriptsDialog(app *App) *GenerateScriptsDialog {
	d := &GenerateScriptsDialog{app: app}
	d.PropertySheet = propsheet.NewPropertySheet(app.screen, "Generate Scripts")
	d.SetScriptOnly(true)
	d.OnLoadPage = d.onLoadPage
	d.OnScript = d.script
	return d
}

// showGenerateScriptsFor opens Generate Scripts on one database.
func (a *App) showGenerateScriptsFor(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.generateScriptsDialog.show(sc, dbName)
}

func (d *GenerateScriptsDialog) show(sc *db.ServerConn, dbName string) {
	d.sc, d.dbName = sc, dbName
	d.perms, d.output = nil, nil
	d.SetHeader("Database: "+dbName, "Server: "+sc.Opts.Server)
	d.SetPages([]string{"General"})
	d.Show()
}

// onLoadPage builds the page at once: there is nothing to read before the
// choices.
func (d *GenerateScriptsDialog) onLoadPage(page, seq int) {
	d.perms = propsheet.Check("Include permissions", false)
	d.output = propsheet.Radio("Output", []string{"New query window", "File"}, genScriptsToQuery)
	d.SetPageForm(page, seq, propsheet.NewForm(
		propsheet.Section("Objects"),
		propsheet.Static("Database", d.dbName),
		propsheet.Note("Every user object, in an order a replay into an empty database accepts: "+
			"schemas and types, tables without their foreign keys and edge constraints, views, "+
			"functions and procedures by dependency, then foreign keys, edge constraints and triggers."),
		propsheet.Section("Options"),
		d.perms,
		propsheet.Note("Adds the owner, users, roles, role memberships and every GRANT/DENY — "+
			"principals first, permissions last."),
		propsheet.Section("Destination"),
		d.output,
		propsheet.Note("Not scripted: data, server-level objects, system objects, rules and defaults, "+
			"CLR assemblies, full-text and Service Broker objects, external objects, credentials, "+
			"certificates and keys, and dependencies across databases."),
	))
}

// script is the Script button: close, script as a Background Task, then
// open or save the result.
func (d *GenerateScriptsDialog) script() {
	if d.perms == nil || d.output == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return
	}
	sc, dbName := d.sc, d.dbName
	perms, toFile := d.perms.Checked(), d.output.Selected() == genScriptsToFile
	d.Dismiss()
	a := d.app
	if !a.requireConn(sc) {
		return
	}

	// Parented on the connection, so a disconnect cancels it.
	task, ctx := a.startTask(sc.Server.Context(), "Generate Scripts: "+dbName)
	task.Message = "reading the catalog"
	a.setStatus(task.Label + " started — Tools > Background Tasks to follow or cancel it")
	// safegoRepair: only the posted markTaskDone finishes the task, and a
	// panic past it would leave one the status bar counts as running forever.
	a.safegoRepair("generating scripts", func() { a.markTaskDone(task, errTaskPanicked) }, func() {
		// Whole-percent steps only: one postAndWake per object would queue
		// thousands of redraws on a large database.
		last := -1
		progress := func(done, total int) {
			pct := 100
			if total > 0 {
				pct = done * 100 / total
			}
			if pct != last {
				last = pct
				a.postProgress(task, pct, fmt.Sprintf("%d of %d objects", done, total))
			}
		}
		r, err := scripting.DatabaseObjects(ctx, sc.Server, dbName, perms, progress)
		a.postAndWake(func() {
			a.markTaskDone(task, err)
			if err != nil {
				return
			}
			msg := fmt.Sprintf("Scripted %d objects", r.Objects) + r.StatusSuffix()
			if toFile {
				a.saveScriptAs(nodeData{Type: NodeDatabase, DBName: dbName, Name: dbName}, r.Text)
				a.setStatus(msg)
			} else {
				a.openQueryWithTextThen(sc, dbName, r.Text, func() { a.setStatus(msg) })
			}
		})
	})
}
