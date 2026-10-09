package planview

import (
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Tab selects which visualization PlanView is currently showing.
type Tab int

const (
	TabPlan Tab = iota // graphical operator plan (default)
	TabTree            // expandable operator tree
	TabXML             // raw plan XML, read-only
)

var tabLabels = [...]string{"Plan", "Tree", "XML"}

// PlanView renders a parsed execution plan as a tabbed control: a
// graphical plan, an expandable tree, and the raw XML. See doc.go.
type PlanView struct {
	rect          core.Rect
	tabRect       core.Rect
	stmtRect      core.Rect
	bannerRect    core.Rect // missing-index banner; zero when the statement has none
	liveRect      core.Rect // live mode's progress row; zero outside live mode
	contentRect   core.Rect
	expandBtnRect core.Rect // zero when OnExpand is nil

	plan      *showplan.Plan
	err       error // set by SetPlanXML on a parse failure
	stmtIdx   int
	activeTab Tab
	active    bool

	xml *controls.Editor // backs TabXML

	// liveOn and live are live mode's state (see live.go): set by SetLive,
	// cleared by SetPlan/SetPlanXML. live may be empty while liveOn — the
	// plan has arrived but no counters yet.
	liveOn   bool
	live     map[int]showplan.LiveCounters
	liveNote string // SetLiveNote's text, "" for the waiting notes

	// selectedID is the selected operator's ID, shared by the Tree and Plan
	// tabs so switching keeps it highlighted. -1 = none.
	selectedID int

	// searchSt is the shared operator search state (see search.go).
	// showEstimated ('p') makes a tile's row-count line prefer the estimate
	// over the actual count.
	searchSt      searchState
	showEstimated bool

	// Tree tab (TabTree) state — see tree.go, details.go, summary.go.
	treeSt             treeState
	treeSplit          *layout.Splitter // divides the tree pane from the details pane
	treeHeaderRect     core.Rect        // statement metrics row
	treePaneRect       core.Rect
	detailsPaneRect    core.Rect // whole right-of-splitter pane (header + content)
	detailsHeaderRect  core.Rect
	detailsContentRect core.Rect
	detailsScroll      int
	bottomMode         bottomMode // hidden / properties / summary — cycled by 'o'
	bottomFocused      bool       // Tab-toggled; keyboard focus on the operator summary table rather than the tree — meaningful only in the Tree tab's bottomSummary mode (the Plan tab has no summary table)
	bottomHeaderRect   core.Rect
	bottomRect         core.Rect
	propsSt            propsState
	summarySt          summaryState

	// Plan tab (TabPlan) state — see graph.go, graph_layout.go. The detail
	// strip (open initially; Enter toggles graphSt.detailOpen) is a draggable
	// graphSplit below the canvas, default 70/30: "Properties" (detailLines
	// for the selected node). No Operator Summary here (Tree-tab-only); its
	// Cost % is folded into detailKVs.
	graphSt              graphState
	graphSplit           *layout.Splitter // divides the canvas from the Properties strip
	graphCanvasRect      core.Rect
	graphPropsHeaderRect core.Rect
	graphPropsRect       core.Rect
	graphPropsScroll     int

	// OnExpand, when set, shows a "[ Expand ]" button in the tab bar and is
	// called on click (the host decides what it opens). Hidden while nil.
	OnExpand func()
	// OnStatus, when set, is called with a one-line status message on
	// notable actions (statement switch, tab switch, ...).
	OnStatus func(msg string)
	// OnMissingIndex, when set, is called with the CREATE INDEX script for
	// every suggestion on the statement when the banner is activated (Enter
	// or click): SSMS's "Missing Index Details...". The banner is drawn even
	// when nil; it just can't be opened.
	OnMissingIndex func(script string)
	// OnCopyRequest, when set, receives clipboard-ready text from the summary's
	// "Copy" menu item and is what makes that item appear (see
	// controls.DataGrid.OnCopyRequest). Wired by QueryPanel and PlanPanel to
	// App.writeClipboard.
	OnCopyRequest func(text string)
	// OnContextMenu, when set, is called with the pointer's screen position on
	// a right-click in the Plan tab's canvas or the Tree tab's operator pane —
	// the host's plan menu (Compare Showplan...). Elsewhere a right-click keeps
	// its own meaning: the operator summary's cell menu, the XML editor's.
	OnContextMenu func(x, y int)

	// mouseDragging distinguishes a fresh Button1 press on the tab bar or
	// statement selector from a continued hold (as Toolbar/TreeView/MenuBar).
	// Without it, tcell's all-motion tracking resends Button1 on every motion
	// while held, so a twitching click would re-fire OnExpand (a second
	// panel), switch tabs or step the selector per event, not once per click.
	mouseDragging bool
}

// New creates an empty PlanView. Call SetPlanXML or SetPlan to load a plan.
func New() *PlanView {
	v := new(PlanView{activeTab: TabPlan, selectedID: -1})
	v.xml = controls.NewEditor(controls.XMLHighlighter(theme.Active()))
	v.xml.SetReadOnly(true)
	v.treeSplit = layout.NewVerticalSplitter()
	v.treeSplit.SetRatio(0.55) // tree gets more room than the details pane
	v.treeSt.collapsed = make(map[int]bool)
	v.summarySt.grid = controls.NewDataGrid()
	// A cell cursor, as every read-only grid: it enables per-cell selection,
	// the right-click / Ctrl+Space menu and "Show Value" (the Status column's
	// full warning text gets clipped). Copy goes through OnCopyRequest.
	v.summarySt.grid.SetCellCursor(true)
	v.graphSplit = layout.NewHorizontalSplitter("")
	v.graphSplit.SetRatio(0.7)
	v.graphSt.detailOpen = true // Properties strip visible from the start
	return v
}

// SetPlanXML parses xml and installs it as the displayed plan. On a parse
// error, the error is kept and rendered inline instead of the plan.
func (v *PlanView) SetPlanXML(xml string) error {
	v.clearLive()
	plan, err := showplan.Parse([]byte(xml))
	if err != nil {
		v.plan = nil
		v.err = err
		v.layout()
		return err
	}
	v.installPlan(plan)
	return nil
}

// SetPlan installs an already-parsed plan (e.g. "[ Expand ]" hands the same
// *showplan.Plan to a new PlanView).
func (v *PlanView) SetPlan(p *showplan.Plan) {
	v.clearLive()
	v.installPlan(p)
}

func (v *PlanView) installPlan(p *showplan.Plan) {
	v.plan = p
	v.err = nil
	v.stmtIdx = 0
	v.activeTab = TabPlan
	v.xml.SetText(showplan.Indent(p.XML))
	v.bottomMode = bottomHidden
	v.treeSt.collapsed = make(map[int]bool)
	// searchSt.matches holds NodeIDs, assigned per statement from 0, so they
	// collide with unrelated operators in a different plan (re-run, an
	// Estimated<->Actual toggle, QueryPanel reusing this *PlanView). Without
	// this reset, n/N after a swap jumps to whatever now owns the stale ID.
	v.searchSt = searchState{}
	v.selectFirstNode()
	v.layout()
	v.syncFocus()
}

// Plan returns the currently displayed plan, or nil.
func (v *PlanView) Plan() *showplan.Plan { return v.plan }

// currentStatement returns the selected statement, or nil if no plan is loaded.
func (v *PlanView) currentStatement() *showplan.Statement {
	if v.plan == nil || v.stmtIdx < 0 || v.stmtIdx >= len(v.plan.Statements) {
		return nil
	}
	return v.plan.Statements[v.stmtIdx]
}

// selectedNode resolves selectedID against the statement's tree, or nil if
// none or it no longer exists (e.g. after changing statement).
func (v *PlanView) selectedNode() *showplan.Node {
	st := v.currentStatement()
	if st == nil || v.selectedID < 0 {
		return nil
	}
	return nodeByID(st.Root, v.selectedID)
}

// selectFirstNode selects the statement's root (or clears the selection if no
// plan tree) and rebuilds dependent tab state, on load and statement switch.
func (v *PlanView) selectFirstNode() {
	st := v.currentStatement()
	if st != nil && st.Root != nil {
		v.selectedID = st.Root.ID
	} else {
		v.selectedID = -1
	}
	v.rebuildTreeRows()
	v.rebuildSummaryRows()
	v.rebuildGraphLayout()
	v.propsSt.scroll = 0
	v.detailsScroll = 0
	v.graphPropsScroll = 0
}

// nodeByID returns the node with the given ID in root's subtree, or nil.
func nodeByID(root *showplan.Node, id int) *showplan.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, c := range root.Children {
		if n := nodeByID(c, id); n != nil {
			return n
		}
	}
	return nil
}

// SelectNode shows statement stmt (an index into Plan().Statements) and selects
// its operator id, expanding any collapsed ancestor so the Tree tab shows it
// (how a host opens a plan "at" an operator, e.g. Compare Showplan's Enter).
// Both are needed because NodeIds are numbered per statement. It reports false,
// changing nothing, when the statement or operator doesn't exist.
func (v *PlanView) SelectNode(stmt, id int) bool {
	if v.plan == nil || stmt < 0 || stmt >= len(v.plan.Statements) ||
		nodeByID(v.plan.Statements[stmt].Root, id) == nil {
		return false
	}
	if stmt != v.stmtIdx {
		v.stmtIdx = stmt
		v.selectFirstNode()
		v.layout() // the missing-index banner row; see stepStatement
	}
	v.revealAndSelect(id)
	return true
}

// SelectedOperator returns the statement on screen and the operator selected in
// it — what SelectNode sets — with id -1 when the statement has no operator.
func (v *PlanView) SelectedOperator() (stmt, id int) { return v.stmtIdx, v.selectedID }

// selectNode changes the selected operator and syncs dependent tab state
// (tree and properties scroll).
func (v *PlanView) selectNode(id int) {
	if v.selectedID == id {
		return
	}
	v.selectedID = id
	v.propsSt.scroll = 0
	v.detailsScroll = 0
	v.graphPropsScroll = 0
	v.ensureTreeRowVisible()
	v.ensureTileVisible(id)
}

// SetBounds positions the control and lays out its tab/statement bars and
// content area.
func (v *PlanView) SetBounds(x, y, w, h int) {
	v.rect = core.Rect{X: x, Y: y, W: w, H: h}
	v.layout()
}

// layout recomputes tabRect/stmtRect/contentRect from rect and the plan (the
// statement bar exists only for multi-statement plans), then re-bounds the XML
// editor.
func (v *PlanView) layout() {
	y := v.rect.Y
	v.tabRect = core.Rect{X: v.rect.X, Y: y, W: v.rect.W, H: 1}
	y++
	if v.plan != nil && len(v.plan.Statements) > 1 {
		v.stmtRect = core.Rect{X: v.rect.X, Y: y, W: v.rect.W, H: 1}
		y++
	} else {
		v.stmtRect = core.Rect{}
	}
	// The banner belongs to the statement on screen, so stepStatement re-runs
	// layout rather than leave a row reserved for one with no suggestion.
	if len(v.missingIndexes()) > 0 {
		v.bannerRect = core.Rect{X: v.rect.X, Y: y, W: v.rect.W, H: 1}
		y++
	} else {
		v.bannerRect = core.Rect{}
	}
	if v.liveOn {
		v.liveRect = core.Rect{X: v.rect.X, Y: y, W: v.rect.W, H: 1}
		y++
	} else {
		v.liveRect = core.Rect{}
	}
	h := max(v.rect.Bottom()-y, 0)
	v.contentRect = core.Rect{X: v.rect.X, Y: y, W: v.rect.W, H: h}
	v.xml.SetBounds(v.contentRect.X, v.contentRect.Y, v.contentRect.W, v.contentRect.H)
	v.layoutTree()
	v.layoutGraphTab()
	// installPlan calls selectFirstNode before layout(), which can't scroll
	// into view yet: SetPlanXML/SetPlan usually precede the host's first
	// SetBounds, so graphCanvasRect/treePaneRect are still zero. The first
	// real rect re-applies "scroll the selection into view"; a no-op on later
	// resizes while it's visible.
	v.ensureTreeRowVisible()
	v.ensureTileVisible(v.selectedID)
}

// SetActive marks the control focused; the XML cursor shows only when focused
// and XML is the active tab.
func (v *PlanView) SetActive(active bool) {
	v.active = active
	v.syncFocus()
}

func (v *PlanView) syncFocus() {
	v.xml.SetActive(v.active && v.activeTab == TabXML)
}

// setActiveTab switches tabs, if t differs from the current one.
func (v *PlanView) setActiveTab(t Tab) {
	if v.activeTab == t {
		return
	}
	v.activeTab = t
	v.syncFocus()
	if v.OnStatus != nil {
		v.OnStatus(tabLabels[t] + " view selected")
	}
}

// stepStatement moves the statement selector by delta, wrapping around.
// A no-op for a plan with fewer than two statements.
func (v *PlanView) stepStatement(delta int) {
	if v.plan == nil || len(v.plan.Statements) < 2 {
		return
	}
	n := len(v.plan.Statements)
	v.stmtIdx = ((v.stmtIdx+delta)%n + n) % n
	v.selectFirstNode()
	// layout, not just its trailing ensure* calls: the missing-index banner
	// belongs to the statement on screen, so its row must be reclaimed when
	// the next statement has no suggestion. The ensure* tail is needed either
	// way: selectFirstNode alone can leave the new root scrolled out of view,
	// since an unbalanced tree's root tile isn't necessarily near (0,0) and a
	// rebuild that still fits the pane doesn't reset scroll.
	v.layout()
}

// statementCostPct returns statement i's percentage of the batch's total
// estimated subtree cost, 0 if that total is zero.
func (v *PlanView) statementCostPct(i int) float64 {
	var total float64
	for _, st := range v.plan.Statements {
		total += st.SubTreeCost
	}
	if total <= 0 {
		return 0
	}
	return v.plan.Statements[i].SubTreeCost / total * 100
}
