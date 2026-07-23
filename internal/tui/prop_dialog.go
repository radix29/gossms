package tui

import (
	"context"
	"strings"
	"time"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// propFetchTimeout bounds a single property page's load — long enough for
// a slow or remote server, short enough that a dead connection doesn't
// leave a page stuck showing "Loading..." forever. Mirrors
// childFetchTimeout, the equivalent bound for Object Explorer loads.
const propFetchTimeout = 30 * time.Second

// propApply runs one page's pending edits against the server. Returned by
// a propPage's load func, closing over the row pointers that func created,
// so it can read their current (possibly edited) values without the
// dialog needing to know each page's row shapes.
type propApply func(ctx context.Context) error

// propPage is one page of a PropDialog: a title for the page list, and a
// loader that fetches the page's data and builds its form. load runs on a
// background goroutine — see PropDialog.onLoadPage.
type propPage struct {
	title string
	load  func(ctx context.Context) (*propsheet.Form, propApply, error)
}

// PropDialog is the app-layer orchestrator for propsheet.PropertySheet:
// it owns the goroutines that load pages and apply edits, translating
// between the framework's page-index/seq contract and SQL Server calls.
// One PropDialog instance is reused for every Server/Database/Login
// Properties invocation — show() re-seeds it with a fresh page set and a
// fresh per-session context each time, so cached edits and in-flight
// loads never leak from one "open" to the next.
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

// show opens the dialog with a fresh page set. sc/database are the
// connection and (optional) database a Script Changes action should open
// its new query window against — database is "" for server- and
// login-scoped dialogs, which have no single database of their own (each
// database-scoped statement they generate carries its own USE, see
// gosmo.WithScript). title is the window title; headerLeft/headerRight are
// the two ends of the header line (e.g. "Instance: SQLMI-PROD" /
// "Connected: yes").
func (d *PropDialog) show(sc *db.ServerConn, database, title, headerLeft, headerRight string, pages []propPage) {
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.sc = sc
	d.database = database
	d.pages = pages
	d.applyFn = make(map[int]propApply, len(pages))

	titles := make([]string, len(pages))
	for i, p := range pages {
		titles[i] = p.title
	}
	d.SetTitle(title)
	d.SetHeader(headerLeft, headerRight)
	d.SetPages(titles)
	d.Show()
}

func (d *PropDialog) onClose() {
	if d.cancel != nil {
		d.cancel()
	}
}

// post schedules fn to run on the UI goroutine (via App.postEvent), then
// wakes the event loop immediately so it runs without waiting for an
// unrelated key press or mouse move to arrive first. The wake must happen
// here, on the calling (background) goroutine — not from inside fn —
// since fn only runs once the loop is already awake and processing
// pending callbacks; a wake nested inside fn can't unblock the very loop
// iteration fn is waiting to run in. Mirrors postProgress/postTaskDone in
// tasks.go, the one existing caller that already gets this right.
func (d *PropDialog) post(fn func()) {
	d.app.postEvent(fn)
	d.app.wakeEventLoop()
}

// onLoadPage runs page's loader on a background goroutine and reports the
// result back on the UI goroutine, guarded by seq the same way every other
// background fetch in this package is (see app_explorer_data.go's
// loadChildren, or the ShowDatabaseProperties this dialog replaces).
func (d *PropDialog) onLoadPage(page, seq int) {
	if page < 0 || page >= len(d.pages) {
		return
	}
	load := d.pages[page].load
	sessionCtx := d.ctx

	go func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		form, apply, err := load(ctx)
		d.post(func() {
			if err != nil {
				d.SetPageError(page, seq, err)
				return
			}
			d.applyFn[page] = apply
			d.SetPageForm(page, seq, form)
		})
	}()
}

// runPageAction runs fn on a background goroutine (a real network round
// trip) and reports its result back on the UI goroutine via onDone — the
// shared "immediate action" pattern for a Properties page's own buttons
// (Check Syntax, Estimate Rows, Rebuild, Reorganize, Update Statistics, ...)
// that run for real immediately, independent of OK/Cancel/Apply. Unlike
// runApply/runScript, this isn't tied to any page's dirty state — the page
// itself decides when its own action buttons are relevant. Callers that
// need a result value out of fn capture it in an outer variable from
// within fn, the same way asyncStatusButton's caller does.
func (d *PropDialog) runPageAction(fn func(ctx context.Context) error, onDone func(err error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), propFetchTimeout)
		defer cancel()
		err := fn(ctx)
		d.post(func() { onDone(err) })
	}()
}

// asyncStatusButton returns a button that runs fn via runPageAction,
// showing busyText in statusRow while it's in flight and either fn's
// returned text or "Error: ..." once it completes.
func (d *PropDialog) asyncStatusButton(label string, statusRow *propsheet.StaticRow, busyText string, fn func(ctx context.Context) (string, error)) *widgets.Button {
	return widgets.NewButton(label, func() {
		statusRow.SetValue(busyText)
		var result string
		d.runPageAction(func(ctx context.Context) error {
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

// onConfirmDiscard prompts before Refresh discards a dirty page's edits,
// via the app's shared confirm dialog — shown nested on top of this one,
// which the dialog stack already supports (see dialog_stack.go).
func (d *PropDialog) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDialog.ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}

// validateDirty runs every dirty page's validator, reporting the first
// failure via SetMessage/SelectPage — the shared preflight for both
// runApply and runScript.
func (d *PropDialog) validateDirty() bool {
	if page, err := d.Validate(); err != nil {
		d.SelectPage(page)
		d.SetMessage(err.Error(), true)
		return false
	}
	return true
}

// dirtyApplyFns returns the apply closures for every dirty page.
func (d *PropDialog) dirtyApplyFns() []propApply {
	dirty := d.DirtyPages()
	fns := make([]propApply, 0, len(dirty))
	for _, page := range dirty {
		if fn := d.applyFn[page]; fn != nil {
			fns = append(fns, fn)
		}
	}
	return fns
}

// runPipeline is the shared shape behind runApply and runScript: validate
// every dirty page, then run their apply closures sequentially against
// runCtx on a background goroutine (each is a real network round trip),
// then report the result back on the UI goroutine via d.post. noChanges
// runs (already on the UI goroutine) when there was nothing dirty to do;
// onSuccess runs once every closure has succeeded. The two callers differ
// only in what context they run closures against (a real one vs. one
// derived from gosmo.WithScript) and what "nothing to do"/"it worked"
// means to them — everything else (validate, collect, SetApplying,
// sequencing, error reporting) lives here once.
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

// runApply validates and applies every dirty page for real. hideOnSuccess
// distinguishes Apply (stay open) from OK (close once the save succeeds);
// on error, neither closes — the edits and the error message both stay
// visible so the user can fix and retry.
func (d *PropDialog) runApply(hideOnSuccess bool) {
	hide := func() {
		if hideOnSuccess {
			d.Hide()
		}
	}
	d.runPipeline(d.ctx, hide, func() {
		d.app.setStatus("Properties saved")
		d.InvalidateAll()
		hide()
	})
}

// runScript validates every dirty page exactly like Apply, then re-runs
// their apply closures under gosmo.WithScript — the same code each page
// would use to save for real, except every write it makes is captured as
// SQL text instead of executed. The result opens in a new query window
// (scoped to this dialog's connection/database) for the user to review
// and run, rather than being applied directly. Any reads an apply closure
// makes along the way (e.g. re-fetching a ConfigurationOption before
// changing it) still hit the server — only writes are intercepted — but
// nothing is ever mutated, since the whole point is a dry run.
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
