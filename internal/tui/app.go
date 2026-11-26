// Package tui is the gossms application layer. It wires the
// application-agnostic controls from github.com/radix29/tuikit into the
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
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/clipboard"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/controls/editor"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/layout"
	"github.com/radix29/tuikit/loop"
	"github.com/radix29/tuikit/theme"
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

	// drag is the Object Explorer node drag toward the query editor: armed by a
	// Button1 press over a draggable node, started past the threshold, dropped
	// on release (handleMouse, routeRelease, dropExplorerNode).
	drag core.Drag[*explorerNode]

	// buttonPress tracks whether Button1 is held, wherever the gesture
	// started. The per-widget latches only catch a resend staying inside one
	// widget; this lets handleMouse tell a drag that started elsewhere and
	// drifted across the status row from a fresh press there.
	buttonPress core.PressLatch

	// gestureOwner is the region that claimed the Button1 press currently held —
	// the App-level counterpart of
	// propsheet.PropertySheet.dragZone and QueryPanel.dragZone. Without it a
	// drag that wanders out of its region is handed to whatever it wanders
	// over: leftward out of a query editor arms an Object Explorer drop that
	// pastes a node's SQL on release, and crossing the splitter resizes the
	// panes. Released on ButtonNone.
	gestureOwner core.Gesture[appGestureOwner]

	// gestureOverlay holds the modal layer as it stood when the current
	// gesture began (overlaySnapshot), so handleMouse can drop held Button1
	// once a dialog, context menu or menu dropdown has opened or closed since.
	gestureOverlay core.OverlayGuard[overlayStack]

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

	// groupBySchema is View > Group by Schema, off at startup and never saved:
	// Object Explorer lists the main object folders one schema node per schema
	// (explorer_group_schema.go). Global to every server in the tree, as in SSMS.
	// Flipped by toggleGroupBySchema (View menu, Alt+G).
	groupBySchema bool

	// statusHistoryDialog records every status message from startup, open or
	// not, and logStatus feeds it from loader goroutines, so unlike every other
	// dialog (app_dialogs.go) it is built eagerly in buildUI.
	statusHistoryDialog *StatusHistoryDialog

	// modals holds every dialog built so far, registered by dialogOf, and the
	// live z-order of the open ones (see dialog_stack.go).
	modals dialogs.Stack

	// tasks is the background task registry (see tasks.go), wired by
	// wireTasks.
	tasks loop.Tasks

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

	// lastButtons is the button state of the previous mouse event, which
	// tells pointer motion (coalesced in Run) from a press or release.
	lastButtons tcell.ButtonMask

	// clipDispatcher moves text between the widgets and the system clipboard;
	// reach it through App.clip, which builds it.
	clipDispatcher *clipboard.Dispatcher

	// wake is the queue background goroutines hand results to the UI
	// goroutine through (see postAndWake), and the gate ordering quit's
	// screen.Fini() against a wake in flight (tuikit's loop.Poster).
	wake loop.Poster

	// reclaiming is true while releaseClosedPanelMemory's background
	// FreeOSMemory is running, so closing several panels in a row queues one
	// reclaim rather than one per panel — a second sweep over a heap the first
	// is still walking buys nothing and costs another full GC.
	reclaiming atomic.Bool
}

// NewApp constructs the application.
func NewApp() *App {
	a := new(App{
		focus:      focusOnExplorer,
		statusText: "Ready  |  F1 Help  |  Ctrl+N New Query  |  F9 Connect  |  Ctrl+Q Quit",
		cfg:        config.Load(),
	})
	a.wireTasks()
	// Editors built where the config is out of reach (the read-only
	// property-sheet T-SQL rows) pick the width up from this default; the ones
	// that can reach it call SetIndentWidth as well, so a live change from
	// Options reaches them without a restart — into the open query panels
	// directly, and into the open property sheets through
	// PropertySheet.SetEditorIndentWidth.
	editor.SetDefaultIndentWidth(a.cfg.IndentWidth)
	a.loadPeerCredentials()
	return a
}

// Run initialises the screen and enters the event loop.
func (a *App) Run() error {
	s, err := core.Init()
	if err != nil {
		return fmt.Errorf("init screen: %w", err)
	}
	a.setScreen(s)
	defer s.Fini()

	applyColorTheme(s, a.cfg.Theme)
	a.buildUI()
	a.layoutAll()
	installEntraSignIn(a)
	a.resealLegacyPasswords()
	// Open Connect on startup — nothing works without a server. modals.Sync
	// must run before the first draw: draw() renders the open dialogs, which are
	// otherwise synced only inside the event loop, so the dialog wouldn't
	// appear until the first keypress.
	a.connectDialog().Show()
	a.modals.Sync()
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
	// Every iteration, whatever woke it: Drain clears the wake coalescing
	// flag before taking the queue, so a post racing this instant still gets
	// its own wake (loop.Poster.Drain).
	a.drainPending()
	a.modals.Sync()

	// The clipboard's share first: a bracketed paste's markers and, between
	// them, every key — pasted content, not typing, or each pasted newline
	// arrives as KeyEnter and IntelliSense's commit binding eats it — and the
	// terminal's OSC 52 reply to a Ctrl+V. A buffered key changes nothing on
	// screen, and a frame per key made a 10,001-line, 170 KB paste take ~6
	// minutes; only keys skip it, so a lost end marker never freezes the screen.
	if consumed, skip := a.clip().HandleEvent(ev); skip {
		return false, skipFrame
	} else if !consumed {
		switch e := ev.(type) {
		case *tcell.EventResize:
			a.screen.Sync()
			a.layoutAll()
		case *tcell.EventInterrupt:
			// triggered after background goroutine posts result
		case *tcell.EventKey:
			if a.handleKey(e) {
				return true, skipFrame
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
		}
	}

	// Re-sync before drawing: the event just handled may have opened or
	// closed a dialog, and draw() renders straight from modals.Open(). The
	// top-of-loop sync still runs so input routing sees drainPending's
	// changes.
	a.modals.Sync()
	a.abandonStaleFormat()
	a.explorer.syncActiveDatabase()
	return false, need
}

// wheelButtons is every wheel direction in a tcell.ButtonMask — wheel notches
// arrive as repeated identical masks, but each is a discrete scroll, not motion.
const wheelButtons = tcell.WheelUp | tcell.WheelDown | tcell.WheelLeft | tcell.WheelRight

// postAndWake queues fn to run on the UI goroutine and immediately wakes the
// event loop, without waiting for an unrelated key or mouse event. Call it from
// a background goroutine, never from the UI goroutine (see wakeEventLoop).
//
// This is how every background operation in internal/tui reports its result,
// and the only way one should: writing the two calls out by hand invites
// nesting the wakeup inside the very closure waiting to be drained, which never
// fires. Also the shared body behind PropDialog.post and every
// New*Dialog.post.
func (a *App) postAndWake(fn func()) { a.wake.Post(fn) }

// drainPending runs the callbacks background goroutines have posted. handleEvent
// calls it at the top of every iteration; tests without an event loop call it
// by hand.
func (a *App) drainPending() { a.wake.Drain() }

// wakeEventLoop nudges the event loop to run one more iteration, draining
// queued callbacks and redrawing, with nothing to post — the one caller that
// needs this alone is animateUntil's spinner clock (QueryPanel's elapsed timer
// among them). Everything with a result uses postAndWake. Never from the UI
// goroutine. A no-op without a screen (every App from newTestApp, so a
// goroutine outliving its test never panics), after quit, and while an earlier
// wake is undrained; the send never blocks, so a full EventQ() under
// all-motion mouse tracking can't hang Ctrl+Q. loop.Poster.Wake has the why of
// each.
func (a *App) wakeEventLoop() { a.wake.Wake() }

// animateUntil wakes the event loop every period until done closes — the
// redraw clock for a widgets.Spinner, which is drawn from elapsed time and has
// nothing else to repaint it while the work it stands for is quiet
// (loop.Runner.Tick). The caller closes done, deferred inside the worker so a
// panic stops it too.
func (a *App) animateUntil(what string, period time.Duration, done <-chan struct{}) {
	a.runner().Tick(what, period, done, a.wakeEventLoop)
}

// buildUI creates all UI components from tuikit building blocks.
func (a *App) buildUI() {
	a.explorer = NewObjectExplorer(a)
	a.explorer.Focus(true)

	a.explorerSplit = newVerticalSplitter()

	a.panels = layout.NewPanelManager()
	a.panels.EmptyText = "(no panels open — Ctrl+N for a new query)"
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

	// Every other dialog is built on first use (app_dialogs.go).
	a.statusHistoryDialog = NewStatusHistoryDialog(a)
	a.modals.Register(a.statusHistoryDialog)
}

// newVerticalSplitter is the app's explorer | panels split: the explorer
// starts at 30% and can take at most 80%. Every top/bottom split (an editor
// or grid over results/details) is a layout.NewStackSplitter.
func newVerticalSplitter() *layout.Splitter {
	sp := layout.NewSplitter(layout.SplitterVertical, 0.3)
	sp.SetLimits(0.1, 0.8)
	return sp
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

// layoutAll recalculates every region from current screen size.
func (a *App) layoutAll() {
	// Dialogs first, and above the too-small guard: a dialog that outgrows the
	// terminal draws its borders and button row off-screen while still taking
	// every key, so the smallest sizes are where re-fitting matters most.
	a.modals.Relayout()

	w, h := a.screen.Size()
	if w < 20 || h < 5 {
		return
	}
	const menuH, statusH = 1, 1
	contentH := h - menuH - statusH

	a.menuBar.SetBounds(0, 0, w)
	labelsEnd := a.menuBar.LabelsEnd() + 1 // one blank column after Help
	a.toolbar.SetBounds(labelsEnd, 0, w-labelsEnd)
	a.explorerSplit.SetBounds(0, menuH, w, contentH)

	// A hidden explorer is a collapsed first pane: an empty FirstRect, and
	// the panels get the whole width.
	left := a.explorerSplit.FirstRect()
	right := a.explorerSplit.SecondRect()
	a.explorer.SetBounds(left.X, left.Y, left.W, left.H)
	a.panels.SetBounds(right.X, right.Y, right.W, right.H)
}

func (a *App) draw() {
	s := a.screen
	w, h := s.Size()
	s.Clear()
	// Clear leaves the terminal cursor where it was. An Editor shows it while
	// it draws active and nothing else ever hides it, so without this the
	// cursor stayed at the caret of an editor focus had left (the query
	// editor after Ctrl+Tab, Connect's Custom Properties after Tab) on every
	// later frame. Whatever is active this frame shows it again below.
	s.HideCursor()

	a.menuBar.Draw(s)
	a.toolbar.Draw(s)
	a.explorerSplit.Draw(s)
	if !a.explorerHidden() {
		a.explorer.Draw(s)
	}
	a.panels.Draw(s)

	// Status bar
	const statusH = 1
	statusStyle := theme.StyleStatusBar()
	core.FillRect(s, core.Rect{X: 0, Y: h - statusH, W: w, H: statusH}, ' ', statusStyle)
	connInfo := ""
	if n := len(a.connections); n > 0 {
		connInfo = fmt.Sprintf("  |  %d server%s connected", n, pluralSuffix(n))
	}
	if n := a.tasks.Running(); n > 0 {
		connInfo += fmt.Sprintf("  |  %d task%s running", n, pluralSuffix(n))
	}
	// DrawTextLine, not DrawTextClipped: a status can carry an error joined
	// from several (a write's, then the failed repair after it — see
	// taskRepairNote), and DrawTextClipped drops the line break, running the
	// second message into the first without a space.
	core.DrawTextLine(s, 1, h-statusH, w-2, statusStyle, a.statusText+connInfo)

	// Overlays last, so the panels and status bar don't paint over the rows the
	// menu dropdown and context menu extend into.
	a.menuBar.DrawOverlay(s)
	a.toolbar.DrawOverlay(s)
	if !a.explorerHidden() {
		a.explorer.DrawOverlay(s)
	}
	a.drawDragGhost(s, w)

	// Modal dialogs, drawn bottom-to-top so a nested dialog paints over its
	// parent. An open one has the keyboard, so the query editor beneath it,
	// still drawn active, must not leave its cursor blinking inside the
	// dialog; an editor in the dialog shows it again.
	if a.modals.Top() != nil {
		s.HideCursor()
	}
	a.modals.Draw(s)

	// The context menu tops everything: it can open over a dialog's field.
	a.contextMenu.DrawOverlay(s)

	s.Show()
}

// quit tears the screen down unconditionally — Fini closes EventQ(), ending
// Run's loop and making the channel unusable. User actions want requestQuit
// instead, which offers to save unsaved query panels first.
//
// a.wake.Close marks the app quitting and calls Fini() under the lock a wake
// sends under, so a racing wakeEventLoop either completes its send first or
// sees the close and skips it, never sending on the closed channel.
//
// screen is nil for the minimal *App newTestApp builds without going through
// Run, and quitting (a.wake.Closed) is what everything else keys off, so it
// must be set either way. Same reasoning as setStatus's nil check.
func (a *App) quit() {
	a.wake.Close(func() {
		if a.screen != nil {
			a.screen.Fini()
		}
	})
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
