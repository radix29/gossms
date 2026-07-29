package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// nloginDBRoles is one online database's assignable role names — "public"
// excluded, since membership is implicit and ALTER ROLE public ADD MEMBER
// is a syntax error, the same exclusion pageLoginUserMapping already makes
// for an existing login.
type nloginDBRoles struct {
	dbName    string
	roleNames []string
}

// nloginPrefetch holds the one shared, one-time fetch every New Login page
// is built from: existing login names (for the name-uniqueness preflight
// check), database names (Default database/User Mapping rows), languages
// (Default language), server roles (Server Roles page), and each online
// database's role list (User Mapping page) — mirrors ndbPrefetch's "one
// fetch, build every page synchronously from it" shape
// (new_database_dialog.go).
type nloginPrefetch struct {
	existingNames map[string]bool
	dbNames       []string
	langNames     []string
	serverRoles   []*gosmo.ServerRole
	dbRoles       []nloginDBRoles
}

func fetchNewLoginPrefetch(ctx context.Context, sc *db.ServerConn) (*nloginPrefetch, error) {
	logins, err := sc.Server.LoginsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(logins))
	for _, l := range logins {
		existing[strings.ToLower(l.Name)] = true
	}

	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	dbNames := make([]string, 0, len(dbs))
	var dbRoles []nloginDBRoles
	for _, d := range dbs {
		dbNames = append(dbNames, d.Name())
		if d.State() != "ONLINE" {
			continue
		}
		roles, err := d.DatabaseRolesContext(ctx)
		if err != nil {
			return nil, err
		}
		var roleNames []string
		for _, r := range roles {
			if r.Name == "public" {
				continue
			}
			roleNames = append(roleNames, r.Name)
		}
		dbRoles = append(dbRoles, nloginDBRoles{dbName: d.Name(), roleNames: roleNames})
	}

	langs, err := sc.Server.LanguagesContext(ctx)
	if err != nil {
		return nil, err
	}
	langNames := make([]string, len(langs))
	for i, lg := range langs {
		langNames[i] = lg.Name
	}

	roles, err := sc.Server.ServerRolesContext(ctx)
	if err != nil {
		return nil, err
	}

	return &nloginPrefetch{
		existingNames: existing,
		dbNames:       dbNames,
		langNames:     langNames,
		serverRoles:   roles,
		dbRoles:       dbRoles,
	}, nil
}

// NewLoginDialog is the New Login creation dialog (Object Explorer's
// Security > Logins folder, "New Login..."). Same fixed-sequence-apply
// shape as NewDatabaseDialog (new_database_dialog.go): General's apply
// always runs first — it's what creates the login — before Server Roles/
// User Mapping/Securables/Status's own applies can target a login that now
// exists, a fixed five-step sequence rather than a discovered dirty set.
// External-provider (Entra ID/Azure AD) logins are not offered: gosmo has
// no support for FROM EXTERNAL PROVIDER.
type NewLoginDialog struct {
	newObjectDialog[nloginPrefetch]
}

// NewNewLoginDialog creates the dialog and wires its callbacks.
func NewNewLoginDialog(app *App) *NewLoginDialog {
	d := &NewLoginDialog{}
	d.init(app, newObjectConfig[nloginPrefetch]{
		title:          "New Login",
		noun:           "Login",
		pages:          []string{"General", "Server Roles", "User Mapping", "Securables", "Status"},
		scriptDatabase: "",
		fetch:          fetchNewLoginPrefetch,
		build:          d.buildPages,
		refresh:        func(sc *db.ServerConn) { d.app.explorer.RefreshLoginsFolder(sc) },
	})
	return d
}

func (d *NewLoginDialog) buildPages(pf *nloginPrefetch) {
	sc := d.sc

	generalForm, generalApply, nameField := buildNewLoginGeneralPage(sc, pf)
	loginName := func() string { return strings.TrimSpace(nameField.Value()) }
	rolesForm, rolesApply := buildNewLoginServerRolesPage(sc, pf, loginName)
	mappingForm, mappingApply := buildNewLoginUserMappingPage(sc, pf, loginName)
	securablesForm, securablesApply := buildNewLoginSecurablesPage(sc, loginName)
	statusForm, statusApply := buildNewLoginStatusPage(sc, loginName)

	d.forms = []*propsheet.Form{generalForm, rolesForm, mappingForm, securablesForm, statusForm}
	d.applyFns = []propApply{generalApply, rolesApply, mappingApply, securablesApply, statusApply}
	d.objectName = loginName
	d.preflight = func() error {
		name := loginName()
		if name == "" {
			return fmt.Errorf("login name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a login named %q already exists", name)
		}
		return nil
	}
}

// onConfirmDiscard guards F5/Refresh (which would otherwise silently
// rebuild whichever page is current from scratch, discarding any pending
// edits on it) behind the same confirmation prompt PropDialog/
// NewDatabaseDialog use.
