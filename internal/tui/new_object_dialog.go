package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_object_dialog.go is the shell every "New <object>" creation dialog is
// built on — New Database, New Login, and the four SQL Server Agent ones
// (Job, Schedule, Alert, Operator). All six are the same dialog with
// different contents: one prefetch, one propsheet.PropertySheet whose pages
// are all built from that prefetch at once, and an OK/Apply/Script pipeline
// that runs each page's apply function in order. Only what they create
// differs, so only that lives in the per-dialog files now — each is a
// newObjectConfig plus a buildPages method.
//
// The PropDialog (properties-of-an-existing-object) shell in prop_dialog.go
// is deliberately separate: it loads its pages lazily one at a time and
// applies a dirty-diff, neither of which a create dialog has any use for.

// errPageLoadPanicked is what a queued page request is failed with when the
// prefetch goroutine panicked — the panic itself is already logged and on
// the status bar, so the page only has to say why it has no content.
var errPageLoadPanicked = errors.New("loading stopped unexpectedly — see the log for details")

// newObjectConfig is everything that differs between one create dialog and
// the next. Passed to newObjectDialog.init at construction.
type newObjectConfig[P any] struct {
	// title is the dialog's window title, e.g. "New Job".
	title string
	// noun names the created object in the success status message, e.g.
	// "Job" → `Job "nightly reindex" created`.
	noun string
	// verb completes that message when "created" would be untrue. Add
	// Database to an availability group creates nothing — it adds an existing
	// database to a group — so it sets "added to the availability group".
	// Empty means "created".
	verb string
	// pages are the page names, in order; forms and applyFns are indexed by
	// the same positions.
	pages []string
	// scriptDatabase is the database Script Changes opens its generated
	// query window in — "msdb" for the Agent objects, whose stored
	// procedures live there, "" (the connection's own default) otherwise.
	scriptDatabase string
	// fetch loads the dialog's prefetch payload — everything its pages need
	// from the server, in one round trip's worth of calls.
	fetch func(context.Context, *db.ServerConn) (*P, error)
	// build populates forms, applyFns, objectName, and preflight from a
	// completed prefetch. All four close over the same widgets, so one
	// function produces them together.
	build func(*P)
	// refresh brings the Object Explorer folder the new object belongs to
	// back in sync, once creation has succeeded.
	refresh func(*db.ServerConn)
}

// pageRequest is one outstanding OnLoadPage call, held until the prefetch
// it's waiting on completes. seq is passed back to SetPageForm unchanged,
// which drops it if the page has been reloaded since.
type pageRequest struct{ page, seq int }

// newObjectDialog is the shared state and behavior behind every create
// dialog. P is the dialog's own prefetch payload type. Concrete dialogs
// embed it by value and add only a buildPages method.
type newObjectDialog[P any] struct {
	*propsheet.PropertySheet
	newObjectConfig[P]

	app *App
	sc  *db.ServerConn

	// ctx spans one show..close of the dialog, derived from the
	// connection's own Context so a disconnect tears down whatever this
	// dialog still has in flight; cancel ends it (see onClose).
	ctx    context.Context
	cancel context.CancelFunc

	prefetch *P
	forms    []*propsheet.Form
	applyFns []propApply

	// fetching guards against a second prefetch: the sheet asks for a page
	// whenever the user selects one, and switching pages while the first
	// fetch is still out would otherwise start another and rebuild every
	// form from it. waiting collects the pages asked for meanwhile; they're
	// all served from the one prefetch when it lands.
	fetching bool
	waiting  []pageRequest

	// objectName returns the name typed into the dialog, for the success
	// message; preflight rejects it before anything is sent. Both are
	// assigned by build, alongside forms and applyFns.
	objectName func() string
	preflight  func() error

	// created is set once the pipeline has run through successfully. Apply
	// leaves the dialog open, so without this a second Apply (or an Apply
	// followed by OK) would re-issue the same CREATE and come back with the
	// server's "already exists". Reset by show.
	created bool
}

// init wires cfg into d and hooks up the PropertySheet callbacks. Call it
// from the concrete dialog's constructor, after the concrete value exists —
// cfg.build is normally that value's own buildPages method.
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
}

// show opens the dialog against sc, discarding everything the previous
// showing left behind — a create dialog always starts empty.
func (d *newObjectDialog[P]) show(sc *db.ServerConn) {
	cancelIfSet(d.cancel)
	d.ctx, d.cancel = context.WithCancel(sc.Context())
	d.sc = sc
	d.prefetch = nil
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	d.objectName = nil
	d.preflight = nil
	d.created = false
	d.fetching = false
	d.waiting = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages(d.pages)
	d.Show()
}

func (d *newObjectDialog[P]) onClose() { cancelIfSet(d.cancel) }

func (d *newObjectDialog[P]) post(fn func()) { d.app.postAndWake(fn) }

// onLoadPage serves page from the already-built forms, or runs the one
// prefetch and builds every page from it. Unlike PropDialog, which loads
// each page separately on demand, a create dialog's pages all come from the
// same fetch — so the first page requested pays for all of them, and the
// rest are free.
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
	sessionCtx := d.ctx
	fetch := d.fetch
	d.app.safegoRepair("loading a new-object page", func() { d.fetchPanicked(sessionCtx) }, func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		pf, err := fetch(ctx, sc)
		d.post(func() {
			// A prefetch outlives the showing that started it if the
			// dialog is closed and reopened before it lands (show's
			// cancel makes err non-nil, but the goroutine still gets
			// here). The per-page seq can't catch that: SetPages rebuilds
			// every slot, so the new showing's first load carries the
			// same seq the old one did. Without this guard the stale
			// callback consumes the *new* showing's waiting list and
			// fails its pages, and the live fetch then lands with nothing
			// waiting and never calls SetPageForm — leaving the page
			// stuck on the previous showing's cancellation error.
			if d.ctx != sessionCtx {
				return
			}
			d.fetching = false
			waiting := d.waiting
			d.waiting = nil
			if err != nil {
				for _, r := range waiting {
					d.SetPageError(r.page, r.seq, err)
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
// goroutine — its App.safegoRepair step. d.fetching is what makes the fetch
// single-flight, so leaving it set means no page of this dialog ever loads
// again; the queued requests have to be failed too, or they sit blank
// forever. Guarded by sessionCtx for the same reason the normal completion
// path is: a reopened dialog has a fetch of its own out.
func (d *newObjectDialog[P]) fetchPanicked(sessionCtx context.Context) {
	if d.ctx != sessionCtx {
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
// goroutine — see PropDialog.applyPanicked for what the latch disables.
func (d *newObjectDialog[P]) applyPanicked() {
	d.SetApplying(false)
	d.SetMessage("Create stopped unexpectedly — see the log for details.", true)
}

func (d *newObjectDialog[P]) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDiscardChanges(proceed)
}

// runPipeline validates the dialog and, if it passes, runs every page's
// apply function in order on a background goroutine, stopping at the first
// error. runCtx is d.ctx for a real Apply/OK and a gosmo.WithScript context
// for Script Changes — the only difference between the two paths.
func (d *newObjectDialog[P]) runPipeline(runCtx context.Context, onSuccess func()) {
	if d.prefetch == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return
	}
	if err := d.preflight(); err != nil {
		// preflight only ever checks the identity fields, which are always
		// on the first page.
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

	d.app.safegoRepair("creating the object", d.applyPanicked, func() {
		var runErr error
		for _, fn := range fns {
			if fn == nil {
				continue
			}
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
	})
}

func (d *newObjectDialog[P]) runApply(hideOnSuccess bool) {
	if d.created {
		// OK after a successful Apply just closes; a second Apply says why
		// nothing happened. Editing what was created is Properties' job.
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
	})
}

// scriptSafeJob / scriptSafeAlert resolve the object a *previous* page of the
// same create dialog produced.
//
// Under Script Changes that object does not exist: the earlier page's apply
// only collected its EXEC, so a JobByName/AlertByName lookup — a real read,
// which WithScript does not intercept — comes back "not found" and the whole
// script fails instead of being produced. gosmo's name-only handle is what
// the dependent statement actually needs; every write reached from here
// (AddStep, AttachSchedule, SetEmailNotify, Notify) builds its statement from
// the name alone. The real Apply path still reads, so a name typo is still
// caught by the server there.
func scriptSafeJob(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Job, error) {
	if gosmo.Scripting(ctx) {
		return sc.Server.Job(name), nil
	}
	return sc.Server.JobByNameContext(ctx, name)
}

func scriptSafeAlert(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Alert, error) {
	if gosmo.Scripting(ctx) {
		return sc.Server.Alert(name), nil
	}
	return sc.Server.AlertByNameContext(ctx, name)
}

func (d *newObjectDialog[P]) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, d.scriptDatabase, strings.Join(script.Statements, "\n\n"))
	})
}
