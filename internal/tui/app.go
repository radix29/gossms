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
	"sync"

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

	explorer      *ObjectExplorer
	panels        *layout.PanelManager
	menuBar       *controls.MenuBar
	toolbar       *controls.Toolbar
	contextMenu   *controls.ContextMenu
	statusText    string
	queryPanelCnt int

	connectDialog   *ConnectDialog
	helpDialog      *HelpDialog
	keyDiagDialog   *KeyDiagnosticsDialog
	propsDialog     *PropertiesDialog
	propDialog      *PropDialog
	pathPrompt      *PathPromptDialog
	queryListDialog *QueryListDialog
	optionsDialog   *OptionsDialog
	tasksDialog     *TasksDialog
	confirmDialog   *dialogs.ConfirmDialog

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

	// Focus: "explorer" | "panels"
	focus string

	pendingMu sync.Mutex
	pending   []func()
}

// NewApp constructs the application.
func NewApp() *App {
	return new(App{
		focus:      "explorer",
		statusText: "Ready  |  F1 Help  |  Ctrl+N New Query  |  Ctrl+O Connect  |  Ctrl+Q Quit",
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
	a.draw()

	// tcell v3's Screen interface has no PollEvent/PostEvent methods; events
	// are delivered and posted through a plain channel, EventQ(). The
	// channel is closed by Fini(), so `for ev := range` exits automatically
	// on quit instead of needing a nil-event sentinel check.
	for ev := range s.EventQ() {
		a.drainPending()
		a.syncDialogStack()

		switch e := ev.(type) {
		case *tcell.EventResize:
			s.Sync()
			a.layoutAll()
		case *tcell.EventInterrupt:
			// triggered after background goroutine posts result
		case *tcell.EventKey:
			if a.handleKey(e) {
				return nil
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
		// closed a dialog (a keystroke or menu click calling Show()/Hide()),
		// and draw() renders straight from dialogStack. Without this the new
		// dialog wouldn't appear — nor a closed one vanish — until the next
		// event happened to arrive. The top-of-loop sync still runs so input
		// routing sees changes made by drainPending's callbacks.
		a.syncDialogStack()
		a.draw()
	}
	return nil
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
// deadlock (the loop can't read EventQ while it's mid-dispatch). Mirrors
// the postEvent+EventInterrupt handoff every other background operation
// here uses (see app_connections.go, tasks.go, query_panel.go).
//
// A no-op when a.screen is nil, which every App built by the tui package's
// own newTestApp helper is — "no screen, no event loop" by design. Without
// this guard, a background goroutine from an async action exercised in such
// a test (connectForQueryPanel, startTask/postTaskDone, ...) can still be
// running after its test function has already returned, and calling this
// then would panic on the nil screen — nothing waits for these goroutines
// to finish, so whether that happens before or after the test binary exits
// is a timing accident, one `go test -race` (which slows everything down)
// made an intermittent, hard-to-reproduce crash.
func (a *App) wakeEventLoop() {
	if a.screen == nil {
		return
	}
	a.screen.EventQ() <- tcell.NewEventInterrupt(nil)
}

// buildUI creates all UI components from tuikit building blocks.
func (a *App) buildUI() {
	a.explorer = NewObjectExplorer(a)
	a.explorer.SetActive(true)

	a.explorerSplit = layout.NewVerticalSplitter()
	a.explorerSplit.SetRatio(0.3)

	a.panels = layout.NewPanelManager()
	a.panels.AddPanel(NewDetailBrowser("Object Explorer Details"))
	a.panels.OnCloseTab = a.requestClosePanel

	a.menuBar = controls.NewMenuBar()
	a.menuBar.SetMenus(a.buildMenus())

	a.toolbar = controls.NewToolbar()
	a.toolbar.SetButtons(a.buildToolbar())

	a.contextMenu = new(controls.ContextMenu{})
	a.connectDialog = NewConnectDialog(a)
	a.helpDialog = NewHelpDialog(a)
	a.keyDiagDialog = NewKeyDiagnosticsDialog(a)
	a.propsDialog = NewPropertiesDialog(a)
	a.propDialog = NewPropDialog(a)
	a.pathPrompt = NewPathPromptDialog(a)
	a.queryListDialog = NewQueryListDialog(a)
	a.optionsDialog = NewOptionsDialog(a)
	a.tasksDialog = NewTasksDialog(a)
	a.confirmDialog = dialogs.NewConfirmDialog(a.screen)

	// Registration order only matters as a tie-break for dialogs that
	// somehow became visible in the same tick (today, never — each Show()
	// is one synchronous call from one key/menu action); see syncDialogStack.
	a.allDialogs = []Dialog{
		a.connectDialog, a.helpDialog, a.keyDiagDialog, a.propsDialog, a.propDialog,
		a.pathPrompt, a.queryListDialog, a.optionsDialog, a.tasksDialog,
		a.confirmDialog,
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
// calls SetActive when its own active index changes (a tab switch); it has
// no idea a.focus even exists. So anything that can leave a.focus ==
// "explorer" while also changing which panel is active — nextPanel/
// prevPanel in particular — must call this too, or the newly-selected panel
// would show itself as focused while Object Explorer still holds real
// keyboard focus.
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
	if len(a.connections) > 0 {
		connInfo = fmt.Sprintf("  |  %d connection(s)", len(a.connections))
	}
	if n := a.runningTaskCount(); n > 0 {
		connInfo += fmt.Sprintf("  |  %d task(s) running", n)
	}
	core.DrawTextClipped(s, 1, h-statusH, w-2, statusStyle, a.statusText+connInfo)

	// Overlays — drawn last so they aren't painted over by panels/status bar,
	// which occupy the same rows the menu dropdown and context menu extend into.
	a.menuBar.DrawOverlay(s)
	a.toolbar.DrawOverlay(s)
	a.contextMenu.Draw(s)

	// Modal dialogs — highest z-order; dialogStack is kept current by
	// syncDialogStack (see Run), so drawing it bottom-to-top here is
	// enough to paint a nested dialog over its parent.
	a.drawDialogs(s)

	s.Show()
}

func (a *App) quit()                { a.screen.Fini() }
func (a *App) setStatus(msg string) { a.statusText = msg }
