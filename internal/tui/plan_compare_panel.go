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
// operator tile is eighteen columns wide and a terminal that fits two of them
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
	rect   core.Rect
	title  string
	active bool

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
)

// NewPlanComparePanel builds the comparison of two parsed plans. Only each
// plan's first statement is compared: a Query Store plan is one statement by
// construction, and pairing statement 3 of one plan with statement 3 of another
// would compare two unrelated queries the moment either had a different number.
func NewPlanComparePanel(app *App, title string, a, b *showplan.Plan) *PlanComparePanel {
	sa, sb := firstStatement(a), firstStatement(b)
	p := new(PlanComparePanel{
		title: title,
		diffs: showplan.CompareStatements(sa, sb),
		props: newQSGrid(app),
		ops:   newQSGrid(app),
		split: layout.NewHorizontalSplitter("─── Operators ─── (drag or Ctrl+Up/Down to resize)"),
	})
	p.split.SetRatio(0.4)
	p.props.SetData(planComparePropColumns, planComparePropRows(showplan.CompareProperties(sa, sb)))
	p.ops.SetData(planCompareColumns, planCompareRows(p.diffs))
	p.props.SetStatus(planCompareSummary(p.diffs))
	p.applyFocus()
	return p
}

// firstStatement is the statement a plan is compared by — the first one
// carrying an operator tree, so a batch whose first statement is a SET does not
// compare as an empty plan.
func firstStatement(p *showplan.Plan) *showplan.Statement {
	if p == nil {
		return nil
	}
	for _, st := range p.Statements {
		if st.Root != nil {
			return st
		}
	}
	if len(p.Statements) > 0 {
		return p.Statements[0]
	}
	return nil
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
	p.split.SetBounds(x, y+1, w, h-1)
	p.layoutChildren()
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
	p.split.Draw(s)
	p.props.Draw(s)
	p.ops.Draw(s)
	// Last, over both grids: a cell's context menu or value popup is drawn
	// outside the grid's own rect, and without this the menu a right-click
	// opens is invisible while still eating every key until Escape.
	p.props.DrawOverlay(s)
	p.ops.DrawOverlay(s)
}

// HandleKey routes to the focused grid, after Tab and the splitter's own
// bindings. Tab leaves the panel on the second press, for the reason
// QueryStorePanel's does: App only moves focus out when the panel declines the
// key.
func (p *PlanComparePanel) HandleKey(ev *tcell.EventKey) bool {
	if g := p.overlayGrid(); g != nil {
		return g.HandleKey(ev)
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
	}
	return true
}

// HasSelection, SelectedText, Cut, Paste and SelectAll implement
// clipboardTarget by forwarding to the focused grid.
func (p *PlanComparePanel) HasSelection() bool   { return p.focusedGrid().HasSelection() }
func (p *PlanComparePanel) SelectedText() string { return p.focusedGrid().SelectedText() }
func (p *PlanComparePanel) Cut() string          { return p.focusedGrid().Cut() }
func (p *PlanComparePanel) Paste(text string)    { p.focusedGrid().Paste(text) }
func (p *PlanComparePanel) SelectAll()           { p.focusedGrid().SelectAll() }
