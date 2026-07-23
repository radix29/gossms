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

// serverRolePropPages builds the page set for Server Role Properties — SSMS
// as reference, no mockup for this one, same as Key Properties earlier this
// month. A server role is a server-level principal (sys.server_principals,
// type='R'), the direct server-scope counterpart of rolePropPages'
// database roles, and reuses that dialog's shape everywhere the two scopes
// actually match. Two rolePropPages pages are dropped: Owned Schemas
// (schemas are database-scoped, a server role can't own one) and Extended
// Properties (live-verified: sp_addextendedproperty rejects both
// @level0type=N'SERVER ROLE' and N'LOGIN' outright — server-level
// principals don't support extended properties at all, unlike every
// database-scoped principal this app has a Properties dialog for).
// Securables reuses pagePrincipalServerPermissions, the same server-scoped
// GRANT/DENY editor Login Properties' own Securables page already uses —
// a server role and a login are both server-level principals that can
// hold the same kind of explicit permission entries.
//
// roleName is boxed in a *string shared by every page below: renaming a
// role is the one edit in this dialog that changes the identity every
// other page's lookup depends on, so pageServerRoleGeneral's apply closure
// updates *roleName in place on success — otherwise Apply (which reloads
// every page via PropDialog.InvalidateAll) would send the very next reload
// looking for a role name that no longer exists. Live-caught: renaming
// claude_role_a and clicking Apply failed every page's reload with
// `server role "claude_role_a" not found` before this fix — same bug class
// as Key Properties' pageKeyGeneral, see key_props.go.
func serverRolePropPages(sc *db.ServerConn, roleName string) []propPage {
	namePtr := &roleName
	return []propPage{
		pageServerRoleGeneral(sc, namePtr),
		pageServerRoleMembers(sc, namePtr),
		pageServerRoleOwnedRoles(sc, namePtr),
		pageServerRoleSecurables(sc, namePtr),
	}
}

// findServerRole resolves roleName to a *gosmo.ServerRole, the one lookup
// every page on this dialog needs first.
func findServerRole(ctx context.Context, sc *db.ServerConn, roleName string) (*gosmo.ServerRole, error) {
	return sc.Server.ServerRoleByNameContext(ctx, roleName)
}

// isBuiltinServerRole reports whether a server role's name/owner can't be
// changed: every fixed role (sysadmin, dbcreator, ...) plus public —
// live-verified that both ALTER SERVER ROLE public WITH NAME=... and
// ALTER AUTHORIZATION ON SERVER ROLE::public are syntax errors ("public"
// is a reserved keyword in this position), even though public's
// is_fixed_role is 0, mirroring the database-level public role's identical
// restriction (see isBuiltinRole in role_props.go).
func isBuiltinServerRole(role *gosmo.ServerRole) bool {
	return role.IsFixedRole || role.Name == "public"
}

// serverPrincipalNames returns every server principal (login or server
// role) that could own a server role or be added as a member — the
// candidate list every owner/member picker on this dialog draws from.
func serverPrincipalNames(logins []*gosmo.Login, roles []*gosmo.ServerRole) []string {
	names := make([]string, 0, len(logins)+len(roles))
	for _, l := range logins {
		names = append(names, l.Name)
	}
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names
}

func pageServerRoleGeneral(sc *db.ServerConn, roleName *string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			role, err := findServerRole(ctx, sc, *roleName)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := sc.Server.ServerRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			perms, err := sc.Server.ServerPermissionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			ownedRoles := 0
			for _, r := range roles {
				if r.Owner == *roleName {
					ownedRoles++
				}
			}
			explicitPerms := 0
			for _, p := range perms {
				if p.Principal == *roleName {
					explicitPerms++
				}
			}

			builtin := isBuiltinServerRole(role)
			roleType := "Server role"
			if builtin {
				roleType = "Fixed server role"
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
				ownerNames := serverPrincipalNames(logins, roles)
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
				propsheet.Static("Owned roles", strconv.Itoa(ownedRoles)),
				propsheet.Static("Explicit permissions", strconv.Itoa(explicitPerms)),
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
					role, err := findServerRole(ctx, sc, *roleName)
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

func pageServerRoleMembers(sc *db.ServerConn, roleName *string) propPage {
	return propPage{
		title: "Members",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			members, err := sc.Server.ServerRoleMembersContext(ctx, *roleName)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := sc.Server.ServerRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			// memberEdit tracks one grid row's pending state — an existing
			// member pending removal, or a brand-new one pending Add — the
			// same shape as rolePropPages' pageRoleMembers.
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

			principalType := make(map[string]string, len(logins)+len(roles))
			for _, l := range logins {
				principalType[l.Name] = l.LoginType
			}
			for _, r := range roles {
				principalType[r.Name] = "SERVER_ROLE"
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
			for _, l := range logins {
				if !memberNames[l.Name] {
					candidates = append(candidates, l.Name)
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
				propsheet.Note("Add a login or another server role from the dropdown, or select a row above and Remove it."),
			)

			apply := func(ctx context.Context) error {
				for _, e := range edits {
					switch {
					case e.pendingRemove && !e.isNew:
						if err := sc.Server.RemoveServerRoleMemberContext(ctx, *roleName, e.name); err != nil {
							return err
						}
					case e.isNew && !e.pendingRemove:
						if err := sc.Server.AddServerRoleMemberContext(ctx, *roleName, e.name); err != nil {
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

func pageServerRoleOwnedRoles(sc *db.ServerConn, roleName *string) propPage {
	return propPage{
		title: "Owned Roles",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			allRoles, err := sc.Server.ServerRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			ownerNames := serverPrincipalNames(logins, allRoles)

			type ownedRoleEdit struct {
				role      *gosmo.ServerRole
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
					rows[i] = []string{e.role.Name, "Server role", strconv.Itoa(len(e.role.Members))}
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

// pageServerRoleSecurables wraps pagePrincipalServerPermissions rather than
// just returning it directly: roleName must be dereferenced at load time,
// not here at page-construction time, so a reload after a rename (see
// serverRolePropPages) picks up the new name instead of freezing the one
// captured when the dialog first opened.
func pageServerRoleSecurables(sc *db.ServerConn, roleName *string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			return pagePrincipalServerPermissions(sc, *roleName).load(ctx)
		},
	}
}
