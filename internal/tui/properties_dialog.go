package tui

import (
	"context"
	"fmt"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// PropertyRow re-exports the tuikit dialogs.PropertyRow type so application
// code can refer to tui.PropertyRow without importing tuikit directly.
type PropertyRow = dialogs.PropertyRow

// PropertiesDialog wraps tuikit/dialogs.PropertiesDialog — the flat,
// single-page key/value viewer used for the About box and Object
// Dependencies. Server and Database Properties moved to the multi-page
// PropDialog (prop_dialog.go / propsheet.PropertySheet); a page list and
// OK/Cancel/Apply are unnecessary weight for these two single, read-only
// lists, so they stayed on the simpler viewer.
type PropertiesDialog struct {
	*dialogs.PropertiesDialog

	// seq guards against a slow, superseded fetch (see ShowDependencies)
	// overwriting the dialog with results for an object that isn't what's
	// being shown (or being shown at all) anymore.
	seq int
}

// NewPropertiesDialog creates a generic properties dialog.
func NewPropertiesDialog(app *App) *PropertiesDialog {
	return &PropertiesDialog{PropertiesDialog: dialogs.NewPropertiesDialog(app.screen)}
}

// ShowGenericProperties shows arbitrary key-value pairs (e.g. About box).
func (d *PropertiesDialog) ShowGenericProperties(title string, rows []PropertyRow) {
	d.ShowProperties(title, rows)
}

// ShowDependencies loads and displays what schema.name depends on and what
// depends on it — SSMS's Object Dependencies dialog — asynchronously and
// seq-guarded the same way ShowDatabaseProperties is, since both
// Dependencies and Dependents are real network round trips.
func (d *PropertiesDialog) ShowDependencies(app *App, sc *db.ServerConn, dbName, schema, name string) {
	if !app.isConnected(sc) {
		d.ShowProperties("Object Dependencies", []PropertyRow{
			{Key: "Status", Value: "Not connected"},
		})
		return
	}
	title := fmt.Sprintf("Dependencies: %s.%s", schema, name)

	d.seq++
	seq := d.seq
	d.ShowProperties(title, []PropertyRow{{Key: "Status", Value: "Loading..."}})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		rows, err := fetchDependencyRows(ctx, sc, dbName, schema, name)
		app.postEvent(func() {
			if seq != d.seq || !d.Visible() {
				return
			}
			if err != nil {
				d.ShowProperties(title, []PropertyRow{{Key: "Error", Value: err.Error()}})
				return
			}
			d.ShowProperties(title, rows)
		})
		app.wakeEventLoop()
	}()
}

// fetchDependencyRows runs the gosmo dependency queries for the
// Dependencies dialog. Called from a background goroutine (see
// ShowDependencies) — must not touch any UI state directly. ctx bounds the
// whole call (see the caller's childFetchTimeout) so a hung server leaves
// the goroutine and its connection to time out instead of blocking forever.
func fetchDependencyRows(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) ([]PropertyRow, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	deps, err := dbObj.DependenciesContext(ctx, schema, name)
	if err != nil {
		return nil, err
	}
	dependents, err := dbObj.DependentsContext(ctx, schema, name)
	if err != nil {
		return nil, err
	}

	var rows []PropertyRow
	if len(deps) == 0 {
		rows = append(rows, PropertyRow{Key: "Depends On", Value: "(none)"})
	}
	for _, dep := range deps {
		rows = append(rows, PropertyRow{Key: "Depends On", Value: fmt.Sprintf("%s.%s (%s)", dep.Schema, dep.Name, dep.TypeDesc)})
	}
	if len(dependents) == 0 {
		rows = append(rows, PropertyRow{Key: "Used By", Value: "(none)"})
	}
	for _, dep := range dependents {
		rows = append(rows, PropertyRow{Key: "Used By", Value: fmt.Sprintf("%s.%s (%s)", dep.Schema, dep.Name, dep.TypeDesc)})
	}
	return rows, nil
}
