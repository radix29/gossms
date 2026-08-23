// Package tui is the gossms application layer. It wires the
// application-agnostic controls from internal/tuikit into the
// SQL-Server-specific Object Explorer, query panels, and dialogs; everything
// here knows about gosmo, config.Connection, and SQL Server object types.
package tui

import (
	"fmt"
	"log"
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

// App is the root application struct, owning the screen and every UI panel —
// the one place tuikit controls are bound to SQL-Server-specific behaviour.
type App struct {
	screen tcell.Screen

	explorerSplit *layout.Splitter

	explorer *ObjectExplorer
	panels   *layout.PanelManager

	// detailBrowser is the always-present Object Explorer Details panel (see
	// DetailBrowser.Closable). Its own field rather than found via
	// panels.ActivePanel(), so a refresh can invalidate its cache while
	// another panel is the active tab.
	detailBrowser *DetailBrowser

	// dragNode is the Object Explorer node being dragged toward the query
	// editor — armed by a Button1 press over a draggable node, cleared on
	// release. dragX/dragY track the cursor so draw can render a ghost of the
	// dragged object's text. See handleMouse/dropExplorerNode.
	dragNode *explorerNode
	dragX    int
	dragY    int

	// mouseButtonDown tracks whether Button1 is held, wherever the gesture
	// started. The per-widget mouseDragging latches only catch a resend staying
	// inside one widget; this lets handleMouse tell a drag that started
	// elsewhere and drifted across the status row from a fresh press there.
	mouseButtonDown bool

	// gestureOwner is the region that claimed the Button1 press currently held,
	// ownerNone between gestures — the App-level counterpart of
	// propsheet.PropertySheet.dragZone and QueryPanel.dragZone. Without it a
	// drag that wanders out of its region is handed to whatever it wanders
	// over: leftward out of a query editor arms an Object Explorer drop that
	// pastes a node's SQL on release, and crossing the splitter resizes the
	// panes. Cleared on release.
	gestureOwner appGestureOwner

	// gestureOverlay is the modal layer as it stood when the current gesture
	// began, so handleMouse can tell a dialog, context menu or menu dropdown
	// has opened or closed since.
	gestureOverlay overlayStack

	menuBar       *controls.MenuBar
	toolbar       *controls.Toolbar
	contextMenu   *controls.ContextMenu
	statusText    string
	queryPanelCnt int

	// actualPlanEnabled is the "Include Actual Execution Plan" toolbar toggle,
	// off by default. QueryPanel.runQuery reads it to choose between
	// query.Execute and query.ExecuteWithPlan.
	actualPlanEnabled bool

	// metaEnabled is the "Show Output Column Metadata" toolbar toggle, off by
	// default. QueryPanel.setResult reads it on the UI goroutine after the
	// query returns, so it needs no snapshot semantics.
	metaEnabled bool

	connectDialog       *ConnectDialog
	findDialog          *FindReplaceDialog
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
	newIndexDialog      *NewIndexDialog
	newStatisticsDialog *NewStatisticsDialog
	newCMKDialog        *NewColumnMasterKeyDialog
	newCEKDialog        *NewColumnEncryptionKeyDialog
	agAddDatabaseDialog *AGAddDatabaseDialog
	agAddListenerDialog *AGAddListenerDialog
	agAddReplicaDialog  *AGAddReplicaDialog
	newAGDialog         *NewAGDialog
	newEndpointDialog   *NewEndpointDialog
	fileDialog          *dialogs.FileDialog
	queryListDialog     *QueryListDialog
	optionsDialog       *OptionsDialog
	filterDialog        *FilterDialog
	logSearchDialog     *LogSearchDialog
	promptDialog        *dialogs.PromptDialog
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

	// savedFilters remembers each filtered folder's nodeFilter by identity
	// rather than node pointer (see filterKey), so reconnecting within a
	// session brings the filters back — a disconnect drops every node, so the
	// tree can't hold them. filterMu guards it: fetchChildren restores from it
	// on the loading goroutine, applyNodeFilter writes from the UI goroutine.
	savedFilters map[filterKey]*nodeFilter
	filterMu     sync.Mutex

	// peerCreds is how to reach each instance the user has connected to, keyed
	// by db.InstanceKey — the resolver behind db.ServerConn.Peer, see
	// app_peer_creds.go. peerCredMu guards it for the same reason filterMu
	// guards savedFilters: background loader goroutines read it through Peer.
	// peerCredAliases is the same keyed by short host name, consulted only when
	// peerCreds misses — see shortHostKey.
	peerCreds       map[string]config.Connection
	peerCredAliases map[string]config.Connection
	peerCredMu      sync.Mutex

	// completionInventories caches one metadata snapshot per
	// server+login+database (see completionInventoryKey), shared by every query
	// panel on that database. UI goroutine only, like every other App field.
	completionInventories map[string]*completionInventory

	// sysCompletionInventories caches one "sys" catalog-view snapshot per
	// server+login (see sysCompletionInventoryKey) — server-scoped, since
	// sys.tables/sys.columns/… are identical in every database. Populated at
	// connect time, not lazily.
	sysCompletionInventories map[string]*completionInventory

	// Focus: "explorer" | "panels"
	focus string

	// Bracketed paste: pasting is true between an *tcell.EventPaste start and
	// its matching end, during which every EventKey is pasted content and
	// accumulates in pasteBuf. See clipboard.go.
	pasting  bool
	pasteBuf strings.Builder

	// pendingPaste is the widget a Ctrl+V was aimed at while the terminal's
	// OSC 52 clipboard reply is outstanding — the fallback when no native
	// clipboard tool answered. The reply arrives as an *tcell.EventClipboard an
	// unbounded time later; App.pasteInto says why the target is remembered
	// rather than resolved again then.
	pendingPaste clipboardTarget

	// pendingPasteToken pins which field of pendingPaste the Ctrl+V was aimed
	// at, for a host that hands back itself rather than the field — see
	// core.ClipboardTargetTokener and App.pasteInto.
	pendingPasteToken any

	pendingMu sync.Mutex
	pending   []func()

	// wakePending coalesces wakeEventLoop calls: true from the moment an
	// EventInterrupt is sent until the loop next wakes and clears it. A
	// goroutine finding it already true skips its own interrupt; its queued
	// callback still runs, since the next wake for any reason drains the whole
	// queue. Without this, a burst of near-simultaneous completions (the
	// per-row fetches in loadDatabasesFolderDetails and friends) each cost a
	// full drainPending + syncDialogStack + draw().
	wakePending atomic.Bool

	// quitMu serializes wakeEventLoop's channel send against quit's
	// screen.Fini(), which closes EventQ(). A flag checked before sending still
	// races — Fini() can close the channel between check and send. Holding
	// quitMu across flag-and-send and across set-flag-and-Fini makes the two
	// mutually exclusive: a send either completes before the close or never
	// happens.
	quitMu   sync.Mutex
	quitting bool
}

// NewApp constructs the application.
func NewApp() *App {
	a := new(App{
		focus:      "explorer",
		statusText: "Ready  |  F1 Help  |  Ctrl+N New Query  |  F9 Connect  |  Ctrl+Q Quit",
		cfg:        config.Load(),
	})
	a.loadPeerCredentials()
	return a
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
	// Open Connect on startup — nothing works without a server. syncDialogStack
	// must run before the first draw: draw() renders from dialogStack, which is
	// otherwise synced only inside the event loop, so the dialog wouldn't
	// appear until the first keypress.
	a.connectDialog.Show()
	a.syncDialogStack()
	a.draw()

	// tcell v3 has no PollEvent/PostEvent; events come from the EventQ()
	// channel, which Fini() closes, so the range exits on quit without a
	// sentinel.
	for ev := range s.EventQ() {
		// Cleared before draining, not after, so a postEvent+wakeEventLoop
		// racing this instant still gets its own wake: if its append to
		// a.pending lands after drainPending's read below, wakePending is
		// already false and its CompareAndSwap succeeds.
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
			// Between the two EventPaste markers every key is pasted content,
			// not typing. Buffer it, or each pasted newline arrives as
			// KeyEnter and IntelliSense's commit binding eats it, silently
			// rewriting the pasted text.
			if a.pasting {
				a.bufferPastedKey(e)
				break
			}
			if a.handleKey(e) {
				return nil
			}
		case *tcell.EventPaste:
			// Terminal bracketed paste — the terminal's own Paste command,
			// or a middle-click.
			if e.Start() {
				a.beginBracketedPaste()
			} else {
				a.endBracketedPaste()
			}
		case *tcell.EventMouse:
			a.handleMouse(e)
		case *tcell.EventClipboard:
			// Response to the GetClipboard() request made from Ctrl+V, which
			// recorded what it was aimed at. Clearing it first also makes an
			// unsolicited reply — the terminal answering a request this app
			// never made — paste nothing anywhere.
			target, token := a.pendingPaste, a.pendingPasteToken
			a.pendingPaste, a.pendingPasteToken = nil, nil
			a.pasteInto(target, token, string(e.Data()))
		}

		// Re-sync before drawing: the event just handled may have opened or
		// closed a dialog, and draw() renders straight from dialogStack. The
		// top-of-loop sync still runs so input routing sees drainPending's
		// changes.
		a.syncDialogStack()
		a.draw()
	}
	return nil
}

// postAndWake queues fn to run on the UI goroutine and immediately wakes the
// event loop, without waiting for an unrelated key or mouse event. Call it from
// a background goroutine, never from the UI goroutine (see wakeEventLoop).
//
// This is how every background operation in internal/tui reports its result,
// and the only way one should: writing the two calls out by hand invites
// nesting the wakeup inside the very closure waiting to be drained, which never
// fires. Also the shared body behind PropDialog.post and every
// New*Dialog.post.
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

// wakeEventLoop nudges the event loop to run one more iteration, draining
// callbacks queued via postEvent and redrawing. Call it from a background
// goroutine after postEvent; from the UI thread it would deadlock, since the
// loop can't read EventQ mid-dispatch.
//
// Prefer postAndWake. The wakeup has to be sent after postEvent and outside its
// closure: the loop drains queued callbacks only when it wakes for an event, so
// a wakeup nested inside the closure waiting to be drained never fires and the
// result sits queued and invisible until an unrelated keypress. The one caller
// that needs this alone is QueryPanel's elapsed-timer tick, which has no
// callback to post.
//
// No-op when a.screen is nil (every App from newTestApp): a background
// goroutine can outlive its test function and would panic on the nil screen.
// Also a no-op if wakePending is already set, or if the app is quitting —
// quitMu makes this and Fini() mutually exclusive.
//
// The send is non-blocking. quitMu is held across it and quit() takes the same
// lock from a UI goroutine that is then not draining EventQ(), so a blocking
// send on a full queue (tcell buffers 128, which all-motion mouse tracking
// fills fast during a slow frame) would hang Ctrl+Q. Giving up on a full queue
// loses nothing: it means the loop is about to wake, and every iteration clears
// wakePending and calls drainPending regardless of what woke it.
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
	a.detailBrowser.OnRefresh = func() { a.detailBrowser.RefreshCurrent(a) }
	a.panels.AddPanel(a.detailBrowser)
	a.panels.OnCloseTab = a.requestClosePanel

	a.menuBar = controls.NewMenuBar()
	a.menuBar.SetMenus(a.buildMenus())

	a.toolbar = controls.NewToolbar()
	a.toolbar.SetButtons(a.buildToolbar())

	a.contextMenu = new(controls.ContextMenu{})

	// Every dialog is constructed through registerDialog, which is what puts it
	// in allDialogs. Registration order is only a tie-break for dialogs that
	// became visible in the same tick; see syncDialogStack.
	a.connectDialog = registerDialog(a, NewConnectDialog(a))
	a.findDialog = registerDialog(a, NewFindReplaceDialog(a))
	a.helpDialog = registerDialog(a, NewHelpDialog(a))
	a.keyDiagDialog = registerDialog(a, NewKeyDiagnosticsDialog(a))
	a.updateDialog = registerDialog(a, NewUpdateDialog(a))
	a.statusHistoryDialog = registerDialog(a, NewStatusHistoryDialog(a))
	a.propsDialog = registerDialog(a, NewPropertiesDialog(a))
	a.propDialog = registerDialog(a, NewPropDialog(a))
	a.newDatabaseDialog = registerDialog(a, NewNewDatabaseDialog(a))
	a.newLoginDialog = registerDialog(a, NewNewLoginDialog(a))
	a.newJobDialog = registerDialog(a, NewNewJobDialog(a))
	a.newScheduleDialog = registerDialog(a, NewNewScheduleDialog(a))
	a.newAlertDialog = registerDialog(a, NewNewAlertDialog(a))
	a.newOperatorDialog = registerDialog(a, NewNewOperatorDialog(a))
	a.newIndexDialog = registerDialog(a, NewNewIndexDialog(a))
	a.newStatisticsDialog = registerDialog(a, NewNewStatisticsDialog(a))
	a.newCMKDialog = registerDialog(a, NewNewColumnMasterKeyDialog(a))
	a.newCEKDialog = registerDialog(a, NewNewColumnEncryptionKeyDialog(a))
	a.logSearchDialog = registerDialog(a, NewLogSearchDialog(a))
	a.agAddDatabaseDialog = registerDialog(a, NewAGAddDatabaseDialog(a))
	a.agAddListenerDialog = registerDialog(a, NewAGAddListenerDialog(a))
	a.agAddReplicaDialog = registerDialog(a, NewAGAddReplicaDialog(a))
	a.newAGDialog = registerDialog(a, NewNewAGDialog(a))
	a.newEndpointDialog = registerDialog(a, NewNewEndpointDialog(a))
	a.fileDialog = registerDialog(a, dialogs.NewFileDialog(a.screen))
	a.fileDialog.OnConfirmOverwrite = func(path string, proceed func()) {
		// serverPathBase, not filepath.Base: the path uses the SQL Server
		// host's separator, so filepath.Base of `C:\Backup\db.bak` on Linux is
		// the whole string.
		a.confirmDialog.ShowConfirm("Confirm Save As",
			serverPathBase(path)+" already exists. Overwrite it?",
			func(confirmed bool) {
				if confirmed {
					proceed()
				}
			})
	}
	a.queryListDialog = registerDialog(a, NewQueryListDialog(a))
	a.optionsDialog = registerDialog(a, NewOptionsDialog(a))
	a.tasksDialog = registerDialog(a, NewTasksDialog(a))
	a.filterDialog = registerDialog(a, NewFilterDialog(a))
	a.promptDialog = registerDialog(a, dialogs.NewPromptDialog(a.screen))
	a.confirmDialog = registerDialog(a, dialogs.NewConfirmDialog(a.screen))
	a.confirmTypedDialog = registerDialog(a, dialogs.NewTypedConfirmDialog(a.screen))
	a.alertDialog = registerDialog(a, dialogs.NewAlertDialog(a.screen))
	a.backupDialog = registerDialog(a, NewBackupDialog(a))
	a.restoreDialog = registerDialog(a, NewRestoreDialog(a))
}

// registerDialog appends d to a.allDialogs and hands it back, so a dialog is
// constructed and registered in one expression. syncDialogStack considers only
// what allDialogs names, so a dialog assigned to its App field and not appended
// is built and shown but never drawn or given input —
// TestEveryAppDialogFieldIsRegisteredInAllDialogs checks for that.
func registerDialog[T Dialog](a *App, d T) T {
	a.allDialogs = append(a.allDialogs, d)
	return d
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

// syncActivePanelFocus keeps the visible panel's Activatable state (title bar
// highlight, cursor visibility) in sync with a.focus. PanelManager knows
// nothing about a.focus and calls SetActive only when its own active index
// changes, so anything that changes the active panel while a.focus stays
// "explorer" (nextPanel/prevPanel) must call this, or the new panel shows as
// focused while Object Explorer holds real keyboard focus.
func (a *App) syncActivePanelFocus() {
	if p, ok := a.panels.ActivePanel().(layout.Activatable); ok {
		p.SetActive(a.focus == "panels")
	}
}

// cycleFocus advances keyboard focus one step (Ctrl+Tab): Object Explorer ->
// the active query panel's editor -> its results pane -> Object Explorer. A
// non-query panel, or a query panel with no results yet, offers no middle stop
// and degrades to a two-way explorer/panels toggle.
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

// cycleFocusReverse is cycleFocus backwards (Ctrl+Shift+Tab): Object Explorer
// -> results pane -> editor -> Object Explorer. Degrades the same way.
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

// nextPanel and prevPanel run the tab-bar's Next/Prev panel action
// (Ctrl+Shift+Right/Left and the View menu), wrapping PanelManager.Next/Prev
// and re-syncing focus visuals, since they can fire while a.focus ==
// "explorer".
func (a *App) nextPanel() {
	a.panels.Next()
	a.syncActivePanelFocus()
}

func (a *App) prevPanel() {
	a.panels.Prev()
	a.syncActivePanelFocus()
}

// jumpToPanel switches to panel i, counted from the left (Ctrl+0..9; 0 is
// always Object Explorer Details). Out-of-range i is a silent no-op.
func (a *App) jumpToPanel(i int) {
	a.panels.SetActive(i)
	a.syncActivePanelFocus()
}

// layoutAll recalculates every region from current screen size.
func (a *App) layoutAll() {
	// Dialogs first, and above the too-small guard: a dialog that outgrows the
	// terminal draws its borders and button row off-screen while still taking
	// every key, so the smallest sizes are where re-fitting matters most.
	a.relayoutDialogs()

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

	// Overlays last, so the panels and status bar don't paint over the rows the
	// menu dropdown and context menu extend into.
	a.menuBar.DrawOverlay(s)
	a.toolbar.DrawOverlay(s)
	a.contextMenu.Draw(s)
	a.drawDragGhost(s, w)

	// Modal dialogs — highest z-order. syncDialogStack keeps dialogStack
	// current, so drawing it bottom-to-top paints a nested dialog over its
	// parent.
	a.drawDialogs(s)

	s.Show()
}

// quit tears the screen down unconditionally — Fini closes EventQ(), ending
// Run's loop and making the channel unusable. User actions want requestQuit
// instead, which offers to save unsaved query panels first.
//
// quitMu is held across setting quitting and calling Fini(), so a racing
// wakeEventLoop either completes its send first or sees quitting and skips it,
// never sending on the closed channel.
//
// screen is nil for the minimal *App newTestApp builds without going through
// Run, and quitting is what everything else keys off, so it must be set either
// way. Same reasoning as setStatus's nil check.
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
	// nil for the minimal *App newTestApp builds without buildUI.
	if a.statusHistoryDialog != nil {
		a.statusHistoryDialog.Record(msg)
	}
}

// logStatus records msg in the log file and the status-history dialog but,
// unlike setStatus, leaves a.statusText alone — for diagnostic detail that
// shouldn't clobber the status bar, such as a config-save failure after a
// successful connect.
func (a *App) logStatus(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	if a.statusHistoryDialog != nil {
		a.statusHistoryDialog.Record(msg)
	}
}

// pluralSuffix returns "" for n == 1 and "s" otherwise, for status-bar
// wording like "1 server connected" vs "2 servers connected".
func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
