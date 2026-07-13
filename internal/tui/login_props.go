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
// stays read-only and server-scoped only for now (see its own comment);
// every other page is editable.
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
			if !isSQLLogin {
				policyRow.SetChecked(false)
				expirationRow.SetChecked(false)
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
				policyRow, expirationRow,
				propsheet.Static("User must change password", boolStr(det.MustChangePassword)),
				propsheet.Section("Defaults"),
				defaultDBRow, defaultLangRow,
				propsheet.Section("Credential"),
				credentialRow,
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
					if err := l.ChangePasswordWithOptionsContext(ctx, passwordRow.Value(), false, false); err != nil {
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
				text[i] = []string{r.Name}
				values[i] = []bool{slices.Contains(r.Members, loginName)}
			}
			rolesGrid := propsheet.NewToggleGrid([]string{"Member", "Role"}, []int{0}, 10)
			rolesGrid.SetRows(text, values)

			f := propsheet.NewForm(
				propsheet.Section("Server role membership"),
				rolesGrid,
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
			mappedUser := make(map[string]string, len(mappings))
			for _, m := range mappings {
				mappedUser[m.Database] = m.User
			}

			var dbNames, users []string
			var text [][]string
			var values [][]bool
			for _, d := range dbs {
				if d.State() != "ONLINE" {
					continue
				}
				user, isMapped := mappedUser[d.Name()]
				if !isMapped {
					user = loginName
				}
				dbNames = append(dbNames, d.Name())
				users = append(users, user)
				text = append(text, []string{d.Name(), user})
				values = append(values, []bool{isMapped})
			}

			mappingGrid := propsheet.NewToggleGrid([]string{"Map", "Database", "User"}, []int{0}, 10)
			mappingGrid.SetRows(text, values)

			f := propsheet.NewForm(
				propsheet.Section("Users mapped to this login"),
				mappingGrid,
				propsheet.Note("Space/Enter (or click) on Map toggles a database's user mapping. A newly mapped database uses the login's own name as the username."),
			)

			apply := func(ctx context.Context) error {
				l, err := findLogin(ctx, sc, loginName)
				if err != nil {
					return err
				}
				for i, v := range mappingGrid.Values() {
					mapped := v[0]
					_, wasMapped := mappedUser[dbNames[i]]
					if mapped == wasMapped {
						continue
					}
					if mapped {
						if err := l.MapToDatabaseContext(ctx, dbNames[i], users[i], ""); err != nil {
							return err
						}
					} else {
						if err := l.UnmapFromDatabaseContext(ctx, dbNames[i]); err != nil {
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

func pageLoginSecurables(sc *db.ServerConn, loginName string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			perms, err := sc.Server.ServerPermissionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, 0, len(perms))
			for _, p := range perms {
				if p.Principal != loginName {
					continue
				}
				rows = append(rows, []string{p.Permission, p.Grantor, p.State})
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Permission", "Grantor", "State"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Explicit server-level permissions"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Note("Read-only here — edit via the Server Properties Permissions page. Database and endpoint securables aren't shown here yet."),
			)
			return f, nil, nil
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
