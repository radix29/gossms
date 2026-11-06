package tui

import (
	"context"
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
// The reads can take a while on a large database, so the dialog closes at
// Script and the work reports through the status bar like every other Script
// As (generateScript).

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
			"schemas and types, tables without their foreign keys, views, functions and procedures "+
			"by dependency, then foreign keys and triggers."),
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

// script is the Script button: close, read in the background, then open or
// save the result.
func (d *GenerateScriptsDialog) script() {
	if d.perms == nil || d.output == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return
	}
	sc, dbName := d.sc, d.dbName
	perms, toFile := d.perms.Checked(), d.output.Selected() == genScriptsToFile
	d.Dismiss()

	var count int
	var notes []string
	gen := func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
		text, c, ns, err := scripting.DatabaseObjects(ctx, sc.Server, n.DBName, perms)
		count, notes = c, ns
		return text, err
	}
	n := nodeData{Type: NodeDatabase, DBName: dbName, Name: dbName}
	a := d.app
	a.generateScript(sc, n, scriptVerb{gen: gen}, func(text string) {
		if toFile {
			a.saveScriptAs(n, text)
		} else {
			a.openQueryWithText(sc, dbName, text)
		}
		msg := fmt.Sprintf("Scripted %d objects", count)
		if len(notes) > 0 {
			msg += fmt.Sprintf(" (%d not scripted: %s)", len(notes), notes[0])
		}
		a.setStatus(msg)
	})
}
