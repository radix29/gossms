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

// PropertySection re-exports dialogs.PropertySection — a group-heading row
// for a PropertyRow list.
func PropertySection(caption string) PropertyRow { return dialogs.PropertySection(caption) }

// PropertiesDialog wraps tuikit/dialogs.PropertiesDialog — the flat,
// single-page key/value viewer used for the About box and Object
// Dependencies. Multi-page, editable dialogs use PropDialog
// (prop_dialog.go / propsheet.PropertySheet) instead; a page list and
// OK/Cancel/Apply are unnecessary weight for these two read-only lists.
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
// Bumps seq like ShowDependencies does on every new show — this dialog is a
// single shared instance reused for both features, so a Dependencies fetch
// still in flight when the dialog is repurposed here (e.g. Escape out of
// Object Dependencies, then Help > About before the fetch lands) must not
// be allowed to land later and silently overwrite these rows with stale
// dependency data.
func (d *PropertiesDialog) ShowGenericProperties(title string, rows []PropertyRow) {
	d.seq++
	d.ShowProperties(title, rows)
}

// ShowGenericPropertiesSized is ShowGenericProperties at an explicit dialog
// size, for content the default 60x24 can't hold (the About box).
func (d *PropertiesDialog) ShowGenericPropertiesSized(title string, rows []PropertyRow, w, h int) {
	d.seq++
	d.ShowPropertiesSized(title, rows, w, h)
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

	app.safego("loading dependencies", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		rows, err := fetchDependencyRows(ctx, sc, dbName, schema, name)
		app.postAndWake(func() {
			if seq != d.seq || !d.Visible() {
				return
			}
			if err != nil {
				d.ShowProperties(title, []PropertyRow{{Key: "Error", Value: err.Error()}})
				return
			}
			d.ShowProperties(title, rows)
		})
	})
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
