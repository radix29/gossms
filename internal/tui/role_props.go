package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// rolePropPages builds the page set for Database Role Properties. Owned
// Roles/Owned Schemas/Members/Extended Properties are editable; General is
// editable except for a built-in role's name/owner. Application roles
// (their own principal type, own tree node/listing that doesn't exist
// yet) and the mockup's search/filter boxes, WITH GRANT OPTION, Column
// Permissions, and Effective Permissions modals aren't built — same
// deferrals as every other Properties dialog this pass.
// roleName is boxed in a *string shared by every page below: renaming a
// role is the one edit in this dialog that changes the identity every
// other page's lookup depends on, so pageRoleGeneral's apply closure
// updates *roleName in place on success — otherwise Apply (which reloads
// every page via PropDialog.InvalidateAll) would send the very next reload
// looking for a role name that no longer exists. Same bug class as Key
// Properties' pageKeyGeneral and Server Role Properties'
// pageServerRoleGeneral (see server_role_props.go). dbName never changes,
// so it stays a plain string.
func rolePropPages(sc *db.ServerConn, dbName, roleName string) []propPage {
	namePtr := &roleName
	return []propPage{
		pageRoleGeneral(sc, dbName, namePtr),
		pageRoleMembers(sc, dbName, namePtr),
		pageRoleOwnedSchemas(sc, dbName, namePtr),
		pageRoleOwnedRoles(sc, dbName, namePtr),
		pageRoleSecurables(sc, dbName, namePtr),
		pageRoleExtendedProperties(sc, dbName, namePtr),
	}
}

// findRole resolves dbName/roleName to a *gosmo.DatabaseRole, the one
// lookup every page on this dialog needs first.
func findRole(ctx context.Context, sc *db.ServerConn, dbName, roleName string) (*gosmo.DatabaseRole, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.RoleByNameContext(ctx, roleName)
}

// isBuiltinRole reports whether a role's name/owner can't be changed:
// every fixed role (db_owner, db_datareader, ...), plus public — verified
// live that ALTER ROLE public WITH NAME=... and ALTER AUTHORIZATION ON
// ROLE::public are both syntax errors, even though sys.database_principals
// reports public's is_fixed_role as 0 like a user-defined role.
func isBuiltinRole(role *gosmo.DatabaseRole) bool {
	return role.IsFixedRole || role.Name == "public"
}

// principalNames returns every database principal (user or role) that
// could own a role or schema, or be added as a role member — the
// candidate list every owner/member picker on this dialog draws from.
func principalNames(users []*gosmo.User, roles []*gosmo.DatabaseRole) []string {
	names := make([]string, 0, len(users)+len(roles))
	for _, u := range users {
		names = append(names, u.Name)
	}
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names
}

func pageRoleGeneral(sc *db.ServerConn, dbName string, roleName *string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			role, err := d.RoleByNameContext(ctx, *roleName)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			schemas, err := d.SchemasContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			securables, err := d.PermissionsForPrincipalContext(ctx, *roleName)
			if err != nil {
				return nil, nil, err
			}

			ownedSchemas := 0
			for _, s := range schemas {
				if s.Owner == *roleName {
					ownedSchemas++
				}
			}
			ownedRoles := 0
			for _, r := range roles {
				if r.Owner == *roleName {
					ownedRoles++
				}
			}
			distinctSecurables := make(map[string]bool)
			for _, e := range securables {
				distinctSecurables[securable{e.SecurableType, e.Schema, e.Name}.key()] = true
			}

			builtin := isBuiltinRole(role)
			roleType := "Database role"
			if builtin {
				roleType = "Fixed database role"
			}

			rows := []propsheet.Row{propsheet.Section("Role information")}
			var nameRow *propsheet.TextRow
			var ownerRow *propsheet.SelectRow
			if builtin {
				rows = append(rows,
					propsheet.Static("Role name", role.Name),
					propsheet.Static("Owner", role.Owner),
				)
			} else {
				ownerNames := principalNames(users, roles)
				nameRow = propsheet.Text("Role name", role.Name, 24)
				ownerRow = propsheet.Select("Owner", ownerNames, indexOf(ownerNames, role.Owner))
				rows = append(rows, nameRow, ownerRow)
			}
			rows = append(rows,
				propsheet.Static("Role type", roleType),
				propsheet.Static("Is fixed role", boolStr(role.IsFixedRole)),
				propsheet.Section("Identity"),
				propsheet.Static("Principal ID", strconv.Itoa(role.ID)),
				propsheet.Static("SID", fmt.Sprintf("0x%X", role.SID)),
				propsheet.Static("Created", formatSQLDate(role.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(role.ModifyDate)),
				propsheet.Section("Summary"),
				propsheet.Static("Direct members", strconv.Itoa(len(role.Members))),
				propsheet.Static("Owned schemas", strconv.Itoa(ownedSchemas)),
				propsheet.Static("Owned roles", strconv.Itoa(ownedRoles)),
				propsheet.Static("Explicit securables", strconv.Itoa(len(distinctSecurables))),
			)
			if builtin {
				rows = append(rows,
					propsheet.Section("Built-in behavior"),
					propsheet.Note("This is a built-in role. Its name, owner, and implicit permission set can't be changed; only membership is editable (see Members)."),
				)
			}

			f := propsheet.NewForm(rows...)

			var apply propApply
			if !builtin {
				apply = func(ctx context.Context) error {
					role, err := findRole(ctx, sc, dbName, *roleName)
					if err != nil {
						return err
					}
					if ownerRow.Dirty() {
						if err := role.ChangeOwnerContext(ctx, ownerRow.Value()); err != nil {
							return err
						}
					}
					if nameRow.Dirty() {
						if err := role.RenameContext(ctx, nameRow.Value()); err != nil {
							return err
						}
						*roleName = nameRow.Value()
					}
					return nil
				}
			}
			return f, apply, nil
		},
	}
}

func pageRoleMembers(sc *db.ServerConn, dbName string, roleName *string) propPage {
	return propPage{
		title: "Members",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			members, err := d.RoleMembersContext(ctx, *roleName)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			// memberEdit tracks one grid row's pending state — an existing
			// member pending removal, or a brand-new one pending Add — the
			// same shape as extPropEdit (prop_grid_helpers.go).
			type memberEdit struct {
				name          string
				principalType string
				isNew         bool
				pendingRemove bool
			}
			edits := make([]*memberEdit, len(members))
			memberNames := make(map[string]bool, len(members))
			for i, m := range members {
				edits[i] = &memberEdit{name: m.Name, principalType: m.Type}
				memberNames[m.Name] = true
			}

			principalType := make(map[string]string, len(users)+len(roles))
			for _, u := range users {
				principalType[u.Name] = u.UserType
			}
			for _, r := range roles {
				principalType[r.Name] = "DATABASE_ROLE"
			}

			visible := func() []*memberEdit {
				out := make([]*memberEdit, 0, len(edits))
				for _, e := range edits {
					if !e.pendingRemove {
						out = append(out, e)
					}
				}
				return out
			}
			rowsFor := func() [][]string {
				vis := visible()
				rows := make([][]string, len(vis))
				for i, e := range vis {
					rows[i] = []string{e.name, e.principalType}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Member", "Type"}, rowsFor())
			grid.SetCellCursor(true)

			var candidates []string
			for _, u := range users {
				if !memberNames[u.Name] {
					candidates = append(candidates, u.Name)
				}
			}
			for _, r := range roles {
				if r.Name != *roleName && !memberNames[r.Name] {
					candidates = append(candidates, r.Name)
				}
			}
			if len(candidates) == 0 {
				candidates = []string{noneItem}
			}
			addSelect := propsheet.Select("Add member", candidates, 0)
			addBtn := widgets.NewButton("Add", func() {
				name := addSelect.Value()
				if name == noneItem || memberNames[name] {
					return
				}
				edits = append(edits, &memberEdit{name: name, principalType: principalType[name], isNew: true})
				memberNames[name] = true
				grid.SetData([]string{"Member", "Type"}, rowsFor())
				grid.SetSelectedRow(len(visible()) - 1)
			})
			removeBtn := widgets.NewButton("Remove", func() {
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					return
				}
				delete(memberNames, vis[i].name)
				vis[i].pendingRemove = true
				grid.SetData([]string{"Member", "Type"}, rowsFor())
				grid.SetSelectedRow(0)
			})

			gridRow := propsheet.NewGridRow(grid, 10)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.pendingRemove || e.isNew {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				kept := edits[:0]
				for _, e := range edits {
					if e.isNew {
						continue
					}
					e.pendingRemove = false
					kept = append(kept, e)
				}
				edits = kept
				memberNames = make(map[string]bool, len(edits))
				for _, e := range edits {
					memberNames[e.name] = true
				}
				grid.SetData([]string{"Member", "Type"}, rowsFor())
			}

			f := propsheet.NewForm(
				propsheet.Section("Role members"),
				gridRow,
				addSelect,
				propsheet.Buttons(addBtn, removeBtn),
				propsheet.Note("Add a user or another role from the dropdown, or select a row above and Remove it."),
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				for _, e := range edits {
					switch {
					case e.pendingRemove && !e.isNew:
						if err := d.RemoveRoleMemberContext(ctx, *roleName, e.name); err != nil {
							return err
						}
					case e.isNew && !e.pendingRemove:
						if err := d.AddRoleMemberContext(ctx, *roleName, e.name); err != nil {
							return err
						}
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

func pageRoleOwnedSchemas(sc *db.ServerConn, dbName string, roleName *string) propPage {
	return propPage{
		title: "Owned Schemas",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			allSchemas, err := d.SchemasContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			ownerNames := principalNames(users, roles)

			type schemaEdit struct {
				schema      *gosmo.Schema
				objectCount int
				origOwner   string
				newOwner    string
			}
			var edits []*schemaEdit
			for _, s := range allSchemas {
				if s.Owner != *roleName {
					continue
				}
				count, err := s.ObjectCountContext(ctx)
				if err != nil {
					return nil, nil, err
				}
				edits = append(edits, &schemaEdit{schema: s, objectCount: count, origOwner: s.Owner, newOwner: s.Owner})
			}

			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{e.schema.Name, e.newOwner}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Schema", "Owner"}, rowsFor())
			grid.SetCellCursor(true)

			nameStatic := propsheet.Static("Name", "")
			ownerStatic := propsheet.Static("Current owner", "")
			objCountStatic := propsheet.Static("Object count", "")
			transferRow := propsheet.Select("Transfer owner to", ownerNames, 0)

			selected := -1
			commitCurrent := func() {
				if selected >= 0 && selected < len(edits) {
					edits[selected].newOwner = transferRow.Value()
				}
			}
			syncFromSelection := func(row int) {
				commitCurrent()
				selected = row
				if row < 0 || row >= len(edits) {
					nameStatic.SetValue("")
					ownerStatic.SetValue("")
					objCountStatic.SetValue("")
					return
				}
				e := edits[row]
				nameStatic.SetValue(e.schema.Name)
				ownerStatic.SetValue(e.origOwner)
				objCountStatic.SetValue(strconv.Itoa(e.objectCount))
				transferRow.SetSelected(indexOf(ownerNames, e.newOwner))
			}
			grid.OnSelectRow = syncFromSelection
			if len(edits) > 0 {
				syncFromSelection(0)
			}

			gridRow := propsheet.NewGridRow(grid, 8)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.newOwner != e.origOwner {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				for _, e := range edits {
					e.newOwner = e.origOwner
				}
				grid.SetData([]string{"Schema", "Owner"}, rowsFor())
				if selected >= 0 && selected < len(edits) {
					transferRow.SetSelected(indexOf(ownerNames, edits[selected].newOwner))
				}
			}

			f := propsheet.NewForm(
				propsheet.Section("Schemas owned by this role"),
				gridRow,
				propsheet.Section("Selected schema"),
				nameStatic, ownerStatic, objCountStatic, transferRow,
				propsheet.Note("Changing schema ownership can affect permission chaining and deployment scripts."),
			)

			apply := func(ctx context.Context) error {
				commitCurrent()
				for _, e := range edits {
					if e.newOwner == e.origOwner {
						continue
					}
					if err := e.schema.ChangeOwnerContext(ctx, e.newOwner); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

func pageRoleOwnedRoles(sc *db.ServerConn, dbName string, roleName *string) propPage {
	return propPage{
		title: "Owned Roles",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			allRoles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			ownerNames := principalNames(users, allRoles)

			type ownedRoleEdit struct {
				role      *gosmo.DatabaseRole
				origOwner string
				newOwner  string
			}
			var edits []*ownedRoleEdit
			for _, r := range allRoles {
				if r.Owner == *roleName {
					edits = append(edits, &ownedRoleEdit{role: r, origOwner: r.Owner, newOwner: r.Owner})
				}
			}

			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{e.role.Name, "Database role", strconv.Itoa(len(e.role.Members))}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Role", "Type", "Members"}, rowsFor())
			grid.SetCellCursor(true)

			nameStatic := propsheet.Static("Name", "")
			ownerStatic := propsheet.Static("Current owner", "")
			transferRow := propsheet.Select("Transfer owner to", ownerNames, 0)

			selected := -1
			commitCurrent := func() {
				if selected >= 0 && selected < len(edits) {
					edits[selected].newOwner = transferRow.Value()
				}
			}
			syncFromSelection := func(row int) {
				commitCurrent()
				selected = row
				if row < 0 || row >= len(edits) {
					nameStatic.SetValue("")
					ownerStatic.SetValue("")
					return
				}
				e := edits[row]
				nameStatic.SetValue(e.role.Name)
				ownerStatic.SetValue(e.origOwner)
				transferRow.SetSelected(indexOf(ownerNames, e.newOwner))
			}
			grid.OnSelectRow = syncFromSelection
			if len(edits) > 0 {
				syncFromSelection(0)
			}

			gridRow := propsheet.NewGridRow(grid, 8)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.newOwner != e.origOwner {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				for _, e := range edits {
					e.newOwner = e.origOwner
				}
				grid.SetData([]string{"Role", "Type", "Members"}, rowsFor())
				if selected >= 0 && selected < len(edits) {
					transferRow.SetSelected(indexOf(ownerNames, edits[selected].newOwner))
				}
			}

			f := propsheet.NewForm(
				propsheet.Section("Roles owned by this role"),
				gridRow,
				propsheet.Section("Selected role"),
				nameStatic, ownerStatic, transferRow,
				propsheet.Note("Ownership is not the same as role membership. Transfer ownership carefully for security-administration roles."),
			)

			apply := func(ctx context.Context) error {
				commitCurrent()
				for _, e := range edits {
					if e.newOwner == e.origOwner {
						continue
					}
					if err := e.role.ChangeOwnerContext(ctx, e.newOwner); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

func pageRoleSecurables(sc *db.ServerConn, dbName string, roleName *string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			entries, err := d.PermissionsForPrincipalContext(ctx, *roleName)
			if err != nil {
				return nil, nil, err
			}
			tables, err := d.TablesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			views, err := d.ViewsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			schemas, err := d.SchemasContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			seen := make(map[string]bool)
			var initial []securable
			for _, e := range entries {
				s := securable{Type: e.SecurableType, Schema: e.Schema, Name: e.Name}
				if !seen[s.key()] {
					seen[s.key()] = true
					initial = append(initial, s)
				}
			}

			var available []securable
			addIfNew := func(s securable) {
				if !seen[s.key()] {
					seen[s.key()] = true
					available = append(available, s)
				}
			}
			addIfNew(securable{Type: "DATABASE"})
			for _, s := range schemas {
				addIfNew(securable{Type: "SCHEMA", Name: s.Name})
			}
			for _, t := range tables {
				addIfNew(securable{Type: "TABLE", Schema: t.Schema, Name: t.Name})
			}
			for _, v := range views {
				addIfNew(securable{Type: "VIEW", Schema: v.Schema, Name: v.Name})
			}

			f, apply := buildSecurablesMatrix(initial, entries, available, 8, 12,
				func(ctx context.Context, s securable, permission string) error {
					return grantSecurable(ctx, d, s, permission, *roleName)
				},
				func(ctx context.Context, s securable, permission string) error {
					return denySecurable(ctx, d, s, permission, *roleName)
				},
				func(ctx context.Context, s securable, permission string) error {
					return revokeSecurable(ctx, d, s, permission, *roleName)
				},
			)
			return f, apply, nil
		},
	}
}

func pageRoleExtendedProperties(sc *db.ServerConn, dbName string, roleName *string) propPage {
	return propPage{
		title: "Extended Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			// Database roles are classed as USER in
			// sp_addextendedproperty/sp_updateextendedproperty level names —
			// they're database principals like users, not a level of their own.
			level := gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: *roleName}
			props, err := d.ExtendedPropertiesContext(ctx, level)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildExtendedPropertiesForm(sc, dbName, level, props)
			return f, apply, nil
		},
	}
}
