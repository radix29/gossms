package tui

import (
	"context"
	"fmt"
	"slices"
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

// nloginSources are the New Login General page's Authentication options, in
// the order they appear on the radio group. The labels match loginAuthLabel's
// wording (login_props.go), so a login created here and then reopened in Login
// Properties describes itself the same way.
var nloginSources = []struct {
	label  string
	source gosmo.LoginSource
}{
	{"SQL Server Authentication", gosmo.LoginSourceSQL},
	{"Windows Authentication", gosmo.LoginSourceWindows},
	{"Microsoft Entra Authentication", gosmo.LoginSourceExternalProvider},
	{"Mapped to a certificate", gosmo.LoginSourceCertificate},
	{"Mapped to an asymmetric key", gosmo.LoginSourceAsymmetricKey},
}

// nloginNoMappable is the Mapped-object picker's content for a source that
// maps to nothing, and for one that does when the instance offers no
// candidate. It is never a name, so the apply's "pick one" refusal cannot be
// satisfied by leaving the picker alone.
const nloginNoMappable = "(None)"

// buildNewLoginGeneralPage builds the General page: login identity (name,
// authentication source and whatever that source needs), password/policy
// (SQL Server auth only), and defaults (database, language). Windows logins
// are typed directly as "DOMAIN\name" text — there's no principal-browse
// picker.
//
// The Authentication group drives the rest of the page through
// RadioRow.SetOnChange: the rows a source does not use are disabled or
// emptied rather than left inviting input the CREATE LOGIN cannot carry.
// Every combination is refused by gosmo as well, so the gating here is the
// dialog being honest about what it will send, not the only check.
func buildNewLoginGeneralPage(sc *db.ServerConn, pf *nloginPrefetch) (*propsheet.Form, propApply, *propsheet.TextRow) {
	nameField := propsheet.Text("Login name", "", 30)
	labels := make([]string, len(nloginSources))
	for i, src := range nloginSources {
		labels[i] = src.label
	}
	authRow := propsheet.Radio("Authentication", labels, 0)
	source := func() gosmo.LoginSource { return nloginSources[authRow.Selected()].source }

	// The object id is optional even for an Entra login: with none, SQL Server
	// resolves the login name against the directory itself, which is the
	// ordinary case. It is only needed for a display name the directory cannot
	// resolve on its own.
	objectIDRow := propsheet.Text("Entra object ID", "", 36)
	mappedRow := propsheet.Select("Mapped to", []string{nloginNoMappable}, 0)

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
		if source() != gosmo.LoginSourceSQL {
			return nil // only a SQL login has a password
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

	// syncSource repoints the source-specific rows at whatever is selected
	// now. Password and object id are TextRows and can be disabled outright;
	// the Mapped-to picker instead has its items replaced, since an
	// out-of-date certificate name left showing under "Windows
	// Authentication" is what the apply would otherwise have to guess about.
	syncSource := func() {
		src := source()
		isSQL := src == gosmo.LoginSourceSQL
		passwordRow.SetEnabled(isSQL)
		confirmRow.SetEnabled(isSQL)
		objectIDRow.SetEnabled(src == gosmo.LoginSourceExternalProvider)

		var items []string
		switch src {
		case gosmo.LoginSourceCertificate:
			items = pf.certNames
		case gosmo.LoginSourceAsymmetricKey:
			items = pf.asymKeyNames
		}
		if len(items) == 0 {
			items = []string{nloginNoMappable}
		}
		mappedRow.SetItems(items)
	}
	authRow.SetOnChange(func(int) { syncSource() })
	syncSource()

	f := propsheet.NewForm(
		propsheet.Section("Login identity"),
		nameField, authRow,
		objectIDRow, mappedRow,
		propsheet.Note("Type a Windows login as DOMAIN\\name. An Entra object ID is only needed when the display name is ambiguous in the directory."),
		propsheet.Section("Password"),
		passwordRow, confirmRow,
		policyRow, expirationRow, mustChangeRow,
		propsheet.Note("Password fields apply to SQL Server Authentication only."),
		propsheet.Section("Defaults"),
		defaultDBRow, defaultLangRow,
		propsheet.Note("A login mapped to a certificate or asymmetric key has no default database or language — SQL Server refuses both."),
	)

	apply := func(ctx context.Context) error {
		name := strings.TrimSpace(nameField.Value())
		src := source()
		isSQL := src == gosmo.LoginSourceSQL
		mapped := src == gosmo.LoginSourceCertificate || src == gosmo.LoginSourceAsymmetricKey

		password := ""
		if isSQL {
			password = passwordRow.Value()
			if password == "" {
				return fmt.Errorf("a password is required for SQL Server Authentication")
			}
		}
		opts := &gosmo.CreateLoginOptions{Source: src, MustChange: isSQL && mustChangeRow.Checked()}
		if src == gosmo.LoginSourceExternalProvider {
			opts.ObjectID = strings.TrimSpace(objectIDRow.Value())
		}
		if mapped {
			// Refused rather than dropped: the user picked a database or a
			// language on a page that then sent neither, and SQL Server's own
			// message arrives after the login already exists.
			if defaultDBRow.Dirty() || defaultLangRow.Dirty() {
				return fmt.Errorf("a login mapped to a certificate or asymmetric key cannot have a default database or language")
			}
			if mappedRow.Value() == nloginNoMappable {
				return fmt.Errorf("select the %s this login maps to", mappedNoun(src))
			}
			if src == gosmo.LoginSourceCertificate {
				opts.CertificateName = mappedRow.Value()
			} else {
				opts.AsymmetricKeyName = mappedRow.Value()
			}
		}
		if !mapped && defaultDBRow.Dirty() {
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
		if !mapped && defaultLangRow.Dirty() {
			if err := sc.Server.Login(name).SetDefaultLanguageContext(ctx, langItems[defaultLangRow.Selected()]); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply, nameField
}

// mappedNoun names the object a mapped source maps to, for the refusal the
// apply raises when nothing is picked.
func mappedNoun(src gosmo.LoginSource) string {
	if src == gosmo.LoginSourceCertificate {
		return "certificate"
	}
	return "asymmetric key"
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

	// schemaNames is the database's own schema list, for the Default schema
	// picker — held per row because it differs per database.
	schemaNames []string
}

// schemaItemsFor is a database's schema list as picker items, never empty:
// a database whose schemas couldn't be listed still needs a usable default.
func schemaItemsFor(names []string) []string {
	if len(names) == 0 {
		return []string{"dbo"}
	}
	return names
}

// defaultSchemaFor picks the schema a newly mapped user starts on — dbo
// where it exists, which is every ordinary database, and otherwise the first
// schema the database actually has.
func defaultSchemaFor(names []string) string {
	if slices.Contains(names, "dbo") || len(names) == 0 {
		return "dbo"
	}
	return names[0]
}

// buildNewLoginUserMappingPage adapts pageLoginUserMapping's grid/role
// idiom (login_props.go) for a login that doesn't exist yet: every row
// starts unmapped, and only checked rows get an apply-time CREATE
// USER/ALTER ROLE ADD MEMBER.
func buildNewLoginUserMappingPage(sc *db.ServerConn, pf *nloginPrefetch, loginName func() string) (*propsheet.Form, propApply) {
	rows := make([]*nloginMapRow, len(pf.dbRoles))
	for i, dr := range pf.dbRoles {
		rows[i] = &nloginMapRow{
			dbName: dr.dbName, schema: defaultSchemaFor(dr.schemaNames),
			schemaNames: dr.schemaNames,
			roleNames:   dr.roleNames, roles: make([]bool, len(dr.roleNames)),
		}
	}

	// The User column shows a fixed placeholder rather than loginName():
	// this page's Form is built once, synchronously, right after the dialog
	// opens (see NewLoginDialog.buildPages), possibly before a login name
	// has been typed on General, and grid text set via SetData isn't
	// recomputed on revisit. loginName() is read fresh at apply time
	// (below), where it's always correct.
	const userPlaceholder = "(same as login name)"
	rowsFor := func() [][]string {
		out := make([][]string, len(rows))
		for i, e := range rows {
			out[i] = []string{mapCell(e.mapped), e.dbName, userPlaceholder, e.schema}
		}
		return out
	}
	grid := controls.NewDataGrid()
	grid.SetData(userMappingColumns, rowsFor())
	grid.SetCellCursor(true)
	grid.OnActivateCell = func(row, col int) {
		if col != 0 || row < 0 || row >= len(rows) {
			return
		}
		rows[row].mapped = !rows[row].mapped
		redrawGrid(grid, userMappingColumns, rowsFor())
	}

	dbStatic := propsheet.Static("Database", "")
	// A picker over the selected database's own schemas rather than a text
	// box: the schema has to exist in *that* database, and a typo only
	// surfaced as a failed CREATE USER at apply time.
	schemaPick := propsheet.Select("Default schema", []string{"dbo"}, 0)
	rolesGrid := propsheet.NewToggleGrid([]string{"Member", "Role"}, []int{0}, 8)

	selected := -1
	commitCurrent := func() {
		if selected < 0 || selected >= len(rows) {
			return
		}
		e := rows[selected]
		e.schema = schemaPick.Value()
		vals := rolesGrid.Values()
		for i := range e.roles {
			if i < len(vals) {
				e.roles[i] = vals[i][0]
			}
		}
	}
	syncFromSelection := func() {
		selected = grid.SelectedRow()
		if selected < 0 || selected >= len(rows) {
			dbStatic.SetValue("")
			schemaPick.SetItems([]string{"dbo"})
			rolesGrid.SetRows(nil, nil)
			return
		}
		e := rows[selected]
		dbStatic.SetValue(e.dbName)
		schemaPick.SetItems(schemaItemsFor(e.schemaNames))
		schemaPick.SetSelected(indexOf(schemaPick.Items(), e.schema))
		setRoleToggles(rolesGrid, e.roleNames, e.roles)
	}
	reload := wireGridEditor(grid, userMappingColumns, rowsFor, commitCurrent, syncFromSelection)

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
			e.schema = defaultSchemaFor(e.schemaNames)
			for i := range e.roles {
				e.roles[i] = false
			}
		}
		// reload, never syncFromSelection directly: reload redraws and reloads
		// the editor *without* committing first, which is the whole difference.
		// The schema picker and role toggles still hold the pre-revert values at
		// this point, so a commit would write the selected row straight back to
		// what Revert just undid.
		reload()
	}

	f := propsheet.NewForm(
		propsheet.Section("Users mapped to this login"),
		mappingRow,
		propsheet.Note("Space/Enter (or click) on Map toggles a database's user mapping. A newly mapped database uses the login's own name as the username."),
		propsheet.Section("Selected mapping"),
		dbStatic, schemaPick,
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
	grid.SetData(permissionStateColumns, rowsFor())
	grid.SetCellCursor(true)
	grid.OnActivateCell = func(row, col int) {
		if col != 1 || row < 0 || row >= len(edits) {
			return
		}
		edits[row].current = nextPermState(edits[row].current)
		redrawGrid(grid, permissionStateColumns, rowsFor())
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
		redrawGrid(grid, permissionStateColumns, rowsFor())
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
