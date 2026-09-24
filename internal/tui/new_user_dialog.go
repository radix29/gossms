package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_user_dialog.go is the New User dialog (a database's Security > Users
// folder), built on newObjectDialog. It offers SSMS's user types — every form
// of gosmo.CreateUserRequest — with Entra only on an Azure engine edition.
//
// Three pages, applied in order: General creates the user, then Owned Schemas
// and Membership target a user that now exists. Under Script Changes the
// later pages address the user by name alone, so nothing has to be read back.

// nuserKinds are the General page's User type options, in radio order. The
// labels are userTypeLabel's (user_props.go), so a user created here and then
// opened in its Properties describes itself the same way.
var nuserKinds = []struct {
	label string
	kind  gosmo.UserKind
}{
	{"SQL user with login", gosmo.UserForLogin},
	{"SQL user with password", gosmo.UserWithPassword},
	{"SQL user without login", gosmo.UserWithoutLogin},
	{"Windows user", gosmo.UserWindows},
	{"User mapped to a certificate", gosmo.UserFromCertificate},
	{"User mapped to an asymmetric key", gosmo.UserFromAsymmetricKey},
	{"External user or group", gosmo.UserFromExternalProvider},
}

// nuserDefaultSchema is the Default schema picker's "leave DEFAULT_SCHEMA
// off" entry — the server then defaults it to dbo.
const nuserDefaultSchema = "(Default)"

// nuserPrefetch is everything the dialog reads before it opens.
type nuserPrefetch struct {
	// existingNames holds every database principal's name, lowered: a user
	// cannot share one with a role either.
	existingNames map[string]bool
	logins        []string
	windowsLogins []string
	schemas       []*gosmo.Schema
	roles         []string
	certNames     []string
	asymKeyNames  []string

	// containment is the database's CONTAINMENT, or "" when it could not be
	// read — then gosmo's own check is the one that refuses.
	containment string
	// azure offers the Entra kind; everyContained skips the containment
	// preflight on Azure SQL Database, where every database takes contained
	// users while reporting CONTAINMENT = NONE.
	azure          bool
	everyContained bool
}

func fetchNewUserPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nuserPrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	pf := &nuserPrefetch{existingNames: map[string]bool{}, azure: serverIsAzure(sc)}
	if info := sc.Server.Info(); info != nil {
		pf.everyContained = gosmo.EngineEdition(info.EngineEdition) == gosmo.EngineAzureSQLDatabase
	}

	users, err := d.Users(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		pf.existingNames[strings.ToLower(u.Name)] = true
	}
	roles, err := d.DatabaseRoles(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		pf.existingNames[strings.ToLower(r.Name)] = true
		// public's membership is implicit and ALTER ROLE public ADD MEMBER
		// is a syntax error — the exclusion every membership page makes.
		if r.Name != "public" {
			pf.roles = append(pf.roles, r.Name)
		}
	}
	if pf.schemas, err = d.Schemas(ctx); err != nil {
		return nil, err
	}

	logins, err := sc.Server.Logins(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range logins {
		pf.logins = append(pf.logins, l.Name)
		if strings.HasPrefix(l.LoginType, "WINDOWS") {
			pf.windowsLogins = append(pf.windowsLogins, l.Name)
		}
	}

	// Best-effort, as masterMappableNames is for New Login: a principal that
	// cannot read the key catalogs still creates every other kind, and an
	// empty picker turns into a refusal naming the missing pick.
	if cs, err := d.Certificates(ctx); err == nil {
		for _, c := range cs {
			pf.certNames = append(pf.certNames, c.Name)
		}
	}
	if ks, err := d.AsymmetricKeys(ctx); err == nil {
		for _, k := range ks {
			pf.asymKeyNames = append(pf.asymKeyNames, k.Name)
		}
	}
	if o, err := d.Options(ctx); err == nil {
		pf.containment = o.Containment
	}
	return pf, nil
}

// NewUserDialog is the New User dialog.
type NewUserDialog struct {
	newObjectDialog[nuserPrefetch]

	// dbName is the database the user is created in, and node the Users
	// folder to refresh afterwards — set by show, before the prefetch that
	// reads them, as NewCertificateDialog does.
	dbName string
	node   *explorerNode
}

// NewNewUserDialog creates the dialog and wires its callbacks.
func NewNewUserDialog(app *App) *NewUserDialog {
	d := &NewUserDialog{}
	d.init(app, newObjectConfig[nuserPrefetch]{
		title: "New User",
		noun:  "User",
		pages: []string{"General", "Owned Schemas", "Membership"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nuserPrefetch, error) {
			return fetchNewUserPrefetch(ctx, sc, d.dbName)
		},
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	return d
}

// show opens the dialog for one database's Users folder.
func (d *NewUserDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *NewUserDialog) buildPages(pf *nuserPrefetch) {
	general := buildNewUserGeneralPage(d.sc, d.dbName, pf)
	userName := func() string { return strings.TrimSpace(general.name.Value()) }
	schemasForm, schemasApply := buildNewUserOwnedSchemasPage(pf, userName)
	membershipForm, membershipApply := buildNewUserMembershipPage(d.sc, d.dbName, pf, userName)

	d.forms = []*propsheet.Form{general.form, schemasForm, membershipForm}
	d.applyFns = []propApply{general.apply, schemasApply, membershipApply}
	d.objectName = userName
	d.preflight = func() error { return validateNewUser(general.input(), pf) }
}

// nuserGeneral is the General page's widgets, for the dialog's preflight and
// the later pages' use of the name.
type nuserGeneral struct {
	form  *propsheet.Form
	apply propApply

	// kinds is the radio's options on this server, in radio order.
	kinds             []gosmo.UserKind
	name              *propsheet.TextRow
	kind              *propsheet.RadioRow
	login, mapped     *propsheet.SelectRow
	password, confirm *propsheet.TextRow
	objectID          *propsheet.TextRow
	schema            *propsheet.SelectRow
}

// nuserKindsFor is the User type options on this server: Entra is left off
// where no engine edition takes it.
func nuserKindsFor(pf *nuserPrefetch) []gosmo.UserKind {
	var out []gosmo.UserKind
	for _, k := range nuserKinds {
		if k.kind == gosmo.UserFromExternalProvider && !pf.azure {
			continue
		}
		out = append(out, k.kind)
	}
	return out
}

func nuserKindLabel(k gosmo.UserKind) string {
	for _, e := range nuserKinds {
		if e.kind == k {
			return e.label
		}
	}
	return k.String()
}

// buildNewUserGeneralPage builds the General page. The User type group drives
// the rest through RadioRow.SetOnChange, as New Login's Authentication group
// does: a row the kind has no clause for is disabled or emptied, never left
// inviting input the CREATE USER cannot carry. gosmo refuses every such
// combination too; the gating is the dialog being honest about what it sends.
func buildNewUserGeneralPage(sc *db.ServerConn, dbName string, pf *nuserPrefetch) *nuserGeneral {
	kinds := nuserKindsFor(pf)
	labels := make([]string, len(kinds))
	for i, k := range kinds {
		labels[i] = nuserKindLabel(k)
	}
	g := &nuserGeneral{
		kinds:    kinds,
		name:     propsheet.Text("User name", "", 30),
		kind:     propsheet.Radio("User type", labels, 0),
		login:    propsheet.Select("Login name", []string{noneItem}, 0),
		mapped:   propsheet.Select("Mapped to", []string{noneItem}, 0),
		password: propsheet.Password("Password", 20),
		confirm:  propsheet.Password("Confirm password", 20),
		objectID: propsheet.Text("Entra object ID", "", 36),
	}
	schemaItems := []string{nuserDefaultSchema}
	for _, s := range pf.schemas {
		schemaItems = append(schemaItems, s.Name)
	}
	g.schema = propsheet.Select("Default schema", schemaItems, 0)
	kind := func() gosmo.UserKind { return kinds[g.kind.Selected()] }

	sync := func() {
		k := kind()
		var logins, mapped []string
		switch k {
		case gosmo.UserForLogin:
			// (None) first, which the preflight refuses: the first login
			// alphabetically is typically a ##MS_...## certificate login,
			// and pre-selecting it made it the answer to a question nobody
			// had answered yet.
			logins = append([]string{noneItem}, pf.logins...)
		case gosmo.UserWindows:
			// (None) first here too, but as a real choice: a Windows user
			// with no login is a contained database's.
			logins = append([]string{noneItem}, pf.windowsLogins...)
		case gosmo.UserFromCertificate:
			mapped = pf.certNames
		case gosmo.UserFromAsymmetricKey:
			mapped = pf.asymKeyNames
		}
		if logins == nil {
			logins = []string{noneItem}
		}
		if len(mapped) == 0 {
			mapped = []string{noneItem}
		}
		g.login.SetItems(logins)
		g.mapped.SetItems(mapped)
		isContained := k == gosmo.UserWithPassword
		g.password.SetEnabled(isContained)
		g.confirm.SetEnabled(isContained)
		g.objectID.SetEnabled(k == gosmo.UserFromExternalProvider)
		if k == gosmo.UserFromCertificate || k == gosmo.UserFromAsymmetricKey {
			g.schema.SetItems([]string{"n/a"})
		} else {
			g.schema.SetItems(schemaItems)
		}
	}
	g.kind.SetOnChange(func(int) { sync() })
	sync()

	containment := orDefault(pf.containment, "unknown")
	if pf.everyContained {
		containment = "every database (Azure SQL Database)"
	}
	g.form = propsheet.NewForm(
		propsheet.Section("User"),
		g.name, g.kind,
		propsheet.Section("Login or mapping"),
		g.login, g.mapped, g.objectID,
		propsheet.Note(`Type a Windows user's name as DOMAIN\name. Pick (None) for a Windows user in a contained database. An Entra object ID is only needed when the display name is ambiguous in the directory.`),
		propsheet.Section("Password"),
		g.password, g.confirm,
		propsheet.Static("Containment", containment),
		propsheet.Note("A user with a password needs a partially contained database, and the server's 'contained database authentication' option on."),
		propsheet.Section("Defaults"),
		g.schema,
		propsheet.Note("A user mapped to a certificate or asymmetric key has no default schema — SQL Server refuses one."),
	)

	g.apply = func(ctx context.Context) error {
		req := g.request()
		req.Password = scriptSafePassword(ctx, req.Password)
		// DatabaseRef, not DatabaseByName: the statement addresses the
		// database by name, and the by-name read would not work under Script
		// Changes.
		return sc.Server.DatabaseRef(dbName).CreateUser(ctx, req)
	}
	return g
}

// nuserInput is the General page's values, for validateNewUser.
type nuserInput struct {
	req     gosmo.CreateUserRequest
	confirm string
}

func (g *nuserGeneral) input() nuserInput {
	return nuserInput{req: g.request(), confirm: g.confirm.Value()}
}

// request reads the page into the gosmo request, taking from each row only
// what the selected kind has a clause for — so a password left typed after
// switching kind is never sent.
func (g *nuserGeneral) request() gosmo.CreateUserRequest {
	k := g.kinds[g.kind.Selected()]
	req := gosmo.CreateUserRequest{Name: strings.TrimSpace(g.name.Value()), Kind: k}
	pick := func(r *propsheet.SelectRow) string {
		if v := r.Value(); v != noneItem {
			return v
		}
		return ""
	}
	switch k {
	case gosmo.UserForLogin, gosmo.UserWindows:
		req.Login = pick(g.login)
	case gosmo.UserWithPassword:
		req.Password = g.password.Value()
	case gosmo.UserFromCertificate:
		req.Certificate = pick(g.mapped)
	case gosmo.UserFromAsymmetricKey:
		req.AsymmetricKey = pick(g.mapped)
	case gosmo.UserFromExternalProvider:
		req.ObjectID = strings.TrimSpace(g.objectID.Value())
	}
	if k != gosmo.UserFromCertificate && k != gosmo.UserFromAsymmetricKey {
		if v := g.schema.Value(); v != nuserDefaultSchema {
			req.DefaultSchema = v
		}
	}
	return req
}

// validateNewUser refuses what the server would, with a message naming the
// field: Msg 33233 for a contained user says nothing about which setting or
// database, and "FOR LOGIN []" is gosmo's refusal of a pick nobody made.
func validateNewUser(in nuserInput, pf *nuserPrefetch) error {
	r := in.req
	if r.Name == "" {
		return fmt.Errorf("user name is required")
	}
	if pf.existingNames[strings.ToLower(r.Name)] {
		return fmt.Errorf("a user or role named %q already exists", r.Name)
	}
	switch r.Kind {
	case gosmo.UserForLogin:
		if r.Login == "" {
			return fmt.Errorf("select the login this user maps to")
		}
	case gosmo.UserWithPassword:
		if r.Password == "" {
			return fmt.Errorf("a user with a password needs a password")
		}
		if r.Password != in.confirm {
			return fmt.Errorf("passwords do not match")
		}
		if !pf.everyContained && pf.containment != "" && pf.containment != "PARTIAL" {
			return fmt.Errorf("a user with a password needs a contained database — this one has CONTAINMENT = %s", pf.containment)
		}
	case gosmo.UserFromCertificate:
		if r.Certificate == "" {
			return fmt.Errorf("select the certificate this user maps to")
		}
	case gosmo.UserFromAsymmetricKey:
		if r.AsymmetricKey == "" {
			return fmt.Errorf("select the asymmetric key this user maps to")
		}
	}
	return nil
}

// buildNewUserOwnedSchemasPage lists every schema whose owner can change,
// unticked; a ticked one is transferred to the new user. sys and
// INFORMATION_SCHEMA are left out — their ownership cannot be changed.
func buildNewUserOwnedSchemasPage(pf *nuserPrefetch, userName func() string) (*propsheet.Form, propApply) {
	var schemas []*gosmo.Schema
	for _, s := range pf.schemas {
		if s.Name == "sys" || s.Name == "INFORMATION_SCHEMA" {
			continue
		}
		schemas = append(schemas, s)
	}
	text := make([][]string, len(schemas))
	values := make([][]bool, len(schemas))
	for i, s := range schemas {
		text[i] = []string{s.Name, s.Owner}
		values[i] = []bool{false}
	}
	grid := propsheet.NewToggleGrid([]string{"Owned", "Schema", "Current owner"}, []int{0}, 12)
	grid.SetRows(text, values)

	f := propsheet.NewForm(
		propsheet.Section("Schemas owned by this user"),
		grid,
		propsheet.Note("A ticked schema is transferred to the new user. Changing schema ownership can affect permission chaining and deployment scripts."),
	)
	apply := func(ctx context.Context) error {
		for i, v := range grid.Values() {
			if !v[0] {
				continue
			}
			// The prefetched schema is enough: ChangeOwner addresses it by
			// name, which is what keeps this page scriptable.
			if err := schemas[i].ChangeOwner(ctx, userName()); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}

// buildNewUserMembershipPage is a tick list of the database's roles, public
// excluded; every ticked role gets an ADD MEMBER.
func buildNewUserMembershipPage(sc *db.ServerConn, dbName string, pf *nuserPrefetch, userName func() string) (*propsheet.Form, propApply) {
	text := make([][]string, len(pf.roles))
	values := make([][]bool, len(pf.roles))
	for i, r := range pf.roles {
		text[i] = []string{r}
		values[i] = []bool{false}
	}
	grid := propsheet.NewToggleGrid([]string{"Member", "Role"}, []int{0}, 12)
	grid.SetRows(text, values)

	f := propsheet.NewForm(
		propsheet.Section("Database role membership"),
		grid,
		propsheet.Note("public is granted to every user automatically and cannot be removed."),
	)
	apply := func(ctx context.Context) error {
		d := sc.Server.DatabaseRef(dbName)
		for i, v := range grid.Values() {
			if !v[0] {
				continue
			}
			if err := d.AddRoleMember(ctx, pf.roles[i], userName()); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}

// showNewUserDialog is the Users folder's entry point.
func (a *App) showNewUserDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newUserDialog.show(sc, node)
}
