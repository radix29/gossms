package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// rolePropPages builds the page set for Database Role Properties. Owned
// Roles/Owned Schemas/Members/Extended Properties are editable; General is
// editable except for a built-in role's name/owner. Application roles are
// not covered — they're a separate principal type with no tree node of
// their own — nor are search/filter boxes, WITH GRANT OPTION, Column
// Permissions, or Effective Permissions.
//
// roleName is boxed in a *string shared by every page below: renaming a role
// changes the identity every other page's lookup depends on. The
// rename is the last write of an Apply/OK run (see propPage.renames),
// and commitRename then updates the box so PropDialog.InvalidateAll's
// reload re-fetches under the new name. dbName never changes, so it
// stays a plain string.
func rolePropPages(sc *db.ServerConn, dbName, roleName string) []propPage {
	namePtr := &roleName
	return []propPage{
		pageRoleGeneral(sc, dbName, namePtr),
		pageRoleMembers(sc, dbName, namePtr),
		pagePrincipalOwnedSchemas(sc, dbName, namePtr, "role"),
		pageRoleOwnedRoles(sc, dbName, namePtr),
		pageDatabasePrincipalSecurables(sc, dbName, namePtr),
		// A database role is classed as USER in sp_addextendedproperty's
		// level names — it's a database principal like a user, not a
		// level of its own.
		pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: *namePtr}
		}),
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
// every fixed role (db_owner, db_datareader, ...), plus public — ALTER ROLE
// public WITH NAME=... and ALTER AUTHORIZATION ON ROLE::public are both
// syntax errors, even though sys.database_principals reports public's
// is_fixed_role as 0 like a user-defined role.
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
		title:   "General",
		renames: true,
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
						commitRename(ctx, roleName, nameRow.Value())
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

			existing := roleMemberSet(members)
			principalType := make(map[string]string, len(users)+len(roles))
			var candidates []string
			for _, u := range users {
				principalType[u.Name] = u.UserType
				if !existing[u.Name] {
					candidates = append(candidates, u.Name)
				}
			}
			for _, r := range roles {
				principalType[r.Name] = "DATABASE_ROLE"
				if r.Name != *roleName && !existing[r.Name] {
					candidates = append(candidates, r.Name)
				}
			}

			f, apply := buildMembershipForm(membershipConfig{
				members:       members,
				candidates:    candidates,
				principalType: principalType,
				note:          "Add a user or another role from the dropdown, or select a row above and Remove it.",
				add: func(ctx context.Context, name string) error {
					return d.AddRoleMemberContext(ctx, *roleName, name)
				},
				remove: func(ctx context.Context, name string) error {
					return d.RemoveRoleMemberContext(ctx, *roleName, name)
				},
			})
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

			var items []*ownerTransferItem[*gosmo.DatabaseRole]
			for _, r := range allRoles {
				if r.Owner == *roleName {
					items = append(items, &ownerTransferItem[*gosmo.DatabaseRole]{
						obj: r, name: r.Name, origOwner: r.Owner, newOwner: r.Owner,
					})
				}
			}

			f, apply := newOwnerTransferPage(items, principalNames(users, allRoles), ownerTransferSpec[*gosmo.DatabaseRole]{
				Headers: []string{"Role", "Type", "Members"},
				Cells: func(it *ownerTransferItem[*gosmo.DatabaseRole]) []string {
					return []string{it.name, "Database role", strconv.Itoa(len(it.obj.Members))}
				},
				GridSection: "Roles owned by this role",
				ItemSection: "Selected role",
				Note:        "Ownership is not the same as role membership. Transfer ownership carefully for security-administration roles.",
				ChangeOwner: func(ctx context.Context, r *gosmo.DatabaseRole, newOwner string) error {
					return r.ChangeOwnerContext(ctx, newOwner)
				},
			})
			return f, apply, nil
		},
	}
}
