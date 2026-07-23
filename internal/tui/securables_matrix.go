package tui

import (
	"context"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// securable identifies one thing a database role's Securables page can
// grant/deny permissions on: a table, a view, a schema, or the database
// itself.
type securable struct {
	Type   string // "TABLE", "VIEW", "SCHEMA", "DATABASE"
	Schema string // empty for DATABASE
	Name   string // empty for DATABASE
}

func (s securable) key() string { return s.Type + "\x00" + s.Schema + "\x00" + s.Name }

func (s securable) label() string {
	switch s.Type {
	case "DATABASE":
		return "(database)"
	case "SCHEMA":
		return "[" + s.Name + "]"
	default:
		return "[" + s.Schema + "].[" + s.Name + "]"
	}
}

// catalog returns every permission name grantable on this securable's type.
func (s securable) catalog() []string {
	switch s.Type {
	case "SCHEMA":
		return gosmo.SchemaPermissionNames()
	case "DATABASE":
		return gosmo.DatabasePermissionNames()
	default:
		return gosmo.ObjectPermissionNames()
	}
}

// securableEdit tracks one (securable, permission) cell's pending state,
// same GRANT -> DENY -> "" (revoke) -> GRANT cycle as permEdit.
type securableEdit struct {
	sec        securable
	permission string
	orig       string
	current    string
}

// buildSecurablesMatrix builds a Database Role Properties' Securables
// page: a securable-list grid (top) and, for whichever securable is
// selected, the full permission catalog for that securable's type x
// Grant/Deny/(none) state (bottom) — the inverse of buildPermissionsMatrix
// (that one is "one securable, every principal"; this is "one principal,
// every securable"). initial is every securable the principal already has
// at least one explicit grant/deny on (seeded from
// gosmo.PermissionsForPrincipalContext); available lists every other
// addable securable — picking one from the dropdown and clicking Add
// appends a new, all-"(none)" row. grantFn/denyFn/revokeFn are routed to
// the right gosmo Grant/Deny/RevokeXPermissionContext call based on the
// selected securable's Type.
func buildSecurablesMatrix(
	initial []securable, entries []*gosmo.PrincipalSecurable, available []securable,
	securablesHeight, permsHeight int,
	grantFn, denyFn, revokeFn func(ctx context.Context, s securable, permission string) error,
) (*propsheet.Form, propApply) {
	type entryKey struct{ secKey, permission string }
	existing := make(map[entryKey]string, len(entries))
	for _, e := range entries {
		k := securable{Type: e.SecurableType, Schema: e.Schema, Name: e.Name}
		existing[entryKey{k.key(), e.Permission}] = e.State
	}

	securables := append([]securable(nil), initial...)

	// editsBySecurable is built lazily, one securable at a time, as the
	// user browses the securable grid — a securable never selected has by
	// definition nothing to apply or check for dirtiness.
	editsBySecurable := make(map[string][]*securableEdit)
	editsFor := func(s securable) []*securableEdit {
		if e, ok := editsBySecurable[s.key()]; ok {
			return e
		}
		catalog := s.catalog()
		edits := make([]*securableEdit, len(catalog))
		for i, perm := range catalog {
			state := existing[entryKey{s.key(), perm}]
			edits[i] = &securableEdit{sec: s, permission: perm, orig: state, current: state}
		}
		editsBySecurable[s.key()] = edits
		return edits
	}

	securableRows := func() [][]string {
		rows := make([][]string, len(securables))
		for i, s := range securables {
			rows[i] = []string{s.label(), s.Type}
		}
		return rows
	}
	securableGrid := controls.NewDataGrid()
	securableGrid.SetData([]string{"Securable", "Type"}, securableRows())

	permGrid := controls.NewDataGrid()
	permGrid.SetCellCursor(true)
	permRowsFor := func(edits []*securableEdit) [][]string {
		rows := make([][]string, len(edits))
		for i, e := range edits {
			rows[i] = []string{e.permission, displayPermState(e.current)}
		}
		return rows
	}

	permSection := propsheet.Section("Permissions")
	selected := -1
	loadSecurable := func(row int) {
		if row < 0 || row >= len(securables) || row == selected {
			return
		}
		selected = row
		permGrid.SetData([]string{"Permission", "State"}, permRowsFor(editsFor(securables[row])))
		permGrid.SetSelectedRow(0)
		permSection.SetTitle("Permissions for " + securables[row].label())
	}
	securableGrid.OnSelectRow = loadSecurable
	if len(securables) > 0 {
		loadSecurable(0)
	}

	permGrid.OnActivateCell = func(row, col int) {
		if selected < 0 || col != 1 {
			return
		}
		edits := editsBySecurable[securables[selected].key()]
		if row < 0 || row >= len(edits) {
			return
		}
		edits[row].current = nextPermState(edits[row].current)
		permGrid.SetData([]string{"Permission", "State"}, permRowsFor(edits))
		permGrid.SetSelectedRow(row)
	}

	availableLabels := make([]string, len(available))
	for i, s := range available {
		availableLabels[i] = s.label()
	}
	if len(availableLabels) == 0 {
		availableLabels = []string{"(none available)"}
	}
	addSelect := propsheet.Select("Add securable", availableLabels, 0)
	addBtn := widgets.NewButton("Add", func() {
		if len(available) == 0 {
			return
		}
		s := available[addSelect.Selected()]
		for _, existingS := range securables {
			if existingS.key() == s.key() {
				return // already present — edit its Permissions row instead
			}
		}
		securables = append(securables, s)
		securableGrid.SetData([]string{"Securable", "Type"}, securableRows())
		securableGrid.SetSelectedRow(len(securables) - 1)
		loadSecurable(len(securables) - 1)
	})

	securablesRow := propsheet.NewGridRow(securableGrid, securablesHeight)
	permsRow := propsheet.NewGridRow(permGrid, permsHeight)
	permsRow.DirtyFn = func() bool {
		for _, edits := range editsBySecurable {
			for _, e := range edits {
				if e.current != e.orig {
					return true
				}
			}
		}
		return false
	}
	permsRow.RevertFn = func() {
		for _, edits := range editsBySecurable {
			for _, e := range edits {
				e.current = e.orig
			}
		}
		if selected >= 0 {
			permGrid.SetData([]string{"Permission", "State"}, permRowsFor(editsBySecurable[securables[selected].key()]))
		}
	}

	f := propsheet.NewForm(
		propsheet.Section("Securables"),
		securablesRow,
		addSelect,
		propsheet.Buttons(addBtn),
		permSection,
		permsRow,
		propsheet.Note("Space/Enter (or click) on State cycles Grant → Deny → (none). Tab switches between the securable list and its permissions. Pick a securable from the dropdown and click Add to give it its own row."),
	)

	apply := func(ctx context.Context) error {
		for _, edits := range editsBySecurable {
			for _, e := range edits {
				if e.current == e.orig {
					continue
				}
				var err error
				switch e.current {
				case "GRANT":
					err = grantFn(ctx, e.sec, e.permission)
				case "DENY":
					err = denyFn(ctx, e.sec, e.permission)
				case "":
					err = revokeFn(ctx, e.sec, e.permission)
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

// grantSecurable, denySecurable, and revokeSecurable route a
// buildSecurablesMatrix grant/deny/revoke call to the right gosmo method
// based on s.Type — tables/views use the object-level trio, schemas the
// schema-level trio, and the database itself the database-scoped trio.
func grantSecurable(ctx context.Context, d *gosmo.Database, s securable, permission, principal string) error {
	switch s.Type {
	case "SCHEMA":
		return d.GrantSchemaPermissionContext(ctx, s.Name, gosmo.ObjectPermission(permission), principal)
	case "DATABASE":
		return d.GrantDatabasePermissionContext(ctx, permission, principal)
	default:
		return d.GrantPermissionContext(ctx, s.Schema, s.Name, gosmo.ObjectPermission(permission), principal)
	}
}

func denySecurable(ctx context.Context, d *gosmo.Database, s securable, permission, principal string) error {
	switch s.Type {
	case "SCHEMA":
		return d.DenySchemaPermissionContext(ctx, s.Name, gosmo.ObjectPermission(permission), principal)
	case "DATABASE":
		return d.DenyDatabasePermissionContext(ctx, permission, principal)
	default:
		return d.DenyPermissionContext(ctx, s.Schema, s.Name, gosmo.ObjectPermission(permission), principal)
	}
}

func revokeSecurable(ctx context.Context, d *gosmo.Database, s securable, permission, principal string) error {
	switch s.Type {
	case "SCHEMA":
		return d.RevokeSchemaPermissionContext(ctx, s.Name, gosmo.ObjectPermission(permission), principal)
	case "DATABASE":
		return d.RevokeDatabasePermissionContext(ctx, permission, principal)
	default:
		return d.RevokePermissionContext(ctx, s.Schema, s.Name, gosmo.ObjectPermission(permission), principal)
	}
}
