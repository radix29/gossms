package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// rolePropPages builds the page set for Database Role Properties. Owned
// Roles/Owned Schemas/Members/Extended Properties are editable; General is
// editable except for a built-in role's name/owner. Application roles are
// not covered — they're a separate principal type with no tree node of
// their own.
//
// There is deliberately no Effective Permissions page here, unlike Database
// User Properties. Resolving effective permissions means impersonating the
// principal (gosmo's EffectivePermissions, and SSMS's own Effective tab,
// both work that way), and SQL Server refuses to impersonate a role —
// Msg 15517, "this type of principal cannot be impersonated", verified live
// 2026-08-05 against a role that did exist. Adding the page back would give
// a tab whose only possible outcome is that error.
//
// roleName is boxed in a *string shared by every page below: renaming a role
// changes the identity every other page's lookup depends on. The
// rename is the last write of an Apply/OK run (see propPage.renames),
// and commitRename then updates the box so PropDialog.InvalidateAll's
// reload re-fetches under the new name. dbName never changes, so it
// stays a plain string.
func rolePropPages(d *PropDialog, sc *db.ServerConn, dbName, roleName string) []propPage {
	namePtr := &roleName
	return []propPage{
		withRequires(pageRoleGeneral(sc, dbName, namePtr), dbName, rightAlterAnyDBRole),
		// Members, alone on this dialog, is gated on the role itself: a class-4
		// DENY on the role withholds ADD/DROP MEMBER while leaving the rename
		// and the drop the other pages make alone. The name is the one the
		// dialog was opened with — a rename is not blocked by that DENY, and
		// the probe that answers the question recorded the old name too.
		withRequiresOn(pageRoleMembers(sc, dbName, namePtr), dbName, "", roleName, rightAlterAnyDBRoleMembers),
		withRequires(pagePrincipalOwnedSchemas(sc, dbName, namePtr, "role"), dbName, rightAlterAnySchema, rightControlDB),
		withRequires(pageRoleOwnedRoles(sc, dbName, namePtr), dbName, rightAlterAnyDBRole),
		withRequires(pageDatabasePrincipalSecurables(d, sc, dbName, namePtr), dbName, rightControlDB),
		// A database role is classed as USER in sp_addextendedproperty's
		// level names — it's a database principal like a user, not a
		// level of its own.
		withRequires(pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: *namePtr}
		}), dbName, rightAlterAnyDBRole),
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
	return roleGeneralPage(roleName,
		func(ctx context.Context) (roleGeneral, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return roleGeneral{}, err
			}
			role, err := d.RoleByNameContext(ctx, *roleName)
			if err != nil {
				return roleGeneral{}, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return roleGeneral{}, err
			}
			roles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return roleGeneral{}, err
			}
			schemas, err := d.SchemasContext(ctx)
			if err != nil {
				return roleGeneral{}, err
			}
			securables, err := d.PermissionsForPrincipalContext(ctx, *roleName)
			if err != nil {
				return roleGeneral{}, err
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
			// Counted distinct: one securable with SELECT, INSERT and UPDATE
			// on it is one securable, not three permission rows.
			distinctSecurables := make(map[string]bool)
			for _, e := range securables {
				distinctSecurables[securable{e.SecurableType, e.Schema, e.Name}.key()] = true
			}

			builtin := isSystemDatabaseRole(role)
			roleType := "Database role"
			if builtin {
				roleType = "Fixed database role"
			}
			return roleGeneral{
				name: role.Name, owner: role.Owner, isFixedRole: role.IsFixedRole,
				id: role.ID, sid: role.SID, created: role.CreateDate, modified: role.ModifyDate,
				members: len(role.Members),
				builtin: builtin, roleType: roleType,
				ownerNames: principalNames(users, roles),
				summary: []propsheet.Row{
					propsheet.Static("Owned schemas", strconv.Itoa(ownedSchemas)),
					propsheet.Static("Owned roles", strconv.Itoa(ownedRoles)),
					propsheet.Static("Explicit securables", strconv.Itoa(len(distinctSecurables))),
				},
			}, nil
		},
		func(ctx context.Context) (roleWriter, error) {
			return findRole(ctx, sc, dbName, *roleName)
		},
	)
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
