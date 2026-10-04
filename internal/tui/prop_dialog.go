package tui

import (
	"context"
	"strings"
	"time"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// propFetchTimeout bounds a single property page's load: long enough for a slow
// server, short enough that a dead connection doesn't leave a page on
// "Loading..." forever. Mirrors childFetchTimeout.
const propFetchTimeout = 30 * time.Second

// propApply runs one page's pending edits against the server. Returned by a
// propPage's load func, closing over the row pointers it created, so the dialog
// needs no knowledge of each page's row shapes.
type propApply func(ctx context.Context) error

// propPage is one page of a PropDialog: a title for the page list and a loader
// that fetches the page's data and builds its form. load runs on a background
// goroutine; see PropDialog.onLoadPage.
type propPage struct {
	title string
	load  func(ctx context.Context) (*propsheet.Form, propApply, error)

	// renames marks the page whose apply can rename the object every other page is
	// addressed by (always General, in dialogs that allow a rename). Its apply is
	// sorted last (see dirtyApplyFns), so the rename is the run's final write and
	// earlier pages use the name the server still has.
	//
	// This matters most for Script Changes, where the rename is recorded rather
	// than executed: sibling statements would otherwise sit *below* the rename and
	// name an object that no longer exists.
	//
	// The pointer is the boxed name commitRename updates; the header re-reads it
	// after a save (PropDialog.headerName).
	renames *string

	// requires are the rights this page's *writes* need; any one is enough. When
	// the server has denied every one, the page still loads and reads but comes up
	// read-only with a note naming them. Leave nil for a read-only page, or one
	// whose write right the probe cannot see.
	//
	// Evaluated on the load goroutine, so a database-scope right may cost a probe;
	// the UI goroutine's gates use the cache (gate.Allows).
	requires []gate.Right

	// requiresIn is the database a database-scope entry in requires is read
	// against. Empty for a server-scoped page.
	requiresIn string

	// requiresSchema and requiresObject name the securable an object- or
	// schema-scoped entry in requires is asked about: the object's schema and the
	// object itself. Both empty when all rights are database- or server-wide.
	//
	// Without them gate.ObjectWriteRights() cannot answer for the principal it
	// exists for: ALTER granted directly on one table is reflected at no wider
	// scope, so every other right denies and the page that principal *can* write
	// comes up read-only. See withRequiresOn.
	requiresSchema string
	requiresObject string
}

// PropDialog is the app-layer orchestrator for propsheet.PropertySheet: it owns
// the goroutines that load pages and apply edits, translating between the
// framework's page-index/seq contract and SQL Server calls. One instance is
// reused for every invocation; show() re-seeds it with a fresh page set and
// context so edits and in-flight loads never leak between openings.
type PropDialog struct {
	*propsheet.PropertySheet

	app     *App
	pages   []propPage
	applyFn map[int]propApply

	sc       *db.ServerConn
	database string

	// detailNode is the node the Details pane was showing when the dialog opened:
	// the object it edits, or the folder listing it when Properties came from a
	// Details row. A saved change stales that node's cached view, so staleDetails
	// drops it after a write lands. Nil when there is no Details pane.
	detailNode *explorerNode

	ctx    context.Context
	cancel context.CancelFunc

	// pageRuns is the cancel half of the latest-only contract for page loads, keyed
	// by page index (ARCHITECTURE.md § Latest-only loads). The sheet owns the seq
	// that discards a superseded result, so this side owns only the cancel that
	// stops the fetch; a full latest per page would shadow the sheet's seq.
	//
	// Whoever starts a load for a page cancels that page's previous run first, so a
	// supersede (InvalidateAll after an Apply, a re-show) releases the pooled
	// connection immediately, rather than at propFetchTimeout with the unwanted
	// read queued ahead of the wanted one. UI goroutine only.
	pageRuns map[int]*pageRun

	// run is the OK/Apply/Script pipeline in flight, if any.
	run applyRun

	// onSaved, when set, runs on the UI goroutine after a write has reached the
	// server (a full Apply, or a failed one some page of which landed), for a
	// dialog whose writes change what the tree shows under its object (Session
	// Properties' targets) or its label (showReloading). Never after Script
	// Changes. Reset by every show, so one dialog's hook never runs for the next.
	onSaved func()

	// planned, when set, makes this showing's Apply a planned run (see applyPlan).
	// Set by showPlanned, cleared by every show.
	planned func(ctx context.Context, plan *applyPlan) error

	// headerName is the renaming page's boxed name (propPage.renames);
	// headerPrefix/headerRight are the rest of the header. saved() redraws the
	// header from them so a rename stops showing the old name. nil when no page
	// renames.
	headerName   *string
	headerPrefix string
	headerRight  string
}

// NewPropDialog creates the properties dialog and wires its callbacks.
func NewPropDialog(app *App) *PropDialog {
	d := &PropDialog{
		app:           app,
		PropertySheet: propsheet.NewPropertySheet(app.screen, "Properties"),
	}
	d.OnLoadPage = d.onLoadPage
	d.OnApply = func() { d.runApply(false) }
	d.OnOK = func() { d.runApply(true) }
	d.OnClose = d.onClose
	d.ConfirmDiscard = d.onConfirmDiscard
	d.OnScript = d.runScript
	d.OnCancelApply = d.run.cancel
	return d
}

// show opens the dialog with a fresh page set. sc and database are what a
// Script Changes action opens its query window against; database is "" for
// server- and login-scoped dialogs, whose database-scoped statements carry
// their own USE. title is the window title; headerLeft/headerRight the two ends
// of the header line.
//
// The connection guard lives here, not in each caller: one that forgets it
// shows a dialog whose every page fails to load, one error per page, instead of
// the status line saying the obvious thing.
//
// pages is a builder so it stays behind the guard; evaluated at the call site
// it would run against a closed connection.
func (d *PropDialog) show(sc *db.ServerConn, database, title, headerLeft, headerRight string, pages func() []propPage) {
	d.showWith(sc, database, title, headerLeft, headerRight, pages, nil)
}

// showReloading is show for a dialog whose Apply can change how the tree lists
// its object: a rename, or a state its label or icon carries ("(Disabled)", a
// job's greyed icon). After a write the folders matching folder are reloaded
// (ObjectExplorer.ReloadFolders); matching the folder rather than the object
// survives a rename, and a second one in the same showing.
func (d *PropDialog) showReloading(sc *db.ServerConn, database, title, headerLeft, headerRight string, pages func() []propPage,
	folder func(nodeData) bool) {
	if d.showWith(sc, database, title, headerLeft, headerRight, pages, nil) {
		d.onSaved = func() { d.app.explorer.ReloadFolders(sc, folder) }
	}
}

// showPlanned is show for a dialog whose pages plan their writes and run
// decides how the plan is carried out (see applyPlan). Reports whether the
// dialog opened, so a caller can go on to select a page.
func (d *PropDialog) showPlanned(sc *db.ServerConn, database, title, headerLeft, headerRight string, pages func() []propPage,
	run func(ctx context.Context, plan *applyPlan) error) bool {
	return d.showWith(sc, database, title, headerLeft, headerRight, pages, run)
}

func (d *PropDialog) showWith(sc *db.ServerConn, database, title, headerLeft, headerRight string, pages func() []propPage,
	planned func(ctx context.Context, plan *applyPlan) error) bool {
	if !d.app.requireConn(sc) {
		return false
	}
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(sc.Server.Context())
	d.stopPageRuns()
	d.sc = sc
	d.database = database
	d.onSaved = nil
	d.planned = planned
	d.detailNode = nil
	if d.app.detailBrowser != nil {
		d.detailNode = d.app.detailBrowser.currentNode
	}
	d.pages = pages()
	d.applyFn = make(map[int]propApply, len(d.pages))

	titles := make([]string, len(d.pages))
	for i, p := range d.pages {
		titles[i] = p.title
	}
	d.SetTitle(title)
	d.SetHeader(headerLeft, headerRight)
	d.trackHeaderName(headerLeft, headerRight)
	d.SetPages(titles)
	d.Show()
	return true
}

// trackHeaderName finds the page that can rename the object and, when
// headerLeft ends with the name it boxes ("Login: " + name), remembers the
// prefix so saved() can redraw the header under the new name.
func (d *PropDialog) trackHeaderName(headerLeft, headerRight string) {
	d.headerName, d.headerPrefix, d.headerRight = nil, "", headerRight
	for _, p := range d.pages {
		if p.renames == nil {
			continue
		}
		if prefix, ok := strings.CutSuffix(headerLeft, *p.renames); ok {
			d.headerName, d.headerPrefix = p.renames, prefix
		}
		return
	}
}

func (d *PropDialog) onClose() {
	cancelIfSet(d.cancel)
	d.stopPageRuns()
}

// pageRun is one in-flight page load, held so a later load of the same page can
// stop it. A struct rather than a bare CancelFunc because the completed run
// must recognise itself in the map; funcs do not compare.
type pageRun struct{ cancel context.CancelFunc }

// startPageRun supersedes page's load in flight, if any, and returns the
// context the replacement runs under.
func (d *PropDialog) startPageRun(page int, parent context.Context) (context.Context, *pageRun) {
	d.stopPageRun(page)
	ctx, cancel := context.WithTimeout(parent, propFetchTimeout)
	run := &pageRun{cancel: cancel}
	if d.pageRuns == nil {
		d.pageRuns = make(map[int]*pageRun, len(d.pages))
	}
	d.pageRuns[page] = run
	return ctx, run
}

// stopPageRun cancels page's load in flight, if any, and forgets it.
func (d *PropDialog) stopPageRun(page int) {
	if run := d.pageRuns[page]; run != nil {
		run.cancel()
		delete(d.pageRuns, page)
	}
}

// endPageRun releases run's context once its result is in hand, unless a newer
// load for the same page has replaced it (that one owns the entry).
func (d *PropDialog) endPageRun(page int, run *pageRun) {
	run.cancel()
	if d.pageRuns[page] == run {
		delete(d.pageRuns, page)
	}
}

// stopPageRuns cancels every load in flight: the dialog is closing or being
// re-seeded. Cancelling d.ctx stops them too, but the map would keep a dead
// entry per page into the next showing.
func (d *PropDialog) stopPageRuns() {
	for page := range d.pageRuns {
		d.stopPageRun(page)
	}
}

// cancelIfSet calls cancel if non-nil: the shared body of every property and
// creation dialog's OnClose, cancelling whatever fetch or apply is in flight.
func cancelIfSet(cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
}

func (d *PropDialog) post(fn func()) { d.app.postAndWake(fn) }

// onLoadPage runs page's loader on a background goroutine and reports the
// result on the UI goroutine, guarded by seq like every other background fetch
// (see app_explorer_data.go's loadChildren).
//
// safegoRepair, not safego: PropertySheet.startLoad latched the slot at
// PageLoading before calling this, and only the callback below clears it. A
// panic unwinds past that callback, and SelectPage refuses to retry a page not
// in PageNotLoaded, so the page would read "Loading..." for the rest of the
// showing, with F5 the only way out.
func (d *PropDialog) onLoadPage(page, seq int) {
	if page < 0 || page >= len(d.pages) {
		return
	}
	p := d.pages[page]
	sc := d.sc
	// On the UI goroutine, before the load starts: the superseded run must stop
	// now, and d.ctx must be read here, not inside the closure (show() rewrites it
	// for the next showing).
	ctx, run := d.startPageRun(page, d.ctx)

	d.app.safegoRepair("loading a properties page", func() {
		d.endPageRun(page, run)
		d.SetPageError(page, seq, errPageLoadPanicked)
	}, func() {
		// Before the load, not after: the probe decides whether the built form is
		// editable, and SetPageReadOnly must reach the slot ahead of SetPageForm.
		readOnly := pageReadOnlyReason(ctx, sc, p)
		form, apply, err := p.load(ctx)
		d.post(func() {
			d.endPageRun(page, run)
			if err != nil {
				d.SetPageError(page, seq, displayError(err))
				return
			}
			// applyFn only once the sheet has taken the form: a load left in flight by an
			// earlier showing, or superseded by a Refresh, would otherwise install its
			// apply (for another object, or over the newer form's rows) in this showing's
			// map.
			d.SetPageReadOnly(page, seq, readOnly)
			if d.SetPageForm(page, seq, form) {
				d.applyFn[page] = apply
			}
		})
	})
}

// runPageAction runs fn on a background goroutine and reports its result on the
// UI goroutine via onDone: the shared pattern for a page's own buttons (Check
// Syntax, Estimate Rows, Rebuild, ...), which act immediately and independently
// of OK/Cancel/Apply. Not tied to any page's dirty state; callers needing a
// value out of fn capture it in an outer variable.
func (d *PropDialog) runPageAction(fn func(ctx context.Context) error, onDone func(err error)) {
	d.app.safego("a properties page action", d.pageActionBody(fn, onDone))
}

// pageActionBody is the goroutine body runPageAction and runPageActionOnce
// hand to safego: one round trip bounded by propFetchTimeout, reported on the
// UI goroutine. They differ only in what happens on a panic.
//
// d.ctx is read here, on the UI goroutine, not inside the closure: show()
// rewrites it for the next showing, so reading it on the goroutine is a data
// race, and an action outliving its showing would run under the new context
// instead of being cancelled with its own.
func (d *PropDialog) pageActionBody(fn func(ctx context.Context) error, onDone func(err error)) func() {
	sessionCtx := d.ctx
	return func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		err := fn(ctx)
		d.post(func() { onDone(err) })
	}
}

// runPageActionOnce is runPageAction with an in-flight latch, for a button that
// writes its result into captured variables. Without it a second click while
// the first round trip is out puts two goroutines in flight writing the same
// variable and filling the same grid, so whichever finishes last wins,
// possibly the older request.
//
// inFlight is a plain bool because both halves run on the UI goroutine: the
// click handler, and onDone via d.post.
//
// safegoRepair, not safego, because of the latch: onDone clears it, and a panic
// unwinds past onDone, leaving the button refusing every later click for the
// dialog's lifetime.
func (d *PropDialog) runPageActionOnce(inFlight *bool, fn func(ctx context.Context) error, onDone func(err error)) {
	if *inFlight {
		return
	}
	*inFlight = true
	d.app.safegoRepair("a properties page action",
		func() { *inFlight = false },
		d.pageActionBody(fn, func(err error) {
			*inFlight = false
			onDone(err)
		}))
}

// asyncStatusButton returns a button that runs fn via runPageAction, showing
// busyText in statusRow while in flight and fn's returned text or "Error: ..."
// on completion.
func (d *PropDialog) asyncStatusButton(label string, statusRow *propsheet.StaticRow, busyText string, fn func(ctx context.Context) (string, error)) *widgets.Button {
	var inFlight bool
	return widgets.NewButton(label, func() {
		if inFlight {
			return // statusRow already reads busyText, so this isn't a silent no-op.
		}
		statusRow.SetValue(busyText)
		var result string
		d.runPageActionOnce(&inFlight, func(ctx context.Context) error {
			r, err := fn(ctx)
			result = r
			return err
		}, func(err error) {
			if err != nil {
				statusRow.SetValue("Error: " + err.Error())
				return
			}
			statusRow.SetValue(result)
		})
	})
}

// onConfirmDiscard prompts before Refresh discards a dirty page's edits, via the
// app's shared confirm dialog, shown nested on top of this one.
func (d *PropDialog) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDiscardChanges(proceed)
}

// readOnlyBannerPrefix opens the banner a page whose writes are refused shows.
const readOnlyBannerPrefix = "Read-only: this login cannot change these settings. "

// pageReadOnlyReason is the sentence a page whose writes are refused carries,
// or "" when it may be written (or nothing was measured).
//
// Runs on the load goroutine, so unlike the menu gates it may probe a database
// it has not seen. Same Allows rule as elsewhere: read-only only when the
// server denied every right that would permit the write.
func pageReadOnlyReason(ctx context.Context, sc *db.ServerConn, p propPage) string {
	if len(p.requires) == 0 || sc == nil {
		return ""
	}
	// The probing form of DatabaseCapabilities, which the menu gates may not use;
	// see gate.RightsAllow, the single copy of the rule both follow.
	dbCaps := func(name string) *gosmo.DatabaseCapabilities {
		return sc.DatabaseCapabilities(ctx, name)
	}
	if gate.RightsAllow(sc.Capabilities(), dbCaps, p.requiresIn, p.requiresSchema, p.requiresObject, p.requires...) {
		return ""
	}
	// A DENY on the object is a different sentence: the login may hold every right
	// the page lists, and telling it to ask for one describes neither what is wrong
	// nor what would fix it.
	if r, at, denied := gate.ObjectDenial(sc.Capabilities(), dbCaps, p.requiresIn, p.requiresSchema, p.requiresObject, p.requires...); denied {
		return readOnlyBannerPrefix + gate.DeniedText(r, at)
	}
	return readOnlyBannerPrefix + gate.RequiresText(p.requires...)
}
