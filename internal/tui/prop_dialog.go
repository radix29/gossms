package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// propFetchTimeout bounds a single property page's load — long enough for a slow
// server, short enough that a dead connection doesn't leave a page on
// "Loading..." forever. Mirrors childFetchTimeout.
const propFetchTimeout = 30 * time.Second

// propApply runs one page's pending edits against the server. Returned by a
// propPage's load func, closing over the row pointers that func created, so the
// dialog needs no knowledge of each page's row shapes.
type propApply func(ctx context.Context) error

// propPage is one page of a PropDialog: a title for the page list, and a
// loader that fetches the page's data and builds its form. load runs on a
// background goroutine — see PropDialog.onLoadPage.
type propPage struct {
	title string
	load  func(ctx context.Context) (*propsheet.Form, propApply, error)

	// renames marks the page whose apply can rename the object every other page
	// is addressed by — always the General page, in the dialogs that allow a
	// rename. Its apply is sorted last (see dirtyApplyFns), so the rename is the
	// run's final write and every page before it uses the name the server still
	// has.
	//
	// This matters most for Script Changes, where the rename is recorded rather
	// than executed: the sibling pages' statements would otherwise sit *below* a
	// rename in the script and name an object that no longer exists.
	renames bool

	// requires are the rights this page's *writes* need — any one of them is
	// enough. When the server has denied every one, the page still loads and
	// still reads, but comes up read-only with a note naming them. Leave nil
	// for a page that only reads, or whose write right the probe cannot see.
	//
	// Evaluated on the load goroutine, so a database-scope right here may cost
	// a probe; the UI goroutine's gates use the cache instead (allowsAction).
	requires []requiredRight

	// requiresIn is the database whose capabilities a database-scope entry in
	// requires is read against. Empty for a server-scoped page.
	requiresIn string

	// requiresSchema and requiresObject name the securable an object- or
	// schema-scoped entry in requires is asked about — the schema the object
	// lives in, and the object itself. Both empty for a page whose rights are
	// all database- or server-wide.
	//
	// Without them objectWriteRights() cannot answer for the principal it
	// exists for: a grant of ALTER made directly on one table is reflected at
	// no wider scope, so every other right in the set denies and the page that
	// principal *can* write comes up read-only. See withRequiresOn.
	requiresSchema string
	requiresObject string
}

// commitRename mirrors a just-completed rename into namePtr — the boxed name
// every other page resolves its object by — but only when the rename really ran.
//
// Under gosmo.WithScript a write is recorded and returns success without the
// server seeing it, while reads still go to the real server. Mirroring the new
// name there points every sibling page's lookup at an object that doesn't exist,
// so each fails with "not found", the run aborts on the first, and no script is
// produced. namePtr also stays wrong for the life of the dialog, so a following
// real Apply fails the same way.
//
// Every dialog that can rename calls this from its General page, which is also
// marked propPage.renames so its apply runs last.
func commitRename(ctx context.Context, namePtr *string, newName string) {
	if !gosmo.Scripting(ctx) {
		*namePtr = newName
	}
}

// committedApplyError marks an apply failure that changed the server anyway, so
// the sheet reloads before the message is shown. A failed apply otherwise keeps
// every page exactly as it was — right when the write never landed, and a lie
// when it partly did. Audit Properties is the case: its ALTER runs inside a
// disable window, and a window that cannot re-enable the audit reports the
// restore's error with the ALTER committed and auditing stopped, leaving the
// page's State row claiming Enabled for an audit the server has switched off.
//
// Reloading discards the page's edits, which is why only a *committed* failure
// is marked: the edits are already on the server, and what the user needs to
// see next is what the server now has.
type committedApplyError struct{ err error }

func (e committedApplyError) Error() string { return e.err.Error() }
func (e committedApplyError) Unwrap() error { return e.err }

// applyCommitted marks err as a failure that still changed the server — see
// committedApplyError.
func applyCommitted(err error) error { return committedApplyError{err} }

// PropDialog is the app-layer orchestrator for propsheet.PropertySheet: it owns
// the goroutines that load pages and apply edits, translating between the
// framework's page-index/seq contract and SQL Server calls. One instance is
// reused for every Properties invocation — show() re-seeds it with a fresh page
// set and context, so edits and in-flight loads never leak between openings.
type PropDialog struct {
	*propsheet.PropertySheet

	app     *App
	pages   []propPage
	applyFn map[int]propApply

	sc       *db.ServerConn
	database string

	ctx    context.Context
	cancel context.CancelFunc
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
	return d
}

// show opens the dialog with a fresh page set. sc and database are what a Script
// Changes action opens its query window against; database is "" for server- and
// login-scoped dialogs, whose database-scoped statements carry their own USE.
// title is the window title, and headerLeft/headerRight the two ends of the
// header line.
//
// The connection guard lives here rather than in each of the twenty-three entry
// points that call this: one that forgets it shows a dialog whose every page
// then fails to load, one error per page, instead of the status line saying the
// obvious thing.
//
// pages is a builder rather than a page set so that it stays behind the guard;
// evaluated at the call site it would run against a closed connection.
func (d *PropDialog) show(sc *db.ServerConn, database, title, headerLeft, headerRight string, pages func() []propPage) {
	if !d.app.requireConn(sc) {
		return
	}
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(sc.Context())
	d.sc = sc
	d.database = database
	d.pages = pages()
	d.applyFn = make(map[int]propApply, len(d.pages))

	titles := make([]string, len(d.pages))
	for i, p := range d.pages {
		titles[i] = p.title
	}
	d.SetTitle(title)
	d.SetHeader(headerLeft, headerRight)
	d.SetPages(titles)
	d.Show()
}

func (d *PropDialog) onClose() { cancelIfSet(d.cancel) }

// cancelIfSet calls cancel if non-nil — the shared body behind every property
// and creation dialog's OnClose, cancelling whatever fetch or apply is still in
// flight for the closing session.
func cancelIfSet(cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
}

func (d *PropDialog) post(fn func()) { d.app.postAndWake(fn) }

// onLoadPage runs page's loader on a background goroutine and reports the result
// back on the UI goroutine, guarded by seq like every other background fetch
// here (see app_explorer_data.go's loadChildren).
//
// safegoRepair, not safego: PropertySheet.startLoad latched the slot at
// PageLoading before calling this, and only the callback below clears it. A
// panic unwinds past that callback, and SelectPage then refuses to retry a page
// whose state is not PageNotLoaded — so the page reads "Loading..." for the rest
// of the showing, with F5 the only way out.
func (d *PropDialog) onLoadPage(page, seq int) {
	if page < 0 || page >= len(d.pages) {
		return
	}
	p := d.pages[page]
	sessionCtx := d.ctx
	sc := d.sc

	d.app.safegoRepair("loading a properties page", func() {
		d.SetPageError(page, seq, errPageLoadPanicked)
	}, func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		// Before the load, not after: the probe is what decides whether the
		// form the load builds is editable, and SetPageReadOnly has to reach
		// the slot ahead of SetPageForm.
		readOnly := pageReadOnlyReason(ctx, sc, p)
		form, apply, err := p.load(ctx)
		d.post(func() {
			if err != nil {
				d.SetPageError(page, seq, displayError(err))
				return
			}
			d.applyFn[page] = apply
			d.SetPageReadOnly(page, seq, readOnly)
			d.SetPageForm(page, seq, form)
		})
	})
}

// runPageAction runs fn on a background goroutine and reports its result back on
// the UI goroutine via onDone — the shared pattern for a Properties page's own
// buttons (Check Syntax, Estimate Rows, Rebuild, …), which act immediately and
// independently of OK/Cancel/Apply. Not tied to any page's dirty state; callers
// needing a value out of fn capture it in an outer variable.
func (d *PropDialog) runPageAction(fn func(ctx context.Context) error, onDone func(err error)) {
	d.app.safego("a properties page action", d.pageActionBody(fn, onDone))
}

// pageActionBody is the goroutine body both runPageAction and runPageActionOnce
// hand to safego: one round trip bounded by propFetchTimeout, reported back on
// the UI goroutine. The two differ only in what happens on a panic.
func (d *PropDialog) pageActionBody(fn func(ctx context.Context) error, onDone func(err error)) func() {
	return func() {
		ctx, cancel := context.WithTimeout(d.ctx, propFetchTimeout)
		defer cancel()
		err := fn(ctx)
		d.post(func() { onDone(err) })
	}
}

// runPageActionOnce is runPageAction with an in-flight latch, for a button that
// writes its result into captured variables. Without it a second click while the
// first round trip is out puts two goroutines in flight, both writing the same
// variable and filling the same grid, so what survives is whichever finished
// last — possibly the older request.
//
// inFlight is a plain bool because both halves run on the UI goroutine: the
// click handler, and onDone via d.post.
//
// safegoRepair rather than safego precisely because there is a latch: onDone
// clears it, and a panic unwinds past onDone, leaving the button refusing every
// later click for the dialog's lifetime.
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
// busyText in statusRow while it is in flight and fn's returned text or
// "Error: …" once it completes.
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

// validateDirty runs every dirty page's validator, reporting the first failure
// via SetMessage/SelectPage — the preflight for both runApply and runScript.
func (d *PropDialog) validateDirty() bool {
	if page, err := d.Validate(); err != nil {
		d.SelectPage(page)
		d.SetMessage(err.Error(), true)
		return false
	}
	return true
}

// dirtyApplyFns returns the apply closures for every dirty page in page order,
// except that a renaming page's apply moves to the end — see propPage.renames.
func (d *PropDialog) dirtyApplyFns() []propApply {
	dirty := d.DirtyPages()
	fns := make([]propApply, 0, len(dirty))
	var last []propApply
	for _, page := range dirty {
		fn := d.applyFn[page]
		if fn == nil {
			continue
		}
		if page < len(d.pages) && d.pages[page].renames {
			last = append(last, fn)
			continue
		}
		fns = append(fns, fn)
	}
	return append(fns, last...)
}

// runPipeline is the shared shape behind runApply and runScript: validate every
// dirty page, run their apply closures sequentially against runCtx on a
// background goroutine, then report back on the UI goroutine via d.post.
// noChanges runs on the UI goroutine when nothing was dirty; onSuccess once
// every closure has succeeded. The callers differ only in which context they run
// against — a real one or one derived from gosmo.WithScript — and what "nothing
// to do" and "it worked" mean to them.
func (d *PropDialog) runPipeline(runCtx context.Context, noChanges, onSuccess func()) {
	if !d.validateDirty() {
		return
	}
	fns := d.dirtyApplyFns()
	if len(fns) == 0 {
		noChanges()
		return
	}

	d.SetApplying(true)
	d.SetMessage("", false)

	d.app.safegoRepair("applying property changes", d.applyPanicked, func() {
		var runErr error
		for _, fn := range fns {
			if runErr = fn(runCtx); runErr != nil {
				break
			}
		}
		d.post(func() {
			d.SetApplying(false)
			if runErr != nil {
				d.SetMessage(withPermissionAdvice(runErr).Error(), true)
				// After the message, not before: the reload leaves it
				// standing, and it is the only account of what went wrong.
				if _, ok := errors.AsType[committedApplyError](runErr); ok {
					d.InvalidateAll()
				}
				return
			}
			onSuccess()
		})
	})
}

// applyPanicked releases the applying latch after a panic in runPipeline's
// goroutine — its App.safegoRepair step. While applying, PropertySheet ignores
// every button and defers page loads, so without this the whole dialog, Cancel
// included, is inert.
func (d *PropDialog) applyPanicked() {
	d.SetApplying(false)
	d.SetMessage("Apply stopped unexpectedly — see the log for details.", true)
}

// runApply validates and applies every dirty page for real. hideOnSuccess
// distinguishes Apply (stay open) from OK (close on success); on error neither
// closes, so the edits and the message stay visible.
func (d *PropDialog) runApply(hideOnSuccess bool) {
	hide := func() {
		if hideOnSuccess {
			d.Dismiss()
		}
	}
	d.runPipeline(d.ctx, hide, func() {
		d.app.setStatus("Properties saved")
		d.InvalidateAll()
		hide()
	})
}

// runScript validates every dirty page like Apply, then re-runs their apply
// closures under gosmo.WithScript: the same code each page would use to save for
// real, except every write is captured as SQL text instead of executed. The
// result opens in a new query window scoped to this dialog's
// connection/database. Reads along the way still hit the server — only writes
// are intercepted — but nothing is mutated.
func (d *PropDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc, database := d.sc, d.database
	noChanges := func() { d.SetMessage("No changes to script.", false) }
	d.runPipeline(scriptCtx, noChanges, func() {
		if len(script.Statements) == 0 {
			noChanges()
			return
		}
		d.app.openQueryWithText(sc, database, strings.Join(script.Statements, "\n\n"))
	})
}

// readOnlyBannerPrefix opens the banner a page whose writes are refused shows.
const readOnlyBannerPrefix = "Read-only: this login cannot change these settings. "

// pageReadOnlyReason is the sentence a page whose writes are refused carries,
// or "" when it may be written (or when nothing was measured).
//
// Runs on the load goroutine, so unlike the menu gates it may probe a database
// it has not seen before. Same Allows rule as everywhere else: read-only only
// when the server denied every right that would permit the write.
func pageReadOnlyReason(ctx context.Context, sc *db.ServerConn, p propPage) string {
	if len(p.requires) == 0 || sc == nil {
		return ""
	}
	// The probing form of DatabaseCapabilities, which the menu gates may not
	// use — see rightsAllow, which is the single copy of the rule both follow.
	dbCaps := func(name string) *gosmo.DatabaseCapabilities {
		return sc.DatabaseCapabilities(ctx, name)
	}
	if rightsAllow(sc.Capabilities(), dbCaps, p.requiresIn, p.requiresSchema, p.requiresObject, p.requires...) {
		return ""
	}
	// A DENY on the object is a different sentence: the login may hold every
	// right the page lists, and telling it to go and ask for one of them
	// describes neither what is wrong nor what would fix it.
	if r, at, denied := objectDenial(sc.Capabilities(), dbCaps, p.requiresIn, p.requiresSchema, p.requiresObject, p.requires...); denied {
		return readOnlyBannerPrefix + deniedText(r, at)
	}
	return readOnlyBannerPrefix + requiresText(p.requires...)
}
