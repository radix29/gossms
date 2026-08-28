package tui

import (
	"context"
	"slices"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// permEntry is the common shape of gosmo.ServerPermissionEntry and
// gosmo.DatabasePermissionEntry (Principal/PrincipalType/Grantor/
// Permission/State, all strings) — normalized at the call site so the
// permissions-grid editor, identical for both scopes, is built once.
type permEntry struct {
	Principal     string
	PrincipalType string
	Grantor       string
	Permission    string
	State         string
}

// permEdit tracks one grid row's pending state through nextPermState's
// cycle, driven by Space/Enter/click on the State column. orig is kept
// alongside current because the statement that applies the change depends on
// both — see permTransition.
type permEdit struct {
	entry   permEntry
	orig    string
	current string
}

// permPrincipal is one row in the Permissions page's principal list —
// every login/user or role that could be granted a permission at this
// scope, not just the ones with an existing GRANT/DENY entry.
type permPrincipal struct {
	Name string
	Type string
}

// databasePermPrincipals is the principal list every database-scope
// Permissions page offers: every user, then every database role. The database,
// schema, and object pages differ only in which permission catalog they pair
// it with, so the list itself is built once.
func databasePermPrincipals(users []*gosmo.User, roles []*gosmo.DatabaseRole) []permPrincipal {
	principals := make([]permPrincipal, 0, len(users)+len(roles))
	for _, u := range users {
		principals = append(principals, permPrincipal{Name: u.Name, Type: u.UserType})
	}
	for _, r := range roles {
		principals = append(principals, permPrincipal{Name: r.Name, Type: "DATABASE_ROLE"})
	}
	return principals
}

// objectPermEntries normalizes the schema- and object-scope entries, whose
// Permission and State are named string types rather than the plain strings
// the server- and database-scope ones carry.
func objectPermEntries(perms []*gosmo.PermissionEntry) []permEntry {
	entries := make([]permEntry, len(perms))
	for i, p := range perms {
		entries[i] = permEntry{
			Principal: p.Principal, PrincipalType: p.PrincipalType,
			Grantor: p.Grantor, Permission: string(p.Permission), State: string(p.State),
		}
	}
	return entries
}

// buildPermissionsMatrix builds the Permissions page's two-pane editor: a
// principal list (top grid) and, for whichever principal is selected, the
// full catalog of grantable permissions at this scope (bottom grid) — not
// just the ones with an existing GRANT/DENY entry, so a principal with no
// prior ACL rows still shows every permission it could be granted. Tab
// switches focus between the two grids; the bottom grid's State column
// cycles Grant -> Grant With Grant -> Deny -> (none) on activation. A filter
// box above each grid narrows it live. Wired to applyFn for whichever scope
// (server-, database-, schema- or object-level GRANT/DENY/REVOKE) the caller
// needs.
func buildPermissionsMatrix(
	principals []permPrincipal, catalog []string, entries []permEntry,
	principalsHeight, permsHeight int,
	applyFn permApplyFn,
) (*propsheet.Form, propApply) {
	type entryKey struct{ principal, permission string }
	existing := make(map[entryKey]permEntry, len(entries))
	for _, e := range entries {
		existing[entryKey{e.Principal, e.Permission}] = e
	}

	// editsByPrincipal is built lazily, one principal at a time, as the
	// user browses the principal grid — a principal never selected has by
	// definition nothing to apply or check for dirtiness.
	editsByPrincipal := make(map[string][]*permEdit)
	editsFor := func(p permPrincipal) []*permEdit {
		if e, ok := editsByPrincipal[p.Name]; ok {
			return e
		}
		edits := make([]*permEdit, len(catalog))
		for i, perm := range catalog {
			ent, ok := existing[entryKey{p.Name, perm}]
			if !ok {
				ent = permEntry{Principal: p.Name, PrincipalType: p.Type, Permission: perm}
			}
			edits[i] = &permEdit{entry: ent, orig: ent.State, current: ent.State}
		}
		editsByPrincipal[p.Name] = edits
		return edits
	}

	// visible is the filtered view of principals; the grid's row indices
	// address it, never principals directly. Filtering by name is what makes
	// these pages usable against a server with hundreds of logins.
	visible := slices.Clone(principals)
	principalGrid := controls.NewDataGrid()
	principalRowsFor := func() [][]string {
		rows := make([][]string, len(visible))
		for i, p := range visible {
			rows[i] = []string{p.Name, p.Type}
		}
		return rows
	}
	principalGrid.SetData([]string{"Name", "Type"}, principalRowsFor())

	permGrid := controls.NewDataGrid()
	permGrid.SetCellCursor(true)

	// permFilter narrows the bottom grid the same way, and visiblePerms maps
	// its row indices back onto the selected principal's full edit slice —
	// which is indexed by the whole catalog, filter or no filter.
	permFilter := ""
	var visiblePerms []*permEdit
	permRowsFor := func(edits []*permEdit) [][]string {
		visiblePerms = visiblePerms[:0]
		for _, e := range edits {
			if matchesFilter(permFilter, e.entry.Permission) {
				visiblePerms = append(visiblePerms, e)
			}
		}
		rows := make([][]string, len(visiblePerms))
		for i, e := range visiblePerms {
			rows[i] = []string{e.entry.Permission, displayPermState(e.current)}
		}
		return rows
	}

	permSection := propsheet.Section("Explicit permissions")
	selected := -1
	// selectedEdits is the edit slice the bottom grid is showing, held
	// directly rather than re-derived from an index into visible — a filter
	// change rebuilds visible underneath it, and an index would then address
	// a different principal than the one on screen.
	var selectedEdits []*permEdit
	// clearSelection empties the bottom grid. A filter that matches nothing
	// has to reach this: leaving it showing the previously selected principal
	// means cycling a State there edits — and Apply then writes — permissions
	// for a principal the page no longer lists.
	clearSelection := func() {
		selected = -1
		selectedEdits = nil
		visiblePerms = visiblePerms[:0]
		permGrid.SetData(permissionStateColumns, nil)
		permSection.SetTitle("Explicit permissions")
	}
	loadPrincipal := func(row int) {
		if row < 0 || row >= len(visible) {
			clearSelection()
			return
		}
		if row == selected {
			return
		}
		selected = row
		selectedEdits = editsFor(visible[row])
		resetGrid(permGrid, permissionStateColumns, permRowsFor(selectedEdits), 0)
		permSection.SetTitle("Explicit permissions for " + visible[row].Name)
	}
	principalGrid.OnSelectRow = loadPrincipal
	loadPrincipal(0)

	permGrid.OnActivateCell = func(row, col int) {
		if col != 1 || row < 0 || row >= len(visiblePerms) {
			return
		}
		e := visiblePerms[row]
		e.current = nextPermState(e.current)
		redrawGrid(permGrid, permissionStateColumns, permRowsFor(selectedEdits))
	}

	principalFilterRow := propsheet.Text("Filter principals", "", 28)
	principalFilterRow.SetDirtyTracked(false)
	principalFilterRow.SetOnChange(func(term string) {
		visible = visible[:0]
		for _, p := range principals {
			if matchesFilter(term, p.Name, p.Type) {
				visible = append(visible, p)
			}
		}
		redrawGrid(principalGrid, []string{"Name", "Type"}, principalRowsFor())
		// The row that was selected is probably not at the same index (or
		// present at all) in the new list, so re-select from the top rather
		// than leave the bottom grid describing a principal that scrolled
		// out from under the cursor.
		selected = -1
		principalGrid.SetSelectedRow(0)
		loadPrincipal(0)
	})

	permFilterRow := propsheet.Text("Filter permissions", "", 28)
	permFilterRow.SetDirtyTracked(false)
	permFilterRow.SetOnChange(func(term string) {
		permFilter = term
		if selectedEdits != nil {
			resetGrid(permGrid, permissionStateColumns, permRowsFor(selectedEdits), 0)
		}
	})

	principalsRow := propsheet.NewGridRow(principalGrid, principalsHeight)
	permsRow := propsheet.NewGridRow(permGrid, permsHeight)
	permsRow.DirtyFn = func() bool {
		for _, edits := range editsByPrincipal {
			for _, e := range edits {
				if e.current != e.orig {
					return true
				}
			}
		}
		return false
	}
	permsRow.RevertFn = func() {
		for _, edits := range editsByPrincipal {
			for _, e := range edits {
				e.current = e.orig
			}
		}
		if selectedEdits != nil {
			redrawGrid(permGrid, permissionStateColumns, permRowsFor(selectedEdits))
		}
	}

	f := propsheet.NewForm(
		propsheet.Section("Principals"),
		principalFilterRow,
		principalsRow,
		permSection,
		permFilterRow,
		permsRow,
		propsheet.Note(permStateCycleNote+" Tab switches between the principal list and its permissions. The filter boxes narrow each grid as you type; a hidden row keeps whatever edit it already had."),
	)

	apply := func(ctx context.Context) error {
		// Walked over principals, not over editsByPrincipal: ranging a map
		// picks a different order every run, so which statements landed before
		// a mid-apply failure isn't reproducible and Script Changes reorders
		// its output between presses. editsFor is only ever called with a
		// principal from this slice, so nothing is missed.
		for _, p := range principals {
			for _, e := range editsByPrincipal[p.Name] {
				if err := applyPermEdit(ctx, applyFn, e, e.entry.Principal); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
}

// pagePrincipalServerPermissions builds a "Securables" page listing every
// server-scoped permission (gosmo.ServerPermissionNames) with a
// Grant/Deny/(none) state cyclable per row, scoped to one principal — a
// login or server role. Shared by Login Properties and Server Role
// Properties, both server-level principals that can hold explicit
// server-scoped GRANT/DENY entries the same way. Unlike
// buildPermissionsMatrix (Server Properties' own Permissions page,
// server_props.go), which browses every principal at once, this only ever
// shows principalName's own entries — no principal picker.
func pagePrincipalServerPermissions(sc *db.ServerConn, principalName string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			perms, err := sc.Server.ServerPermissionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			states := make(map[string]string, len(perms))
			for _, p := range perms {
				if p.Principal == principalName {
					states[p.Permission] = p.State
				}
			}

			catalog := gosmo.ServerPermissionNames()
			edits := make([]*permEdit, len(catalog))
			for i, perm := range catalog {
				state := states[perm]
				entry := permEntry{Principal: principalName, Permission: perm, State: state}
				edits[i] = &permEdit{entry: entry, orig: state, current: state}
			}

			// visible maps the grid's row indices onto edits, which stays
			// indexed by the full catalog however the filter is set.
			filter := ""
			var visible []*permEdit
			rowsFor := func() [][]string {
				visible = visible[:0]
				for _, e := range edits {
					if matchesFilter(filter, e.entry.Permission) {
						visible = append(visible, e)
					}
				}
				rows := make([][]string, len(visible))
				for i, e := range visible {
					rows[i] = []string{e.entry.Permission, displayPermState(e.current)}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData(permissionStateColumns, rowsFor())
			grid.SetCellCursor(true)
			grid.OnActivateCell = func(row, col int) {
				if col != 1 || row < 0 || row >= len(visible) {
					return
				}
				visible[row].current = nextPermState(visible[row].current)
				redrawGrid(grid, permissionStateColumns, rowsFor())
			}

			filterRow := propsheet.Text("Filter permissions", "", 28)
			filterRow.SetDirtyTracked(false)
			filterRow.SetOnChange(func(term string) {
				filter = term
				resetGrid(grid, permissionStateColumns, rowsFor(), 0)
			})

			gridRow := propsheet.NewGridRow(grid, 12)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.current != e.orig {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				for _, e := range edits {
					e.current = e.orig
				}
				redrawGrid(grid, permissionStateColumns, rowsFor())
			}

			f := propsheet.NewForm(
				propsheet.Section("Explicit server-level permissions"),
				filterRow,
				gridRow,
				propsheet.Note(permStateCycleNote+" The filter box narrows the list as you type. Database and endpoint securables aren't modeled here yet."),
			)

			applyFn := serverPermApply(sc.Server)
			apply := func(ctx context.Context) error {
				for _, e := range edits {
					if err := applyPermEdit(ctx, applyFn, e, principalName); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
