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
	contentRect   core.Rect
	expandBtnRect core.Rect // zero when OnExpand is nil

	plan      *showplan.Plan
	err       error // set by SetPlanXML on a parse failure
	stmtIdx   int
	activeTab Tab
	active    bool

	xml *controls.Editor // backs TabXML

	// selectedID is the ID of the operator selected in the Tree tab (and,
	// once built, the Plan/graph tab) — shared across both so switching
	// tabs keeps the same operator highlighted. -1 = nothing selected.
	selectedID int

	// searchSt is the Tree/Plan tabs' shared operator search state (see
	// search.go). showEstimated toggles ('p') whether a tile's row-count
	// line prefers the estimate over the actual count when both exist.
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
	// strip (visible from the start; graphSt.detailOpen toggles it off via
	// Enter) is a draggable graphSplit below the canvas, defaulting to
	// 70/30: "Properties" (detailLines for the selected node). There's no
	// Operator Summary here — it's Tree-tab-only, and its Cost % info is
	// folded into detailKVs.
	graphSt              graphState
	graphSplit           *layout.Splitter // divides the canvas from the Properties strip
	graphCanvasRect      core.Rect
	graphPropsHeaderRect core.Rect
	graphPropsRect       core.Rect
	graphPropsScroll     int

	// OnExpand, when set, shows a "[ Expand ]" button in the tab bar and is
	// called when it's clicked — the host decides what "open in a new
	// panel" means. Hidden entirely while nil.
	OnExpand func()
	// OnStatus, when set, is called with a one-line status message on
	// notable actions (statement switch, tab switch, ...).
	OnStatus func(msg string)
	// OnMissingIndex, when set, is called with the CREATE INDEX script for
	// every suggestion on the current statement when the missing-index
	// banner is activated (Enter, or a click on it) — SSMS's "Missing Index
	// Details...". The host decides where the script goes. The banner is
	// still drawn when this is nil, since it says something worth reading on
	// its own; it just can't be opened.
	OnMissingIndex func(script string)
	// OnCopyRequest, when set, is called with clipboard-ready text from the
	// operator summary's "Copy" menu item, and is what makes that item
	// appear at all (see controls.DataGrid.OnCopyRequest — the grid can't
	// reach the screen's clipboard itself, so the host does it). Wired by
	// QueryPanel and PlanPanel to App.writeClipboard.
	OnCopyRequest func(text string)

	// mouseDragging distinguishes a fresh Button1 press on the tab bar or
	// statement selector from a continued hold — mirrors Toolbar's/
	// TreeView's/MenuBar's field of the same name and purpose. Without it,
	// tcell's all-motion mouse tracking resends Buttons()==Button1 on
	// every motion event while the button stays down, so a click that so
	// much as twitches before release would re-fire OnExpand (opening a
	// second panel), switch tabs again, or step the statement selector
	// again, on every resent event instead of once per physical click.
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
	// A cell cursor, like every other read-only grid in the app: it's what
	// enables per-cell selection and the right-click / Ctrl+Space menu, and
	// so "Show Value" — the Status column carries an operator's full warning
	// text, which is exactly the sort of thing that gets clipped to the
	// column width and needs opening up. Copy is forwarded through
	// OnCopyRequest, which the host wires.
	v.summarySt.grid.SetCellCursor(true)
	v.graphSplit = layout.NewHorizontalSplitter("")
	v.graphSplit.SetRatio(0.7)
	v.graphSt.detailOpen = true // Properties strip visible from the start
	return v
}

// SetPlanXML parses xml and installs it as the displayed plan. On a parse
// error, the error is kept and rendered inline instead of the plan.
func (v *PlanView) SetPlanXML(xml string) error {
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

// SetPlan installs a plan the caller has already parsed, avoiding a
// re-parse (used by "[ Expand ]" to hand the same *showplan.Plan to a
// freshly created PlanView).
func (v *PlanView) SetPlan(p *showplan.Plan) {
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
	// searchSt.matches holds NodeIDs, which SQL Server assigns per statement
	// starting at 0 — near-certain to collide with unrelated operators in a
	// different plan (a re-run after an edit, an Estimated<->Actual toggle,
	// or QueryPanel simply reusing this same *PlanView for a new query).
	// Without resetting here, n/N after a plan swap can silently jump to
	// whatever operator now happens to own the stale ID.
	v.searchSt = searchState{}
	v.selectFirstNode()
	v.layout()
	v.syncFocus()
}

// Plan returns the currently displayed plan, or nil.
func (v *PlanView) Plan() *showplan.Plan { return v.plan }

// currentStatement returns the statement the statement selector is
// currently pointing at, or nil if no plan is loaded.
func (v *PlanView) currentStatement() *showplan.Statement {
	if v.plan == nil || v.stmtIdx < 0 || v.stmtIdx >= len(v.plan.Statements) {
		return nil
	}
	return v.plan.Statements[v.stmtIdx]
}

// selectedNode resolves selectedID against the current statement's tree,
// or nil if there's no selection or it no longer exists (e.g. after
// stepping to a different statement).
func (v *PlanView) selectedNode() *showplan.Node {
	st := v.currentStatement()
	if st == nil || v.selectedID < 0 {
		return nil
	}
	return nodeByID(st.Root, v.selectedID)
}

// selectFirstNode resets the selection to the current statement's root
// (or clears it, if the statement has no plan tree) and rebuilds the
// dependent tab state — called on load and on every statement switch.
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

// selectNode changes the selected operator and keeps every tab that
// depends on it in sync (tree scroll position, properties scroll).
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

// layout recomputes tabRect/stmtRect/contentRect from rect and the
// current plan (the statement bar only exists for a multi-statement
// plan), then re-bounds the XML editor to match.
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
	// The banner belongs to the statement on screen, so stepping the
	// statement selector re-runs layout (see stepStatement) rather than
	// leaving a row reserved for a statement that has no suggestion.
	if len(v.missingIndexes()) > 0 {
		v.bannerRect = core.Rect{X: v.rect.X, Y: y, W: v.rect.W, H: 1}
		y++
	} else {
		v.bannerRect = core.Rect{}
	}
	h := v.rect.Bottom() - y
	if h < 0 {
		h = 0
	}
	v.contentRect = core.Rect{X: v.rect.X, Y: y, W: v.rect.W, H: h}
	v.xml.SetBounds(v.contentRect.X, v.contentRect.Y, v.contentRect.W, v.contentRect.H)
	v.layoutTree()
	v.layoutGraphTab()
	// installPlan calls selectFirstNode before its own layout() call, and
	// that can't scroll anything into view yet: SetPlanXML/SetPlan are
	// routinely called before the host's first SetBounds, so
	// graphCanvasRect/treePaneRect are still zero. The first real rect this
	// control gets therefore re-applies "scroll the selection into view"
	// itself. A no-op on later resizes whenever it's already visible.
	v.ensureTreeRowVisible()
	v.ensureTileVisible(v.selectedID)
}

// SetActive marks the control as focused, showing the XML editor's cursor
// only while both the control has focus and XML is the active tab.
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
	// layout, not just the two ensure* calls it ends with: the missing-index
	// banner belongs to the statement on screen, so the row it occupies has
	// to be reclaimed when the next statement carries no suggestion. Its
	// ensure* tail is needed either way — selectFirstNode alone can leave the
	// new statement's root scrolled out of view, since an unbalanced tree's
	// root tile isn't necessarily near (0,0) and a rebuild that still fits
	// the pane doesn't reset its scroll offset.
	v.layout()
}

// statementCostPct returns statement i's share of the batch's total
// estimated subtree cost, as a percentage — 0 if the batch's total cost
// is zero.
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
