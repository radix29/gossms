package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// userPropPages builds the page set for Database User Properties. General
// is editable except for the fixed system users (dbo/guest/sys/
// INFORMATION_SCHEMA, which reject ALTER USER entirely); Owned Schemas,
// Membership, Securables, and Extended Properties are always editable.
// Contained/password users and external Microsoft Entra users aren't built.
//
// userName is boxed in a *string shared by every page below: renaming a user
// changes the identity every other page's lookup depends on. The
// rename is the last write of an Apply/OK run (see propPage.renames),
// and commitRename then updates the box so PropDialog.InvalidateAll's
// reload re-fetches under the new name. dbName never changes, so it
// stays a plain string.
func userPropPages(sc *db.ServerConn, dbName, userName string) []propPage {
	namePtr := &userName
	return []propPage{
		pageUserGeneral(sc, dbName, namePtr),
		pagePrincipalOwnedSchemas(sc, dbName, namePtr, "user"),
		pageUserMembership(sc, dbName, namePtr),
		pageDatabasePrincipalSecurables(sc, dbName, namePtr),
		pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: *namePtr}
		}),
	}
}

// findUser resolves dbName/userName to a *gosmo.User, the one lookup
// every page on this dialog needs first.
func findUser(ctx context.Context, sc *db.ServerConn, dbName, userName string) (*gosmo.User, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.UserByNameContext(ctx, userName)
}

// isSystemUser reports whether a user's name/login/default schema can't be
// changed: ALTER USER on any of these fails outright ("Cannot rename the
// user 'guest'.", "Cannot alter the user 'dbo'.", same for
// sys/INFORMATION_SCHEMA), unlike the ordinary permission errors other
// ALTER USER failures produce.
func isSystemUser(name string) bool {
	switch name {
	case "dbo", "guest", "sys", "INFORMATION_SCHEMA":
		return true
	default:
		return false
	}
}

func pageUserGeneral(sc *db.ServerConn, dbName string, userName *string) propPage {
	return propPage{
		title:   "General",
		renames: true,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			u, err := d.UserByNameContext(ctx, *userName)
			if err != nil {
				return nil, nil, err
			}
			schemas, err := d.SchemasContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			securables, err := d.PermissionsForPrincipalContext(ctx, *userName)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			ownedSchemas := 0
			for _, s := range schemas {
				if s.Owner == *userName {
					ownedSchemas++
				}
			}
			memberships := 0
			for _, r := range roles {
				if r.Name != "public" && slices.Contains(r.Members, *userName) {
					memberships++
				}
			}
			distinctSecurables := make(map[string]bool)
			for _, e := range securables {
				distinctSecurables[securable{e.SecurableType, e.Schema, e.Name}.key()] = true
			}

			var userType string
			switch u.AuthType {
			case "INSTANCE":
				if u.LoginName != "" {
					userType = "SQL user with login"
				} else {
					// A genuine CREATE USER ... WITHOUT LOGIN reports
					// authentication_type_desc = NONE, not INSTANCE:
					// INSTANCE with no matching login only happens when a
					// FOR LOGIN user's login was dropped out from under
					// it, i.e. orphaned.
					userType = "SQL user with login (not found)"
				}
			case "DATABASE":
				userType = "SQL user with password"
			case "EXTERNAL":
				userType = "External user or group"
			default:
				userType = "SQL user without login"
			}

			builtin := isSystemUser(u.Name)

			rows := []propsheet.Row{propsheet.Section("User information")}
			var nameRow *propsheet.TextRow
			var loginRow, schemaRow *propsheet.SelectRow
			if builtin {
				rows = append(rows,
					propsheet.Static("User name", u.Name),
					propsheet.Static("User type", userType),
					propsheet.Static("Login name", orDefault(u.LoginName, "n/a")),
					propsheet.Static("Default schema", orDefault(u.DefaultSchema, "n/a")),
				)
			} else {
				nameRow = propsheet.Text("User name", u.Name, 24)

				loginNames := make([]string, len(logins))
				for i, l := range logins {
					loginNames[i] = l.Name
				}
				loginItems := append([]string{noneItem}, loginNames...)
				loginSelected := 0
				if u.LoginName != "" {
					loginSelected = indexOf(loginItems, u.LoginName)
				}
				loginRow = propsheet.Select("Login name", loginItems, loginSelected)

				schemaNames := make([]string, len(schemas))
				for i, s := range schemas {
					schemaNames[i] = s.Name
				}
				schemaRow = propsheet.Select("Default schema", schemaNames, indexOf(schemaNames, u.DefaultSchema))

				rows = append(rows,
					nameRow,
					propsheet.Static("User type", userType),
					loginRow,
					schemaRow,
				)
			}
			rows = append(rows,
				propsheet.Static("Authentication type", u.AuthType),
				propsheet.Section("Identity"),
				propsheet.Static("Principal ID", strconv.Itoa(u.ID)),
				propsheet.Static("SID", fmt.Sprintf("0x%X", u.SID)),
				propsheet.Static("Created", formatSQLDate(u.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(u.ModifyDate)),
				propsheet.Section("Status"),
				propsheet.Static("Login disabled", loginDisabledStr(u)),
				propsheet.Section("Summary"),
				propsheet.Static("Database role memberships", strconv.Itoa(memberships)),
				propsheet.Static("Owned schemas", strconv.Itoa(ownedSchemas)),
				propsheet.Static("Explicit securables", strconv.Itoa(len(distinctSecurables))),
			)
			if builtin {
				rows = append(rows,
					propsheet.Section("Built-in user"),
					propsheet.Note("This is a built-in user. Its name, login mapping, and default schema can't be changed."),
				)
			}

			f := propsheet.NewForm(rows...)

			var apply propApply
			if !builtin {
				apply = func(ctx context.Context) error {
					u, err := findUser(ctx, sc, dbName, *userName)
					if err != nil {
						return err
					}
					if schemaRow.Dirty() {
						if err := u.SetDefaultSchemaContext(ctx, schemaRow.Value()); err != nil {
							return err
						}
					}
					if loginRow.Dirty() && loginRow.Value() != noneItem {
						if err := u.SetLoginContext(ctx, loginRow.Value()); err != nil {
							return err
						}
					}
					if nameRow.Dirty() {
						if err := u.RenameContext(ctx, nameRow.Value()); err != nil {
							return err
						}
						commitRename(ctx, userName, nameRow.Value())
					}
					return nil
				}
			}
			return f, apply, nil
		},
	}
}

// loginDisabledStr renders a user's mapped-login disabled state, or
// "n/a" when no login is mapped (WITHOUT LOGIN, or the login no longer
// exists — SQL Server's catalog metadata can't tell those apart).
func loginDisabledStr(u *gosmo.User) string {
	if u.LoginName == "" {
		return "n/a"
	}
	return boolStr(u.LoginDisabled)
}

func pageUserMembership(sc *db.ServerConn, dbName string, userName *string) propPage {
	return propPage{
		title: "Membership",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			allRoles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			// public's membership is implicit (never an explicit row in
			// sys.database_role_members) and ALTER ROLE public ADD/DROP
			// MEMBER is a syntax error — exclude it, same as
			// role_props.go's isBuiltinRole treats it as non-interactive.
			var roles []*gosmo.DatabaseRole
			for _, r := range allRoles {
				if r.Name != "public" {
					roles = append(roles, r)
				}
			}

			text := make([][]string, len(roles))
			values := make([][]bool, len(roles))
			for i, r := range roles {
				text[i] = []string{r.Name, fixedRoleDescriptions[r.Name]}
				values[i] = []bool{slices.Contains(r.Members, *userName)}
			}
			rolesGrid := propsheet.NewToggleGrid([]string{"Member", "Role", "Description"}, []int{0}, 10)
			rolesGrid.SetRows(text, values)

			roleTypeStatic := propsheet.Static("Role type", "")
			ownerStatic := propsheet.Static("Owner", "")
			membersStatic := propsheet.Static("Members", "")
			syncFromSelection := func(row int) {
				if row < 0 || row >= len(roles) {
					roleTypeStatic.SetValue("")
					ownerStatic.SetValue("")
					membersStatic.SetValue("")
					return
				}
				r := roles[row]
				roleType := "Database role"
				if r.IsFixedRole {
					roleType = "Fixed database role"
				}
				roleTypeStatic.SetValue(roleType)
				ownerStatic.SetValue(r.Owner)
				membersStatic.SetValue(strconv.Itoa(len(r.Members)))
			}
			rolesGrid.Grid.OnSelectRow = syncFromSelection
			if len(roles) > 0 {
				syncFromSelection(0)
			}

			f := propsheet.NewForm(
				propsheet.Section("Database role membership"),
				rolesGrid,
				propsheet.Section("Selected role"),
				roleTypeStatic, ownerStatic, membersStatic,
				propsheet.Note("Space/Enter (or click) on Member toggles this user's membership in the selected role."),
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				for i, v := range rolesGrid.Values() {
					member := v[0]
					wasMember := slices.Contains(roles[i].Members, *userName)
					if member == wasMember {
						continue
					}
					if member {
						if err := d.AddRoleMemberContext(ctx, roles[i].Name, *userName); err != nil {
							return err
						}
					} else {
						if err := d.RemoveRoleMemberContext(ctx, roles[i].Name, *userName); err != nil {
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
