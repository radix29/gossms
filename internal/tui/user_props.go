package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// userPropPages builds the page set for Database User Properties. General
// is editable except for the fixed system users (dbo/guest/sys/
// INFORMATION_SCHEMA, verified live to reject ALTER USER entirely); Owned
// Schemas, Membership, Securables, and Extended Properties are always
// editable. Contained/password users and external Microsoft Entra users
// aren't built — this environment has neither (contained database
// authentication is disabled server-wide, and there's no Azure AD auth
// here to test against).
func userPropPages(sc *db.ServerConn, dbName, userName string) []propPage {
	return []propPage{
		pageUserGeneral(sc, dbName, userName),
		pageUserOwnedSchemas(sc, dbName, userName),
		pageUserMembership(sc, dbName, userName),
		pageUserSecurables(sc, dbName, userName),
		pageUserExtendedProperties(sc, dbName, userName),
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

// isSystemUser reports whether a user's name/login/default schema can't
// be changed — verified live that ALTER USER on any of these fails
// ("Cannot rename the user 'guest'.", "Cannot alter the user 'dbo'.",
// same for sys/INFORMATION_SCHEMA), unlike the ordinary permission errors
// other ALTER USER failures produce.
func isSystemUser(name string) bool {
	switch name {
	case "dbo", "guest", "sys", "INFORMATION_SCHEMA":
		return true
	default:
		return false
	}
}

func pageUserGeneral(sc *db.ServerConn, dbName, userName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			u, err := d.UserByNameContext(ctx, userName)
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
			securables, err := d.PermissionsForPrincipalContext(ctx, userName)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			ownedSchemas := 0
			for _, s := range schemas {
				if s.Owner == userName {
					ownedSchemas++
				}
			}
			memberships := 0
			for _, r := range roles {
				if r.Name != "public" && slices.Contains(r.Members, userName) {
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
					// authentication_type_desc = NONE, not INSTANCE
					// (verified live) — INSTANCE with no matching login
					// only happens when a FOR LOGIN user's login was
					// later dropped out from under it, i.e. orphaned.
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
					u, err := findUser(ctx, sc, dbName, userName)
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

func pageUserOwnedSchemas(sc *db.ServerConn, dbName, userName string) propPage {
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
				if s.Owner != userName {
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
				propsheet.Section("Schemas owned by this user"),
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

func pageUserMembership(sc *db.ServerConn, dbName, userName string) propPage {
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
				values[i] = []bool{slices.Contains(r.Members, userName)}
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
					wasMember := slices.Contains(roles[i].Members, userName)
					if member == wasMember {
						continue
					}
					if member {
						if err := d.AddRoleMemberContext(ctx, roles[i].Name, userName); err != nil {
							return err
						}
					} else {
						if err := d.RemoveRoleMemberContext(ctx, roles[i].Name, userName); err != nil {
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

func pageUserSecurables(sc *db.ServerConn, dbName, userName string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			entries, err := d.PermissionsForPrincipalContext(ctx, userName)
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
					return grantSecurable(ctx, d, s, permission, userName)
				},
				func(ctx context.Context, s securable, permission string) error {
					return denySecurable(ctx, d, s, permission, userName)
				},
				func(ctx context.Context, s securable, permission string) error {
					return revokeSecurable(ctx, d, s, permission, userName)
				},
			)
			return f, apply, nil
		},
	}
}

func pageUserExtendedProperties(sc *db.ServerConn, dbName, userName string) propPage {
	return propPage{
		title: "Extended Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			level := gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: userName}
			props, err := d.ExtendedProperties(level)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildExtendedPropertiesForm(sc, dbName, level, props)
			return f, apply, nil
		},
	}
}
