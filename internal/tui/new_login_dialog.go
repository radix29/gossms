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
// External-provider (Entra ID/Azure AD) logins are deliberately not
// offered — SQL Server auth and Windows auth cover every server this
// project can live-test against, and gosmo has no support for FROM
// EXTERNAL PROVIDER today; a real, separate gap for a future pass, the
// same treatment given to every previously-deferred platform-conditional
// feature in this project.
type NewLoginDialog struct {
	*propsheet.PropertySheet

	app *App
	sc  *db.ServerConn

	ctx    context.Context
	cancel context.CancelFunc

	prefetch  *nloginPrefetch
	forms     [5]*propsheet.Form
	applyFns  [5]propApply
	loginName func() string
	preflight func() error
}

// NewNewLoginDialog creates the dialog and wires its callbacks.
func NewNewLoginDialog(app *App) *NewLoginDialog {
	d := &NewLoginDialog{
		app:           app,
		PropertySheet: propsheet.NewPropertySheet(app.screen, "New Login"),
	}
	d.OnLoadPage = d.onLoadPage
	d.OnApply = func() { d.runApply(false) }
	d.OnOK = func() { d.runApply(true) }
	d.OnClose = d.onClose
	d.ConfirmDiscard = d.onConfirmDiscard
	d.OnScript = d.runScript
	return d
}

// show opens the dialog against sc, resetting all pending state — a fresh
// page set and a fresh per-session context each time, mirroring
// NewDatabaseDialog.show.
func (d *NewLoginDialog) show(sc *db.ServerConn) {
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.sc = sc
	d.prefetch = nil
	d.forms = [5]*propsheet.Form{}
	d.applyFns = [5]propApply{}
	d.loginName = nil
	d.preflight = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages([]string{"General", "Server Roles", "User Mapping", "Securables", "Status"})
	d.Show()
}

func (d *NewLoginDialog) onClose() {
	if d.cancel != nil {
		d.cancel()
	}
}

// post schedules fn to run on the UI goroutine, then wakes the event loop
// immediately — the wake must happen here, on the calling background
// goroutine, not nested inside fn. See wakeEventLoop's doc comment in
// app.go and PropDialog.post, which this mirrors exactly.
func (d *NewLoginDialog) post(fn func()) {
	d.app.postEvent(fn)
	d.app.wakeEventLoop()
}

// onLoadPage answers PropertySheet's per-page load contract. The first
// call (always page 0, General, triggered by Show()) does the one real
// network fetch and builds all five pages' forms/applies from it; every
// later call (the user visiting another page for the first time) answers
// instantly from the already-cached prefetch/forms — no second fetch, no
// per-page spinner.
func (d *NewLoginDialog) onLoadPage(page, seq int) {
	if d.prefetch != nil {
		d.SetPageForm(page, seq, d.forms[page])
		return
	}
	sc := d.sc
	sessionCtx := d.ctx
	go func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		pf, err := fetchNewLoginPrefetch(ctx, sc)
		d.post(func() {
			if err != nil {
				d.SetPageError(page, seq, err)
				return
			}
			d.buildPages(pf)
			d.SetPageForm(page, seq, d.forms[page])
		})
	}()
}

// buildPages constructs all five pages' forms/applies from pf, once —
// cheap and synchronous (no further IO), so there's no cost to building
// every page immediately instead of waiting for the user to visit it.
func (d *NewLoginDialog) buildPages(pf *nloginPrefetch) {
	d.prefetch = pf
	sc := d.sc

	generalForm, generalApply, nameField := buildNewLoginGeneralPage(sc, pf)
	loginName := func() string { return strings.TrimSpace(nameField.Value()) }
	rolesForm, rolesApply := buildNewLoginServerRolesPage(sc, pf, loginName)
	mappingForm, mappingApply := buildNewLoginUserMappingPage(sc, pf, loginName)
	securablesForm, securablesApply := buildNewLoginSecurablesPage(sc, loginName)
	statusForm, statusApply := buildNewLoginStatusPage(sc, loginName)

	d.forms = [5]*propsheet.Form{generalForm, rolesForm, mappingForm, securablesForm, statusForm}
	d.applyFns = [5]propApply{generalApply, rolesApply, mappingApply, securablesApply, statusApply}
	d.loginName = loginName
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
func (d *NewLoginDialog) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDialog.ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}

// runPipeline validates, then runs every page's apply closure in a fixed
// order (General, Server Roles, User Mapping, Securables, Status) on a
// background goroutine — General's closure is a real network round trip
// (the CREATE LOGIN itself) that must complete before the later pages' own
// closures can target the login it just created.
func (d *NewLoginDialog) runPipeline(runCtx context.Context, onSuccess func()) {
	if d.prefetch == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return
	}
	if err := d.preflight(); err != nil {
		d.SelectPage(0)
		d.SetMessage(err.Error(), true)
		return
	}
	if page, err := d.Validate(); err != nil {
		d.SelectPage(page)
		d.SetMessage(err.Error(), true)
		return
	}

	fns := d.applyFns
	d.SetApplying(true)
	d.SetMessage("", false)

	go func() {
		var runErr error
		for _, fn := range fns {
			if runErr = fn(runCtx); runErr != nil {
				break
			}
		}
		d.post(func() {
			d.SetApplying(false)
			if runErr != nil {
				d.SetMessage(runErr.Error(), true)
				return
			}
			onSuccess()
		})
	}()
}

// runApply validates and creates the login for real. hideOnSuccess
// distinguishes Apply (stay open) from OK (close once creation succeeds).
func (d *NewLoginDialog) runApply(hideOnSuccess bool) {
	d.runPipeline(d.ctx, func() {
		d.app.setStatus(fmt.Sprintf("Login %q created", d.loginName()))
		d.app.explorer.RefreshLoginsFolder(d.sc)
		if hideOnSuccess {
			d.Hide()
		}
	})
}

// runScript re-runs the same fixed apply sequence under a
// gosmo.WithScript-derived context, capturing every write as SQL text
// instead of executing it, then opens it in a new query window — General's
// apply always emits at least the CREATE LOGIN statement, so there's no
// "nothing to script" case to handle.
func (d *NewLoginDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "", strings.Join(script.Statements, "\n\n"))
	})
}
