package tui

import (
	"context"

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

// permEdit tracks one grid row's pending state through the cycle
// GRANT -> DENY -> "" (revoke) -> GRANT, driven by Space/Enter/click on
// the State column.
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

func nextPermState(s string) string {
	switch s {
	case "GRANT", "GRANT_WITH_GRANT_OPTION":
		return "DENY"
	case "DENY":
		return ""
	default:
		return "GRANT"
	}
}

func displayPermState(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// buildPermissionsMatrix builds the Permissions page's two-pane editor: a
// principal list (top grid) and, for whichever principal is selected, the
// full catalog of grantable permissions at this scope (bottom grid) — not
// just the ones with an existing GRANT/DENY entry, so a principal with no
// prior ACL rows still shows every permission it could be granted. Tab
// switches focus between the two grids; the bottom grid's State column
// cycles Grant -> Deny -> (none) on activation, same as before. Wired to
// grant/deny/revokeFn for whichever scope (server- or database-level
// GRANT/DENY/REVOKE) the caller needs.
func buildPermissionsMatrix(
	principals []permPrincipal, catalog []string, entries []permEntry,
	principalsHeight, permsHeight int,
	grantFn, denyFn, revokeFn func(ctx context.Context, permission, principal string) error,
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

	principalRows := make([][]string, len(principals))
	for i, p := range principals {
		principalRows[i] = []string{p.Name, p.Type}
	}
	principalGrid := controls.NewDataGrid()
	principalGrid.SetData([]string{"Name", "Type"}, principalRows)

	permGrid := controls.NewDataGrid()
	permGrid.SetCellCursor(true)
	permRowsFor := func(edits []*permEdit) [][]string {
		rows := make([][]string, len(edits))
		for i, e := range edits {
			rows[i] = []string{e.entry.Permission, displayPermState(e.current)}
		}
		return rows
	}

	permSection := propsheet.Section("Explicit permissions")
	selected := -1
	loadPrincipal := func(row int) {
		if row < 0 || row >= len(principals) || row == selected {
			return
		}
		selected = row
		permGrid.SetData([]string{"Permission", "State"}, permRowsFor(editsFor(principals[row])))
		permGrid.SetSelectedRow(0)
		permSection.SetTitle("Explicit permissions for " + principals[row].Name)
	}
	principalGrid.OnSelectRow = loadPrincipal
	if len(principals) > 0 {
		loadPrincipal(0)
	}

	permGrid.OnActivateCell = func(row, col int) {
		if selected < 0 || col != 1 {
			return
		}
		edits := editsByPrincipal[principals[selected].Name]
		if row < 0 || row >= len(edits) {
			return
		}
		edits[row].current = nextPermState(edits[row].current)
		permGrid.SetData([]string{"Permission", "State"}, permRowsFor(edits))
		permGrid.SetSelectedRow(row)
	}

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
		if selected >= 0 {
			permGrid.SetData([]string{"Permission", "State"}, permRowsFor(editsByPrincipal[principals[selected].Name]))
		}
	}

	f := propsheet.NewForm(
		propsheet.Section("Principals"),
		principalsRow,
		permSection,
		permsRow,
		propsheet.Note("Space/Enter (or click) on State cycles Grant → Deny → (none). Tab switches between the principal list and its permissions."),
	)

	apply := func(ctx context.Context) error {
		for _, edits := range editsByPrincipal {
			for _, e := range edits {
				if e.current == e.orig {
					continue
				}
				var err error
				switch e.current {
				case "GRANT":
					err = grantFn(ctx, e.entry.Permission, e.entry.Principal)
				case "DENY":
					err = denyFn(ctx, e.entry.Permission, e.entry.Principal)
				case "":
					err = revokeFn(ctx, e.entry.Permission, e.entry.Principal)
				}
				if err != nil {
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

			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{e.entry.Permission, displayPermState(e.current)}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Permission", "State"}, rowsFor())
			grid.SetCellCursor(true)
			grid.OnActivateCell = func(row, col int) {
				if col != 1 || row < 0 || row >= len(edits) {
					return
				}
				edits[row].current = nextPermState(edits[row].current)
				grid.SetData([]string{"Permission", "State"}, rowsFor())
				grid.SetSelectedRow(row)
			}

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
				grid.SetData([]string{"Permission", "State"}, rowsFor())
			}

			f := propsheet.NewForm(
				propsheet.Section("Explicit server-level permissions"),
				gridRow,
				propsheet.Note("Space/Enter (or click) on State cycles Grant → Deny → (none). Database and endpoint securables aren't modeled here yet."),
			)

			apply := func(ctx context.Context) error {
				for _, e := range edits {
					if e.current == e.orig {
						continue
					}
					var err error
					switch e.current {
					case "GRANT":
						err = sc.Server.GrantServerPermissionContext(ctx, e.entry.Permission, principalName)
					case "DENY":
						err = sc.Server.DenyServerPermissionContext(ctx, e.entry.Permission, principalName)
					case "":
						err = sc.Server.RevokeServerPermissionContext(ctx, e.entry.Permission, principalName)
					}
					if err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
