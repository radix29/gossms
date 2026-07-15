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

// loginPropPages builds the page set for Login Properties. Securables
// only models the single SERVER-scope securable for now (see its own
// comment) — every page is editable.
func loginPropPages(sc *db.ServerConn, loginName string) []propPage {
	return []propPage{
		pageLoginGeneral(sc, loginName),
		pageLoginServerRoles(sc, loginName),
		pageLoginUserMapping(sc, loginName),
		pageLoginSecurables(sc, loginName),
		pageLoginStatus(sc, loginName),
	}
}

// findLogin is a thin wrapper over gosmo.Server.LoginByNameContext, kept so
// every page's load/apply closure has one short name to call rather than
// reaching into sc.Server directly.
func findLogin(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Login, error) {
	return sc.Server.LoginByNameContext(ctx, name)
}

const noneItem = "(None)"

func pageLoginGeneral(sc *db.ServerConn, loginName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			l, err := findLogin(ctx, sc, loginName)
			if err != nil {
				return nil, nil, err
			}
			det, err := l.DetailsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			isSQLLogin := l.LoginType == "SQL_LOGIN"
			authType := "Windows Authentication"
			if isSQLLogin {
				authType = "SQL Server Authentication"
			}

			dbs, err := sc.Server.DatabasesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			dbNames := make([]string, len(dbs))
			for i, d := range dbs {
				dbNames[i] = d.Name()
			}
			langs, err := sc.Server.LanguagesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			langNames := make([]string, len(langs))
			for i, lg := range langs {
				langNames[i] = lg.Name
			}
			creds, err := sc.Server.CredentialsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			credItems := append([]string{noneItem}, credNames(creds)...)

			nameRow := propsheet.Text("Login name", l.Name, 24)
			passwordRow := propsheet.Password("Password", 20)
			confirmRow := propsheet.Password("Confirm password", 20)
			// The mismatch check must live on passwordRow, not confirmRow:
			// Form.Validate only runs a row's validator while that row
			// itself is dirty, and a user who types a new password but
			// never touches Confirm (leaving it at its blank baseline)
			// would otherwise skip the check entirely. passwordRow is
			// dirty exactly when non-blank, so this always fires whenever
			// there's a password change to validate.
			passwordRow.SetValidate(func(v string) error {
				if v != confirmRow.Value() {
					return fmt.Errorf("passwords do not match")
				}
				return nil
			})
			if !isSQLLogin {
				passwordRow.SetEnabled(false)
				confirmRow.SetEnabled(false)
			}
			policyRow := propsheet.Check("Enforce password policy", det.IsPolicyChecked)
			expirationRow := propsheet.Check("Enforce password expiration", det.IsExpirationChecked)
			mustChangeRow := propsheet.Check("User must change password at next login", det.MustChangePassword)
			unlockRow := propsheet.Check("Unlock login", false)
			if !isSQLLogin {
				policyRow.SetChecked(false)
				expirationRow.SetChecked(false)
				mustChangeRow.SetChecked(false)
				unlockRow.SetChecked(false)
			}

			defaultDBRow := propsheet.Select("Default database", dbNames, indexOf(dbNames, l.DefaultDatabase))
			defaultLangRow := propsheet.Select("Default language", langNames, indexOf(langNames, det.DefaultLanguage))
			origCredential := det.CredentialName
			credSelected := 0
			if origCredential != "" {
				credSelected = indexOf(credItems, origCredential)
			}
			credentialRow := propsheet.Select("Map to credential", credItems, credSelected)

			f := propsheet.NewForm(
				propsheet.Section("Login identity"),
				nameRow,
				propsheet.Static("Authentication", authType),
				propsheet.Section("Password"),
				passwordRow, confirmRow,
				propsheet.Note("Leave both password fields blank to keep the current password."),
				propsheet.Section("Password policy"),
				policyRow, expirationRow, mustChangeRow, unlockRow,
				propsheet.Note("\"User must change password\" and \"Unlock login\" only take effect together with a password change above — SQL Server's ALTER LOGIN only accepts MUST_CHANGE/UNLOCK alongside PASSWORD =."),
				propsheet.Section("Defaults"),
				defaultDBRow, defaultLangRow,
				propsheet.Section("Credential"),
				credentialRow,
				propsheet.Section("Summary"),
				propsheet.Static("Login type", l.LoginType),
				propsheet.Static("SID", fmt.Sprintf("0x%X", l.SID)),
				propsheet.Static("Created", formatSQLDate(l.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(l.ModifyDate)),
			)

			apply := func(ctx context.Context) error {
				l, err := findLogin(ctx, sc, loginName)
				if err != nil {
					return err
				}
				if nameRow.Dirty() {
					if err := l.RenameContext(ctx, nameRow.Value()); err != nil {
						return err
					}
				}
				if isSQLLogin && passwordRow.Value() != "" {
					if err := l.ChangePasswordWithOptionsContext(ctx, passwordRow.Value(), mustChangeRow.Checked(), unlockRow.Checked()); err != nil {
						return err
					}
				}
				if isSQLLogin && (policyRow.Dirty() || expirationRow.Dirty()) {
					if err := l.SetPasswordPolicyContext(ctx, policyRow.Checked(), expirationRow.Checked()); err != nil {
						return err
					}
				}
				if defaultDBRow.Dirty() {
					if err := l.SetDefaultDatabaseContext(ctx, defaultDBRow.Value()); err != nil {
						return err
					}
				}
				if defaultLangRow.Dirty() {
					if err := l.SetDefaultLanguageContext(ctx, defaultLangRow.Value()); err != nil {
						return err
					}
				}
				if credentialRow.Dirty() {
					if origCredential != "" {
						if err := l.UnmapCredentialContext(ctx, origCredential); err != nil {
							return err
						}
					}
					if v := credentialRow.Value(); v != noneItem {
						if err := l.MapCredentialContext(ctx, v); err != nil {
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

func pageLoginServerRoles(sc *db.ServerConn, loginName string) propPage {
	return propPage{
		title: "Server Roles",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			roles, err := sc.Server.ServerRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			text := make([][]string, len(roles))
			values := make([][]bool, len(roles))
			for i, r := range roles {
				impact := serverRoleImpact[r.Name]
				if impact == "" {
					impact = "Low"
				}
				text[i] = []string{r.Name, impact}
				values[i] = []bool{slices.Contains(r.Members, loginName)}
			}
			rolesGrid := propsheet.NewToggleGrid([]string{"Member", "Role", "Impact"}, []int{0}, 10)
			rolesGrid.SetRows(text, values)

			roleNameStatic := propsheet.Static("Role name", "")
			descStatic := propsheet.Static("Description", "")
			membersStatic := propsheet.Static("Members", "")
			syncFromSelection := func(row int) {
				if row < 0 || row >= len(roles) {
					roleNameStatic.SetValue("")
					descStatic.SetValue("")
					membersStatic.SetValue("")
					return
				}
				r := roles[row]
				roleNameStatic.SetValue(r.Name)
				descStatic.SetValue(fixedServerRoleDescriptions[r.Name])
				membersStatic.SetValue(strconv.Itoa(len(r.Members)))
			}
			rolesGrid.Grid.OnSelectRow = syncFromSelection
			if len(roles) > 0 {
				syncFromSelection(0)
			}

			f := propsheet.NewForm(
				propsheet.Section("Server role membership"),
				rolesGrid,
				propsheet.Section("Selected role"),
				roleNameStatic, descStatic, membersStatic,
				propsheet.Note("Membership in sysadmin grants unrestricted control over the SQL Server instance."),
			)

			apply := func(ctx context.Context) error {
				l, err := findLogin(ctx, sc, loginName)
				if err != nil {
					return err
				}
				for i, v := range rolesGrid.Values() {
					member := v[0]
					wasMember := slices.Contains(roles[i].Members, loginName)
					if member == wasMember {
						continue
					}
					if member {
						if err := l.AddServerRoleMemberContext(ctx, roles[i].Name); err != nil {
							return err
						}
					} else {
						if err := l.RemoveServerRoleMemberContext(ctx, roles[i].Name); err != nil {
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

// mapEdit tracks one User Mapping row's pending state: whether the
// database is mapped, and — if it is — the mapped user's default schema
// and database role membership. Schema/role edits only make sense once
// mapped (there's no user to apply them to otherwise), so apply skips
// them for any row that ends up unmapped.
type mapEdit struct {
	dbName     string
	origMapped bool
	mapped     bool
	user       string
	origSchema string
	schema     string
	roleNames  []string
	origRoles  []bool
	roles      []bool
}

func mapCell(mapped bool) string {
	if mapped {
		return "[x]"
	}
	return "[ ]"
}

func pageLoginUserMapping(sc *db.ServerConn, loginName string) propPage {
	return propPage{
		title: "User Mapping",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			l, err := findLogin(ctx, sc, loginName)
			if err != nil {
				return nil, nil, err
			}
			dbs, err := sc.Server.DatabasesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			mappings, err := l.UserMappingsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			mappingByDB := make(map[string]*gosmo.LoginUserMapping, len(mappings))
			for _, m := range mappings {
				mappingByDB[m.Database] = m
			}

			var edits []*mapEdit
			for _, d := range dbs {
				if d.State() != "ONLINE" {
					continue
				}
				m, isMapped := mappingByDB[d.Name()]
				user := loginName
				schema := "dbo"
				if isMapped {
					user = m.User
					if m.DefaultSchema != "" {
						schema = m.DefaultSchema
					}
				}
				roles, err := d.DatabaseRolesContext(ctx)
				if err != nil {
					return nil, nil, err
				}
				var roleNames []string
				var origRoles []bool
				for _, r := range roles {
					// public's membership is implicit and ALTER ROLE
					// public ADD/DROP MEMBER is a syntax error — same
					// exclusion user_props.go's Membership page makes.
					if r.Name == "public" {
						continue
					}
					roleNames = append(roleNames, r.Name)
					origRoles = append(origRoles, isMapped && slices.Contains(m.Roles, r.Name))
				}
				edits = append(edits, &mapEdit{
					dbName: d.Name(), origMapped: isMapped, mapped: isMapped,
					user: user, origSchema: schema, schema: schema,
					roleNames: roleNames, origRoles: origRoles,
					roles: append([]bool(nil), origRoles...),
				})
			}

			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{mapCell(e.mapped), e.dbName, e.user, e.origSchema}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Map", "Database", "User", "Schema"}, rowsFor())
			grid.SetCellCursor(true)
			grid.OnActivateCell = func(row, col int) {
				if col != 0 || row < 0 || row >= len(edits) {
					return
				}
				edits[row].mapped = !edits[row].mapped
				grid.SetData([]string{"Map", "Database", "User", "Schema"}, rowsFor())
				grid.SetSelectedRow(row)
			}

			dbStatic := propsheet.Static("Database", "")
			schemaText := propsheet.Text("Default schema", "", 20)
			rolesGrid := propsheet.NewToggleGrid([]string{"Member", "Role"}, []int{0}, 8)

			selected := -1
			commitCurrent := func() {
				if selected < 0 || selected >= len(edits) {
					return
				}
				e := edits[selected]
				e.schema = schemaText.Value()
				vals := rolesGrid.Values()
				for i := range e.roles {
					if i < len(vals) {
						e.roles[i] = vals[i][0]
					}
				}
			}
			syncFromSelection := func(row int) {
				commitCurrent()
				selected = row
				if row < 0 || row >= len(edits) {
					dbStatic.SetValue("")
					schemaText.SetValue("")
					rolesGrid.SetRows(nil, nil)
					return
				}
				e := edits[row]
				dbStatic.SetValue(e.dbName)
				schemaText.SetValue(e.schema)
				roleText := make([][]string, len(e.roleNames))
				roleVals := make([][]bool, len(e.roleNames))
				for i, name := range e.roleNames {
					roleText[i] = []string{name}
					roleVals[i] = []bool{e.roles[i]}
				}
				rolesGrid.SetRows(roleText, roleVals)
			}
			grid.OnSelectRow = syncFromSelection
			if len(edits) > 0 {
				syncFromSelection(0)
			}

			mappingRow := propsheet.NewGridRow(grid, 10)
			mappingRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.mapped != e.origMapped || e.schema != e.origSchema {
						return true
					}
					for i := range e.roles {
						if e.roles[i] != e.origRoles[i] {
							return true
						}
					}
				}
				return false
			}
			mappingRow.RevertFn = func() {
				for _, e := range edits {
					e.mapped = e.origMapped
					e.schema = e.origSchema
					e.roles = append([]bool(nil), e.origRoles...)
				}
				grid.SetData([]string{"Map", "Database", "User", "Schema"}, rowsFor())
				if selected >= 0 && selected < len(edits) {
					syncFromSelection(selected)
				}
			}

			f := propsheet.NewForm(
				propsheet.Section("Users mapped to this login"),
				mappingRow,
				propsheet.Note("Space/Enter (or click) on Map toggles a database's user mapping. A newly mapped database uses the login's own name as the username."),
				propsheet.Section("Selected mapping"),
				dbStatic, schemaText,
				propsheet.Section("Database role membership"),
				rolesGrid,
				propsheet.Note("Schema/role changes only take effect for a mapped database. Space/Enter (or click) on Member toggles role membership."),
			)

			apply := func(ctx context.Context) error {
				commitCurrent()
				l, err := findLogin(ctx, sc, loginName)
				if err != nil {
					return err
				}
				for _, e := range edits {
					if e.mapped == e.origMapped {
						continue
					}
					if e.mapped {
						if err := l.MapToDatabaseContext(ctx, e.dbName, e.user, ""); err != nil {
							return err
						}
					} else {
						if err := l.UnmapFromDatabaseContext(ctx, e.dbName); err != nil {
							return err
						}
					}
				}
				for _, e := range edits {
					if !e.mapped {
						continue
					}
					if e.schema != e.origSchema {
						d, err := sc.Server.DatabaseByNameContext(ctx, e.dbName)
						if err != nil {
							return err
						}
						u, err := d.UserByNameContext(ctx, e.user)
						if err != nil {
							return err
						}
						if err := u.SetDefaultSchemaContext(ctx, e.schema); err != nil {
							return err
						}
					}
					var rolesChanged bool
					for i := range e.roles {
						if e.roles[i] != e.origRoles[i] {
							rolesChanged = true
							break
						}
					}
					if !rolesChanged {
						continue
					}
					d, err := sc.Server.DatabaseByNameContext(ctx, e.dbName)
					if err != nil {
						return err
					}
					for i, name := range e.roleNames {
						if e.roles[i] == e.origRoles[i] {
							continue
						}
						if e.roles[i] {
							if err := d.AddRoleMemberContext(ctx, name, e.user); err != nil {
								return err
							}
						} else {
							if err := d.RemoveRoleMemberContext(ctx, name, e.user); err != nil {
								return err
							}
						}
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

func pageLoginSecurables(sc *db.ServerConn, loginName string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			perms, err := sc.Server.ServerPermissionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			states := make(map[string]string, len(perms))
			for _, p := range perms {
				if p.Principal == loginName {
					states[p.Permission] = p.State
				}
			}

			catalog := gosmo.ServerPermissionNames()
			type secEdit struct {
				permission string
				orig       string
				current    string
			}
			edits := make([]*secEdit, len(catalog))
			for i, perm := range catalog {
				state := states[perm]
				edits[i] = &secEdit{permission: perm, orig: state, current: state}
			}

			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{e.permission, displayPermState(e.current)}
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
						err = sc.Server.GrantServerPermissionContext(ctx, e.permission, loginName)
					case "DENY":
						err = sc.Server.DenyServerPermissionContext(ctx, e.permission, loginName)
					case "":
						err = sc.Server.RevokeServerPermissionContext(ctx, e.permission, loginName)
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

var connectPermissionItems = []string{"Grant", "Deny", "Default"}

func pageLoginStatus(sc *db.ServerConn, loginName string) propPage {
	return propPage{
		title: "Status",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			l, err := findLogin(ctx, sc, loginName)
			if err != nil {
				return nil, nil, err
			}
			det, err := l.DetailsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			lastLogin := "Unknown"
			if !det.LastLogin.IsZero() {
				lastLogin = formatSQLDate(det.LastLogin)
			}
			badPasswordTime := "-"
			if !det.BadPasswordTime.IsZero() {
				badPasswordTime = formatSQLDate(det.BadPasswordTime)
			}

			sessions, err := sc.Server.ActiveSessionsContext(ctx, true)
			if err != nil {
				return nil, nil, err
			}
			activeSessions := 0
			for _, s := range sessions {
				if s.LoginName == loginName {
					activeSessions++
				}
			}

			connectIdx := 2 // Default
			switch det.ConnectSQLState {
			case "GRANT", "GRANT_WITH_GRANT_OPTION":
				connectIdx = 0
			case "DENY":
				connectIdx = 1
			}
			connectRow := propsheet.Radio("Permission to connect to database engine", connectPermissionItems, connectIdx)
			enabledRow := propsheet.Radio("Login", []string{"Enabled", "Disabled"}, boolIdx(l.IsDisabled))

			f := propsheet.NewForm(
				propsheet.Section("Permission to connect to database engine"),
				connectRow,
				propsheet.Section("Login"),
				enabledRow,
				propsheet.Section("SQL login status"),
				propsheet.Static("Is locked out", boolStr(det.IsLocked)),
				propsheet.Static("Password expired", boolStr(det.IsExpired)),
				propsheet.Static("Password policy checked", boolStr(det.IsPolicyChecked)),
				propsheet.Static("Last password set", formatSQLDate(det.PasswordLastSet)),
				propsheet.Static("Last successful login", lastLogin),
				propsheet.Static("Failed login count", strconv.Itoa(det.BadPasswordCount)),
				propsheet.Static("Bad password time", badPasswordTime),
				propsheet.Section("Active sessions"),
				propsheet.Static("Active sessions", strconv.Itoa(activeSessions)),
				propsheet.Note("Unlock and password reset are on the General page (set a new password there)."),
			)

			apply := func(ctx context.Context) error {
				l, err := findLogin(ctx, sc, loginName)
				if err != nil {
					return err
				}
				if connectRow.Dirty() {
					switch connectRow.Selected() {
					case 0:
						if err := sc.Server.GrantServerPermissionContext(ctx, "CONNECT SQL", loginName); err != nil {
							return err
						}
					case 1:
						if err := sc.Server.DenyServerPermissionContext(ctx, "CONNECT SQL", loginName); err != nil {
							return err
						}
					case 2:
						if err := sc.Server.RevokeServerPermissionContext(ctx, "CONNECT SQL", loginName); err != nil {
							return err
						}
					}
				}
				if enabledRow.Dirty() {
					if enabledRow.Selected() == 1 {
						if err := l.DisableContext(ctx); err != nil {
							return err
						}
					} else {
						if err := l.EnableContext(ctx); err != nil {
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
