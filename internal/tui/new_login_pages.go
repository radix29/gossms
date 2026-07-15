package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// serverDefaultLangItem is the Default language row's sentinel meaning
// "don't set DEFAULT_LANGUAGE at all — let the server's own default
// apply", the same nil-means-omit convention noneItem/credItems use on
// Login Properties' Credential row.
const serverDefaultLangItem = "(Server default)"

// buildNewLoginGeneralPage builds the General page: login identity
// (name, authentication type), password/policy (SQL Server auth only),
// and defaults (database, language). Windows logins are typed directly as
// "DOMAIN\name" text — no principal-browse picker, the same simplification
// every prior dialog in this project makes for principal pickers. External
// provider authentication is deliberately not offered (see
// NewLoginDialog's doc comment).
func buildNewLoginGeneralPage(sc *db.ServerConn, pf *nloginPrefetch) (*propsheet.Form, propApply, *propsheet.TextRow) {
	nameField := propsheet.Text("Login name", "", 30)
	authRow := propsheet.Radio("Authentication", []string{"SQL Server Authentication", "Windows Authentication"}, 0)

	passwordRow := propsheet.Password("Password", 20)
	confirmRow := propsheet.Password("Confirm password", 20)
	// Whether a password is required at all depends on authRow, a sibling
	// row — and Form.Validate only runs a row's validator while that row
	// itself is dirty, so an untouched-but-required password field can't
	// be caught here (see the apply closure below, which checks it
	// directly instead). This validator only guards the one thing that
	// legitimately needs live dirty-gating: a typed password that doesn't
	// match its confirmation.
	passwordRow.SetValidate(func(v string) error {
		if authRow.Selected() != 0 {
			return nil // Windows logins don't use a password
		}
		if v != confirmRow.Value() {
			return fmt.Errorf("passwords do not match")
		}
		return nil
	})

	policyRow := propsheet.Check("Enforce password policy", true)
	expirationRow := propsheet.Check("Enforce password expiration", false)
	mustChangeRow := propsheet.Check("Must change password at next login", false)

	defaultDBRow := propsheet.Select("Default database", pf.dbNames, indexOf(pf.dbNames, "master"))
	langItems := append([]string{serverDefaultLangItem}, pf.langNames...)
	defaultLangRow := propsheet.Select("Default language", langItems, 0)

	f := propsheet.NewForm(
		propsheet.Section("Login identity"),
		nameField, authRow,
		propsheet.Section("Password"),
		passwordRow, confirmRow,
		policyRow, expirationRow, mustChangeRow,
		propsheet.Note("Password fields only apply for SQL Server Authentication; for Windows Authentication, type the login name as DOMAIN\\name above."),
		propsheet.Section("Defaults"),
		defaultDBRow, defaultLangRow,
	)

	apply := func(ctx context.Context) error {
		name := strings.TrimSpace(nameField.Value())
		isSQL := authRow.Selected() == 0
		password := ""
		if isSQL {
			password = passwordRow.Value()
			if password == "" {
				return fmt.Errorf("a password is required for SQL Server Authentication")
			}
		}
		opts := &gosmo.CreateLoginOptions{MustChange: isSQL && mustChangeRow.Checked()}
		if defaultDBRow.Dirty() {
			opts.DefaultDatabase = defaultDBRow.Value()
		}
		if err := sc.Server.CreateLoginContext(ctx, name, password, opts); err != nil {
			return err
		}
		if isSQL && (policyRow.Dirty() || expirationRow.Dirty()) {
			if err := sc.Server.Login(name).SetPasswordPolicyContext(ctx, policyRow.Checked(), expirationRow.Checked()); err != nil {
				return err
			}
		}
		if defaultLangRow.Dirty() {
			if err := sc.Server.Login(name).SetDefaultLanguageContext(ctx, langItems[defaultLangRow.Selected()]); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply, nameField
}

// buildNewLoginServerRolesPage reuses Login Properties' Server Roles page
// idiom (pageLoginServerRoles, login_props.go), minus the "already a
// member" diff — the login doesn't exist yet, so every checked row is
// simply a pending ADD. public is excluded (implicit, mandatory
// membership — see nloginDBRoles's doc comment for the same exclusion
// rule applied to database roles).
func buildNewLoginServerRolesPage(sc *db.ServerConn, pf *nloginPrefetch, loginName func() string) (*propsheet.Form, propApply) {
	var toggleable []*gosmo.ServerRole
	for _, r := range pf.serverRoles {
		if r.Name == "public" {
			continue
		}
		toggleable = append(toggleable, r)
	}
	text := make([][]string, len(toggleable))
	values := make([][]bool, len(toggleable))
	for i, r := range toggleable {
		impact := serverRoleImpact[r.Name]
		if impact == "" {
			impact = "Low"
		}
		text[i] = []string{r.Name, impact}
		values[i] = []bool{false}
	}
	rolesGrid := propsheet.NewToggleGrid([]string{"Member", "Role", "Impact"}, []int{0}, 10)
	rolesGrid.SetRows(text, values)

	roleNameStatic := propsheet.Static("Role name", "")
	descStatic := propsheet.Static("Description", "")
	syncFromSelection := func(row int) {
		if row < 0 || row >= len(toggleable) {
			roleNameStatic.SetValue("")
			descStatic.SetValue("")
			return
		}
		r := toggleable[row]
		roleNameStatic.SetValue(r.Name)
		descStatic.SetValue(fixedServerRoleDescriptions[r.Name])
	}
	rolesGrid.Grid.OnSelectRow = syncFromSelection
	if len(toggleable) > 0 {
		syncFromSelection(0)
	}

	f := propsheet.NewForm(
		propsheet.Section("Server role membership"),
		rolesGrid,
		propsheet.Note("public is granted to every login automatically and cannot be removed."),
		propsheet.Section("Selected role"),
		roleNameStatic, descStatic,
		propsheet.Note("Membership in sysadmin grants unrestricted control over the SQL Server instance."),
	)

	apply := func(ctx context.Context) error {
		l := sc.Server.Login(loginName())
		for i, v := range rolesGrid.Values() {
			if !v[0] {
				continue
			}
			if err := l.AddServerRoleMemberContext(ctx, toggleable[i].Name); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}

// nloginMapRow tracks one User Mapping row's pending state for a login
// that doesn't exist yet — unlike Login Properties' mapEdit (login_props.go),
// there's no "already mapped" baseline to diff against, and the mapped
// username is always the new login's own name (matching a plain CREATE
// USER [login] FOR LOGIN [login], the same default Login Properties' own
// User Mapping page falls back to for an unmapped database), so it isn't
// a separately editable field here.
type nloginMapRow struct {
	dbName    string
	mapped    bool
	schema    string
	roleNames []string
	roles     []bool
}

// buildNewLoginUserMappingPage adapts pageLoginUserMapping's grid/role
// idiom (login_props.go) for a login that doesn't exist yet: every row
// starts unmapped, and only checked rows get an apply-time CREATE
// USER/ALTER ROLE ADD MEMBER.
func buildNewLoginUserMappingPage(sc *db.ServerConn, pf *nloginPrefetch, loginName func() string) (*propsheet.Form, propApply) {
	rows := make([]*nloginMapRow, len(pf.dbRoles))
	for i, dr := range pf.dbRoles {
		rows[i] = &nloginMapRow{dbName: dr.dbName, schema: "dbo", roleNames: dr.roleNames, roles: make([]bool, len(dr.roleNames))}
	}

	// The User column shows a fixed placeholder rather than loginName()
	// itself — this page's Form is built once, synchronously, right after
	// the dialog opens (see NewLoginDialog.buildPages), before the user
	// has necessarily typed a login name on General yet, and grid cell
	// text set via SetData doesn't get recomputed on its own when the user
	// later visits/revisits this page. loginName() is only read fresh at
	// apply time (below), where it's always correct.
	const userPlaceholder = "(same as login name)"
	rowsFor := func() [][]string {
		out := make([][]string, len(rows))
		for i, e := range rows {
			out[i] = []string{mapCell(e.mapped), e.dbName, userPlaceholder, e.schema}
		}
		return out
	}
	grid := controls.NewDataGrid()
	grid.SetData([]string{"Map", "Database", "User", "Schema"}, rowsFor())
	grid.SetCellCursor(true)
	grid.OnActivateCell = func(row, col int) {
		if col != 0 || row < 0 || row >= len(rows) {
			return
		}
		rows[row].mapped = !rows[row].mapped
		grid.SetData([]string{"Map", "Database", "User", "Schema"}, rowsFor())
		grid.SetSelectedRow(row)
	}

	dbStatic := propsheet.Static("Database", "")
	schemaText := propsheet.Text("Default schema", "", 20)
	rolesGrid := propsheet.NewToggleGrid([]string{"Member", "Role"}, []int{0}, 8)

	selected := -1
	commitCurrent := func() {
		if selected < 0 || selected >= len(rows) {
			return
		}
		e := rows[selected]
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
		if row < 0 || row >= len(rows) {
			dbStatic.SetValue("")
			schemaText.SetValue("")
			rolesGrid.SetRows(nil, nil)
			return
		}
		e := rows[row]
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
	if len(rows) > 0 {
		syncFromSelection(0)
	}

	mappingRow := propsheet.NewGridRow(grid, 10)
	mappingRow.DirtyFn = func() bool {
		for _, e := range rows {
			if e.mapped {
				return true
			}
		}
		return false
	}
	mappingRow.RevertFn = func() {
		for _, e := range rows {
			e.mapped = false
			e.schema = "dbo"
			for i := range e.roles {
				e.roles[i] = false
			}
		}
		grid.SetData([]string{"Map", "Database", "User", "Schema"}, rowsFor())
		if selected >= 0 && selected < len(rows) {
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
		name := loginName()
		l := sc.Server.Login(name)
		for _, e := range rows {
			if !e.mapped {
				continue
			}
			if err := l.MapToDatabaseContext(ctx, e.dbName, name, e.schema); err != nil {
				return err
			}
		}
		for _, e := range rows {
			if !e.mapped {
				continue
			}
			var rolesChanged bool
			for _, checked := range e.roles {
				if checked {
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
			for i, roleName := range e.roleNames {
				if !e.roles[i] {
					continue
				}
				if err := d.AddRoleMemberContext(ctx, roleName, name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
}

// buildNewLoginSecurablesPage reuses Login Properties' Securables page
// idiom (pageLoginSecurables, login_props.go) — same single SERVER-scope
// securable model, same Grant/Deny/(none) cycling grid — seeded with no
// explicit permissions at all, since the login doesn't exist yet.
func buildNewLoginSecurablesPage(sc *db.ServerConn, loginName func() string) (*propsheet.Form, propApply) {
	catalog := gosmo.ServerPermissionNames()
	type secEdit struct {
		permission string
		current    string
	}
	edits := make([]*secEdit, len(catalog))
	for i, perm := range catalog {
		edits[i] = &secEdit{permission: perm}
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
			if e.current != "" {
				return true
			}
		}
		return false
	}
	gridRow.RevertFn = func() {
		for _, e := range edits {
			e.current = ""
		}
		grid.SetData([]string{"Permission", "State"}, rowsFor())
	}

	f := propsheet.NewForm(
		propsheet.Section("Explicit server-level permissions"),
		gridRow,
		propsheet.Note("Space/Enter (or click) on State cycles Grant → Deny → (none). Database and endpoint securables aren't modeled here yet."),
	)

	apply := func(ctx context.Context) error {
		name := loginName()
		for _, e := range edits {
			if e.current == "" {
				continue
			}
			var err error
			switch e.current {
			case "GRANT":
				err = sc.Server.GrantServerPermissionContext(ctx, e.permission, name)
			case "DENY":
				err = sc.Server.DenyServerPermissionContext(ctx, e.permission, name)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}

// buildNewLoginStatusPage reuses Login Properties' Status page idiom
// (pageLoginStatus, login_props.go), minus every read-only server-reported
// stat (last login, bad password count, active sessions, ...) — none of
// that exists yet for a login that hasn't been created. Baselines are
// SQL Server's own real defaults for a bare CREATE LOGIN: no explicit
// CONNECT SQL grant/deny (public's implicit grant already covers it) and
// enabled.
func buildNewLoginStatusPage(sc *db.ServerConn, loginName func() string) (*propsheet.Form, propApply) {
	connectRow := propsheet.Radio("Permission to connect to database engine", connectPermissionItems, 2)
	enabledRow := propsheet.Radio("Login", []string{"Enabled", "Disabled"}, 0)

	f := propsheet.NewForm(
		propsheet.Section("Permission to connect to database engine"),
		connectRow,
		propsheet.Section("Login"),
		enabledRow,
		propsheet.Note("A new login is created enabled by default; disabling it takes effect immediately after creation."),
	)

	apply := func(ctx context.Context) error {
		name := loginName()
		if connectRow.Dirty() {
			switch connectRow.Selected() {
			case 0:
				if err := sc.Server.GrantServerPermissionContext(ctx, "CONNECT SQL", name); err != nil {
					return err
				}
			case 1:
				if err := sc.Server.DenyServerPermissionContext(ctx, "CONNECT SQL", name); err != nil {
					return err
				}
			}
		}
		if enabledRow.Dirty() && enabledRow.Selected() == 1 {
			if err := sc.Server.Login(name).DisableContext(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}
