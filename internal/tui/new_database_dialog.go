package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ndbPrefetch holds the one shared, one-time fetch every New Database page
// is built from: existing database names (for the name-uniqueness
// preflight check), server logins (for the Owner row), model's current
// options/recovery model/compatibility level (the Options/General pages'
// baselines — a real CREATE DATABASE already inherits these from model,
// so seeding each row's Dirty() baseline from model makes the existing
// pageDatabaseOptions-style apply-if-dirty logic correct for a brand-new
// database too, not just an edited one — see new_database_pages.go), and
// the server's default data/log paths (for file Path fields left blank).
type ndbPrefetch struct {
	existingNames map[string]bool
	loginNames    []string
	modelOptions  *gosmo.DatabaseOptions
	modelRecovery gosmo.RecoveryModel
	modelCompat   gosmo.CompatibilityLevel

	defaultDataPath string
	defaultLogPath  string
	defaultOwner    string
}

func fetchNewDatabasePrefetch(ctx context.Context, sc *db.ServerConn) (*ndbPrefetch, error) {
	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(dbs))
	for _, d := range dbs {
		existing[strings.ToLower(d.Name())] = true
	}

	logins, err := sc.Server.LoginsContext(ctx)
	if err != nil {
		return nil, err
	}
	loginNames := make([]string, len(logins))
	for i, l := range logins {
		loginNames[i] = l.Name
	}
	sort.Strings(loginNames)

	model, err := sc.Server.DatabaseByNameContext(ctx, "model")
	if err != nil {
		return nil, err
	}
	modelOpts, err := model.OptionsContext(ctx)
	if err != nil {
		return nil, err
	}

	info := sc.Server.Info()
	return &ndbPrefetch{
		existingNames:   existing,
		loginNames:      loginNames,
		modelOptions:    modelOpts,
		modelRecovery:   model.RecoveryModel(),
		modelCompat:     model.CompatibilityLevel(),
		defaultDataPath: info.DefaultDataPath,
		defaultLogPath:  info.DefaultLogPath,
		defaultOwner:    sc.Opts.User,
	}, nil
}

// NewDatabaseDialog is the New Database creation dialog (Object Explorer's
// server node and "Databases" folder, "New Database..."). Unlike
// PropDialog (prop_dialog.go), it doesn't diff "dirty" pages against a
// loaded baseline — there is no existing object to diff against. General's
// apply always runs first, unconditionally (it's what creates the
// database), before Options'/Filegroups' own applies can target a
// database that now exists — a fixed three-step sequence, not a
// discovered dirty set. One instance is reused for every invocation, same
// as PropDialog.
type NewDatabaseDialog struct {
	*propsheet.PropertySheet

	app *App
	sc  *db.ServerConn

	ctx    context.Context
	cancel context.CancelFunc

	prefetch  *ndbPrefetch
	forms     [3]*propsheet.Form
	applyFns  [3]propApply
	dbName    func() string
	preflight func() error
}

// NewNewDatabaseDialog creates the dialog and wires its callbacks.
func NewNewDatabaseDialog(app *App) *NewDatabaseDialog {
	d := &NewDatabaseDialog{
		app:           app,
		PropertySheet: propsheet.NewPropertySheet(app.screen, "New Database"),
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
// PropDialog.show.
func (d *NewDatabaseDialog) show(sc *db.ServerConn) {
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.sc = sc
	d.prefetch = nil
	d.forms = [3]*propsheet.Form{}
	d.applyFns = [3]propApply{}
	d.dbName = nil
	d.preflight = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages([]string{"General", "Options", "Filegroups"})
	d.Show()
}

func (d *NewDatabaseDialog) onClose() {
	if d.cancel != nil {
		d.cancel()
	}
}

// post schedules fn to run on the UI goroutine, then wakes the event loop
// immediately — the wake must happen here, on the calling background
// goroutine, not nested inside fn. See wakeEventLoop's doc comment in
// app.go and PropDialog.post, which this mirrors exactly.
func (d *NewDatabaseDialog) post(fn func()) {
	d.app.postEvent(fn)
	d.app.wakeEventLoop()
}

// onLoadPage answers PropertySheet's per-page load contract. The first
// call (always page 0, General, triggered by Show()) does the one real
// network fetch and builds all three pages' forms/applies from it; every
// later call (the user visiting Options/Filegroups for the first time)
// answers instantly from the already-cached prefetch/forms — no second
// fetch, no per-page spinner.
func (d *NewDatabaseDialog) onLoadPage(page, seq int) {
	if d.prefetch != nil {
		d.SetPageForm(page, seq, d.forms[page])
		return
	}
	sc := d.sc
	sessionCtx := d.ctx
	go func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		pf, err := fetchNewDatabasePrefetch(ctx, sc)
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

// buildPages constructs all three pages' forms/applies from pf, once —
// cheap and synchronous (no further IO), so there's no cost to building
// every page immediately instead of waiting for the user to visit it.
func (d *NewDatabaseDialog) buildPages(pf *ndbPrefetch) {
	d.prefetch = pf
	sc := d.sc

	generalForm, generalApply, nameField := buildNewDatabaseGeneralPage(sc, pf)
	dbName := func() string { return strings.TrimSpace(nameField.Value()) }
	optionsForm, optionsApply := buildNewDatabaseOptionsPage(sc, pf, dbName)
	filegroupsForm, filegroupsApply := buildNewDatabaseFilegroupsPage(sc, pf, dbName)

	d.forms = [3]*propsheet.Form{generalForm, optionsForm, filegroupsForm}
	d.applyFns = [3]propApply{generalApply, optionsApply, filegroupsApply}
	d.dbName = dbName
	d.preflight = func() error {
		name := dbName()
		if name == "" {
			return fmt.Errorf("database name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a database named %q already exists", name)
		}
		return nil
	}
}

// onConfirmDiscard guards F5/Refresh (which would otherwise silently
// rebuild whichever page is current from scratch, discarding any pending
// edits on it) behind the same confirmation prompt PropDialog uses.
func (d *NewDatabaseDialog) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDialog.ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}

// runPipeline validates, then runs every page's apply closure in a fixed
// order (General, Options, Filegroups) on a background goroutine —
// General's closure is a real network round trip (the CREATE DATABASE
// itself) that must complete before Options'/Filegroups' own closures can
// target the database it just created.
func (d *NewDatabaseDialog) runPipeline(runCtx context.Context, onSuccess func()) {
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

// runApply validates and creates the database for real. hideOnSuccess
// distinguishes Apply (stay open) from OK (close once creation succeeds).
func (d *NewDatabaseDialog) runApply(hideOnSuccess bool) {
	d.runPipeline(d.ctx, func() {
		d.app.setStatus(fmt.Sprintf("Database %q created", d.dbName()))
		d.app.explorer.RefreshDatabasesFolder(d.sc)
		if hideOnSuccess {
			d.Hide()
		}
	})
}

// runScript re-runs the same fixed apply sequence under a
// gosmo.WithScript-derived context, capturing every write as SQL text
// instead of executing it, then opens it in a new query window — General's
// apply always emits at least the CREATE DATABASE statement, so (unlike
// PropDialog.runScript) there's no "nothing to script" case to handle.
func (d *NewDatabaseDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "", strings.Join(script.Statements, "\n\n"))
	})
}
