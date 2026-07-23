package planview

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
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
	// 70/30: "Properties" (detailLines for the selected node). No Operator
	// Summary here — that stayed Tree-tab-only; its Cost % info was folded
	// into detailKVs instead of duplicating the grid in a second tab.
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
	v.xml = controls.NewEditor(nil)
	v.xml.SetReadOnly(true)
	v.treeSplit = layout.NewVerticalSplitter()
	v.treeSplit.SetRatio(0.55) // tree gets more room than the details pane
	v.treeSt.collapsed = make(map[int]bool)
	v.summarySt.grid = controls.NewDataGrid()
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
	h := v.rect.Bottom() - y
	if h < 0 {
		h = 0
	}
	v.contentRect = core.Rect{X: v.rect.X, Y: y, W: v.rect.W, H: h}
	v.xml.SetBounds(v.contentRect.X, v.contentRect.Y, v.contentRect.W, v.contentRect.H)
	v.layoutTree()
	v.layoutGraphTab()
	// installPlan calls selectFirstNode (which sets selectedID but can't
	// scroll anything into view yet — SetPlanXML/SetPlan are routinely
	// called before the host's first SetBounds, so graphCanvasRect/
	// treePaneRect are still zero at that point) before its own layout()
	// call, so the very first real rect this control ever gets needs to
	// re-apply "scroll the current selection into view" itself, once
	// there's an actual rect to scroll within. Cheap and correct on every
	// later resize too: a no-op whenever the selection is already visible.
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

// Draw renders the tab bar, statement selector (when shown), and the
// active tab's content.
func (v *PlanView) Draw(s tcell.Screen) {
	v.drawTabBar(s)
	v.drawStatementBar(s)
	switch {
	case v.err != nil:
		v.drawMessage(s, fmt.Sprintf("Error parsing execution plan: %v", v.err))
	case v.plan == nil:
		v.drawMessage(s, "No execution plan loaded")
	case v.activeTab == TabXML:
		v.xml.Draw(s)
	case v.activeTab == TabTree:
		v.drawTreeTab(s)
	default: // TabPlan
		v.drawGraphTab(s)
	}
}

// drawMessage fills the content area with a single line of placeholder
// or error text.
func (v *PlanView) drawMessage(s tcell.Screen, msg string) {
	st := theme.StylePanel()
	core.FillRect(s, v.contentRect, ' ', st)
	if v.contentRect.H > 0 && v.contentRect.W > 2 {
		core.DrawTextClipped(s, v.contentRect.X+1, v.contentRect.Y, v.contentRect.W-2, st, msg)
	}
}

// tabSegments computes each Plan/Tree/XML tab's on-screen extent. Draw and
// hit-test both build their column math from this same call so hits line up
// with what's actually on screen.
func (v *PlanView) tabSegments() [][]controls.TabSegment {
	widths := make([][]int, len(tabLabels))
	for i, label := range tabLabels {
		widths[i] = []int{controls.TabLabelWidth(label)}
	}
	return controls.TabStripSegments(v.tabRect.X+1, widths, v.tabRect.Right())
}

// drawTabBar renders the Plan/Tree/XML tabs and, when OnExpand is set, a
// right-aligned "[ Expand ]" button.
func (v *PlanView) drawTabBar(s tcell.Screen) {
	pal := theme.Active()
	bar := theme.StyleMenuBar()
	core.FillRect(s, v.tabRect, ' ', bar)
	col := v.tabRect.X + 1
	for i, seg := range v.tabSegments() {
		st := bar
		if Tab(i) == v.activeTab {
			st = tcell.StyleDefault.Background(pal.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
		}
		core.DrawText(s, seg[0].X, v.tabRect.Y, st, " "+tabLabels[i]+" ")
		col = seg[0].X + seg[0].W + 1
	}
	v.expandBtnRect = core.Rect{}
	if search := v.searchIndicatorText(); search != "" {
		w := core.DisplayWidth(search)
		x := v.tabRect.Right() - w - 1
		if x > col {
			core.DrawText(s, x, v.tabRect.Y, bar, search)
		}
		return // the search indicator takes priority over Expand
	}
	if v.OnExpand != nil {
		label := "[ Expand ]"
		w := core.DisplayWidth(label)
		x := v.tabRect.Right() - w - 1
		if x > col {
			core.DrawText(s, x, v.tabRect.Y, bar, label)
			v.expandBtnRect = core.Rect{X: x, Y: v.tabRect.Y, W: w, H: 1}
		}
	}
}

// searchIndicatorText returns the tab bar's right-aligned search
// display: "/query_" while typing, "/query (i/n)" once confirmed with
// matches, or "" when there's nothing to show.
func (v *PlanView) searchIndicatorText() string {
	switch {
	case v.searchSt.active:
		return "/" + v.searchSt.query + "_"
	case len(v.searchSt.matches) > 0:
		return fmt.Sprintf("/%s (%d/%d)", v.searchSt.query, v.searchSt.idx+1, len(v.searchSt.matches))
	}
	return ""
}

// tabAt returns the tab index at screen column mx on the tab bar, or -1.
func (v *PlanView) tabAt(mx int) int {
	for i, seg := range v.tabSegments() {
		if mx >= seg[0].X && mx < seg[0].X+seg[0].W {
			return i
		}
	}
	return -1
}

// arrowRects returns the ◀ and ▶ hit-test rectangles for the statement
// selector, matching drawStatementBar's column layout exactly.
func (v *PlanView) arrowRects() (prev, next core.Rect) {
	x0 := v.stmtRect.X + 1
	mid := fmt.Sprintf(" Statement %d/%d ", v.stmtIdx+1, len(v.plan.Statements))
	x1 := x0 + core.DisplayWidth("◀")
	x2 := x1 + core.DisplayWidth(mid)
	return core.Rect{X: x0, Y: v.stmtRect.Y, W: x1 - x0, H: 1},
		core.Rect{X: x2, Y: v.stmtRect.Y, W: core.DisplayWidth("▶"), H: 1}
}

// drawStatementBar renders the "◀ Statement i/n ▶  cost%  statement text"
// row, shown only for a multi-statement plan (see layout).
func (v *PlanView) drawStatementBar(s tcell.Screen) {
	if v.stmtRect.H != 1 {
		return
	}
	pal := theme.Active()
	st := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Text)
	core.FillRect(s, v.stmtRect, ' ', st)

	stmt := v.plan.Statements[v.stmtIdx]
	prev, next := v.arrowRects()
	core.DrawText(s, prev.X, prev.Y, st, "◀")
	core.DrawText(s, prev.X+prev.W, prev.Y, st,
		fmt.Sprintf(" Statement %d/%d ", v.stmtIdx+1, len(v.plan.Statements)))
	core.DrawText(s, next.X, next.Y, st, "▶")

	rest := fmt.Sprintf("  %.0f%%  %s", v.statementCostPct(v.stmtIdx), oneLine(stmt.Text))
	restX := next.X + next.W
	if w := v.stmtRect.Right() - restX; w > 0 {
		core.DrawTextClipped(s, restX, v.stmtRect.Y, w, st, rest)
	}
}

// oneLine collapses embedded line breaks and repeated whitespace in a
// multi-line statement (e.g. a formatted CREATE VIEW body) to a single
// display line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

// HandleKey switches tabs (1/2/3), pages the statement selector ([/]), or
// forwards to the XML editor when it's the active tab. Returns false for
// anything else so the host can route focus-navigation keys elsewhere.
func (v *PlanView) HandleKey(ev *tcell.EventKey) bool {
	// Search must get first refusal of every key while active (or while
	// idle but eligible, for '/', 'n', 'N', 'w', 'p') — otherwise a typed
	// digit like '1' would switch tabs instead of extending the query.
	if v.handleSearchKey(ev) {
		return true
	}
	switch core.EvRune(ev) {
	case '1':
		v.setActiveTab(TabPlan)
		return true
	case '2':
		v.setActiveTab(TabTree)
		return true
	case '3':
		v.setActiveTab(TabXML)
		return true
	case '[':
		v.stepStatement(-1)
		return true
	case ']':
		v.stepStatement(1)
		return true
	}
	switch {
	case v.activeTab == TabXML:
		return v.xml.HandleKey(ev)
	case v.activeTab == TabTree:
		return v.handleTreeTabKey(ev)
	default: // TabPlan
		return v.handleGraphTabKey(ev)
	}
}

// routeToContent forwards ev to whichever tab is currently active (XML
// editor, Tree, or Plan/graph) — shared by HandleMouse's release branch,
// its already-latched tab/stmt branches, and its own default case.
func (v *PlanView) routeToContent(ev *tcell.EventMouse) bool {
	switch {
	case v.activeTab == TabXML:
		return v.xml.HandleMouse(ev)
	case v.activeTab == TabTree:
		return v.handleTreeTabMouse(ev)
	default: // TabPlan
		return v.handleGraphTabMouse(ev)
	}
}

// HandleMouse routes clicks to the tab bar, the "[ Expand ]" button, the
// statement selector's ◀/▶ arrows, or the XML editor.
func (v *PlanView) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	// Always forward release events to the XML editor, regardless of
	// position, so an in-progress text-selection drag terminates cleanly
	// even if the cursor has moved outside this control before release —
	// same reasoning as QueryPanel.HandleMouse.
	if ev.Buttons() == tcell.ButtonNone {
		v.mouseDragging = false
		return v.routeToContent(ev)
	}
	if !v.rect.Contains(mx, my) {
		return false
	}
	if v.tabRect.H == 1 && my == v.tabRect.Y && ev.Buttons() == tcell.Button1 {
		// A drag that started in the content area (e.g. an XML text
		// selection) resends Button1 on every motion event while held —
		// if the cursor drifts up into the tab row mid-drag, mouseDragging
		// is already true from that press, so forward to the content
		// handler instead of misfiring a tab switch/Expand/statement step.
		if v.mouseDragging {
			return v.routeToContent(ev)
		}
		v.mouseDragging = true
		if v.expandBtnRect.W > 0 && v.expandBtnRect.Contains(mx, my) {
			if v.OnExpand != nil {
				v.OnExpand()
			}
			return true
		}
		if i := v.tabAt(mx); i >= 0 {
			v.setActiveTab(Tab(i))
		}
		return true
	}
	if v.stmtRect.H == 1 && my == v.stmtRect.Y && ev.Buttons() == tcell.Button1 {
		if v.mouseDragging {
			return v.routeToContent(ev)
		}
		v.mouseDragging = true
		prev, next := v.arrowRects()
		switch {
		case prev.Contains(mx, my):
			v.stepStatement(-1)
		case next.Contains(mx, my):
			v.stepStatement(1)
		}
		return true
	}
	if ev.Buttons() == tcell.Button1 {
		v.mouseDragging = true
	}
	return v.routeToContent(ev)
}

// HasSelection, SelectedText, Cut, Paste, and SelectAll let a host wire
// PlanView into its own App-level Copy/Cut/Paste (see
// internal/tui/clipboard.go's clipboardTarget interface) exactly like
// DetailBrowser does for its grid — the XML tab forwards to its editor's
// text selection; the Plan and Tree tabs report the selected operator's
// details (see details.go's formatDetailsText) as their "selection"
// instead, since there's no free-form text selection to speak of there.
func (v *PlanView) HasSelection() bool {
	switch {
	case v.activeTab == TabXML:
		return v.xml.HasSelection()
	case v.activeTab == TabTree, v.activeTab == TabPlan:
		return v.selectedNode() != nil
	}
	return false
}
func (v *PlanView) SelectedText() string {
	switch {
	case v.activeTab == TabXML:
		return v.xml.SelectedText()
	case v.activeTab == TabTree, v.activeTab == TabPlan:
		if n := v.selectedNode(); n != nil {
			return formatDetailsText(n, v.currentStatement())
		}
	}
	return ""
}
func (v *PlanView) Cut() string       { return v.SelectedText() }
func (v *PlanView) Paste(text string) {}
func (v *PlanView) SelectAll() {
	if v.activeTab == TabXML {
		v.xml.SelectAll()
	}
}
