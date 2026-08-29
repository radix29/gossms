package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// The scope options on the database-principal Effective Permissions page,
// in the order the Select row lists them.
const (
	effScopeDatabase = iota
	effScopeSchema
	effScopeObject
)

var effScopeItems = []string{"Database", "Schema", "Table or view"}

// effectivePermsCols is the result grid's header, hoisted because both the
// empty grid and every refill have to use the same one — SetData takes the
// header with the rows, so two literals are two things to keep in step.
var effectivePermsCols = []string{"Permission", "Column", "Entity"}

// effectivePermsGrid builds the result grid every Effective Permissions
// page shares, plus the function that fills it from a gosmo result.
//
// Permission comes first because it is what the question is about;
// Column is the fn_my_permissions subentity, non-empty only for a
// column-level permission on an object.
func effectivePermsGrid() (*controls.DataGrid, func(perms []*gosmo.EffectivePermission)) {
	grid := controls.NewDataGrid()
	grid.SetData(effectivePermsCols, nil)
	fill := func(perms []*gosmo.EffectivePermission) {
		rows := make([][]string, len(perms))
		for i, p := range perms {
			rows[i] = []string{p.Permission, p.Subentity, p.Entity}
		}
		// resetGrid, not SetData: the columns are effectivePermsCols on every
		// run, so a column the user dragged wider stays meaningful across a
		// re-Show — SetData discarded it. Row 0 because these are a different
		// principal's or scope's permissions, so the old cursor means nothing.
		resetGrid(grid, effectivePermsCols, rows, 0)
	}
	return grid, fill
}

// effectivePermsNote explains what the page is showing — the distinction
// between this and the Securables page is the whole point of it, and is not
// obvious from a grid of permission names.
const effectivePermsNote = "Effective permissions are what the principal can actually do: role membership, ownership, and permissions inherited from a wider scope (a schema grant covering its tables, CONTROL implying everything under it) are already resolved in, and anything a DENY takes away is simply absent. The Securables page shows the explicit GRANT/DENY rows instead. Nothing here is editable — change a permission on Securables and re-run Show."

// pagePrincipalEffectivePermissions builds the "Effective Permissions" page
// for a database principal — a user or a database role. The scope picker
// chooses what to resolve against; Show runs it.
//
// It is a read-only page: the scope rows are marked untracked so browsing
// them never leaves the dialog claiming unsaved changes, and its apply
// writes nothing.
func pagePrincipalEffectivePermissions(d *PropDialog, sc *db.ServerConn, dbName string, principal *string) propPage {
	return propPage{
		title: "Effective Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			database, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}

			scopeRow := propsheet.Select("Resolve against", effScopeItems, effScopeDatabase)
			scopeRow.SetDirtyTracked(false)
			schemaRow := propsheet.Text("Schema", "dbo", 32)
			schemaRow.SetDirtyTracked(false)
			objectRow := propsheet.Text("Table or view", "", 32)
			objectRow.SetDirtyTracked(false)
			hint := propsheet.Hint()
			grid, fill := effectivePermsGrid()

			// Only the rows the selected scope resolves against stay live —
			// a disabled row is drawn dim and skipped by focus cycling, so
			// Database scope no longer offers a Schema box that does
			// nothing. The Show guards below stay: a row can be enabled and
			// still blank.
			syncScopeRows := func() {
				scope := scopeRow.Selected()
				schemaRow.SetEnabled(scope != effScopeDatabase)
				objectRow.SetEnabled(scope == effScopeObject)
			}
			syncScopeRows()
			scopeRow.SetOnChange(func(string) { syncScopeRows() })

			var showBusy bool
			showBtn := widgets.NewButton("Show", func() {
				scope := scopeRow.Selected()
				schema := strings.TrimSpace(schemaRow.Value())
				object := strings.TrimSpace(objectRow.Value())
				if scope == effScopeSchema && schema == "" {
					hint.SetError("Enter a schema name.")
					return
				}
				if scope == effScopeObject && (schema == "" || object == "") {
					hint.SetError("Enter both a schema and a table or view name.")
					return
				}
				hint.Set("Resolving...")

				var perms []*gosmo.EffectivePermission
				d.runPageActionOnce(&showBusy, func(ctx context.Context) error {
					var err error
					switch scope {
					case effScopeSchema:
						perms, err = database.EffectiveSchemaPermissionsContext(ctx, schema, *principal)
					case effScopeObject:
						perms, err = database.EffectiveObjectPermissionsContext(ctx, schema, object, *principal)
					default:
						perms, err = database.EffectivePermissionsContext(ctx, *principal)
					}
					return err
				}, func(err error) {
					if err != nil {
						hint.SetError("Error: " + err.Error())
						return
					}
					fill(perms)
					hint.Set(effectiveResultSummary(len(perms), *principal))
				})
			})

			f := propsheet.NewForm(
				propsheet.Section("Scope"),
				scopeRow,
				schemaRow,
				objectRow,
				propsheet.Buttons(showBtn),
				hint,
				propsheet.Section("Effective permissions"),
				propsheet.NewGridRow(grid, 14),
				propsheet.Note(effectivePermsNote),
			)
			return f, func(context.Context) error { return nil }, nil
		},
	}
}

// pageLoginEffectivePermissions is the server-scope counterpart, for a
// login or a server role. There is only one scope to resolve against, so
// there is no picker.
func pageLoginEffectivePermissions(d *PropDialog, sc *db.ServerConn, principal *string) propPage {
	return propPage{
		title: "Effective Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			hint := propsheet.Hint()
			grid, fill := effectivePermsGrid()

			var showBusy bool
			showBtn := widgets.NewButton("Show", func() {
				hint.Set("Resolving...")
				var perms []*gosmo.EffectivePermission
				d.runPageActionOnce(&showBusy, func(ctx context.Context) error {
					var err error
					perms, err = sc.Server.EffectiveServerPermissionsContext(ctx, *principal)
					return err
				}, func(err error) {
					if err != nil {
						hint.SetError("Error: " + err.Error())
						return
					}
					fill(perms)
					hint.Set(effectiveResultSummary(len(perms), *principal))
				})
			})

			f := propsheet.NewForm(
				propsheet.Section("Scope"),
				propsheet.Static("Resolve against", "Server"),
				propsheet.Buttons(showBtn),
				hint,
				propsheet.Section("Effective server permissions"),
				propsheet.NewGridRow(grid, 14),
				propsheet.Note(effectivePermsNote+" Resolving another login's permissions impersonates it (EXECUTE AS LOGIN), which needs IMPERSONATE on that login — CONTROL SERVER covers it."),
			)
			return f, func(context.Context) error { return nil }, nil
		},
	}
}

// effectiveResultSummary is the line under the Show button once a run
// completes. An empty result is a real answer, not a failure, and says so.
func effectiveResultSummary(n int, principal string) string {
	if n == 0 {
		return fmt.Sprintf("%s holds no effective permissions on that securable.", principal)
	}
	return fmt.Sprintf("%d effective permission(s) for %s.", n, principal)
}
