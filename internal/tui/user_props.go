package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// userPropPages builds the page set for Database User Properties. General
// is editable except for the fixed system users (dbo/guest/sys/
// INFORMATION_SCHEMA, which reject ALTER USER entirely); Owned Schemas,
// Membership, Securables, and Extended Properties are always editable, and
// Effective Permissions is a read-only listing by design (see
// effectivePermsNote).
//
// userName is boxed in a *string shared by every page below: renaming a user
// changes the identity every other page's lookup depends on. The
// rename is the last write of an Apply/OK run (see propPage.renames),
// and commitRename then updates the box so PropDialog.InvalidateAll's
// reload re-fetches under the new name. dbName never changes, so it
// stays a plain string.
func userPropPages(d *PropDialog, sc *db.ServerConn, dbName, userName string) []propPage {
	namePtr := &userName
	return []propPage{
		// withRequiresOn, not withRequires: gate.AlterAnyUser carries the
		// class-4 arm, and a page that names no securable asks it about ""
		// and withholds nothing — the page opened editable for a user carrying
		// DENY ALTER ON USER::x and its rename was refused on Apply with
		// Msg 15151. Caught by TestAnObjectScopedPageNamesItsSecurable.
		withRequiresOn(pageUserGeneral(sc, dbName, namePtr), dbName, "", userName, gate.AlterAnyUser),
		withRequires(pagePrincipalOwnedSchemas(sc, dbName, namePtr, "user"), dbName, gate.AlterAnySchema, gate.ControlDB),
		// Gated on the user, not on the roles it lists: membership checks ALTER
		// on the member as well as on the role, so a user carrying a class-4
		// DENY cannot be added to any role — verified live 2026-09-04, ALTER
		// ROLE db_datareader ADD MEMBER u refused under DENY ALTER ON USER::u.
		// The per-role half is not expressible in one page-level banner, the
		// same reason Login Properties' User Mapping declares nothing.
		withRequiresOn(pageUserMembership(sc, dbName, namePtr), dbName, "", userName, gate.AlterAnyDBRoleMembers),
		withRequires(pageDatabasePrincipalSecurables(d, sc, dbName, namePtr), dbName, gate.ControlDB),
		pagePrincipalEffectivePermissions(d, sc, dbName, namePtr),
		// Named for the same reason General is: sp_addextendedproperty at
		// @level0type = N'USER' checks ALTER on that user, which the class-4
		// DENY withholds.
		withRequiresOn(pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: *namePtr}
		}), dbName, "", userName, gate.AlterAnyUser),
	}
}

// findUser resolves dbName/userName to a *gosmo.User, the one lookup
// every page on this dialog needs first.
func findUser(ctx context.Context, sc *db.ServerConn, dbName, userName string) (*gosmo.User, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.UserByName(ctx, userName)
}

func pageUserGeneral(sc *db.ServerConn, dbName string, userName *string) propPage {
	return propPage{
		title:   "General",
		renames: true,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			u, err := d.UserByName(ctx, *userName)
			if err != nil {
				return nil, nil, err
			}
			schemas, err := d.Schemas(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.DatabaseRoles(ctx)
			if err != nil {
				return nil, nil, err
			}
			securables, err := d.PermissionsForPrincipal(ctx, *userName)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.Logins(ctx)
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

			userType := userTypeLabel(u)
			// A certificate- or key-mapped user and an Entra one have no
			// login to change: ALTER USER ... WITH LOGIN is refused for the
			// first two, and a LoginName the SID join found is a login mapped
			// to the same certificate, not this user's. A mapped user refuses
			// DEFAULT_SCHEMA as well.
			mapped := isMappedUser(u)
			fixedLogin := mapped || isExternalUser(u)

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
				rows = append(rows, nameRow, propsheet.Static("User type", userType))

				if fixedLogin {
					rows = append(rows, propsheet.Static("Login name", "n/a"))
				} else {
					loginNames := make([]string, len(logins))
					for i, l := range logins {
						loginNames[i] = l.Name
					}
					loginItems := append([]string{noneItem}, loginNames...)
					loginRow = selectPreserving("Login name", loginItems, u.LoginName, noneItem)
					rows = append(rows, loginRow)
				}

				if mapped {
					rows = append(rows,
						propsheet.Static("Mapped to", orDefault(u.MappedObject, "(not found)")),
						propsheet.Static("Default schema", "n/a"))
				} else {
					schemaNames := make([]string, len(schemas))
					for i, s := range schemas {
						schemaNames[i] = s.Name
					}
					schemaRow = selectPreserving("Default schema", schemaNames, u.DefaultSchema, unsetItem)
					rows = append(rows, schemaRow)
				}
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
					if schema, ok := changedTo(schemaRow, unsetItem); ok {
						if err := u.SetDefaultSchema(ctx, schema); err != nil {
							return err
						}
					}
					if login, ok := changedTo(loginRow, noneItem); ok {
						if err := u.SetLogin(ctx, login); err != nil {
							return err
						}
					}
					if nameRow.Dirty() {
						if err := u.Rename(ctx, nameRow.Value()); err != nil {
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

// userTypeLabel is the General page's "User type" — SSMS's names for the
// CREATE USER forms. The mapped and Windows kinds are told apart by type_desc;
// the SQL ones only by authentication_type_desc.
func userTypeLabel(u *gosmo.User) string {
	switch u.UserType {
	case "CERTIFICATE_MAPPED_USER":
		return "User mapped to a certificate"
	case "ASYMMETRIC_KEY_MAPPED_USER":
		return "User mapped to an asymmetric key"
	case "WINDOWS_USER":
		return "Windows user"
	case "WINDOWS_GROUP":
		return "Windows group"
	}
	switch u.AuthType {
	case "INSTANCE":
		if u.LoginName != "" {
			return "SQL user with login"
		}
		// A genuine CREATE USER ... WITHOUT LOGIN reports
		// authentication_type_desc = NONE, not INSTANCE: INSTANCE with no
		// matching login only happens when a FOR LOGIN user's login was
		// dropped out from under it, i.e. orphaned.
		return "SQL user with login (not found)"
	case "DATABASE":
		return "SQL user with password"
	case "EXTERNAL":
		return "External user or group"
	}
	return "SQL user without login"
}

// isMappedUser reports a certificate- or asymmetric-key-mapped user.
func isMappedUser(u *gosmo.User) bool {
	return u.UserType == "CERTIFICATE_MAPPED_USER" || u.UserType == "ASYMMETRIC_KEY_MAPPED_USER"
}

// isExternalUser reports a Microsoft Entra user or group.
func isExternalUser(u *gosmo.User) bool {
	return u.AuthType == "EXTERNAL" || strings.HasPrefix(u.UserType, "EXTERNAL_")
}

// loginDisabledStr renders a user's mapped-login disabled state, or
// "n/a" when no login is mapped (WITHOUT LOGIN, or the login no longer
// exists — SQL Server's catalog metadata can't tell those apart) or the user
// is of a kind that has none.
func loginDisabledStr(u *gosmo.User) string {
	if u.LoginName == "" || isMappedUser(u) || isExternalUser(u) {
		return "n/a"
	}
	return boolStr(u.LoginDisabled)
}

func pageUserMembership(sc *db.ServerConn, dbName string, userName *string) propPage {
	return propPage{
		title: "Membership",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			allRoles, err := d.DatabaseRoles(ctx)
			if err != nil {
				return nil, nil, err
			}
			// public's membership is implicit (never an explicit row in
			// sys.database_role_members) and ALTER ROLE public ADD/DROP
			// MEMBER is a syntax error — exclude it, same as
			// isSystemDatabaseRole treats it as non-interactive.
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
				d, err := sc.Server.DatabaseByName(ctx, dbName)
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
						if err := d.AddRoleMember(ctx, roles[i].Name, *userName); err != nil {
							return err
						}
					} else {
						if err := d.RemoveRoleMember(ctx, roles[i].Name, *userName); err != nil {
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
