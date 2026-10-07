// Package tui is the gossms application layer. It wires the
// application-agnostic controls from internal/tuikit into the
// SQL-Server-specific Object Explorer, query panels, and dialogs; everything
// here knows about gosmo, config.Connection, and SQL Server object types.
package tui

import (
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/pkg/browser"
	"github.com/radix29/gosmo"
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

	// drag is the Object Explorer node drag toward the query editor (see
	// explorerDrag).
	drag explorerDrag

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
	// query.Session's Execute and ExecuteWithPlan.
	actualPlanEnabled bool

	// liveStatsEnabled is the "Include Live Query Statistics" toggle, off by
	// default. On implies actualPlanEnabled (toggleLiveQueryStatistics and
	// toggleActualExecutionPlan keep the two in step): the live view polls a
	// session profiled by the actual plan's SET STATISTICS XML ON, and becomes
	// that plan when the run ends. See query_panel_live.go.
	liveStatsEnabled bool

	// metaEnabled is the "Show Output Column Metadata" toolbar toggle, off by
	// default. QueryPanel.setResult reads it on the UI goroutine after the
	// query returns, so it needs no snapshot semantics.
	metaEnabled bool

	// wordWrap is Edit > Word Wrap (Alt+Z), off by default: display-only soft
	// wrapping of every query editor, applied to open panels on toggle and to
	// new ones by NewQueryPanel. Per session, like the two toggles above.
	wordWrap bool

	connectDialog               *ConnectDialog
	findDialog                  *FindReplaceDialog
	helpDialog                  *HelpDialog
	keyDiagDialog               *KeyDiagnosticsDialog
	updateDialog                *UpdateDialog
	statusHistoryDialog         *StatusHistoryDialog
	propsDialog                 *PropertiesDialog
	propDialog                  *PropDialog
	newDatabaseDialog           *NewDatabaseDialog
	newLoginDialog              *NewLoginDialog
	newJobDialog                *NewJobDialog
	newScheduleDialog           *NewScheduleDialog
	newAlertDialog              *NewAlertDialog
	newOperatorDialog           *NewOperatorDialog
	newCredentialDialog         *NewCredentialDialog
	sendTestMailDialog          *SendTestMailDialog
	newBackupDeviceDialog       *NewBackupDeviceDialog
	newAuditDialog              *NewAuditDialog
	newXESessionDialog          *NewXESessionDialog
	newAuditSpecificationDialog *NewAuditSpecificationDialog
	newDBAuditSpecDialog        *NewDatabaseAuditSpecificationDialog
	newDBScopedCredDialog       *NewDatabaseScopedCredentialDialog
	newIndexDialog              *NewIndexDialog
	newStatisticsDialog         *NewStatisticsDialog
	newCMKDialog                *NewColumnMasterKeyDialog
	newCEKDialog                *NewColumnEncryptionKeyDialog
	newFullTextCatalogDialog    *NewFullTextCatalogDialog
	newFullTextStoplistDialog   *NewFullTextStoplistDialog
	newSearchPropListDialog     *NewSearchPropertyListDialog
	newFullTextIndexDialog      *NewFullTextIndexDialog
	agAddDatabaseDialog         *AGAddDatabaseDialog
	agAddListenerDialog         *AGAddListenerDialog
	agAddReplicaDialog          *AGAddReplicaDialog
	newAGDialog                 *NewAGDialog
	newEndpointDialog           *NewEndpointDialog
	newCertificateDialog        *NewCertificateDialog
	newUserDialog               *NewUserDialog
	newAsymmetricKeyDialog      *NewAsymmetricKeyDialog
	newSymmetricKeyDialog       *NewSymmetricKeyDialog
	backupCertificateDialog     *BackupCertificateDialog
	backupMasterKeyDialog       *BackupMasterKeyDialog
	regenerateMasterKeyDialog   *RegenerateMasterKeyDialog
	detachDatabaseDialog        *DetachDatabaseDialog
	attachDatabaseDialog        *AttachDatabaseDialog
	newSnapshotDialog           *NewSnapshotDialog
	fileDialog                  *dialogs.FileDialog
	queryListDialog             *QueryListDialog
	optionsDialog               *OptionsDialog
	filterDialog                *FilterDialog
	logSearchDialog             *LogSearchDialog
	promptDialog                *dialogs.PromptDialog
	tasksDialog                 *TasksDialog
	confirmDialog               *dialogs.ConfirmDialog
	confirmTypedDialog          *dialogs.TypedConfirmDialog
	alertDialog                 *dialogs.AlertDialog
	progressDialog              *dialogs.ProgressDialog
	deviceCodeDialog            *DeviceCodeDialog
	backupDialog                *BackupDialog
	restoreDialog               *RestoreDialog

	// allDialogs lists every dialog exactly once, for syncDialogStack to
	// scan; dialogStack is the live z-order (see dialog_stack.go).
	allDialogs  []Dialog
	dialogStack []Dialog

	// tasks is the background task registry (see tasks.go); taskSeq is its
	// monotonic ID counter.
	tasks   []*Task
	taskSeq int

	// progressBusy is set while a runWithProgress job holds the progress
	// dialog; progressSeq numbers the jobs, so a late progressReport cannot
	// rewrite a later job's dialog. UI goroutine only.
	progressBusy bool
	progressSeq  int

	connections []*db.ServerConn
	cfg         *config.Config
	// saves is the background config and tracked-query saves (app_saves.go).
	saves appSaves

	// savedFilters remembers each filtered folder's nodeFilter by identity
	// rather than node pointer (see filterKey), so reconnecting within a
	// session brings the filters back — a disconnect drops every node, so the
	// tree can't hold them. filterMu guards it: fetchChildren restores from it
	// on the loading goroutine, applyNodeFilter writes from the UI goroutine.
	savedFilters map[filterKey]*nodeFilter
	filterMu     sync.Mutex

	// peerCreds is how to reach each instance the user has connected to — the
	// resolver behind db.ServerConn.Peer (see peerCredStore).
	peerCreds peerCredStore

	// completion is the IntelliSense metadata caches query panels share (see
	// completionCaches).
	completion completionCaches

	// focus is which half of the window has the keyboard.
	focus appFocus

	// paste is the state of a paste still arriving (see appPaste).
	paste appPaste

	// lastButtons is the button state of the previous mouse event, which
	// tells pointer motion (coalesced in Run) from a press or release.
	lastButtons tcell.ButtonMask

	// clipWriteMu runs writeClipboard's goroutines one at a time, and
	// clipWriteSeq numbers them, so of two quick copies the later one is what
	// the clipboard ends up holding. See writeClipboard.
	clipWriteMu  sync.Mutex
	clipWriteSeq atomic.Uint64

	// wake is the queue background goroutines hand results to the UI
	// goroutine through (see postAndWake).
	wake wakeQueue

	// reclaiming is true while releaseClosedPanelMemory's background
	// FreeOSMemory is running, so closing several panels in a row queues one
	// reclaim rather than one per panel — a second sweep over a heap the first
	// is still walking buys nothing and costs another full GC.
	reclaiming atomic.Bool

	// quitGate orders quit's screen.Fini() against wakeEventLoop's send (see
	// quitGate).
	quitGate quitGate
}

// NewApp constructs the application.
func NewApp() *App {
	a := new(App{
		focus:      focusOnExplorer,
		statusText: "Ready  |  F1 Help  |  Ctrl+N New Query  |  F9 Connect  |  Ctrl+Q Quit",
		cfg:        config.Load(),
	})
	// Editors built where the config is out of reach (the read-only
	// property-sheet T-SQL rows) pick the width up from this default; the ones
	// that can reach it call SetIndentWidth as well, so a live change from
	// Options reaches them without a restart — into the open query panels
	// directly, and into the open property sheets through
	// PropertySheet.SetEditorIndentWidth.
	controls.SetDefaultIndentWidth(a.cfg.IndentWidth)
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
	installEntraSignIn(a)
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
		quit, need := a.handleEvent(ev)
		if quit {
			return nil
		}
		// All-motion tracking sends an event per cell the pointer crosses, and
		// a frame costs ~6 ms over a results grid: a 400-event drag drew 406
		// frames and left the screen 2.2 s behind the pointer. A motion event
		// with more already queued is not drawn — the queued one will be, and
		// the last of a burst always is. Presses, releases and wheel notches
		// still draw each, so a widget that lays itself out in Draw (a menu
		// just opened by the press) is on screen before the next event is
		// hit-tested against it.
		if need == skipFrame || need == frameUnlessQueued && len(s.EventQ()) > 0 {
			continue
		}
		a.draw()
	}
	return nil
}

// frameNeed is whether the event handleEvent just dispatched needs a frame.
type frameNeed int

const (
	drawFrame         frameNeed = iota
	frameUnlessQueued           // pointer motion: skippable while more is queued
	skipFrame                   // nothing on screen changed
)

// handleEvent dispatches one event from the queue and reports whether the
// event loop should quit and whether the event needs a frame. Run owns the
// drawing, so a test can count frames across an event sequence.
func (a *App) handleEvent(ev tcell.Event) (quit bool, need frameNeed) {
	// Cleared before draining, not after, so a postEvent+wakeEventLoop
	// racing this instant still gets its own wake: if its append to
	// a.wake.pending lands after drainPending's read below, wake.sent is
	// already false and its CompareAndSwap succeeds.
	a.wake.sent.Store(false)
	a.drainPending()
	a.syncDialogStack()

	switch e := ev.(type) {
	case *tcell.EventResize:
		a.screen.Sync()
		a.layoutAll()
	case *tcell.EventInterrupt:
		// triggered after background goroutine posts result
	case *tcell.EventKey:
		// Between the two EventPaste markers every key is pasted content,
		// not typing. Buffer it, or each pasted newline arrives as
		// KeyEnter and IntelliSense's commit binding eats it, silently
		// rewriting the pasted text.
		if a.paste.bracketed {
			a.bufferPastedKey(e)
			// A buffered key changes nothing on screen, and a frame per
			// key made a 10,001-line, 170 KB paste take ~6 minutes. The end
			// marker draws the result. Only keys skip it: should the end
			// marker be lost, every other event (resize, mouse, a
			// background result) still draws, so the screen never freezes.
			return false, skipFrame
		}
		if a.handleKey(e) {
			return true, skipFrame
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
		// A press, a release and every wheel notch change the button
		// state; motion alone repeats it. Only motion is coalesced.
		btns := e.Buttons()
		if btns == a.lastButtons && btns&wheelButtons == 0 {
			need = frameUnlessQueued
		}
		a.lastButtons = btns
	case *tcell.EventClipboard:
		// Response to the GetClipboard() request made from Ctrl+V, which
		// recorded what it was aimed at. Clearing it first also makes an
		// unsolicited reply — the terminal answering a request this app
		// never made — paste nothing anywhere.
		target, token := a.paste.target, a.paste.token
		a.paste.target, a.paste.token = nil, nil
		a.pasteInto(target, token, string(e.Data()))
	}

	// Re-sync before drawing: the event just handled may have opened or
	// closed a dialog, and draw() renders straight from dialogStack. The
	// top-of-loop sync still runs so input routing sees drainPending's
	// changes.
	a.syncDialogStack()
	return false, need
}

// wheelButtons is every wheel direction in a tcell.ButtonMask — wheel notches
// arrive as repeated identical masks, but each is a discrete scroll, not motion.
const wheelButtons = tcell.WheelUp | tcell.WheelDown | tcell.WheelLeft | tcell.WheelRight

// wakeQueue is the callbacks background goroutines have queued for the UI
// goroutine, and whether a wakeup for them is already on its way.
type wakeQueue struct {
	mu      sync.Mutex
	pending []func()

	// sent coalesces wakeEventLoop calls: true from the moment an
	// EventInterrupt is sent until the loop next wakes and clears it. A
	// goroutine finding it already true skips its own interrupt; its queued
	// callback still runs, since the next wake for any reason drains the whole
	// queue. Without this, a burst of near-simultaneous completions (the
	// per-row fetches in loadDatabasesFolderDetails and friends) each cost a
	// full drainPending + syncDialogStack + draw().
	sent atomic.Bool
}

// quitGate serializes wakeEventLoop's channel send against quit's
// screen.Fini(), which closes EventQ(). A flag checked before sending still
// races — Fini() can close the channel between check and send. Holding mu
// across flag-and-send and across set-flag-and-Fini makes the two mutually
// exclusive: a send either completes before the close or never happens.
type quitGate struct {
	mu       sync.Mutex
	quitting bool
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
	a.wake.mu.Lock()
	a.wake.pending = append(a.wake.pending, fn)
	a.wake.mu.Unlock()
}

func (a *App) drainPending() {
	a.wake.mu.Lock()
	fns := a.wake.pending
	a.wake.pending = nil
	a.wake.mu.Unlock()
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
// Also a no-op if wake.sent is already set, or if the app is quitting —
// quitGate makes this and Fini() mutually exclusive.
//
// The send is non-blocking. quitGate.mu is held across it and quit() takes the same
// lock from a UI goroutine that is then not draining EventQ(), so a blocking
// send on a full queue (tcell buffers 128, which all-motion mouse tracking
// fills fast during a slow frame) would hang Ctrl+Q. Giving up on a full queue
// loses nothing: it means the loop is about to wake, and every iteration clears
// wake.sent and calls drainPending regardless of what woke it.
func (a *App) wakeEventLoop() {
	if a.screen == nil {
		return
	}
	if !a.wake.sent.CompareAndSwap(false, true) {
		return
	}
	a.quitGate.mu.Lock()
	defer a.quitGate.mu.Unlock()
	if a.quitGate.quitting {
		return
	}
	select {
	case a.screen.EventQ() <- tcell.NewEventInterrupt(nil):
	default:
	}
}

// animateUntil wakes the event loop every period until done closes — the
// redraw clock for a widgets.Spinner, which is drawn from elapsed time and has
// nothing else to repaint it while the work it stands for is quiet. The
// caller closes done, deferred inside the worker so a panic stops it too.
func (a *App) animateUntil(what string, period time.Duration, done <-chan struct{}) {
	a.safego(what, func() {
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				a.wakeEventLoop()
			}
		}
	})
}

// buildUI creates all UI components from tuikit building blocks.
func (a *App) buildUI() {
	a.explorer = NewObjectExplorer(a)
	a.explorer.SetActive(true)

	a.explorerSplit = layout.NewVerticalSplitter()
	a.explorerSplit.SetRatio(0.3)

	a.panels = layout.NewPanelManager()
	a.detailBrowser = a.newDetailBrowser()
	a.panels.AddPanel(a.detailBrowser)
	a.panels.OnCloseTab = a.requestClosePanel

	a.menuBar = controls.NewMenuBar()
	a.menuBar.SetMenus(a.buildMenus())
	// Rebuilt on every open, for Query > Compare with ▸: its submenu lists the
	// plans open right now, which no toggle's rebuild would keep current.
	a.menuBar.OnBeforeOpen = func() { a.menuBar.SetMenus(a.buildMenus()) }

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
	a.newCredentialDialog = registerDialog(a, NewNewCredentialDialog(a))
	a.sendTestMailDialog = registerDialog(a, NewSendTestMailDialog(a))
	a.newBackupDeviceDialog = registerDialog(a, NewNewBackupDeviceDialog(a))
	a.newAuditDialog = registerDialog(a, NewNewAuditDialog(a))
	a.newXESessionDialog = registerDialog(a, NewNewXESessionDialog(a))
	a.newAuditSpecificationDialog = registerDialog(a, NewNewAuditSpecificationDialog(a))
	a.newDBAuditSpecDialog = registerDialog(a, NewNewDatabaseAuditSpecificationDialog(a))
	a.newDBScopedCredDialog = registerDialog(a, NewNewDatabaseScopedCredentialDialog(a))
	a.newIndexDialog = registerDialog(a, NewNewIndexDialog(a))
	a.newStatisticsDialog = registerDialog(a, NewNewStatisticsDialog(a))
	a.newCMKDialog = registerDialog(a, NewNewColumnMasterKeyDialog(a))
	a.newCEKDialog = registerDialog(a, NewNewColumnEncryptionKeyDialog(a))
	a.newFullTextCatalogDialog = registerDialog(a, NewNewFullTextCatalogDialog(a))
	a.newFullTextStoplistDialog = registerDialog(a, NewNewFullTextStoplistDialog(a))
	a.newSearchPropListDialog = registerDialog(a, NewNewSearchPropertyListDialog(a))
	a.newFullTextIndexDialog = registerDialog(a, NewNewFullTextIndexDialog(a))
	a.logSearchDialog = registerDialog(a, NewLogSearchDialog(a))
	a.agAddDatabaseDialog = registerDialog(a, NewAGAddDatabaseDialog(a))
	a.agAddListenerDialog = registerDialog(a, NewAGAddListenerDialog(a))
	a.agAddReplicaDialog = registerDialog(a, NewAGAddReplicaDialog(a))
	a.newAGDialog = registerDialog(a, NewNewAGDialog(a))
	a.newEndpointDialog = registerDialog(a, NewNewEndpointDialog(a))
	a.newCertificateDialog = registerDialog(a, NewNewCertificateDialog(a))
	a.newUserDialog = registerDialog(a, NewNewUserDialog(a))
	a.newAsymmetricKeyDialog = registerDialog(a, NewNewAsymmetricKeyDialog(a))
	a.newSymmetricKeyDialog = registerDialog(a, NewNewSymmetricKeyDialog(a))
	a.backupCertificateDialog = registerDialog(a, NewBackupCertificateDialog(a))
	a.backupMasterKeyDialog = registerDialog(a, NewBackupMasterKeyDialog(a))
	a.regenerateMasterKeyDialog = registerDialog(a, NewRegenerateMasterKeyDialog(a))
	a.detachDatabaseDialog = registerDialog(a, NewDetachDatabaseDialog(a))
	a.attachDatabaseDialog = registerDialog(a, NewAttachDatabaseDialog(a))
	a.newSnapshotDialog = registerDialog(a, NewNewSnapshotDialog(a))
	a.fileDialog = registerDialog(a, dialogs.NewFileDialog(a.screen))
	a.fileDialog.OnConfirmOverwrite = func(path string, proceed func()) {
		// gosmo.ServerPathBase, not filepath.Base: the path uses the SQL Server
		// host's separator, so filepath.Base of `C:\Backup\db.bak` on Linux is
		// the whole string.
		a.confirmDialog.ShowConfirm("Confirm Save As",
			gosmo.ServerPathBase(path)+" already exists. Overwrite it?",
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
	a.progressDialog = registerDialog(a, dialogs.NewProgressDialog(a.screen))
	a.progressDialog.RevealDelay = progressRevealDelay
	// After the Connect dialog, which a device code opens over.
	a.deviceCodeDialog = registerDialog(a, NewDeviceCodeDialog(a))
	a.backupDialog = registerDialog(a, NewBackupDialog(a))
	a.restoreDialog = registerDialog(a, NewRestoreDialog(a))
}

// installEntraSignIn points the two things a Microsoft Entra sign-in shows
// the user at the TUI rather than at the process's standard output and
// error, which under tcell are the terminal it is drawing on: the device
// code, which goes to a's DeviceCodeDialog, and the output of the program
// MFA's sign-in opens a browser with (xdg-open, open, rundll32), which is
// dropped — a browser launched through xdg-open may print a line of its own,
// which would land on the screen at the cursor and stay there until the next
// full redraw.
func installEntraSignIn(a *App) {
	db.SetDeviceCodePrompt(a.promptDeviceCode)
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
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

// appFocus names which half of the window has the keyboard: Object Explorer or
// the panel area.
type appFocus int

const (
	focusOnExplorer appFocus = iota
	focusOnPanels
)

func (f appFocus) String() string {
	if f == focusOnPanels {
		return "panels"
	}
	return "explorer"
}

func (a *App) focusExplorer() {
	a.focus = focusOnExplorer
	a.explorer.SetActive(true)
	a.syncActivePanelFocus()
}

func (a *App) focusPanels() {
	a.focus = focusOnPanels
	a.explorer.SetActive(false)
	a.syncActivePanelFocus()
}

// syncActivePanelFocus keeps the visible panel's Activatable state (title bar
// highlight, cursor visibility) in sync with a.focus. PanelManager knows
// nothing about a.focus and calls SetActive only when its own active index
// changes, so anything that changes the active panel while a.focus stays
// focusOnExplorer (nextPanel/prevPanel) must call this, or the new panel shows as
// focused while Object Explorer holds real keyboard focus.
func (a *App) syncActivePanelFocus() {
	if p, ok := a.panels.ActivePanel().(layout.Activatable); ok {
		p.SetActive(a.focus == focusOnPanels)
	}
}

// cycleFocus advances keyboard focus one step (Ctrl+Tab): Object Explorer ->
// the active query panel's editor -> its results pane -> Object Explorer. A
// non-query panel, or a query panel with no results yet, offers no middle stop
// and degrades to a two-way explorer/panels toggle.
func (a *App) cycleFocus() {
	qp := a.activeQueryPanel()
	switch {
	case a.focus == focusOnExplorer:
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
	case a.focus == focusOnExplorer:
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
// focusOnExplorer.
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
// quitGate.mu is held across setting quitting and calling Fini(), so a racing
// wakeEventLoop either completes its send first or sees quitting and skips it,
// never sending on the closed channel.
//
// screen is nil for the minimal *App newTestApp builds without going through
// Run, and quitting is what everything else keys off, so it must be set either
// way. Same reasoning as setStatus's nil check.
func (a *App) quit() {
	a.quitGate.mu.Lock()
	defer a.quitGate.mu.Unlock()
	a.quitGate.quitting = true
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
