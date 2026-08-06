package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// serverRolePropPages builds the page set for Server Role Properties. A
// server role is a server-level principal (sys.server_principals, type='R'),
// the server-scope counterpart of rolePropPages' database roles, and reuses
// that dialog's shape wherever the two scopes match. Two of its pages are
// dropped: Owned Schemas (schemas are database-scoped, a server role can't
// own one) and Extended Properties (sp_addextendedproperty rejects both
// @level0type=N'SERVER ROLE' and N'LOGIN' — server-level principals don't
// support extended properties at all). Securables reuses
// pagePrincipalServerPermissions, the same server-scoped GRANT/DENY editor
// Login Properties' Securables page uses.
//
// Effective Permissions is dropped for the same reason Database Role
// Properties drops it — a server role cannot be impersonated either
// (Msg 15406, the server-principal wording of the same error). See
// rolePropPages.
//
// roleName is boxed in a *string shared by every page below: renaming a role
// changes the identity every other page's lookup depends on. The
// rename is the last write of an Apply/OK run (see propPage.renames),
// and commitRename then updates the box so PropDialog.InvalidateAll's
// reload re-fetches under the new name.
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
// changed: every fixed role (sysadmin, dbcreator, ...) plus public — both
// ALTER SERVER ROLE public WITH NAME=... and ALTER AUTHORIZATION ON SERVER
// ROLE::public are syntax errors ("public" is a reserved keyword in this
// position), even though public's is_fixed_role is 0. The database-level
// public role has the same restriction (see isBuiltinRole in
// role_props.go).
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
		title:   "General",
		renames: true,
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
						commitRename(ctx, roleName, nameRow.Value())
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

			existing := roleMemberSet(members)
			principalType := make(map[string]string, len(logins)+len(roles))
			var candidates []string
			for _, l := range logins {
				principalType[l.Name] = l.LoginType
				if !existing[l.Name] {
					candidates = append(candidates, l.Name)
				}
			}
			for _, r := range roles {
				principalType[r.Name] = "SERVER_ROLE"
				if r.Name != *roleName && !existing[r.Name] {
					candidates = append(candidates, r.Name)
				}
			}

			f, apply := buildMembershipForm(membershipConfig{
				members:       members,
				candidates:    candidates,
				principalType: principalType,
				note:          "Add a login or another server role from the dropdown, or select a row above and Remove it.",
				add: func(ctx context.Context, name string) error {
					return sc.Server.AddServerRoleMemberContext(ctx, *roleName, name)
				},
				remove: func(ctx context.Context, name string) error {
					return sc.Server.RemoveServerRoleMemberContext(ctx, *roleName, name)
				},
			})
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

			var items []*ownerTransferItem[*gosmo.ServerRole]
			for _, r := range allRoles {
				if r.Owner == *roleName {
					items = append(items, &ownerTransferItem[*gosmo.ServerRole]{
						obj: r, name: r.Name, origOwner: r.Owner, newOwner: r.Owner,
					})
				}
			}

			f, apply := newOwnerTransferPage(items, serverPrincipalNames(logins, allRoles), ownerTransferSpec[*gosmo.ServerRole]{
				Headers: []string{"Role", "Type", "Members"},
				Cells: func(it *ownerTransferItem[*gosmo.ServerRole]) []string {
					return []string{it.name, "Server role", strconv.Itoa(len(it.obj.Members))}
				},
				GridSection: "Roles owned by this role",
				ItemSection: "Selected role",
				Note:        "Ownership is not the same as role membership. Transfer ownership carefully for security-administration roles.",
				ChangeOwner: func(ctx context.Context, r *gosmo.ServerRole, newOwner string) error {
					return r.ChangeOwnerContext(ctx, newOwner)
				},
			})
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
