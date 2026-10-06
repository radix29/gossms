package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// plan_compare_panel.go is SSMS's Compare Showplan: two plans of one query read
// against each other. Two grids rather than two plan graphs side by side — an
// operator tile is twenty columns wide (planview.graphTileW) and a terminal that
// fits two of them
// side by side has no room left for either plan's properties, and the question
// a comparison answers ("what changed") is a list, not a picture.
//
// The pairing itself is showplan.CompareStatements; nothing here decides what
// counts as a difference.

// planCompareColumns are the operator grid's columns. A and B are the two
// plans, labelled by the panel's title bar rather than in every header — the
// plan ids are long enough to push the numbers off an 80-column pane.
var planCompareColumns = []string{"Operator", "Change", "Est cost A", "Est cost B",
	"Est rows A", "Est rows B", "Actual rows A", "Actual rows B", "Differences"}

// planComparePropColumns are the statement-property grid's.
var planComparePropColumns = []string{"Property", "Plan A", "Plan B", ""}

// PlanComparePanel shows one comparison: the statement properties above, the
// paired operator trees below. Everything is computed when the panel is built —
// there is no connection here and nothing to reload.
type PlanComparePanel struct {
	app    *App
	rect   core.Rect
	title  string
	active bool

	// planA and planB are the two plans; stmtA and stmtB index the statement of
	// each that is compared, which the toolbar's two pickers change.
	planA, planB *showplan.Plan
	stmtA, stmtB int

	// sideNames name where A and B came from (a panel, a .sqlplan), for the
	// title of a plan openOperator opens. Empty for Query Store's comparison,
	// whose plans are named only by the panel title.
	sideNames [2]string

	// pickers are the toolbar's two statement selectors, A then B (pcPickA,
	// pcPickB). The row is there only when either plan has a statement to
	// choose (toolRect.H == 1): a Query Store plan is one statement, and a row
	// holding two selectors with nothing to select would be a row of nothing.
	pickers  []toolButton
	toolRect core.Rect

	diffs []showplan.NodeDiff

	props *controls.DataGrid
	ops   *controls.DataGrid
	split *layout.Splitter

	// focusOps names which grid has the keyboard, the way QueryStorePanel's
	// qsFocus does.
	focusOps bool

	// dragZone names the sub-region that claimed the in-progress gesture, the
	// way QueryStorePanel's qsDragZone does. A bool naming only "the ops grid
	// or not" was one owner short: the splitter and the properties grid shared
	// the false case, and the held-button branch then re-offered the event to
	// the splitter first — so a selection dragged in the properties grid was
	// taken over by the splitter the moment the pointer crossed it, and the
	// pane resized instead. A gesture belongs to whatever claimed its first
	// press until the release; see ARCHITECTURE.md § The mouseDragging idiom.
	dragZone pcDragZone
}

// pcDragZone names the sub-region owning a gesture between press and release.
type pcDragZone int

const (
	pcZoneNone pcDragZone = iota
	pcZoneSplit
	pcZoneOps
	pcZoneProps
	pcZoneToolbar
)

// The toolbar's picker cells, in layout order.
const (
	pcPickA = iota
	pcPickB
)

// NewPlanComparePanel builds the comparison of two parsed plans. Each side
// starts on its first statement with a plan (firstStatement), and the toolbar's
// pickers move either side independently. There is no pairing of statement N
// with statement N: two batches that differ by one statement would then compare
// unrelated queries all the way down, so which statement faces which is the
// user's call. A Query Store plan is one statement, so its comparison is the
// same as it always was.
func NewPlanComparePanel(app *App, title string, a, b *showplan.Plan) *PlanComparePanel {
	p := new(PlanComparePanel{
		app:   app,
		title: title,
		planA: a,
		planB: b,
		stmtA: firstStatement(a),
		stmtB: firstStatement(b),
		props: newQSGrid(app),
		ops:   newQSGrid(app),
		split: layout.NewHorizontalSplitter("─── Operators ─── (drag or Ctrl+Up/Down to resize)"),
	})
	p.pickers = []toolButton{
		{action: func() { p.showStatementMenu(pcPickA) }},
		{action: func() { p.showStatementMenu(pcPickB) }},
	}
	p.ops.OnMenuItems = p.operatorMenuItems
	p.split.SetRatio(0.4)
	p.compare()
	p.applyFocus()
	return p
}

// compare (re)builds both grids from the chosen statement pair. resetGrid, not
// SetData: a new pair is a new row set, but a column the user dragged wider to
// read the Differences cell should stay wide for the next pair.
func (p *PlanComparePanel) compare() {
	sa, sb := statementAt(p.planA, p.stmtA), statementAt(p.planB, p.stmtB)
	p.diffs = showplan.CompareStatements(sa, sb)
	resetGrid(p.props, planComparePropColumns, planComparePropRows(showplan.CompareProperties(sa, sb)), 0)
	resetGrid(p.ops, planCompareColumns, planCompareRows(p.diffs), 0)
	p.props.SetStatus(planCompareSummary(p.diffs))
	p.refreshPickerLabels()
}

// firstStatement is the index of the statement a plan is first compared by —
// the first one carrying an operator tree, so a batch whose first statement is
// a SET does not compare as an empty plan.
func firstStatement(p *showplan.Plan) int {
	if p == nil {
		return 0
	}
	for i, st := range p.Statements {
		if st.Root != nil {
			return i
		}
	}
	return 0
}

// statementAt is statement i of p, or nil.
func statementAt(p *showplan.Plan, i int) *showplan.Statement {
	if p == nil || i < 0 || i >= len(p.Statements) {
		return nil
	}
	return p.Statements[i]
}

// side returns the plan and the chosen statement index behind picker i.
func (p *PlanComparePanel) side(i int) (*showplan.Plan, *int) {
	if i == pcPickA {
		return p.planA, &p.stmtA
	}
	return p.planB, &p.stmtB
}

// hasPicker reports whether either plan has a second statement to choose.
func (p *PlanComparePanel) hasPicker() bool {
	return statementCount(p.planA) > 1 || statementCount(p.planB) > 1
}

func statementCount(p *showplan.Plan) int {
	if p == nil {
		return 0
	}
	return len(p.Statements)
}

// refreshPickerLabels names each side's statement on its cell: number, count
// and the start of its text, so the pair being compared can be read off the
// toolbar without opening either menu.
//
// Each label is cut to half the row. A toolbar cell that does not fit is not
// drawn at all (layoutToolButtons), so a long statement on A would otherwise
// take B's picker off the row — and the mouse route to B with it.
func (p *PlanComparePanel) refreshPickerLabels() {
	limit := (p.toolRect.W-6)/2 - core.DisplayWidth(" ▾")
	for i, name := range []string{"A", "B"} {
		plan, idx := p.side(i)
		label := fmt.Sprintf("%s: statement %d/%d", name, *idx+1, statementCount(plan))
		if st := statementAt(plan, *idx); st != nil {
			label += " " + core.Truncate(oneLineText(st.Text), 30)
		}
		if p.toolRect.H == 1 {
			label = core.Truncate(label, max(limit, 1))
		}
		p.pickers[i].label = label + " ▾"
	}
	p.layoutPickers()
}

// oneLineText folds a statement's text onto one line for a toolbar cell or a
// menu entry.
func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

// statementMenuItems lists plan's statements for a picker, marking the one in
// force. A statement with no operator tree (a SET, a DECLARE) is listed but
// withheld, with the reason as its note: choosing it would compare nothing.
func statementMenuItems(plan *showplan.Plan, current int, choose func(int)) []controls.MenuItem {
	if plan == nil {
		return nil
	}
	items := make([]controls.MenuItem, 0, len(plan.Statements))
	for i, st := range plan.Statements {
		text := fmt.Sprintf("%d. %s", i+1, core.Truncate(oneLineText(st.Text), 60))
		if i == current {
			text = "• " + text
		}
		hasPlan := st.Root != nil
		items = append(items, controls.MenuItem{
			Label:   text,
			Enabled: func() bool { return hasPlan },
			Note:    "no plan",
			Action:  func() { choose(i) },
		})
	}
	return items
}

// runPicker opens picker i's menu, or says why it did not: a one-statement side
// is drawn dimmed, and a dimmed cell that swallowed its click silently is what
// the context-gating rule exists to prevent.
func (p *PlanComparePanel) runPicker(i int) {
	if plan, _ := p.side(i); statementCount(plan) < 2 {
		p.app.setStatus(fmt.Sprintf("Plan %s has only the one statement", []string{"A", "B"}[i]))
		return
	}
	p.pickers[i].action()
}

// showStatementMenu pops picker i's statement list under its cell.
func (p *PlanComparePanel) showStatementMenu(i int) {
	plan, idx := p.side(i)
	r := p.pickers[i].rect
	if r.IsZero() {
		r = core.Rect{X: p.rect.X, Y: p.rect.Y}
	}
	p.app.contextMenu.Show(r.X, r.Y+1, statementMenuItems(plan, *idx, func(s int) { p.chooseStatement(i, s) }))
}

// chooseStatement compares statement s on side i.
func (p *PlanComparePanel) chooseStatement(i, s int) {
	_, idx := p.side(i)
	if *idx == s {
		return
	}
	*idx = s
	p.compare()
}

// stepStatement moves side i to its next (delta 1) or previous (-1) statement
// with a plan, wrapping — the keyboard route to the pickers, '[' ']' for A and
// '{' '}' for B, as planview's '[' ']' step its own statement bar. It reports
// whether the side moved: a side with nothing else to step to declines the key.
func (p *PlanComparePanel) stepStatement(i, delta int) bool {
	plan, idx := p.side(i)
	n := statementCount(plan)
	for k := 1; k < n; k++ {
		s := ((*idx+delta*k)%n + n) % n
		if plan.Statements[s].Root != nil {
			p.chooseStatement(i, s)
			return true
		}
	}
	return false
}

// operatorSide is the side an Enter on the selected operator row opens: the
// side of the column the cell cursor is on (" B" columns are B, every other
// column A), or the other side when that one has no such operator.
func (p *PlanComparePanel) operatorSide() (side int, n *showplan.Node) {
	row, col := p.ops.SelectedCell()
	if row < 0 || row >= len(p.diffs) {
		return pcPickA, nil
	}
	nodes := [2]*showplan.Node{p.diffs[row].Left, p.diffs[row].Right}
	side = pcPickA
	if col >= 0 && col < len(planCompareColumns) && strings.HasSuffix(planCompareColumns[col], " B") {
		side = pcPickB
	}
	if nodes[side] == nil {
		side = 1 - side
	}
	return side, nodes[side]
}

// openOperator opens side's plan in its own PlanPanel with operator n
// selected — SSMS's way from a compared operator back to the plan it belongs
// to, where its full properties and its place in the tree are.
func (p *PlanComparePanel) openOperator(side int, n *showplan.Node) {
	if n == nil {
		return
	}
	plan, idx := p.side(side)
	name := []string{"A", "B"}[side]
	title := fmt.Sprintf("Plan %s — %s", name, p.title)
	if p.sideNames[side] != "" {
		title = fmt.Sprintf("Plan %s: %s", name, p.sideNames[side])
	}
	pp := NewPlanPanel(p.app, title, plan)
	pp.planView.SelectNode(*idx, n.ID)
	p.app.panels.SetActive(p.app.panels.AddPanel(pp))
	p.app.focusPanels()
}

// operatorMenuItems adds the mouse route to openOperator to the operator
// grid's cell menu: one entry per side, each withheld where its side has no
// such operator.
func (p *PlanComparePanel) operatorMenuItems() []controls.MenuItem {
	row, _ := p.ops.SelectedCell()
	if row < 0 || row >= len(p.diffs) {
		return nil
	}
	d := p.diffs[row]
	item := func(side int, n *showplan.Node) controls.MenuItem {
		return controls.MenuItem{
			Label:   fmt.Sprintf("Open Plan %s at Operator", []string{"A", "B"}[side]),
			Action:  func() { p.openOperator(side, n) },
			Enabled: func() bool { return n != nil },
			Note:    "not in this plan",
		}
	}
	return []controls.MenuItem{item(pcPickA, d.Left), item(pcPickB, d.Right)}
}

// planCompareSummary counts what the comparison found, so a user who sees a
// screen of "Same" knows the pane is not simply showing one plan twice.
func planCompareSummary(diffs []showplan.NodeDiff) string {
	var changed, only int
	for _, d := range diffs {
		switch d.Kind {
		case showplan.ChangeDifferent:
			changed++
		case showplan.ChangeOnlyLeft, showplan.ChangeOnlyRight:
			only++
		}
	}
	if changed == 0 && only == 0 {
		return fmt.Sprintf("%d operators, no differences", len(diffs))
	}
	return fmt.Sprintf("%d operators — %d changed, %d in one plan only", len(diffs), changed, only)
}

// planComparePropRows renders the statement-property comparison, marking the
// rows that moved: the grid draws no colour of its own, so the marker column is
// what a user scans down.
func planComparePropRows(props []showplan.PropDiff) [][]string {
	rows := make([][]string, 0, len(props))
	for _, p := range props {
		marker := ""
		if p.Different {
			marker = "◆"
		}
		rows = append(rows, []string{p.Name, p.Left, p.Right, marker})
	}
	return rows
}

// planCompareRows renders the paired operators. The operator column is indented
// by tree depth, which is what makes two adjacent lines readable as parent and
// child once the pairing has reordered them.
func planCompareRows(diffs []showplan.NodeDiff) [][]string {
	rows := make([][]string, 0, len(diffs))
	for _, d := range diffs {
		rows = append(rows, []string{
			strings.Repeat("  ", d.Depth) + operatorLabel(d.Node()),
			d.Kind.String(),
			nodeCost(d.Left), nodeCost(d.Right),
			nodeEstRows(d.Left), nodeEstRows(d.Right),
			nodeActualRows(d.Left), nodeActualRows(d.Right),
			strings.Join(d.Changes, "; "),
		})
	}
	return rows
}

// operatorLabel names an operator the way the plan tree does: the physical
// operator, and the object it reads where there is one.
func operatorLabel(n *showplan.Node) string {
	if n == nil {
		return ""
	}
	if n.Object.IsZero() {
		return n.PhysicalOp
	}
	return n.PhysicalOp + " " + n.Object.Short()
}

// nodeCost, nodeEstRows and nodeActualRows render one side of a comparison row,
// or a dash where that side has no operator — an empty cell there would read as
// a zero cost rather than an absent operator.
func nodeCost(n *showplan.Node) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%.4f", n.EstSubtreeCost)
}

func nodeEstRows(n *showplan.Node) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f", n.EstRows)
}

func nodeActualRows(n *showplan.Node) string {
	if n == nil || n.Runtime == nil {
		return "-"
	}
	return core.FormatThousands(n.Runtime.Rows)
}

// Title returns the panel's tab title (Panel interface).
func (p *PlanComparePanel) Title() string { return p.title }

// SetActive marks this panel focused (Activatable interface).
func (p *PlanComparePanel) SetActive(v bool) {
	p.active = v
	p.split.SetActive(v)
	p.applyFocus()
}

func (p *PlanComparePanel) applyFocus() {
	p.props.Focus(p.active && !p.focusOps)
	p.ops.Focus(p.active && p.focusOps)
}

// SetBounds positions the title bar and the two grids either side of the
// splitter.
func (p *PlanComparePanel) SetBounds(x, y, w, h int) {
	p.rect = core.Rect{X: x, Y: y, W: w, H: h}
	top := y + 1
	p.toolRect = core.Rect{}
	if p.hasPicker() && h > 2 {
		p.toolRect = core.Rect{X: x, Y: top, W: w, H: 1}
		top++
	}
	p.split.SetBounds(x, top, w, h-(top-y))
	p.layoutChildren()
	p.refreshPickerLabels() // the labels fit the row, so a new width relabels
}

// layoutPickers places the picker cells — on every relabel, which every bounds
// change makes, and never in Draw, since a click is hit-tested against these
// rects.
func (p *PlanComparePanel) layoutPickers() {
	if p.toolRect.H != 1 {
		for i := range p.pickers {
			p.pickers[i].rect = core.Rect{}
		}
		return
	}
	layoutToolButtons(p.pickers, p.toolRect, "")
}

func (p *PlanComparePanel) layoutChildren() {
	a, b := p.split.FirstRect(), p.split.SecondRect()
	p.props.SetBounds(a.X, a.Y, a.W, a.H)
	p.ops.SetBounds(b.X, b.Y, b.W, b.H)
}

// Draw renders the title bar and both grids.
func (p *PlanComparePanel) Draw(s tcell.Screen) {
	pal := theme.Active()
	titleStyle := tcell.StyleDefault.Background(pal.MenuBar).Foreground(pal.Text)
	if p.active {
		titleStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
	}
	core.FillRect(s, core.Rect{X: p.rect.X, Y: p.rect.Y, W: p.rect.W, H: 1}, ' ', titleStyle)
	core.DrawTextClipped(s, p.rect.X+1, p.rect.Y, p.rect.W-2, titleStyle, p.title)
	p.drawPickers(s)
	p.split.Draw(s)
	p.props.Draw(s)
	p.ops.Draw(s)
	// Last, over both grids: a cell's context menu or value popup is drawn
	// outside the grid's own rect, and without this the menu a right-click
	// opens is invisible while still eating every key until Escape.
	p.props.DrawOverlay(s)
	p.ops.DrawOverlay(s)
}

// drawPickers paints the statement-picker row in the tooltip scheme Query
// Store's toolbar uses. A picker on a one-statement side is dimmed: its menu
// would offer the statement already compared.
func (p *PlanComparePanel) drawPickers(s tcell.Screen) {
	if p.toolRect.H != 1 {
		return
	}
	core.FillRect(s, p.toolRect, ' ', theme.StyleMenuBar())
	for i, t := range p.pickers {
		if t.rect.IsZero() {
			continue
		}
		style := theme.StyleTooltip()
		if plan, _ := p.side(i); statementCount(plan) < 2 {
			style = style.Foreground(theme.Active().TextDim)
		}
		core.FillRect(s, t.rect, ' ', style)
		core.DrawText(s, t.rect.X+1, t.rect.Y, style, t.label)
	}
}

// HandleKey routes to the focused grid, after Tab, the statement keys and the
// splitter's own bindings. Tab leaves the panel on the second press, for the
// reason QueryStorePanel's does: App only moves focus out when the panel
// declines the key.
func (p *PlanComparePanel) HandleKey(ev *tcell.EventKey) bool {
	if g := p.overlayGrid(); g != nil {
		return g.HandleKey(ev)
	}
	if ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == 0 {
		switch core.EvRune(ev) {
		case '[':
			return p.stepStatement(pcPickA, -1)
		case ']':
			return p.stepStatement(pcPickA, 1)
		case '{':
			return p.stepStatement(pcPickB, -1)
		case '}':
			return p.stepStatement(pcPickB, 1)
		}
	}
	if ev.Key() == tcell.KeyEnter && p.focusOps {
		if side, n := p.operatorSide(); n != nil {
			p.openOperator(side, n)
			return true
		}
	}
	if ev.Key() == tcell.KeyTab {
		if !p.focusOps {
			p.setFocus(true)
			return true
		}
		p.setFocus(false)
		return false
	}
	if p.split.HandleKey(ev) {
		p.layoutChildren()
		return true
	}
	return p.focusedGrid().HandleKey(ev)
}

func (p *PlanComparePanel) overlayGrid() *controls.DataGrid {
	switch {
	case p.props.OverlayActive():
		return p.props
	case p.ops.OverlayActive():
		return p.ops
	}
	return nil
}

func (p *PlanComparePanel) focusedGrid() *controls.DataGrid {
	if p.focusOps {
		return p.ops
	}
	return p.props
}

func (p *PlanComparePanel) setFocus(ops bool) {
	p.focusOps = ops
	p.applyFocus()
}

// HandleMouse routes by sub-region, with the gesture rules in ARCHITECTURE.md
// § The mouseDragging idiom: the press claims the gesture, and the release goes
// to every latch-bearing child wherever the pointer ended up.
func (p *PlanComparePanel) HandleMouse(ev *tcell.EventMouse) bool {
	if g := p.overlayGrid(); g != nil {
		return g.HandleMouse(ev)
	}
	if ev.Buttons() == tcell.ButtonNone {
		handled := false
		if p.split.HandleMouse(ev) {
			p.layoutChildren()
			handled = true
		}
		if p.props.HandleMouse(ev) {
			handled = true
		}
		if p.ops.HandleMouse(ev) {
			handled = true
		}
		p.dragZone = pcZoneNone
		return handled
	}
	if p.dragZone != pcZoneNone {
		if ev.Buttons() == tcell.Button1 {
			return p.routeDrag(ev)
		}
		return true
	}
	mx, _ := ev.Position()
	if mx < p.rect.X || mx >= p.rect.X+p.rect.W {
		return false
	}
	if _, my := ev.Position(); ev.Buttons() == tcell.Button1 && p.toolRect.H == 1 && my == p.toolRect.Y {
		if i := toolButtonAt(p.pickers, mx, my); i >= 0 {
			p.runPicker(i)
		}
		p.armDrag(ev, pcZoneToolbar)
		return true
	}
	if p.split.HandleMouse(ev) {
		p.layoutChildren()
		p.armDrag(ev, pcZoneSplit)
		return true
	}
	if p.ops.HandleMouse(ev) {
		p.armDrag(ev, pcZoneOps)
		p.setFocus(true)
		return true
	}
	if p.props.HandleMouse(ev) {
		p.armDrag(ev, pcZoneProps)
		p.setFocus(false)
		return true
	}
	return false
}

func (p *PlanComparePanel) armDrag(ev *tcell.EventMouse, zone pcDragZone) {
	if ev.Buttons() == tcell.Button1 {
		p.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to the sub-region that armed the
// gesture, and to nothing else — the point of owning it is that no other
// sub-region sees the repeats.
func (p *PlanComparePanel) routeDrag(ev *tcell.EventMouse) bool {
	switch p.dragZone {
	case pcZoneSplit:
		if p.split.HandleMouse(ev) {
			p.layoutChildren()
		}
	case pcZoneOps:
		p.ops.HandleMouse(ev)
	case pcZoneProps:
		p.props.HandleMouse(ev)
	case pcZoneToolbar:
		// The picker acted on the press; the repeats tcell sends while the
		// button is held must not open its menu again.
	}
	return true
}

// HasSelection and the SelectedText, Cut, Paste and SelectAll beside it
// implement clipboardTarget by forwarding to the focused grid.
func (p *PlanComparePanel) HasSelection() bool   { return p.focusedGrid().HasSelection() }
func (p *PlanComparePanel) SelectedText() string { return p.focusedGrid().SelectedText() }
func (p *PlanComparePanel) Cut() string          { return p.focusedGrid().Cut() }
func (p *PlanComparePanel) Paste(text string)    { p.focusedGrid().Paste(text) }
func (p *PlanComparePanel) SelectAll()           { p.focusedGrid().SelectAll() }
