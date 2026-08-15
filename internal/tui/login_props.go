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
//
// loginName is boxed in a *string shared by every page below: renaming a
// login changes the identity every other page's lookup depends on. The
// rename is the last write of an Apply/OK run (see propPage.renames), and
// commitRename then updates the box so PropDialog.InvalidateAll's reload
// re-fetches under the new name. Key Properties' pageKeyGeneral and Server
// Role Properties' pageServerRoleGeneral box their names the same way.
func loginPropPages(d *PropDialog, sc *db.ServerConn, loginName string) []propPage {
	namePtr := &loginName
	return []propPage{
		pageLoginGeneral(sc, namePtr),
		pageLoginServerRoles(sc, namePtr),
		pageLoginUserMapping(sc, namePtr),
		pageLoginSecurables(sc, namePtr),
		pageLoginEffectivePermissions(d, sc, namePtr),
		pageLoginStatus(sc, namePtr),
	}
}

// findLogin is a thin wrapper over gosmo.Server.LoginByNameContext, kept so
// every page's load/apply closure has one short name to call rather than
// reaching into sc.Server directly.
func findLogin(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Login, error) {
	return sc.Server.LoginByNameContext(ctx, name)
}

const noneItem = "(None)"

func pageLoginGeneral(sc *db.ServerConn, loginName *string) propPage {
	return propPage{
		title:   "General",
		renames: true,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			l, err := findLogin(ctx, sc, *loginName)
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

			dbNames, err := databaseNames(ctx, sc)
			if err != nil {
				return nil, nil, err
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

			// The ## logins are the one thing on this page the server would
			// happily let through: ALTER LOGIN ... WITH NAME succeeds on
			// [##MS_PolicyEventProcessingLogin##] and silently orphans the
			// matching users in master and msdb. See isSystemLogin.
			builtin := isSystemLogin(l)
			var nameRow *propsheet.TextRow
			var identityRow propsheet.Row = propsheet.Static("Login name", l.Name)
			if !builtin {
				nameRow = propsheet.Text("Login name", l.Name, 24)
				identityRow = nameRow
			}
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

			defaultDBRow := selectPreserving("Default database", dbNames, l.DefaultDatabase, unsetItem)
			defaultLangRow := selectPreserving("Default language", langNames, det.DefaultLanguage, unsetItem)
			origCredential := det.CredentialName
			credSelected := 0
			if origCredential != "" {
				credSelected = indexOf(credItems, origCredential)
			}
			credentialRow := propsheet.Select("Map to credential", credItems, credSelected)

			rows := []propsheet.Row{
				propsheet.Section("Login identity"),
				identityRow,
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
			}
			if builtin {
				rows = append(rows,
					propsheet.Section("Built-in login"),
					propsheet.Note("This is a built-in login SQL Server manages for itself. Its name can't be changed."),
				)
			}

			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				l, err := findLogin(ctx, sc, *loginName)
				if err != nil {
					return err
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
				if db, ok := changedTo(defaultDBRow, unsetItem); ok {
					if err := l.SetDefaultDatabaseContext(ctx, db); err != nil {
						return err
					}
				}
				if lang, ok := changedTo(defaultLangRow, unsetItem); ok {
					if err := l.SetDefaultLanguageContext(ctx, lang); err != nil {
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
				// Renaming last keeps every write above addressed by the
				// name the server still has — see propPage.renames.
				if nameRow != nil && nameRow.Dirty() {
					if err := l.RenameContext(ctx, nameRow.Value()); err != nil {
						return err
					}
					commitRename(ctx, loginName, nameRow.Value())
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

func pageLoginServerRoles(sc *db.ServerConn, loginName *string) propPage {
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
				values[i] = []bool{slices.Contains(r.Members, *loginName)}
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
				l, err := findLogin(ctx, sc, *loginName)
				if err != nil {
					return err
				}
				for i, v := range rolesGrid.Values() {
					member := v[0]
					wasMember := slices.Contains(roles[i].Members, *loginName)
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

// setRoleToggles fills a one-checkbox-per-role toggle grid from the parallel
// name/checked slices both User Mapping pages keep a database's role
// membership in — this one and buildNewLoginUserMappingPage's
// (new_login_pages.go).
func setRoleToggles(g *propsheet.ToggleGridRow, names []string, checked []bool) {
	text := make([][]string, len(names))
	vals := make([][]bool, len(names))
	for i, name := range names {
		text[i] = []string{name}
		vals[i] = []bool{checked[i]}
	}
	g.SetRows(text, vals)
}

// userMappingColumns are the User Mapping grid's columns, shared by this
// page and buildNewLoginUserMappingPage — both grids are the same grid.
var userMappingColumns = []string{"Map", "Database", "User", "Schema"}

func mapCell(mapped bool) string {
	if mapped {
		return "[x]"
	}
	return "[ ]"
}

func pageLoginUserMapping(sc *db.ServerConn, loginName *string) propPage {
	return propPage{
		title: "User Mapping",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			l, err := findLogin(ctx, sc, *loginName)
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

			// One more round trip per ONLINE database on top of the one
			// UserMappingsContext already makes per database, so serially
			// this page is 2N latencies deep. A database whose roles can't
			// be read drops out of the list the same way an offline one
			// does — the alternative is one unreadable availability-group
			// secondary failing a page that has every other database to
			// show, which is not what the mapping half above does.
			edits, err := eachDatabase(ctx, onlineDatabases(dbs), func(ctx context.Context, d *gosmo.Database) (*mapEdit, error) {
				m, isMapped := mappingByDB[d.Name()]
				user := *loginName
				schema := "dbo"
				if isMapped {
					user = m.User
					if m.DefaultSchema != "" {
						schema = m.DefaultSchema
					}
				}
				roles, err := d.DatabaseRolesContext(ctx)
				if err != nil {
					return nil, err
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
				return &mapEdit{
					dbName: d.Name(), origMapped: isMapped, mapped: isMapped,
					user: user, origSchema: schema, schema: schema,
					roleNames: roleNames, origRoles: origRoles,
					roles: append([]bool(nil), origRoles...),
				}, nil
			})
			if err != nil {
				return nil, nil, err
			}

			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					// e.schema, not e.origSchema: the redraw after each commit
					// exists so the grid reports what the editor below it just
					// wrote, and showing the loaded value instead left an edited
					// schema invisible in the grid until Apply.
					rows[i] = []string{mapCell(e.mapped), e.dbName, e.user, e.schema}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData(userMappingColumns, rowsFor())
			grid.SetCellCursor(true)
			grid.OnActivateCell = func(row, col int) {
				if col != 0 || row < 0 || row >= len(edits) {
					return
				}
				edits[row].mapped = !edits[row].mapped
				redrawGrid(grid, userMappingColumns, rowsFor())
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
			syncFromSelection := func() {
				selected = grid.SelectedRow()
				if selected < 0 || selected >= len(edits) {
					dbStatic.SetValue("")
					schemaText.SetValue("")
					rolesGrid.SetRows(nil, nil)
					return
				}
				e := edits[selected]
				dbStatic.SetValue(e.dbName)
				schemaText.SetValue(e.schema)
				setRoleToggles(rolesGrid, e.roleNames, e.roles)
			}
			reload := wireGridEditor(grid, userMappingColumns, rowsFor, commitCurrent, syncFromSelection)

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
				// reload, never syncFromSelection directly: reload redraws and
				// reloads the editor *without* committing first, which is the
				// whole difference. The schema box and role toggles still hold
				// the pre-revert values at this point, so a commit would write
				// the selected row straight back to what Revert just undid.
				reload()
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
				l, err := findLogin(ctx, sc, *loginName)
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

func pageLoginSecurables(sc *db.ServerConn, loginName *string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			return pagePrincipalServerPermissions(sc, *loginName).load(ctx)
		},
	}
}

var connectPermissionItems = []string{"Grant", "Deny", "Default"}

func pageLoginStatus(sc *db.ServerConn, loginName *string) propPage {
	return propPage{
		title: "Status",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			l, err := findLogin(ctx, sc, *loginName)
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
				if s.LoginName == *loginName {
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
				l, err := findLogin(ctx, sc, *loginName)
				if err != nil {
					return err
				}
				if connectRow.Dirty() {
					switch connectRow.Selected() {
					case 0:
						if err := sc.Server.GrantServerPermissionContext(ctx, "CONNECT SQL", *loginName); err != nil {
							return err
						}
					case 1:
						if err := sc.Server.DenyServerPermissionContext(ctx, "CONNECT SQL", *loginName); err != nil {
							return err
						}
					case 2:
						if err := sc.Server.RevokeServerPermissionContext(ctx, "CONNECT SQL", *loginName); err != nil {
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
