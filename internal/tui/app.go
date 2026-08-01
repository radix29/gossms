// Package tui is the gossms application layer. It wires together the
// reusable, embeddable controls from internal/tuikit (theme, core, widgets,
// layout, dialogs, controls) into the SQL-Server-specific Object Explorer,
// query panels, and dialogs that make up goSSMS.
//
// Everything in tuikit is application-agnostic; everything in this package
// knows about gosmo, config.Connection, and SQL Server object types.
package tui

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// App is the root application struct that owns the screen and all UI panels.
// It is the only place in the codebase where tuikit controls are bound to
// SQL-Server-specific behaviour.
type App struct {
	screen tcell.Screen

	explorerSplit *layout.Splitter

	explorer *ObjectExplorer
	panels   *layout.PanelManager

	// detailBrowser is the single, always-present Object Explorer Details
	// panel (see DetailBrowser.Closable) — kept as its own field, not just
	// found via panels.ActivePanel(), so refresh actions can invalidate its
	// cache even when some other panel is the active tab.
	detailBrowser *DetailBrowser

	// dragNode is the Object Explorer node currently being dragged toward
	// the query editor (armed by a Button1 press over a draggable node,
	// cleared on release) — nil when no drag is in progress. See
	// handleMouse/dropExplorerNode. dragX/dragY track the mouse position
	// while a drag is in progress, so draw can render a ghost of the
	// dragged object's text following the cursor.
	dragNode *explorerNode
	dragX    int
	dragY    int

	// mouseButtonDown tracks whether Button1 is currently held, regardless
	// of where the gesture started or which widget is under the cursor.
	// Unlike the per-widget mouseDragging latches (Toolbar, TreeView,
	// MenuBar, ModalDialog), which only catch a resend staying within the
	// same widget, this is what lets handleMouse tell a drag that started
	// elsewhere and merely drifted across the status row from a fresh press
	// starting there. See handleMouse's use for the Status History dialog.
	mouseButtonDown bool

	// gestureOwner is the region that claimed the Button1 press currently
	// being held, or ownerNone between gestures — the App-level counterpart
	// of propsheet.PropertySheet.dragZone and QueryPanel.dragZone. Without
	// it, a drag that merely wanders out of the region it started in is
	// handed to whatever it wanders over: leftward out of a query editor
	// arms an Object Explorer drag-and-drop that pastes a node's SQL on
	// release, and across the explorer/panels splitter bar resizes the two.
	// Cleared on the release.
	gestureOwner appGestureOwner

	// gestureOverlay is the modal layer as it stood when the current
	// gesture began, so handleMouse can tell that a dialog, context menu,
	// or menu dropdown has opened or closed since — see its use there.
	gestureOverlay overlayStack

	menuBar       *controls.MenuBar
	toolbar       *controls.Toolbar
	contextMenu   *controls.ContextMenu
	statusText    string
	queryPanelCnt int

	// actualPlanEnabled is the "Include Actual Execution Plan" toolbar
	// toggle's state — off by default. Read by QueryPanel.runQuery to
	// decide between query.Execute and query.ExecuteWithPlan.
	actualPlanEnabled bool

	connectDialog       *ConnectDialog
	helpDialog          *HelpDialog
	keyDiagDialog       *KeyDiagnosticsDialog
	updateDialog        *UpdateDialog
	statusHistoryDialog *StatusHistoryDialog
	propsDialog         *PropertiesDialog
	propDialog          *PropDialog
	newDatabaseDialog   *NewDatabaseDialog
	newLoginDialog      *NewLoginDialog
	newJobDialog        *NewJobDialog
	newScheduleDialog   *NewScheduleDialog
	newAlertDialog      *NewAlertDialog
	newOperatorDialog   *NewOperatorDialog
	fileDialog          *dialogs.FileDialog
	queryListDialog     *QueryListDialog
	optionsDialog       *OptionsDialog
	tasksDialog         *TasksDialog
	confirmDialog       *dialogs.ConfirmDialog
	confirmTypedDialog  *dialogs.TypedConfirmDialog
	alertDialog         *dialogs.AlertDialog
	backupDialog        *BackupDialog
	restoreDialog       *RestoreDialog

	// allDialogs lists every dialog exactly once, for syncDialogStack to
	// scan; dialogStack is the live z-order (see dialog_stack.go).
	allDialogs  []Dialog
	dialogStack []Dialog

	// tasks is the background task registry (see tasks.go); taskSeq is its
	// monotonic ID counter.
	tasks   []*Task
	taskSeq int

	connections []*db.ServerConn
	cfg         *config.Config

	// completionInventories caches one metadata snapshot per server+login+
	// database (see completionInventoryKey), shared by every query panel
	// connected to that same database — see completion_inventory.go. Only
	// ever read/written from the UI goroutine (direct calls, or inside a
	// postEvent closure), same convention as every other App field.
	completionInventories map[string]*completionInventory

	// sysCompletionInventories caches one "sys" schema catalog-view
	// snapshot per server+login (see sysCompletionInventoryKey) — unlike
	// completionInventories this is server-scoped, not per-database, since
	// sys.tables/sys.columns/... are defined identically in every database
	// on a server and only need loading once per connection. Populated
	// proactively at connect time (see connectServer/connectForQueryPanel)
	// rather than lazily on first keystroke.
	sysCompletionInventories map[string]*completionInventory

	// Focus: "explorer" | "panels"
	focus string

	// Bracketed paste: pasting is true between an *tcell.EventPaste start
	// and its matching end, during which every EventKey is pasted content
	// rather than typing and accumulates in pasteBuf. See
	// bufferPastedKey/endBracketedPaste in clipboard.go.
	pasting  bool
	pasteBuf strings.Builder

	pendingMu sync.Mutex
	pending   []func()

	// wakePending coalesces wakeEventLoop calls: true from the moment an
	// EventInterrupt has been sent until the event loop next wakes for any
	// event and clears it. A background goroutine that finds it already true
	// skips sending its own interrupt — the callback it queued via postEvent
	// still runs, because drainPending() drains the whole a.pending queue
	// the next time the loop wakes for any reason. Without this, a burst of
	// near-simultaneous background completions (the bounded per-row fetches
	// in loadDatabasesFolderDetails/loadTablesFolderDetails) each cost a
	// full drainPending + syncDialogStack + draw().
	wakePending atomic.Bool

	// quitMu serializes wakeEventLoop's channel send against quit's call to
	// screen.Fini(), which closes EventQ() — see quit's and wakeEventLoop's
	// doc comments. Checking a quitting flag before sending would still
	// race: Fini() can close the channel between the check and the send.
	// Holding quitMu across both the flag and the send (in wakeEventLoop),
	// and across setting the flag and calling Fini() (in quit), makes the
	// two mutually exclusive — a send either completes before Fini() closes
	// the channel or never happens at all.
	quitMu   sync.Mutex
	quitting bool
}

// NewApp constructs the application.
func NewApp() *App {
	return new(App{
		focus:      "explorer",
		statusText: "Ready  |  F1 Help  |  Ctrl+N New Query  |  Ctrl+Shift+O Connect  |  Ctrl+Q Quit",
		cfg:        config.Load(),
	})
}

// Run initialises the screen and enters the event loop.
func (a *App) Run() error {
	s, err := core.Init()
	if err != nil {
		return fmt.Errorf("init screen: %w", err)
	}
	a.screen = s
	defer s.Fini()

	a.buildUI()
	a.layoutAll()
	// Open Connect on startup: nothing in the app works without a server,
	// so the first thing shown is the dialog the user would have opened
	// anyway. syncDialogStack must run before the first draw — draw()
	// renders from dialogStack, which is otherwise only synced inside the
	// event loop, so without this the dialog wouldn't appear until the
	// first keypress.
	a.connectDialog.Show()
	a.syncDialogStack()
	a.draw()

	// tcell v3's Screen interface has no PollEvent/PostEvent methods; events
	// are delivered and posted through a plain channel, EventQ(). The
	// channel is closed by Fini(), so `for ev := range` exits automatically
	// on quit instead of needing a nil-event sentinel check.
	for ev := range s.EventQ() {
		// Cleared before draining, not after, so a postEvent+wakeEventLoop
		// racing this instant still gets a wake of its own: if its append
		// to a.pending lands after drainPending's mutex-protected read
		// below, wakePending is already false again, so its CompareAndSwap
		// succeeds and queues a fresh EventInterrupt.
		a.wakePending.Store(false)
		a.drainPending()
		a.syncDialogStack()

		switch e := ev.(type) {
		case *tcell.EventResize:
			s.Sync()
			a.layoutAll()
		case *tcell.EventInterrupt:
			// triggered after background goroutine posts result
		case *tcell.EventKey:
			// Between the two EventPaste markers every key is pasted
			// content, not typing — buffer it instead of handling it, or
			// each pasted newline arrives as KeyEnter and gets eaten by
			// IntelliSense's commit-the-selected-candidate binding, which
			// silently rewrites the pasted text (see bufferPastedKey).
			if a.pasting {
				a.bufferPastedKey(e)
				break
			}
			if a.handleKey(e) {
				return nil
			}
		case *tcell.EventPaste:
			// Terminal bracketed paste (the terminal's own Paste command, or
			// a middle-click) — see beginBracketedPaste/endBracketedPaste.
			if e.Start() {
				a.beginBracketedPaste()
			} else {
				a.endBracketedPaste()
			}
		case *tcell.EventMouse:
			a.handleMouse(e)
		case *tcell.EventClipboard:
			// Response to a GetClipboard() request made from Ctrl+V — see
			// activeClipboardTarget for how the destination widget is
			// resolved.
			if target := a.activeClipboardTarget(); target != nil {
				target.Paste(string(e.Data()))
				if a.connectDialog.Visible() {
					a.connectDialog.updateMatches()
				}
			}
		}

		// Re-sync before drawing: the event just handled may have opened or
		// closed a dialog, and draw() renders straight from dialogStack, so
		// without this a new dialog wouldn't appear — nor a closed one
		// vanish — until the next event arrived. The top-of-loop sync still
		// runs so input routing sees changes made by drainPending.
		a.syncDialogStack()
		a.draw()
	}
	return nil
}

// postAndWake queues fn to run on the UI goroutine (via postEvent) and
// immediately wakes the event loop to run it, without waiting for an
// unrelated key press or mouse move to arrive first. Call it from a
// background goroutine, after any other work it needs to do — never from
// the UI goroutine itself (see wakeEventLoop's doc comment).
//
// This is how every background operation in internal/tui reports its
// result, and the only way one should: writing the two calls out by hand
// invites nesting the wakeup inside the very closure that's waiting to be
// drained, which never fires. See wakeEventLoop for why. It's also the
// shared body behind PropDialog.post and every New*Dialog.post.
func (a *App) postAndWake(fn func()) {
	a.postEvent(fn)
	a.wakeEventLoop()
}

func (a *App) postEvent(fn func()) {
	a.pendingMu.Lock()
	a.pending = append(a.pending, fn)
	a.pendingMu.Unlock()
}

func (a *App) drainPending() {
	a.pendingMu.Lock()
	fns := a.pending
	a.pending = nil
	a.pendingMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// wakeEventLoop nudges the event loop to run one more iteration — draining
// callbacks queued via postEvent and redrawing. Call it from a background
// goroutine after postEvent; calling it from the UI thread itself would
// deadlock (the loop can't read EventQ while it's mid-dispatch).
//
// Reach for postAndWake rather than this directly. The wakeup has to be
// sent after the postEvent call and outside its closure: Run()'s loop only
// drains queued callbacks when it wakes for some event on EventQ(), so a
// wakeup nested inside the very closure waiting to be drained can never
// fire, and the result sits queued and invisible until an unrelated
// keypress happens to drain it. That was a real shipped bug. The one
// caller left that needs this on its own is QueryPanel's elapsed-timer
// tick, which has no callback to post.
//
// A no-op when a.screen is nil, as it is for every App built by newTestApp:
// a background goroutine from an async action exercised in such a test can
// still be running after its test function returned, and would otherwise
// panic on the nil screen.
//
// Also a no-op if wakePending is already set (see its doc comment on App)
// or if the app is quitting (see quit's — quitMu makes the two mutually
// exclusive, so this either sends before Fini() closes EventQ() or not at
// all, never after).
//
// The send is non-blocking. quitMu is held across it, and quit() takes the
// same lock from the UI goroutine, which is then not draining EventQ(): a
// blocking send on a full queue (tcell buffers 128, which all-motion mouse
// tracking fills fast during a slow frame) would hold quitMu while quit()
// waited for it, hanging Ctrl+Q. Giving up on a full queue loses nothing —
// a full queue guarantees the loop is about to wake for those events, and
// the top of Run()'s loop clears wakePending and calls drainPending() on
// every iteration regardless of what woke it.
func (a *App) wakeEventLoop() {
	if a.screen == nil {
		return
	}
	if !a.wakePending.CompareAndSwap(false, true) {
		return
	}
	a.quitMu.Lock()
	defer a.quitMu.Unlock()
	if a.quitting {
		return
	}
	select {
	case a.screen.EventQ() <- tcell.NewEventInterrupt(nil):
	default:
	}
}

// buildUI creates all UI components from tuikit building blocks.
func (a *App) buildUI() {
	a.explorer = NewObjectExplorer(a)
	a.explorer.SetActive(true)

	a.explorerSplit = layout.NewVerticalSplitter()
	a.explorerSplit.SetRatio(0.3)

	a.panels = layout.NewPanelManager()
	a.detailBrowser = NewDetailBrowser("Object Explorer Details")
	a.detailBrowser.OnRefresh = a.refreshSelected
	a.panels.AddPanel(a.detailBrowser)
	a.panels.OnCloseTab = a.requestClosePanel

	a.menuBar = controls.NewMenuBar()
	a.menuBar.SetMenus(a.buildMenus())

	a.toolbar = controls.NewToolbar()
	a.toolbar.SetButtons(a.buildToolbar())

	a.contextMenu = new(controls.ContextMenu{})
	a.connectDialog = NewConnectDialog(a)
	a.helpDialog = NewHelpDialog(a)
	a.keyDiagDialog = NewKeyDiagnosticsDialog(a)
	a.updateDialog = NewUpdateDialog(a)
	a.statusHistoryDialog = NewStatusHistoryDialog(a)
	a.propsDialog = NewPropertiesDialog(a)
	a.propDialog = NewPropDialog(a)
	a.newDatabaseDialog = NewNewDatabaseDialog(a)
	a.newLoginDialog = NewNewLoginDialog(a)
	a.newJobDialog = NewNewJobDialog(a)
	a.newScheduleDialog = NewNewScheduleDialog(a)
	a.newAlertDialog = NewNewAlertDialog(a)
	a.newOperatorDialog = NewNewOperatorDialog(a)
	a.fileDialog = dialogs.NewFileDialog(a.screen)
	a.fileDialog.OnConfirmOverwrite = func(path string, proceed func()) {
		a.confirmDialog.ShowConfirm("Confirm Save As",
			filepath.Base(path)+" already exists. Overwrite it?",
			func(confirmed bool) {
				if confirmed {
					proceed()
				}
			})
	}
	a.queryListDialog = NewQueryListDialog(a)
	a.optionsDialog = NewOptionsDialog(a)
	a.tasksDialog = NewTasksDialog(a)
	a.confirmDialog = dialogs.NewConfirmDialog(a.screen)
	a.confirmTypedDialog = dialogs.NewTypedConfirmDialog(a.screen)
	a.alertDialog = dialogs.NewAlertDialog(a.screen)
	a.backupDialog = NewBackupDialog(a)
	a.restoreDialog = NewRestoreDialog(a)

	// Registration order only matters as a tie-break for dialogs that
	// somehow became visible in the same tick (today, never — each Show()
	// is one synchronous call from one key/menu action); see syncDialogStack.
	a.allDialogs = []Dialog{
		a.connectDialog, a.helpDialog, a.keyDiagDialog, a.updateDialog, a.statusHistoryDialog, a.propsDialog, a.propDialog,
		a.newDatabaseDialog, a.newLoginDialog,
		a.newJobDialog, a.newScheduleDialog, a.newAlertDialog, a.newOperatorDialog,
		a.fileDialog, a.queryListDialog, a.optionsDialog, a.tasksDialog,
		a.confirmDialog, a.confirmTypedDialog, a.alertDialog, a.backupDialog, a.restoreDialog,
	}
}

func (a *App) focusExplorer() {
	a.focus = "explorer"
	a.explorer.SetActive(true)
	a.syncActivePanelFocus()
}

func (a *App) focusPanels() {
	a.focus = "panels"
	a.explorer.SetActive(false)
	a.syncActivePanelFocus()
}

// syncActivePanelFocus keeps the visible panel's Activatable state (title
// bar highlight, cursor visibility) in sync with a.focus. PanelManager only
// calls SetActive when its own active index changes, and knows nothing
// about a.focus — so anything that can leave a.focus == "explorer" while
// changing which panel is active (nextPanel/prevPanel) must call this too,
// or the newly-selected panel shows as focused while Object Explorer still
// holds real keyboard focus.
func (a *App) syncActivePanelFocus() {
	if p, ok := a.panels.ActivePanel().(layout.Activatable); ok {
		p.SetActive(a.focus == "panels")
	}
}

// cycleFocus advances keyboard focus one step (Ctrl+Tab): Object Explorer
// -> the active query panel's editor -> its results pane -> Object Explorer
// again. A non-query panel (e.g. Object Explorer Details), or a query panel
// with no results yet, has no middle/last stop to offer, so this degrades
// to the plain two-way explorer/panels toggle for those.
func (a *App) cycleFocus() {
	qp := a.activeQueryPanel()
	switch {
	case a.focus == "explorer":
		a.focusPanels()
		if qp != nil {
			qp.setResultsFocused(false)
		}
	case qp != nil && !qp.resultsFocused && qp.result != nil:
		qp.setResultsFocused(true)
	default:
		a.focusExplorer()
	}
}

// cycleFocusReverse is cycleFocus run backwards (Ctrl+Shift+Tab): Object
// Explorer -> the active query panel's results pane -> its editor -> Object
// Explorer again. Degrades the same way cycleFocus does when there's no
// results pane to stop at.
func (a *App) cycleFocusReverse() {
	qp := a.activeQueryPanel()
	switch {
	case a.focus == "explorer":
		a.focusPanels()
		if qp != nil {
			qp.setResultsFocused(qp.result != nil)
		}
	case qp != nil && qp.resultsFocused:
		qp.setResultsFocused(false)
	default:
		a.focusExplorer()
	}
}

// nextPanel and prevPanel run the tab-bar's Next/Prev panel action (Ctrl+
// Shift+Right/Left and the View menu) — thin wrappers over PanelManager.
// Next/Prev that also re-sync the newly active panel's focus visuals (see
// syncActivePanelFocus), since these can fire while a.focus == "explorer".
func (a *App) nextPanel() {
	a.panels.Next()
	a.syncActivePanelFocus()
}

func (a *App) prevPanel() {
	a.panels.Prev()
	a.syncActivePanelFocus()
}

// jumpToPanel switches directly to panel i, counted from the left
// (Ctrl+0..9 — 0 is always Object Explorer Details, see
// DetailBrowser.Closable). An out-of-range i is a silent no-op, same as
// PanelManager.SetActive.
func (a *App) jumpToPanel(i int) {
	a.panels.SetActive(i)
	a.syncActivePanelFocus()
}

// layoutAll recalculates every region from current screen size.
func (a *App) layoutAll() {
	w, h := a.screen.Size()
	if w < 20 || h < 5 {
		return
	}
	const menuH, statusH = 1, 1
	contentH := h - menuH - statusH

	a.menuBar.SetBounds(0, 0, w)
	a.toolbar.SetBounds(0, 0, w)
	a.explorerSplit.SetBounds(0, menuH, w, contentH)

	left := a.explorerSplit.FirstRect()
	right := a.explorerSplit.SecondRect()
	a.explorer.SetBounds(left.X, left.Y, left.W, left.H)
	a.panels.SetBounds(right.X, right.Y, right.W, right.H)
}

func (a *App) draw() {
	s := a.screen
	w, h := s.Size()
	s.Clear()

	a.menuBar.Draw(s)
	a.toolbar.Draw(s)
	a.explorerSplit.Draw(s)
	a.explorer.Draw(s)
	a.panels.Draw(s)

	// Status bar
	const statusH = 1
	statusStyle := theme.StyleStatusBar()
	core.FillRect(s, core.Rect{X: 0, Y: h - statusH, W: w, H: statusH}, ' ', statusStyle)
	connInfo := ""
	if n := len(a.connections); n > 0 {
		connInfo = fmt.Sprintf("  |  %d server%s connected", n, pluralSuffix(n))
	}
	if n := a.runningTaskCount(); n > 0 {
		connInfo += fmt.Sprintf("  |  %d task%s running", n, pluralSuffix(n))
	}
	core.DrawTextClipped(s, 1, h-statusH, w-2, statusStyle, a.statusText+connInfo)

	// Overlays — drawn last so they aren't painted over by panels/status bar,
	// which occupy the same rows the menu dropdown and context menu extend into.
	a.menuBar.DrawOverlay(s)
	a.toolbar.DrawOverlay(s)
	a.contextMenu.Draw(s)
	a.drawDragGhost(s, w)

	// Modal dialogs — highest z-order; dialogStack is kept current by
	// syncDialogStack (see Run), so drawing it bottom-to-top here is
	// enough to paint a nested dialog over its parent.
	a.drawDialogs(s)

	s.Show()
}

// quit tears the screen down unconditionally — Fini closes EventQ(), which
// is what ends Run's loop, and s.EventQ() is unusable from then on. Callers
// reached from a user action want requestQuit instead, which offers to save
// unsaved query panels first.
//
// quitMu is held across setting quitting and calling Fini() so a
// wakeEventLoop call racing this from a background goroutine either
// completes its send before Fini() closes EventQ(), or sees quitting and
// skips sending entirely — never sends on the closed channel, which would
// panic.
//
// screen is nil for the minimal *App some tests build by hand (see
// newTestApp in app_connections_test.go) without going through Run; quitting
// is the flag everything else keys off, so setting it is the part that has
// to happen either way. Same reasoning as setStatus's nil check below.
func (a *App) quit() {
	a.quitMu.Lock()
	defer a.quitMu.Unlock()
	a.quitting = true
	if a.screen != nil {
		a.screen.Fini()
	}
}

func (a *App) setStatus(msg string) {
	a.statusText = msg
	// statusHistoryDialog is nil for the minimal *App some tests build by
	// hand (see newTestApp in app_connections_test.go) without going
	// through buildUI.
	if a.statusHistoryDialog != nil {
		a.statusHistoryDialog.Record(msg)
	}
}

// logStatus records msg in both the log file and the status-history dialog
// but, unlike setStatus, never touches the single-line a.statusText the
// status bar shows. Used for background/diagnostic detail — a config-save
// failure after an otherwise successful connect, a child-node fetch error
// already surfaced as an error tree node — that shouldn't clobber the
// status bar.
func (a *App) logStatus(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	if a.statusHistoryDialog != nil {
		a.statusHistoryDialog.Record(msg)
	}
}

// pluralSuffix returns "" for n == 1 and "s" otherwise, for simple
// singular/plural status-bar wording ("1 server connected" vs "2 servers
// connected").
func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
