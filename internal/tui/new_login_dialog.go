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

	// schemaNames is the database's schemas, for the Default schema picker
	// on the User Mapping page — a schema that does not exist there fails
	// the CREATE USER at apply time, which is what a free-text box invited.
	schemaNames []string
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

	// certNames and asymKeyNames are master's certificates and asymmetric
	// keys, for the Mapped-object picker a certificate- or asymmetric-key-
	// mapped login needs. Either list can be empty because the read failed
	// rather than because the instance has none — see fetchNewLoginPrefetch.
	certNames    []string
	asymKeyNames []string
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
	for _, d := range dbs {
		dbNames = append(dbNames, d.Name())
	}
	// Two round trips per ONLINE database, so serially this is 2N latencies
	// inside one propFetchTimeout before the dialog can show any page at all.
	// A database that drops out here — offline, or unreadable — simply has no
	// roles or schemas to offer, which is what the User Mapping page already
	// shows for an offline one.
	dbRoles, err := eachDatabase(ctx, onlineDatabases(dbs), func(ctx context.Context, d *gosmo.Database) (nloginDBRoles, error) {
		roles, err := d.DatabaseRolesContext(ctx)
		if err != nil {
			return nloginDBRoles{}, err
		}
		var roleNames []string
		for _, r := range roles {
			if r.Name == "public" {
				continue
			}
			roleNames = append(roleNames, r.Name)
		}
		schemas, err := d.SchemasContext(ctx)
		if err != nil {
			return nloginDBRoles{}, err
		}
		schemaNames := make([]string, len(schemas))
		for i, sch := range schemas {
			schemaNames[i] = sch.Name
		}
		return nloginDBRoles{dbName: d.Name(), roleNames: roleNames, schemaNames: schemaNames}, nil
	})
	if err != nil {
		return nil, err
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

	certNames, asymKeyNames := masterMappableNames(ctx, sc)

	return &nloginPrefetch{
		existingNames: existing,
		dbNames:       dbNames,
		langNames:     langNames,
		serverRoles:   roles,
		dbRoles:       dbRoles,
		certNames:     certNames,
		asymKeyNames:  asymKeyNames,
	}, nil
}

// masterMappableNames reads the certificates and asymmetric keys in master a
// mapped login can be created from. A failure here is deliberately not the
// dialog's failure: sys.certificates and sys.asymmetric_keys are readable only
// with permission on master, and a login that lacks it still creates ordinary
// SQL and Windows logins. An empty list leaves the picker offering nothing,
// which the General page's apply turns into a refusal naming the missing pick.
func masterMappableNames(ctx context.Context, sc *db.ServerConn) (certs, keys []string) {
	master := sc.Server.Database("master")
	if cs, err := master.CertificatesContext(ctx); err == nil {
		for _, c := range cs {
			certs = append(certs, c.Name)
		}
	}
	if ks, err := master.AsymmetricKeysContext(ctx); err == nil {
		for _, k := range ks {
			keys = append(keys, k.Name)
		}
	}
	return certs, keys
}

// NewLoginDialog is the New Login creation dialog (Object Explorer's
// Security > Logins folder, "New Login..."). Same fixed-sequence-apply
// shape as NewDatabaseDialog (new_database_dialog.go): General's apply
// always runs first — it's what creates the login — before Server Roles/
// User Mapping/Securables/Status's own applies can target a login that now
// exists, a fixed five-step sequence rather than a discovered dirty set.
// All five of gosmo's login sources are offered — see
// buildNewLoginGeneralPage.
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
