package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

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
// the whole sheet reloads before the message is shown. A failed apply otherwise
// keeps every page whose statements never reached the server exactly as it was,
// and reloads the ones that did — see applyProgress.
//
// That per-page account comes from gosmo's statement observer, which cannot see
// one case: a disable window whose closing re-enable is refused. The window's
// own brackets are not reported (they normally leave the server as it was), so
// an ALTER that was refused inside it counts as nothing having landed — while
// the audit has in fact been left switched off. Audit Properties is the case:
// switching an enabled audit to SECURITY LOG where the service account may not
// write it, the restore is refused and the page's State row went on claiming
// Enabled for an audit the server has switched off. The page re-reads the state
// and marks the failure itself (auditApplyFailure).
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

// applyRun is the stop switch for one in-flight OK/Apply/Script pipeline —
// PropDialog's and newObjectDialog's — behind the sheet's live Cancel button
// (propsheet.PropertySheet.OnCancelApply). UI goroutine only: start and stop
// run there, and so does the posted completion that reads cancelled.
type applyRun struct {
	stop      context.CancelFunc
	cancelled bool
}

// start derives the context one run executes under from parent, which keeps
// parent's values — a WithScript collector stays in force.
func (r *applyRun) start(parent context.Context) context.Context {
	ctx, stop := context.WithCancel(parent)
	r.stop, r.cancelled = stop, false
	return ctx
}

// cancel is OnCancelApply: stop the run, and remember that it was the user who
// stopped it. The driver's error for a cancelled statement need not wrap
// context.Canceled, so the flag, not the error, is what tells the completion
// to say "cancelled".
func (r *applyRun) cancel() {
	if r.stop != nil {
		r.cancelled = true
		r.stop()
	}
}

// applyProgress is how far runApplySteps got, and which steps reached the
// server. A step is committed once one of its statements has executed — a
// failure after that leaves the server changed, and a page that keeps its
// edits then re-sends them: harmless for an ALTER, and a duplicate for an ADD
// FILE, a job step or a schedule attach. A failed New-object dialog is the
// same shape one level up: the CREATE landed and a later page did not.
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
// reports how far it got. ctx is checked before every step as well as handed
// to it: a step whose writes don't all take the context would otherwise run to
// the end of the pipeline after a cancel.
//
// Which steps reached the server comes from gosmo's statement observer, so it
// is only as complete as the step's use of ctx: a write issued on some other
// context is invisible to it. Under Script Changes nothing executes, and no
// step ever counts as committed.
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
// steps tagged with a phase, carried out afterwards in phase order. A dialog
// is planned when its pages edit one configuration whose statements must be
// ordered across pages and finished once — Resource Governor Properties,
// where a workload group's pool has to exist before the group moves into it,
// and the pool cannot be dropped until no group uses it, whichever pages hold
// those edits; and every Apply ends in one ALTER RESOURCE GOVERNOR
// RECONFIGURE, wherever the edits were. Page order cannot express either.
//
// A planned page's apply is called with a context carrying the plan
// (applyPlanFrom) and only adds steps; the dialog's run function (showPlanned)
// then decides how they are carried out. Steps are apply closures in every
// respect — they run on the pipeline goroutine, under Script Changes too, and
// never write page state.
type applyPlan struct {
	steps []plannedStep
	// values carries what a page decided for the run function, by key — a
	// setting the finishing step needs and only that page can say (Resource
	// Governor's Enabled box). Absent when the page was not dirty.
	values map[string]any
}

type plannedStep struct {
	phase int
	fn    propApply
}

type applyPlanKey struct{}

// applyPlanFrom returns the plan a planned page's apply adds its steps to, or
// nil when the page is applied any other way — which is a wiring bug the
// page reports rather than writing directly and out of order.
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

// run carries out every step in phase order, stopping at the first error.
func (p *applyPlan) run(ctx context.Context) error {
	return p.runPhases(ctx, func(int) bool { return true })
}

// runPhases is run restricted to the steps whose phase keep accepts — for a
// run function that carries out some phases inside a transaction and the
// rest, which a transaction refuses, after it.
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

// plannedApply folds a planned dialog's dirty pages into one pipeline step:
// each page's apply plans against a fresh plan, then run carries it out.
//
// The step reports a failure that came after a statement had already reached
// the server as committed, which reloads every page: the plan interleaves the
// pages' statements, so there is no telling which page's edits landed.
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
	pages, fns := d.dirtyApplyFns()
	if len(fns) == 0 {
		noChanges()
		return
	}
	if d.planned != nil {
		// One step for every dirty page: it either reached the server or it
		// did not, and a step that did reports itself committed, which
		// reloads the whole sheet.
		fns, pages = []propApply{plannedApply(fns, d.planned)}, pages[:1]
	}

	d.StartApplying(pipelineLabel(runCtx))
	d.SetMessage("", false)
	runCtx = d.run.start(runCtx)
	stop := d.run.stop

	done := make(chan struct{})
	d.app.animateUntil("animating the properties spinner", propsheet.ApplyingSpinner.Period, done)
	d.app.safegoRepair("applying property changes", d.applyPanicked, func() {
		defer close(done)
		defer stop()
		progress, runErr := runApplySteps(runCtx, fns)
		d.post(func() {
			d.SetApplying(false)
			if runErr != nil {
				d.applyFailed(runCtx, runErr, progress, pages)
				return
			}
			onSuccess()
		})
	})
}

// applyFailed reports a pipeline that stopped at runErr, and reloads every page
// whose statements reached the server — pages[i] is fns[i]'s page. Those pages'
// edits are on the server now, and keeping them would re-send them on the next
// Apply; every other page keeps its edits, so the user can correct the one that
// failed and try again.
func (d *PropDialog) applyFailed(runCtx context.Context, runErr error, progress applyProgress, pages []int) {
	// A committed failure outranks the cancel that may have led to it: it
	// says what the server was left in, which the cancel message cannot.
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
	// After the message, not before: the reload leaves it standing, and it is
	// the only account of what went wrong.
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

// propCancelledMessage is what the message line says once a run the user
// cancelled has returned. A cancelled statement is rolled back, but a page's
// apply can be several statements and the cancel may land between them — or
// after the last one had already committed — so a real run never claims that
// nothing changed. Pages that ran to completion before the cancel certainly
// did, and are counted.
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
//
// Every page's commit hook (propsheet.Form.SetCommit) runs first, here on the
// UI goroutine: a grid-plus-detail page's editor fields reach its model there
// and nowhere else, since its apply runs on the pipeline's goroutine.
//
// A page whose edits carry a consequence beyond their value registers a
// warning (propsheet.Form.SetApplyConfirm), and nothing is written until the
// user accepts it; a No leaves every edit in place. Script Changes does not
// ask: it writes nothing, and the script shows the consequence as text.
func (d *PropDialog) runApply(hideOnSuccess bool) {
	d.Commit()
	warnings := d.ApplyConfirmations()
	if len(warnings) == 0 {
		d.applyNow(hideOnSuccess)
		return
	}
	// Validation first, so a Yes is never followed by a refusal the user
	// could have been told about before being asked.
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
	d.runPipeline(d.ctx, hide, func() {
		d.app.setStatus("Properties saved")
		// Only a dialog that stays open reloads: InvalidateAll dispatches the
		// current page's fetch at once, and OK would close over it.
		if !hideOnSuccess {
			d.InvalidateAll()
		}
		d.saved()
		hide()
	})
}

// saved is what follows a write that reached the server: the Details pane's
// stale view goes, and the dialog's own onSaved hook runs.
func (d *PropDialog) saved() {
	d.staleDetails()
	if d.headerName != nil {
		d.SetHeader(d.headerPrefix+*d.headerName, d.headerRight)
	}
	if d.onSaved != nil {
		d.onSaved()
	}
}

// staleDetails drops the Details pane's cached view of what this dialog just
// wrote, so the pane stops showing pre-edit values until ⟳. That is the node the
// dialog was opened over, its parent folder (whose list may carry the edited
// columns), and its cached children (the edited object itself, when the dialog
// was opened from a row of that folder's list). The one on screen refetches.
func (d *PropDialog) staleDetails() {
	node := d.detailNode
	if node == nil {
		return
	}
	d.app.detailBrowser.InvalidateWhere(d.app, func(n *explorerNode) bool {
		return n == node || (node.parent != nil && n == node.parent) || n.parent == node
	})
}

// runScript validates every dirty page like Apply, then re-runs their apply
// closures under gosmo.WithScript: the same code each page would use to save for
// real, except every write is captured as SQL text instead of executed. The
// result opens in a new query window scoped to this dialog's
// connection/database. Reads along the way still hit the server — only writes
// are intercepted — but nothing is mutated.
func (d *PropDialog) runScript() {
	d.Commit()
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc, database := d.sc, d.database
	noChanges := func() { d.SetMessage("No changes to script.", false) }
	d.runPipeline(scriptCtx, noChanges, func() {
		if script.Len() == 0 {
			noChanges()
			return
		}
		d.app.openQueryWithText(sc, database, script.String())
	})
}
