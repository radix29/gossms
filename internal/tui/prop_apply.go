package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/propsheet"
)

// commitRename mirrors a just-completed rename into namePtr (the boxed name
// every other page resolves its object by), but only when it really ran.
//
// Under gosmo.WithScript a write is recorded and returns success without the
// server seeing it, while reads still hit the server. Mirroring the new name
// would point every sibling page's lookup at a nonexistent object: each fails
// "not found", the run aborts, no script is produced, and namePtr stays wrong
// for the dialog's life.
//
// Every dialog that can rename calls this from its General page, which is also
// marked propPage.renames so its apply runs last.
func commitRename(ctx context.Context, namePtr *string, newName string) {
	if !gosmo.Scripting(ctx) {
		*namePtr = newName
	}
}

// committedApplyError marks an apply failure that changed the server anyway, so
// the whole sheet reloads before the message shows. Otherwise a failed apply
// keeps pages whose statements never reached the server as they were and reloads
// the ones that did (applyProgress).
//
// That per-page account comes from gosmo's statement observer, which cannot see
// a disable window whose closing re-enable is refused: the window's own brackets
// are not reported, so a refused ALTER inside it counts as nothing landed while
// the audit was left switched off. Audit Properties is the case (enabled audit
// switched to SECURITY LOG where the service account may not write it). The page
// re-reads the state and marks the failure itself (auditApplyFailure).
//
// Reloading discards the page's edits, so only a *committed* failure is marked:
// its edits are already on the server.
type committedApplyError struct{ err error }

func (e committedApplyError) Error() string { return e.err.Error() }
func (e committedApplyError) Unwrap() error { return e.err }

// applyCommitted marks err as a failure that still changed the server; see
// committedApplyError.
func applyCommitted(err error) error { return committedApplyError{err} }

// applyRun is the stop switch for one in-flight OK/Apply/Script pipeline
// (PropDialog's and newObjectDialog's) behind the sheet's live Cancel button
// (propsheet.PropertySheet.OnCancelApply). UI goroutine only, including the
// posted completion that reads cancelled.
type applyRun struct {
	stop      context.CancelFunc
	cancelled bool
}

// start derives the context one run executes under from parent, keeping parent's
// values (a WithScript collector stays in force).
func (r *applyRun) start(parent context.Context) context.Context {
	ctx, stop := context.WithCancel(parent)
	r.stop, r.cancelled = stop, false
	return ctx
}

// cancel is OnCancelApply: stop the run and remember the user did it. The
// driver's error for a cancelled statement need not wrap context.Canceled, so the
// flag, not the error, makes the completion say "cancelled".
func (r *applyRun) cancel() {
	if r.stop != nil {
		r.cancelled = true
		r.stop()
	}
}

// applyProgress is how far runApplySteps got, and which steps reached the
// server. A step is committed once a statement executed: a later failure leaves
// the server changed, and a page keeping its edits would re-send them (harmless
// for ALTER, a duplicate for ADD FILE, a job step or a schedule attach). A failed
// New-object dialog is the same shape: CREATE landed, a later page did not.
type applyProgress struct {
	// completed counts the steps that ran to the end; nil steps don't count.
	completed int
	// stopped is the index in fns of the step that failed, or that a cancel
	// kept from starting; len(fns) when every step ran.
	stopped int
	// wrote reports that the step at stopped executed at least one statement
	// before it failed.
	wrote bool
	// scripted is a Script Changes run, where a completed step only collected
	// its statements and nothing reached the server.
	scripted bool
}

// committed reports whether fns[i] reached the server.
func (p applyProgress) committed(i int) bool {
	return !p.scripted && (i < p.stopped || i == p.stopped && p.wrote)
}

// anyCommitted reports whether any step reached the server.
func (p applyProgress) anyCommitted() bool {
	return !p.scripted && (p.completed > 0 || p.wrote)
}

// runApplySteps runs fns in order against ctx, stopping at the first error, and
// reports how far it got. ctx is checked before every step as well as handed to
// it, since a step whose writes ignore ctx would otherwise run on after a cancel.
//
// Which steps reached the server comes from gosmo's statement observer, so a
// write on some other context is invisible to it. Under Script Changes nothing
// executes and no step counts as committed.
func runApplySteps(ctx context.Context, fns []propApply) (applyProgress, error) {
	var wrote atomic.Bool
	ctx = gosmo.WithStatementObserver(ctx, func(gosmo.ScriptEntry) { wrote.Store(true) })
	p := applyProgress{scripted: gosmo.Scripting(ctx)}
	for i, fn := range fns {
		if fn == nil {
			continue
		}
		p.stopped = i
		if err := ctx.Err(); err != nil {
			return p, err
		}
		wrote.Store(false)
		if err := fn(ctx); err != nil {
			p.wrote = wrote.Load()
			return p, err
		}
		p.completed++
	}
	p.stopped = len(fns)
	return p, nil
}

// applyPlan is what a planned dialog's pages write into instead of the server:
// steps tagged with a phase, carried out afterwards in phase order. A dialog is
// planned when its pages edit one configuration whose statements must be ordered
// across pages and finished once: Resource Governor, where a group's pool must
// exist before the group moves into it, a pool cannot be dropped until no group
// uses it, and every Apply ends in one ALTER RESOURCE GOVERNOR RECONFIGURE. Page
// order cannot express that.
//
// A planned page's apply gets a context carrying the plan (applyPlanFrom) and
// only adds steps; the dialog's run function (showPlanned) decides how they run.
// Steps are apply closures in every respect: pipeline goroutine, under Script
// Changes too, never writing page state.
type applyPlan struct {
	steps []plannedStep
	// values carries what a page decided for the run function, by key (e.g.
	// Resource Governor's Enabled box). Absent when the page was not dirty.
	values map[string]any
}

type plannedStep struct {
	phase int
	fn    propApply
}

type applyPlanKey struct{}

// applyPlanFrom returns the plan a planned page's apply adds steps to, or nil
// when applied any other way: a wiring bug the page reports rather than writing
// directly out of order.
func applyPlanFrom(ctx context.Context) *applyPlan {
	p, _ := ctx.Value(applyPlanKey{}).(*applyPlan)
	return p
}

// errNotPlanned is a planned page's apply called without a plan.
var errNotPlanned = errors.New("internal error: this page's changes can only be applied with the rest of the dialog")

// add queues fn to run in phase; steps of one phase run in the order added.
func (p *applyPlan) add(phase int, fn propApply) {
	p.steps = append(p.steps, plannedStep{phase: phase, fn: fn})
}

// set records a value for the run function under key.
func (p *applyPlan) set(key string, v any) {
	if p.values == nil {
		p.values = map[string]any{}
	}
	p.values[key] = v
}

// value is what set recorded under key, if anything.
func (p *applyPlan) value(key string) (any, bool) {
	v, ok := p.values[key]
	return v, ok
}

// applyNotes collects what a successful run has to tell the user beyond "saved":
// a result that differs from what was asked without being an error, such as a
// shrink that stopped above its target. Written from the apply goroutine.
type applyNotes struct {
	mu    sync.Mutex
	notes []string
}

type applyNotesKey struct{}

// noteApply records msg for the run's success report; a no-op on a context no
// pipeline gave a collector.
func noteApply(ctx context.Context, msg string) {
	n, _ := ctx.Value(applyNotesKey{}).(*applyNotes)
	if n == nil {
		return
	}
	n.mu.Lock()
	n.notes = append(n.notes, msg)
	n.mu.Unlock()
}

// text is every note recorded, in order, as one message.
func (n *applyNotes) text() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.notes, " ")
}

// run carries out every step in phase order, stopping at the first error.
func (p *applyPlan) run(ctx context.Context) error {
	return p.runPhases(ctx, func(int) bool { return true })
}

// runPhases is run restricted to steps whose phase keep accepts, for a run
// function doing some phases in a transaction and the rest (which a transaction
// refuses) after it.
func (p *applyPlan) runPhases(ctx context.Context, keep func(phase int) bool) error {
	steps := slices.Clone(p.steps)
	slices.SortStableFunc(steps, func(a, b plannedStep) int { return a.phase - b.phase })
	for _, st := range steps {
		if !keep(st.phase) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := st.fn(ctx); err != nil {
			return err
		}
	}
	return nil
}

// plannedApply folds a planned dialog's dirty pages into one pipeline step: each
// page's apply plans against a fresh plan, then run carries it out.
//
// A failure after a statement reached the server is reported as committed,
// reloading every page: the plan interleaves the pages' statements, so there is
// no telling which page's edits landed.
func plannedApply(fns []propApply, run func(ctx context.Context, plan *applyPlan) error) propApply {
	return func(ctx context.Context) error {
		plan := &applyPlan{}
		planCtx := context.WithValue(ctx, applyPlanKey{}, plan)
		for _, fn := range fns {
			if err := fn(planCtx); err != nil {
				return err
			}
		}
		var wrote atomic.Bool
		runCtx := gosmo.WithStatementObserver(ctx, func(gosmo.ScriptEntry) { wrote.Store(true) })
		if err := run(runCtx, plan); err != nil {
			if wrote.Load() {
				return applyCommitted(err)
			}
			return err
		}
		return nil
	}
}

// validateDirty runs every dirty page's validator, reporting the first failure
// via SetMessage/SelectPage: the preflight for runApply and runScript.
func (d *PropDialog) validateDirty() bool {
	if page, err := d.Validate(); err != nil {
		d.SelectPage(page)
		d.SetMessage(err.Error(), true)
		return false
	}
	return true
}

// dirtyApplyFns returns the apply closures for every dirty page in page order,
// except that a renaming page's apply moves to the end (propPage.renames).
// pages[i] is the page fns[i] belongs to.
func (d *PropDialog) dirtyApplyFns() (pages []int, fns []propApply) {
	var lastPages []int
	var last []propApply
	for _, page := range d.DirtyPages() {
		fn := d.applyFn[page]
		if fn == nil {
			continue
		}
		if page < len(d.pages) && d.pages[page].renames != nil {
			lastPages, last = append(lastPages, page), append(last, fn)
			continue
		}
		pages, fns = append(pages, page), append(fns, fn)
	}
	return append(pages, lastPages...), append(fns, last...)
}

// runPipeline is the shared shape behind runApply and runScript: validate dirty
// pages, run their closures sequentially against runCtx on a background
// goroutine, report back on the UI goroutine via d.post. noChanges runs on the UI
// goroutine when nothing was dirty; onSuccess once every closure succeeded.
// Callers differ in the context (real or gosmo.WithScript) and what "nothing to
// do" and "it worked" mean. onSuccess gets what the closures noted (noteApply),
// "" for nothing.
func (d *PropDialog) runPipeline(runCtx context.Context, noChanges func(), onSuccess func(notes string)) {
	if !d.validateDirty() {
		return
	}
	pages, fns := d.dirtyApplyFns()
	if len(fns) == 0 {
		noChanges()
		return
	}
	if d.planned != nil {
		// One step for every dirty page: it either reached the server or it did not, and
		// a step that did reports itself committed, which reloads the whole sheet.
		fns, pages = []propApply{plannedApply(fns, d.planned)}, pages[:1]
	}

	d.app.runApplyPipeline(applyHost{
		sheet:    d.PropertySheet,
		sc:       d.sc,
		run:      &d.run,
		spinner:  "animating the properties spinner",
		work:     "applying property changes",
		panicked: d.applyPanicked,
	}, runCtx, fns, func(runCtx context.Context, progress applyProgress, runErr error, notes string) {
		if runErr != nil {
			d.applyFailed(runCtx, runErr, progress, pages)
			return
		}
		onSuccess(notes)
	})
}

// applyHost is what runApplyPipeline needs of the dialog running it: the sheet
// whose spinner and message it drives, the connection watchBlocking asks, the
// dialog's Cancel switch, the log labels for the spinner and the goroutine, and
// the panic handler that releases the applying latch.
type applyHost struct {
	sheet    *propsheet.PropertySheet
	sc       *db.ServerConn
	run      *applyRun
	spinner  string
	work     string
	panicked func()
}

// runApplyPipeline is the tail PropDialog.runPipeline and
// newObjectDialog.runPipeline share once they have validated and chosen fns:
// start the spinner, run fns in order under runCtx on a background goroutine
// (runApplySteps), and report back on the UI goroutine. onDone gets the run's
// own context (for gosmo.Scripting and the created handoff), how far it got,
// its error, and what its steps noted (noteApply) — a collector is attached for
// both dialogs, so a note from a New dialog's step is not silently dropped. The
// spinner is already cleared when onDone runs.
func (a *App) runApplyPipeline(h applyHost, runCtx context.Context, fns []propApply,
	onDone func(runCtx context.Context, progress applyProgress, err error, notes string)) {
	label := pipelineLabel(runCtx)
	h.sheet.StartApplying(label)
	h.sheet.SetMessage("", false)
	notes := &applyNotes{}
	runCtx = h.run.start(context.WithValue(runCtx, applyNotesKey{}, notes))
	stop := h.run.stop

	done := make(chan struct{})
	a.animateUntil(h.spinner, propsheet.ApplyingSpinner.Period, done)
	a.watchBlocking(h.sc, label, done, h.sheet.SetApplyingLabel)
	a.safegoRepair(h.work, h.panicked, func() {
		defer close(done)
		defer stop()
		progress, runErr := runApplySteps(runCtx, fns)
		a.postAndWake(func() {
			h.sheet.SetApplying(false)
			onDone(runCtx, progress, runErr, notes.text())
		})
	})
}

// How long a run goes before watchBlocking first asks the server whether it is
// waiting on another session, how often it asks again, and how long one asking
// may take.
const (
	blockedCheckAfter   = 2 * time.Second
	blockedCheckEvery   = 2 * time.Second
	blockedCheckTimeout = 3 * time.Second
)

// watchBlocking says beside the spinner when the run behind done has stopped to
// wait on another session, and on which. Without it a run held by a lock
// elsewhere — an open transaction in another session — spins with no sign of
// why: a Max DOP change waited behind another session's uncommitted
// sp_configure, and only Cancel ended it. From blockedCheckAfter on it reads
// this process's blocked requests every blockedCheckEvery until done closes,
// and setLabel gets the blocker, or base once the wait is over. A login without
// VIEW SERVER STATE sees no other session, so the label stays base.
//
// setLabel runs on the UI goroutine and is skipped once done has closed: the
// run's own completion, posted before done closes, has cleared the spinner, and
// a report arriving after it would retitle the next run's.
func (a *App) watchBlocking(sc *db.ServerConn, base string, done <-chan struct{}, setLabel func(string)) {
	if sc == nil || sc.Server == nil {
		return
	}
	srv := sc.Server
	started := time.Now()
	a.safego("watching a run for blocking", func() {
		shown := base
		timer := time.NewTimer(blockedCheckAfter)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-timer.C:
			}
			ctx, cancel := context.WithTimeout(srv.Context(), blockedCheckTimeout)
			reqs, err := srv.BlockedRequests(ctx, os.Getpid())
			cancel()
			if err == nil {
				if label := blockedLabel(base, reqs, time.Since(started)); label != shown {
					shown = label
					a.postAndWake(func() {
						select {
						case <-done:
						default:
							setLabel(label)
						}
					})
				}
			}
			timer.Reset(blockedCheckEvery)
		}
	})
}

// blockedLabel is the spinner label for a run that has been going for running:
// the blocker of the longest wait that can be the run's, or base when none is.
// reqs is every blocked request of this process, longest wait first, so it can
// include another of its sessions — a query panel's statement. A wait longer
// than the run began before it and is not the run's (a second of slack covers
// the clocks).
func blockedLabel(base string, reqs []gosmo.BlockedRequest, running time.Duration) string {
	for _, r := range reqs {
		wait := time.Duration(r.WaitTimeMS) * time.Millisecond
		if wait > running+time.Second {
			continue
		}
		label := fmt.Sprintf("Blocked %ds by session %d", int(wait.Seconds()), r.BlockingSessionID)
		who := r.BlockerLogin
		if r.BlockerHost != "" {
			who += "@" + r.BlockerHost
		}
		if r.BlockerProgram != "" {
			who = strings.TrimPrefix(who+", "+r.BlockerProgram, ", ")
		}
		if who != "" {
			label += " (" + who + ")"
		}
		return label
	}
	return base
}

// applyFailed reports a pipeline that stopped at runErr and reloads every page
// whose statements reached the server (pages[i] is fns[i]'s page); keeping their
// edits would re-send them next Apply. Other pages keep their edits so the user
// can fix the failed one and retry.
func (d *PropDialog) applyFailed(runCtx context.Context, runErr error, progress applyProgress, pages []int) {
	// A committed failure outranks the cancel that may have led to it: it says what
	// the server was left in, which the cancel message cannot.
	_, marked := errors.AsType[committedApplyError](runErr)
	if d.run.cancelled && !marked {
		d.SetMessage(propCancelledMessage(runCtx, progress.completed, len(pages)), false)
	} else {
		d.SetMessage(withPermissionAdvice(runErr).Error(), true)
	}
	var landed []int
	for i, page := range pages {
		if progress.committed(i) {
			landed = append(landed, page)
		}
	}
	// After the message, not before: the reload leaves it standing, and it is the
	// only account of what went wrong.
	switch {
	case marked:
		d.InvalidateAll()
	case len(landed) > 0:
		d.InvalidatePages(landed)
	default:
		return
	}
	if !gosmo.Scripting(runCtx) {
		d.saved()
	}
}

// propCancelledMessage is the message line after a user-cancelled run. A
// cancelled statement is rolled back, but a page's apply can be several
// statements and the cancel may land between them or after the last committed,
// so a real run never claims nothing changed. Pages that ran to completion
// before the cancel are counted.
func propCancelledMessage(runCtx context.Context, completed, total int) string {
	if gosmo.Scripting(runCtx) {
		return "Script Changes cancelled."
	}
	if completed == 0 {
		return "Apply cancelled. Part of it may already have been saved — F5 reloads a page from the server."
	}
	return fmt.Sprintf("Apply cancelled after %d of %d pages were saved — F5 reloads a page from the server.", completed, total)
}

// applyPanicked releases the applying latch after a panic in runPipeline's
// goroutine (App.safegoRepair step). While applying, PropertySheet ignores every
// button, so without this the whole dialog, Cancel included, is inert.
func (d *PropDialog) applyPanicked() {
	d.SetApplying(false)
	d.SetMessage("Apply stopped unexpectedly — see the log for details.", true)
}

// runApply validates and applies every dirty page for real. hideOnSuccess
// distinguishes Apply (stay open) from OK (close on success); on error neither
// closes, so the edits and message stay visible.
//
// Every page's commit hook (propsheet.Form.SetCommit) runs first on the UI
// goroutine: a grid-plus-detail page's editor fields reach its model only there,
// since its apply runs on the pipeline's goroutine.
//
// A page whose edits carry a consequence registers a warning
// (propsheet.Form.SetApplyConfirm); nothing is written until accepted, and a No
// leaves every edit in place. Script Changes does not ask: it writes nothing and
// the script shows the consequence.
func (d *PropDialog) runApply(hideOnSuccess bool) {
	d.Commit()
	warnings := d.ApplyConfirmations()
	if len(warnings) == 0 {
		d.applyNow(hideOnSuccess)
		return
	}
	// Validation first, so a Yes is never followed by a refusal the user could have
	// been told about before being asked.
	if !d.validateDirty() {
		return
	}
	d.app.confirmDialog.ShowConfirm("Apply Changes", strings.Join(warnings, " ")+" Continue?",
		func(confirmed bool) {
			if confirmed {
				d.applyNow(hideOnSuccess)
			}
		})
}

// applyNow is runApply once every warning has been accepted.
func (d *PropDialog) applyNow(hideOnSuccess bool) {
	hide := func() {
		if hideOnSuccess {
			d.Dismiss()
		}
	}
	d.runPipeline(d.ctx, hide, func(notes string) {
		if notes == "" {
			d.app.setStatus("Properties saved")
		} else {
			// The status bar too, since OK closes the dialog the message is shown in.
			d.app.setStatus("Properties saved. " + notes)
		}
		// Only a dialog that stays open reloads: InvalidateAll dispatches the current
		// page's fetch at once, and OK would close over it.
		if !hideOnSuccess {
			d.InvalidateAll()
			d.SetMessage(notes, false)
		}
		d.saved()
		hide()
	})
}

// saved is what follows a write that reached the server: the Details pane's
// stale view goes, and the dialog's onSaved hook runs.
func (d *PropDialog) saved() {
	d.staleDetails()
	if d.headerName != nil {
		d.SetHeader(d.headerPrefix+*d.headerName, d.headerRight)
	}
	if d.onSaved != nil {
		d.onSaved()
	}
}

// staleDetails drops the Details pane's cached view of what this dialog wrote:
// the node it opened over, its parent folder (whose list may carry the edited
// columns) and its cached children (the edited object, when opened from a row of
// the folder's list). The one on screen refetches.
func (d *PropDialog) staleDetails() {
	node := d.detailNode
	if node == nil {
		return
	}
	d.app.detailBrowser.InvalidateWhere(d.app, func(n *explorerNode) bool {
		return n == node || (node.parent != nil && n == node.parent) || n.parent == node
	})
}

// runScript validates like Apply, then re-runs the apply closures under
// gosmo.WithScript: the code a real save uses, with every write captured as SQL
// text. The result opens in a new query window scoped to this dialog's
// connection/database. Reads still hit the server; only writes are intercepted.
func (d *PropDialog) runScript() {
	d.Commit()
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc, database := d.sc, d.database
	noChanges := func() { d.SetMessage("No changes to script.", false) }
	d.runPipeline(scriptCtx, noChanges, func(string) {
		if script.Len() == 0 {
			noChanges()
			return
		}
		d.app.openQueryWithText(sc, database, script.String())
	})
}
