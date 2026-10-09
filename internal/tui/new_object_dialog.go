package tui

import (
	"context"
	"errors"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_object_dialog.go is the shell every "New <object>" dialog is built on: one
// prefetch, one propsheet.PropertySheet whose pages are built from it at once,
// and an OK/Apply/Script pipeline running each page's apply in order. Each
// per-dialog file is a newObjectConfig plus a buildPages method.
//
// PropDialog (prop_dialog.go) is deliberately separate: it loads pages lazily
// and applies a dirty-diff, neither of which a create dialog needs.

// errPageLoadPanicked fails a page request when the goroutine loading it
// panicked (this dialog's prefetch, PropDialog's per-page load). The panic is
// already logged; the page only has to say why it has no content.
var errPageLoadPanicked = errors.New("loading stopped unexpectedly — see the log for details")

// newObjectConfig is everything that differs between create dialogs, passed to
// newObjectDialog.init.
type newObjectConfig[P any] struct {
	// title is the dialog's window title, e.g. "New Job".
	title string
	// noun names the created object in the success message, e.g. "Job" -> `Job
	// "nightly reindex" created`.
	noun string
	// verb completes that message when "created" would be untrue (Add Database to an
	// availability group: "added to the availability group"). Empty means "created".
	verb string
	// pages are the page names, in order; forms and applyFns share the indexing.
	pages []string
	// scriptDatabase is the database Script Changes opens its query window in:
	// "msdb" for Agent objects, "" (connection default) otherwise.
	scriptDatabase string
	// fetch loads the prefetch payload: everything the pages need from the server.
	fetch func(context.Context, *db.ServerConn) (*P, error)
	// build populates forms, applyFns, objectName and preflight from a completed
	// prefetch. All four close over the same widgets, so one function makes them.
	build func(*P)
	// refresh resyncs the Object Explorer folder the new object belongs to, once
	// creation has succeeded.
	refresh func(*db.ServerConn)
}

// pageRequest is one outstanding OnLoadPage call, held until its prefetch
// completes. seq goes back to SetPageForm unchanged, which drops it if the page
// has been reloaded since.
type pageRequest struct{ page, seq int }

// newObjectDialog is the shared state and behaviour behind every create dialog.
// P is the prefetch payload type; concrete dialogs embed it by value and add a
// buildPages method.
type newObjectDialog[P any] struct {
	*propsheet.PropertySheet
	newObjectConfig[P]

	app *App
	sc  *db.ServerConn

	// ctx spans one show..close, derived from the connection's Context so a
	// disconnect tears down in-flight work; cancel ends it (onClose).
	ctx    context.Context
	cancel context.CancelFunc

	prefetch *P
	forms    []*propsheet.Form
	applyFns []propApply

	// fetching guards against a second prefetch: the sheet asks for a page whenever
	// one is selected, and switching pages mid-fetch would start another and rebuild
	// every form. waiting collects the pages asked for meanwhile, all served from
	// the one prefetch.
	fetching bool
	waiting  []pageRequest

	// prefetchRun is the prefetch in flight, superseded by each new showing. A
	// prefetch outlives its showing when the dialog is closed and reopened before it
	// lands; the sheet's per-page seq keeps the stale result off the new pages but
	// not off this dialog's own state (onLoadPage).
	prefetchRun latest

	// objectName returns the typed name for the success message; preflight rejects
	// it before anything is sent. Both are assigned by build.
	objectName func() string
	preflight  func() error

	// afterCreate, when build sets it, runs on the UI goroutine once a real Apply or
	// OK has created the object, after the dialog closes on OK (New Session opens
	// Watch Live Data on the new session).
	afterCreate func()

	// created is set once the pipeline has run through successfully. Apply leaves
	// the dialog open, so without it a second Apply, or Apply then OK, re-issues the
	// CREATE and fails with "already exists".
	created bool

	// run is the OK/Apply/Script pipeline in flight, if any.
	run applyRun
}

// probeReplicaEndpoint reaches instance name with this dialog's credentials and
// reads its database mirroring endpoint, delivering both to onOK on the UI
// goroutine. onErr handles a failure; nil shows the error in the dialog.
//
// Shared by Add Replica and New Availability Group, which need an instance's
// real endpoint URL before writing ADD REPLICA (a guessed one gives a replica
// that never connects).
func (d *newObjectDialog[P]) probeReplicaEndpoint(label, name string,
	onOK func(peer *db.ServerConn, ep *gosmo.DatabaseMirroringEndpoint),
	onErr func(error)) {

	sc := d.sc
	d.SetMessage("Connecting to "+name+"...", false)
	var peer *db.ServerConn
	var ep *gosmo.DatabaseMirroringEndpoint
	d.probe(label, func(ctx context.Context) error {
		var err error
		if peer, err = sc.Peer(ctx, name); err != nil {
			return err
		}
		ep, err = replicaEndpoint(ctx, peer)
		return err
	}, func(err error) {
		if err != nil {
			if onErr != nil {
				onErr(err)
				return
			}
			d.SetMessage(err.Error(), true)
			return
		}
		onOK(peer, ep)
	})
}

// probe runs work (a round trip to another instance) on a background goroutine
// and hands its error to onDone on the UI goroutine; work passes anything else
// out through captured variables, which onDone may read.
//
// Every peer probe goes through here (Add Replica, New Availability Group, New
// Endpoint's Add Instance) for two easy-to-miss parts: the session-context
// snapshot is compared again on delivery, so a result for a dialog since closed
// and reopened is dropped, and the propFetchTimeout derives from that context,
// so a disconnect tears the probe down.
func (d *newObjectDialog[P]) probe(label string, work func(ctx context.Context) error, onDone func(error)) {
	sessionCtx := d.ctx
	d.app.safego(label, func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		err := work(ctx)
		d.app.postAndWake(func() {
			if d.ctx != sessionCtx {
				return
			}
			onDone(err)
		})
	})
}

// init wires cfg into d and hooks up the PropertySheet callbacks. Call it from
// the concrete dialog's constructor after the concrete value exists; cfg.build is
// normally that value's buildPages.
func (d *newObjectDialog[P]) init(app *App, cfg newObjectConfig[P]) {
	d.app = app
	d.newObjectConfig = cfg
	d.PropertySheet = propsheet.NewPropertySheet(app.screen, cfg.title)
	d.OnLoadPage = d.onLoadPage
	d.OnApply = func() { d.runApply(false) }
	d.OnOK = func() { d.runApply(true) }
	d.OnClose = d.onClose
	d.ConfirmDiscard = d.onConfirmDiscard
	d.OnScript = d.runScript
	d.OnCancelApply = d.run.cancel
}

// show opens the dialog against sc, discarding the previous showing's state: a
// create dialog always starts empty.
func (d *newObjectDialog[P]) show(sc *db.ServerConn) {
	cancelIfSet(d.cancel)
	d.prefetchRun.Abandon()
	d.ctx, d.cancel = context.WithCancel(sc.Server.Context())
	d.sc = sc
	d.prefetch = nil
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	d.objectName = nil
	d.preflight = nil
	d.afterCreate = nil
	d.created = false
	d.fetching = false
	d.waiting = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages(d.pages)
	d.Show()
}

// dbFolderDialog is a New dialog opened from one database's Object Explorer
// folder: the object is created in that database, Script Changes opens its query
// window there, and the folder is reloaded once the create succeeds.
type dbFolderDialog[P any] struct {
	newObjectDialog[P]

	// dbName is the database the object is created in, node the folder to reload
	// afterwards. Both are set by show before the embedded show runs the prefetch
	// that reads them.
	dbName string
	node   *explorerNode
}

// initInFolder is init with cfg.refresh reloading the folder show was given.
func (d *dbFolderDialog[P]) initInFolder(app *App, cfg newObjectConfig[P]) {
	cfg.refresh = func(sc *db.ServerConn) { d.app.explorer.ReloadFolders(sc, sameNodeAs(d.node)) }
	d.init(app, cfg)
}

// show opens the dialog for node, a folder of one database.
func (d *dbFolderDialog[P]) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *newObjectDialog[P]) onClose() { cancelIfSet(d.cancel) }

func (d *newObjectDialog[P]) post(fn func()) { d.app.postAndWake(fn) }

// onLoadPage serves page from the built forms, or runs the one prefetch and
// builds every page from it. Unlike PropDialog's on-demand pages, all of a
// create dialog's pages come from one fetch, so the first requested pays for
// all.
func (d *newObjectDialog[P]) onLoadPage(page, seq int) {
	if d.prefetch != nil {
		d.SetPageForm(page, seq, d.forms[page])
		return
	}
	d.waiting = append(d.waiting, pageRequest{page: page, seq: seq})
	if d.fetching {
		return
	}
	d.fetching = true
	sc := d.sc
	fetch := d.fetch
	// Derived from d.ctx, so closing or disconnecting tears the prefetch down as
	// well as its own timeout.
	ctx, token := d.prefetchRun.BeginTimeout(d.ctx, propFetchTimeout)
	d.app.safegoRepair("loading a new-object page", func() { d.fetchPanicked(token) }, func() {
		pf, err := fetch(ctx, sc)
		d.post(func() {
			// Without this guard the stale callback consumes the *new* showing's waiting
			// list and clears fetching, and the live fetch then lands with nothing waiting
			// and never calls SetPageForm.
			if !d.prefetchRun.Done(token) {
				return
			}
			d.fetching = false
			waiting := d.waiting
			d.waiting = nil
			if err != nil {
				for _, r := range waiting {
					d.SetPageError(r.page, r.seq, displayError(err))
				}
				return
			}
			d.prefetch = pf
			d.build(pf)
			for _, r := range waiting {
				d.SetPageForm(r.page, r.seq, d.forms[r.page])
			}
		})
	})
}

// fetchPanicked releases the prefetch latch after a panic in onLoadPage's
// goroutine (its App.safegoRepair step). d.fetching makes the fetch
// single-flight, so leaving it set means no page ever loads again, and queued
// requests must be failed too or they sit blank. Guarded by the run token like
// the normal completion: a reopened dialog has its own fetch out.
func (d *newObjectDialog[P]) fetchPanicked(token int) {
	if !d.prefetchRun.Done(token) {
		return
	}
	d.fetching = false
	waiting := d.waiting
	d.waiting = nil
	for _, r := range waiting {
		d.SetPageError(r.page, r.seq, errPageLoadPanicked)
	}
}

// applyPanicked releases the applying latch after a panic in runPipeline's
// goroutine; see PropDialog.applyPanicked for what the latch disables.
func (d *newObjectDialog[P]) applyPanicked() {
	d.SetApplying(false)
	d.SetMessage("Create stopped unexpectedly — see the log for details.", true)
}

func (d *newObjectDialog[P]) onConfirmDiscard(_ int, proceed func()) {
	d.app.confirmDiscardChanges(proceed)
}

// pipelineLabel is what the button-row spinner says while runCtx's pipeline runs
// (this shell's and PropDialog's). Script Changes runs the same apply closures as
// Apply, so the context alone tells them apart.
func pipelineLabel(runCtx context.Context) string {
	if gosmo.Scripting(runCtx) {
		return "Scripting..."
	}
	return "Applying..."
}

// runPipeline validates the dialog and, if it passes, runs every page's apply in
// order on a background goroutine, stopping at the first error. runCtx is d.ctx
// for a real Apply/OK and a gosmo.WithScript context for Script Changes, the only
// difference between the paths.
func (d *newObjectDialog[P]) runPipeline(runCtx context.Context, onSuccess func()) {
	if d.prefetch == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return
	}
	// Here on the UI goroutine: the apply functions run on the pipeline's, so a
	// page's editor fields must reach its model now (propsheet.Form.SetCommit).
	// preflight builds its request after it. d.forms rather than the sheet's: every
	// page's apply runs, opened or not.
	for _, f := range d.forms {
		if f != nil {
			f.Commit()
		}
	}
	if err := d.preflight(); err != nil {
		// preflight only checks the identity fields, always on the first page.
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
	d.StartApplying(pipelineLabel(runCtx))
	d.SetMessage("", false)
	runCtx = d.run.start(withCreatedHandoff(runCtx))
	stop := d.run.stop

	done := make(chan struct{})
	d.app.animateUntil("animating the create dialog spinner", propsheet.ApplyingSpinner.Period, done)
	d.app.safegoRepair("creating the object", d.applyPanicked, func() {
		defer close(done)
		defer stop()
		progress, runErr := runApplySteps(runCtx, fns)
		d.post(func() {
			d.SetApplying(false)
			if runErr != nil {
				d.createFailed(runCtx, runErr, progress)
				return
			}
			onSuccess()
		})
	})
}

// createFailed reports a run that stopped at runErr. A create is usually several
// statements (the CREATE, then options, members or schedules), so a failure or
// cancel can land after the CREATE committed and leave the object half-configured.
//
// Once the first page ran to the end the object exists: the dialog counts as
// created (a second Apply doesn't re-send the CREATE into "already exists"), the
// folder is refreshed, and the message names the page that did not land. A first
// page that failed after some statements ran leaves it unknown whether the
// object exists, so the dialog stays uncreated and the message points at Object
// Explorer.
func (d *newObjectDialog[P]) createFailed(runCtx context.Context, runErr error, progress applyProgress) {
	if gosmo.Scripting(runCtx) {
		if d.run.cancelled {
			d.SetMessage("Script Changes cancelled.", false)
			return
		}
		d.SetMessage(withPermissionAdvice(runErr).Error(), true)
		return
	}
	created := progress.completed > 0
	if created {
		d.created = true
	}
	if d.run.cancelled || progress.anyCommitted() {
		d.refresh(d.sc)
	}
	what := fmt.Sprintf("%s %q was %s", d.noun, d.objectName(), orDefault(d.verb, "created"))
	switch {
	case d.run.cancelled && created:
		d.SetMessage("Create cancelled after "+what+" — open its Properties to finish.", false)
	case d.run.cancelled:
		d.SetMessage("Create cancelled. Part of it may already have run — check Object Explorer before trying again.", false)
	case created:
		// The note leads: the message line hard-clips, and SQL Server's own reason is
		// long enough to push anything appended off the end.
		d.SetMessage(fmt.Sprintf("%s, but %s failed — open its Properties to finish: %v",
			what, d.stepName(progress.stopped), withPermissionAdvice(runErr)), true)
	case progress.wrote:
		d.SetMessage("Part of it ran before it failed — check Object Explorer before trying again: "+
			withPermissionAdvice(runErr).Error(), true)
	default:
		d.SetMessage(withPermissionAdvice(runErr).Error(), true)
	}
}

// createdKey carries a run's createdHandoff; see withCreatedHandoff.
type createdKey struct{}

// createdHandoff holds what a New-object dialog's create step returned, for a
// later step of the same run (New Schedule's Jobs page attaches the schedule
// General created, by id, because schedule names are not unique).
type createdHandoff struct{ v any }

// withCreatedHandoff returns ctx carrying an empty createdHandoff. Owned by the
// run (runPipeline makes a fresh one per run), not the dialog, so the pipeline
// goroutine writes nothing the UI goroutine reads (docs/ui-rules.md).
func withCreatedHandoff(ctx context.Context) context.Context {
	return context.WithValue(ctx, createdKey{}, &createdHandoff{})
}

// handOffCreated records v as what this run's create step made. Without a
// handoff in ctx it does nothing.
func handOffCreated(ctx context.Context, v any) {
	if h, ok := ctx.Value(createdKey{}).(*createdHandoff); ok {
		h.v = v
	}
}

// createdFrom is what this run's create step handed off, if it was a T.
func createdFrom[T any](ctx context.Context) (T, bool) {
	h, _ := ctx.Value(createdKey{}).(*createdHandoff)
	if h == nil {
		var zero T
		return zero, false
	}
	v, ok := h.v.(T)
	return v, ok
}

// stepName names applyFns[i]'s page for a message.
func (d *newObjectDialog[P]) stepName(i int) string {
	if i >= 0 && i < len(d.pages) {
		return d.pages[i]
	}
	return "a later page"
}

func (d *newObjectDialog[P]) runApply(hideOnSuccess bool) {
	if d.created {
		// OK after a successful Apply just closes; a second Apply says why nothing
		// happened. Editing what was created is Properties' job.
		if hideOnSuccess {
			d.Dismiss()
			return
		}
		d.SetMessage(fmt.Sprintf("%s %q has already been %s — close this dialog and use its Properties to change it further.",
			d.noun, d.objectName(), orDefault(d.verb, "created")), true)
		return
	}
	d.runPipeline(d.ctx, func() {
		d.created = true
		d.app.setStatus(fmt.Sprintf("%s %q %s", d.noun, d.objectName(), orDefault(d.verb, "created")))
		d.refresh(d.sc)
		if hideOnSuccess {
			d.Dismiss()
		}
		if d.afterCreate != nil {
			d.afterCreate()
		}
	})
}

// scriptSafeJob / scriptSafeAlert resolve the object a *previous* page of the
// same create dialog produced.
//
// Under Script Changes that object does not exist: the earlier page's apply only
// collected its EXEC, so a JobByName/AlertByName lookup (a real read, which
// WithScript does not intercept) returns "not found" and fails the script.
// gosmo's name-only handle is what the dependent statement needs; every write
// from here builds its statement from the name alone. The real Apply path still
// reads, so a name typo is still caught there.
func scriptSafeJob(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Job, error) {
	if gosmo.Scripting(ctx) {
		return sc.Server.JobRef(name), nil
	}
	return sc.Server.JobByName(ctx, name)
}

func scriptSafeAlert(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Alert, error) {
	if gosmo.Scripting(ctx) {
		return sc.Server.AlertRef(name), nil
	}
	return sc.Server.AlertByName(ctx, name)
}

// runScript re-runs every page's apply under gosmo.WithScript, opening the
// collected statements in a query window instead of executing them (the
// create-dialog half of PropDialog.runScript).
//
// The empty check is PropDialog's: a page set can validate, report work to do,
// and still collect nothing (every write conditional), and an empty query window
// says less than a message does.
func (d *newObjectDialog[P]) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		if script.Len() == 0 {
			d.SetMessage("No changes to script.", false)
			return
		}
		d.app.openQueryWithText(sc, d.scriptDatabase, script.String())
	})
}
