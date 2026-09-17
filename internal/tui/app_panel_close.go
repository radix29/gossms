package tui

import (
	"fmt"
	"runtime/debug"

	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// app_panel_close.go is closing a panel and quitting: what a close releases,
// the commit prompt an open transaction earns first, the save prompts quit
// walks, and the accessors every query action reaches the active panel through.

// closePanelAt removes the panel at index i, first releasing what it owns
// through layout.Disposable: an Activity Monitor's collector and per-tab
// connections, a QueryPanel's dedicated connection and session, and the
// in-flight reads of a Log Viewer, Query Store panel or AG dashboard, which
// would otherwise run to completion server-side and fire their postEvent
// closures against a panel that is no longer hosted. One interface check, not
// a per-type switch: the switch it replaced had missed QueryStorePanel, whose
// reads kept running on the shared Object Explorer pool until qsReadTimeout.
// Ending a QueryPanel's session rolls back a transaction still open on it —
// requestClosePanel is where the user is offered a commit first.
func (a *App) closePanelAt(i int) {
	if d, ok := a.panels.PanelAt(i).(layout.Disposable); ok {
		d.Close()
	}
	a.panels.RemovePanel(i)
	a.releaseClosedPanelMemory()
}

// releaseClosedPanelMemory hands a closed panel's heap back to the OS. A
// QueryPanel holds every row of its last result set for as long as it is open
// — a large one runs to gigabytes (see query.scanResultSet's cellArena) — and
// dropping the panel only makes that garbage: Go's pacer collects it whenever
// it next decides to, and the scavenger returns the pages later still, so a
// user who closed the tab precisely because the machine was struggling watches
// RSS sit where it was. debug.FreeOSMemory does both now.
//
// On a background goroutine because it is not free: FreeOSMemory is a full
// blocking GC plus a scavenge, and on a multi-gigabyte heap that is long
// enough to stall a redraw if it ran on the UI goroutine. Nothing waits on the
// result, so the close returns immediately either way.
func (a *App) releaseClosedPanelMemory() {
	if !a.reclaiming.CompareAndSwap(false, true) {
		return // one is already running; it will sweep this panel's heap too
	}
	a.safego("reclaiming a closed panel's memory", func() {
		defer a.reclaiming.Store(false)
		debug.FreeOSMemory()
	})
}

// closePanelByPointer closes p by locating its current index, for callbacks
// holding only the panel, whose index may have shifted while they were
// pending.
func (a *App) closePanelByPointer(p layout.Panel) {
	if i := a.panels.FindIndex(func(x layout.Panel) bool { return x == p }); i >= 0 {
		a.closePanelAt(i)
	}
}

// panelHosted reports whether p is still one of App's live panels — how an
// async operation that captured a panel pointer detects the panel being closed
// while it was in flight.
func (a *App) panelHosted(p layout.Panel) bool {
	return a.panels.FindIndex(func(x layout.Panel) bool { return x == p }) >= 0
}

// requestClosePanel implements Ctrl+W / File > Close and a tab's [x] button:
// closes the panel at i outright, unless it is a QueryPanel whose session has
// an open transaction or whose editor has unsaved changes — each of which
// prompts first, the transaction first, as SSMS asks. A panel whose
// layout.Closable reports false (Object Explorer Details) can't be closed at
// all — the tab bar omits its [x], and this is Ctrl+W's backstop.
func (a *App) requestClosePanel(i int) {
	if !layout.PanelClosable(a.panels.PanelAt(i)) {
		return
	}
	qp, ok := a.panels.PanelAt(i).(*QueryPanel)
	if !ok {
		a.closePanelAt(i)
		return
	}
	a.confirmOpenTransactions(qp, "closing the window", func() {
		if !qp.Dirty() {
			a.closePanelByPointer(qp)
			return
		}
		// Three-way, not Yes/No: "No" here discards the panel's unsaved SQL, so
		// Escape must not silently mean it — see ShowConfirmCancel.
		a.confirmDialog.ShowConfirmCancel("Close Query",
			qp.Title()+" has unsaved changes. Save before closing?",
			func(answer dialogs.ConfirmAnswer) {
				switch answer {
				case dialogs.ConfirmYes:
					a.saveQueryPanel(qp, false, func() { a.closePanelByPointer(qp) })
				case dialogs.ConfirmNo:
					a.closePanelByPointer(qp)
				}
				// ConfirmCancel: the panel stays open, unsaved.
			})
	})
}

// hasOpenTransaction reports whether qp's session is holding a transaction
// that ending the session would roll back. A panel mid-run is left out: its
// count is from before the run, and the run is cancelled on close anyway.
func (qp *QueryPanel) hasOpenTransaction() bool {
	return qp.tranCount > 0 && qp.connected() && !qp.executing
}

// confirmOpenTransactions asks SSMS's "There are uncommitted transactions"
// question before something that ends qp's session — closing it, reconnecting
// it, exiting — and runs then once the transactions are dealt with: Yes
// commits, No rolls back, Cancel abandons the action. With no open transaction
// then runs straight away.
//
// A commit that fails stops there with an alert (see endTransactions) rather
// than going on to end the session, which would roll back what the user just
// asked to keep.
func (a *App) confirmOpenTransactions(qp *QueryPanel, action string, then func()) {
	if !qp.hasOpenTransaction() {
		then()
		return
	}
	a.confirmDialog.ShowConfirmCancel("Uncommitted Transactions",
		fmt.Sprintf("There are uncommitted transactions in %s. Do you wish to commit them before %s?", qp.Title(), action),
		func(answer dialogs.ConfirmAnswer) {
			switch answer {
			case dialogs.ConfirmYes:
				qp.endTransactions(true, then)
			case dialogs.ConfirmNo:
				qp.endTransactions(false, then)
			}
			// ConfirmCancel: nothing happens; the transaction stays open.
		})
}

// requestQuit implements Ctrl+Q / File > Exit: offers to commit every query
// panel's open transaction and save every unsaved one before tearing the
// screen down, and abandons the quit if any prompt is cancelled. quit() itself
// is unconditional, so without this Ctrl+Q discards every dirty panel, and
// rolls back every open transaction, with no prompt.
func (a *App) requestQuit() (quitting bool) {
	pending := a.queryPanelsToAskBeforeQuit()
	if len(pending) == 0 {
		a.quit()
		return true
	}
	a.askBeforeQuit(pending, 0)
	return false
}

// needsQuitPrompt reports whether quitting would lose something of qp's.
func (qp *QueryPanel) needsQuitPrompt() bool {
	return qp.Dirty() || qp.hasOpenTransaction()
}

// queryPanelsToAskBeforeQuit lists every open query panel with an open
// transaction or unsaved changes, in tab order.
func (a *App) queryPanelsToAskBeforeQuit() []*QueryPanel {
	var pending []*QueryPanel
	for i := 0; i < a.panels.Count(); i++ {
		if qp, ok := a.panels.PanelAt(i).(*QueryPanel); ok && qp.needsQuitPrompt() {
			pending = append(pending, qp)
		}
	}
	return pending
}

// askBeforeQuit walks panels from index i, asking about each one's open
// transaction and then its unsaved changes, and quitting once it runs off the
// end. Recursion through the dialogs' callbacks rather than a loop, since each
// prompt must be answered — and a Yes must finish committing or writing —
// before the next is asked. Panels are re-checked as they are reached: an
// earlier Save As may have targeted a file another panel also shows, and a
// panel may have been closed from the prompt chain itself.
//
// A cancelled prompt, a failed commit, or a Save backed out of at the file
// dialog stops the walk and leaves the app open.
func (a *App) askBeforeQuit(panels []*QueryPanel, i int) {
	for i < len(panels) && (!a.panelHosted(panels[i]) || !panels[i].needsQuitPrompt()) {
		i++
	}
	if i >= len(panels) {
		a.quit()
		return
	}
	qp := panels[i]
	next := func() { a.askBeforeQuit(panels, i+1) }
	a.confirmOpenTransactions(qp, "exiting", func() {
		if !qp.Dirty() {
			next()
			return
		}
		a.confirmDialog.ShowConfirmCancel("Exit goSSMS",
			qp.Title()+" has unsaved changes. Save before exiting?",
			func(answer dialogs.ConfirmAnswer) {
				switch answer {
				case dialogs.ConfirmYes:
					a.saveQueryPanel(qp, false, next)
				case dialogs.ConfirmNo:
					next()
				}
				// ConfirmCancel: abandon the quit; every panel stays as it is.
			})
	})
}

func (a *App) closeActivePanel() {
	if i := a.panels.ActiveIndex(); i >= 0 {
		a.requestClosePanel(i)
	}
}

func (a *App) executeActiveQuery() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.Execute() })
}

// executeSelectedQuery runs the toolbar's "Execute Selection" button.
func (a *App) executeSelectedQuery() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.ExecuteSelection() })
}

// activeQueryPanel returns the active panel as a *QueryPanel, or nil if it
// isn't one — the type assertion every Query-menu action needs.
func (a *App) activeQueryPanel() *QueryPanel {
	if p := a.panels.ActivePanel(); p != nil {
		if qp, ok := p.(*QueryPanel); ok {
			return qp
		}
	}
	return nil
}

// withQueryPanel runs fn on the active query panel, or says there isn't one.
// Every Query-menu action and toolbar button that acts on the editor goes
// through it, so none of them can be the one that quietly does nothing when
// the active panel is a plan or a dashboard — see docs/ui-rules.md on
// context-gating. The Enabled predicates gate the same actions ahead of
// the click; this is what happens when one is reached anyway.
func (a *App) withQueryPanel(fn func(*QueryPanel)) {
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus(noActiveQueryPanelMessage)
		return
	}
	fn(qp)
}

// noActiveQueryPanelMessage is the one wording used everywhere an action needs
// a query panel and the active panel isn't one — the counterpart to
// notConnectedMessage.
const noActiveQueryPanelMessage = "No active query panel"
