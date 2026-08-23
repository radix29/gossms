package controls

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// TreeView
// ---------------------------------------------------------------------------

// TreeNodeID uniquely identifies a node within a TreeView.
type TreeNodeID = int

// TreeNode holds the data for one node in a TreeView.
type TreeNode struct {
	ID       TreeNodeID
	Label    string
	Icon     rune
	Depth    int
	Expanded bool
	Loaded   bool
	HasKids  bool // true = node can have children (even if not yet loaded)
	Parent   TreeNodeID
	Tag      any // application data attached to this node
}

// TreeView is a collapsible/expandable tree control. The application populates
// it with SetNodes and wires up the OnExpand, OnSelect and OnRightClick
// callbacks.
type TreeView struct {
	rect core.Rect

	nodes []TreeNode // flat, ordered visible list

	sel    int
	scroll int
	active bool

	// scrollX is the horizontal scroll offset, in display columns, applied to
	// every row's rendered content. contentW is the widest row across tv.nodes,
	// recomputed by SetNodes — the horizontal counterpart of scroll and
	// len(tv.nodes) above.
	scrollX  int
	contentW int

	// lastClickIdx/lastClickAt time consecutive presses on one row, which is how
	// a double-click — the mouse spelling of Enter's default action — is
	// recognised. A zero lastClickAt means "no press to pair with", and is also
	// how a completed double-click resets.
	lastClickIdx int
	lastClickAt  time.Time

	// mouseDragging distinguishes a fresh Button1 press from a continued hold
	// over the same row — MenuBar, DataGrid and Editor have the same field.
	// Without it, tcell's all-motion tracking resends Button1 on every cursor
	// motion while the button is down, so a click that twitches re-fires the
	// click handling on every resend.
	mouseDragging bool

	// sbDragging is true while the scrollbar thumb is being dragged — see
	// DataGrid's field of the same name for why it is separate from
	// mouseDragging.
	sbDragging  bool
	sbDraggingX bool // same, for the horizontal scrollbar thumb

	// Callbacks — set by the application layer
	OnExpand   func(nodeID TreeNodeID) // called when a node is expanded
	OnCollapse func(nodeID TreeNodeID) // called when a node is collapsed
	OnSelect   func(nodeID TreeNodeID) // called when selection changes
	// OnActivate is the selected node's default action — Enter, or a second click
	// on the row within doubleClickInterval. It reports whether it handled the
	// node; false or unset falls back to expand/collapse, which keeps Enter
	// working on a folder.
	OnActivate   func(nodeID TreeNodeID) bool
	OnRightClick func(nodeID TreeNodeID, x, y int)
}

// NewTreeView creates a TreeView.
func NewTreeView() *TreeView {
	return new(TreeView{})
}

// SetBounds positions the tree view.
func (tv *TreeView) SetBounds(x, y, w, h int) {
	tv.rect = core.Rect{X: x, Y: y, W: w, H: h}
}

// SetActive marks the tree as focused.
func (tv *TreeView) SetActive(v bool) { tv.active = v }

// SetNodes replaces the entire visible node list — typically rebuilt in
// OnExpand after loading children.
func (tv *TreeView) SetNodes(nodes []TreeNode) {
	tv.nodes = nodes
	tv.sel = core.Clamp(tv.sel, 0, max(0, len(nodes)-1))
	// A collapse or refresh that shrinks the list below the old scroll offset
	// would otherwise leave scroll past the end of nodes, and Draw's loop breaks
	// on its first iteration — nothing renders until an arrow key recomputes
	// scroll through ensureVisible.
	tv.ensureVisible(tv.rect.Inner(1).H)

	tv.contentW = 0
	for _, n := range nodes {
		if w := tv.lineWidth(n); w > tv.contentW {
			tv.contentW = w
		}
	}
	tv.scrollX = core.Clamp(tv.scrollX, 0, max(0, tv.contentW-tv.rect.Inner(1).W))
}

// lineWidth returns n's rendered row width in display columns: indent (2 per
// depth level) + the 4-column expander field + the icon and its separating
// space, if any + the label.
func (tv *TreeView) lineWidth(n TreeNode) int {
	w := n.Depth*2 + 4
	if n.Icon != 0 {
		w += max(1, core.DisplayWidth(string(n.Icon))) + 1
	}
	w += core.DisplayWidth(n.Label)
	return w
}

// SelectID selects the node with the given ID, if present, and fires OnSelect —
// unlike SetNodes, whose tv.sel clamp is a bounds check with no notion of which
// node the caller means. Use it for a node added or replaced programmatically
// that should end up selected and reported like a click would.
func (tv *TreeView) SelectID(id TreeNodeID) {
	for i, n := range tv.nodes {
		if n.ID == id {
			tv.sel = i
			tv.ensureVisible(tv.rect.Inner(1).H)
			tv.fireSelect()
			return
		}
	}
}

// SelectedNode returns the currently highlighted node, or nil.
func (tv *TreeView) SelectedNode() *TreeNode {
	if tv.sel >= 0 && tv.sel < len(tv.nodes) {
		return &tv.nodes[tv.sel]
	}
	return nil
}

// Draw renders the tree view.
func (tv *TreeView) Draw(s tcell.Screen) {
	p := theme.Active()
	borderStyle := theme.StyleBorder()
	if tv.active {
		borderStyle = theme.StyleActiveBorder()
	}
	titleStyle := tcell.StyleDefault.Background(p.PanelBg).Foreground(p.TextHighlight).Bold(true)
	core.FillRect(s, tv.rect, ' ', theme.StylePanel())
	core.DrawBoxTitle(s, tv.rect, "Object Explorer", borderStyle, titleStyle)

	inner := tv.rect.Inner(1)

	for row := 0; row < inner.H; row++ {
		idx := tv.scroll + row
		if idx >= len(tv.nodes) {
			break
		}
		node := tv.nodes[idx]
		y := inner.Y + row

		expander := "    "
		if node.HasKids {
			if node.Expanded {
				expander = "[-] "
			} else {
				expander = "[+] "
			}
		}

		style := theme.StylePanel()
		if idx == tv.sel {
			style = theme.StyleSelected()
		}
		core.FillRect(s, core.Rect{X: inner.X, Y: y, W: inner.W, H: 1}, ' ', style)

		// The whole row — indent, expander, icon, label — is built as one
		// logical line and drawn through DrawTextOffset, so tv.scrollX shifts it
		// uniformly and wide glyphs measure by display column, not by rune.
		var line strings.Builder
		line.WriteString(strings.Repeat(" ", node.Depth*2))
		line.WriteString(expander)
		if node.Icon != 0 {
			line.WriteRune(node.Icon)
			line.WriteByte(' ')
		}
		line.WriteString(node.Label)
		core.DrawTextOffset(s, inner.X, y, tv.scrollX, inner.W, style, line.String())
	}

	if len(tv.nodes) > inner.H {
		sbStyle := tcell.StyleDefault.Background(p.PanelBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, tv.rect.Right()-1, inner.Y, inner.H,
			len(tv.nodes), inner.H, tv.scroll, sbStyle, sbThumb)
	}
	if tv.contentW > inner.W {
		sbStyle := tcell.StyleDefault.Background(p.PanelBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbarH(s, inner.X, tv.rect.Bottom()-1, inner.W,
			tv.contentW, inner.W, tv.scrollX, sbStyle, sbThumb)
	}
}

// HandleKey handles keyboard events.
func (tv *TreeView) HandleKey(ev *tcell.EventKey) bool {
	inner := tv.rect.Inner(1)
	switch ev.Key() {
	case tcell.KeyUp:
		if tv.sel > 0 {
			tv.sel--
			tv.ensureVisible(inner.H)
			tv.fireSelect()
		}
		return true
	case tcell.KeyDown:
		if tv.sel < len(tv.nodes)-1 {
			tv.sel++
			tv.ensureVisible(inner.H)
			tv.fireSelect()
		}
		return true
	case tcell.KeyPgUp:
		tv.sel = max(0, tv.sel-inner.H)
		tv.ensureVisible(inner.H)
		tv.fireSelect()
		return true
	case tcell.KeyPgDn:
		tv.sel = min(len(tv.nodes)-1, tv.sel+inner.H)
		tv.ensureVisible(inner.H)
		tv.fireSelect()
		return true
	case tcell.KeyHome:
		tv.sel = 0
		tv.ensureVisible(inner.H)
		tv.fireSelect()
		return true
	case tcell.KeyEnd:
		if len(tv.nodes) > 0 {
			tv.sel = len(tv.nodes) - 1
		}
		tv.ensureVisible(inner.H)
		tv.fireSelect()
		return true
	case tcell.KeyEnter:
		// Enter is "do the default thing with this node"; Right only ever
		// expands, so the activation hook hangs off Enter alone.
		if !tv.activateSelected() {
			tv.toggleExpand()
		}
		return true
	case tcell.KeyRight:
		tv.toggleExpand()
		return true
	case tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		tv.collapseSelected()
		return true
	case tcell.KeyF10:
		// Shift+F10 is the cross-platform "open context menu" convention, and
		// the binding most terminal apps use on macOS, which has no dedicated
		// context-menu key.
		if ev.Modifiers()&tcell.ModShift != 0 {
			tv.openContextMenuAtSelection()
			return true
		}
	case tcell.KeyMenu:
		// The dedicated Menu/Application key present on most Windows and
		// Linux keyboards.
		tv.openContextMenuAtSelection()
		return true
	}
	if ev.Modifiers()&tcell.ModCtrl != 0 && core.EvRune(ev) == ' ' {
		// Ctrl+Space: a third, always-available way to open the context menu,
		// alongside Shift+F10 and Menu above.
		tv.openContextMenuAtSelection()
		return true
	}
	switch core.EvRune(ev) {
	case '+':
		tv.toggleExpand()
		return true
	case '-':
		tv.collapseSelected()
		return true
	}
	return false
}

// HandleMouse handles mouse events.
func (tv *TreeView) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	if ev.Buttons() == tcell.ButtonNone {
		tv.mouseDragging = false
		tv.sbDragging = false
		tv.sbDraggingX = false
	}
	if !tv.rect.Contains(mx, my) {
		return false
	}
	inner := tv.rect.Inner(1)

	// Scrollbar drag/click takes priority over the row hit-testing below. The
	// vertical bar is drawn over the right border column and the horizontal bar
	// over the bottom border row (see Draw), and the latter sits outside the row
	// range the hit-test checks — so both scrollbar checks must run first.
	if core.HandleScrollbarDrag(ev, tv.rect.Right()-1, inner.Y, inner.H, len(tv.nodes), &tv.sbDragging, &tv.scroll) {
		return true
	}
	if core.HandleScrollbarDragH(ev, inner.X, tv.rect.Bottom()-1, inner.W, tv.contentW, &tv.sbDraggingX, &tv.scrollX) {
		return true
	}

	row := my - inner.Y
	if row < 0 || row >= inner.H {
		return false
	}

	idx := tv.scroll + row
	if idx < 0 || idx >= len(tv.nodes) {
		return false
	}
	// A press acts on one specific node, so it needs the stricter content
	// hit-test too: the row bound above doesn't constrain mx, and the vertical
	// scrollbar is drawn over the right border column. HandleScrollbarDrag has
	// already claimed a press there whenever the bar shows, so this only changes
	// the no-bar case — but nodeIndexAt is also what app-level drag arming
	// hit-tests with (see NodeIDAt), and the two must agree on what "landed on a
	// node" means. The wheel cases below keep the looser row-only bound.
	if b := ev.Buttons(); b == tcell.Button1 || b == tcell.Button2 {
		if _, ok := tv.nodeIndexAt(mx, my); !ok {
			return false
		}
	}
	switch ev.Buttons() {
	case tcell.Button1:
		if tv.mouseDragging {
			// Still the same physical press, or the same drag toward the query
			// editor (see explorer_drag.go) — don't re-select or re-toggle on
			// every resent motion event.
			return true
		}
		tv.mouseDragging = true
		node := &tv.nodes[idx]
		// A second press on the same row inside the interval is a double-click:
		// activate rather than re-select. Recorded before the expander test
		// below so a double-click on the glyph only toggles — activating from it
		// too would fire on the second half of an ordinary expand.
		// doubleClickInterval is the Editor's, one speed for the whole app.
		doubleClick := idx == tv.lastClickIdx && !tv.lastClickAt.IsZero() &&
			time.Since(tv.lastClickAt) <= doubleClickInterval
		tv.lastClickIdx = idx
		tv.lastClickAt = time.Now()
		// Only the "[+]"/"[-]" expander glyph toggles expand/collapse; a click
		// elsewhere on the row reselects it. That is what lets a node be
		// click-dragged into the query editor without flipping its expand state,
		// since a drag starts on the label. mx is a screen column while the
		// expander's position is virtual (depth*2..depth*2+4, see Draw), so it
		// must be translated through tv.scrollX or this matches only at
		// scrollX==0.
		vcol := mx - inner.X + tv.scrollX
		onExpander := node.HasKids && vcol >= node.Depth*2 && vcol < node.Depth*2+4
		if tv.sel != idx {
			tv.sel = idx
			tv.fireSelect()
		}
		if onExpander {
			tv.toggleExpand()
			return true
		}
		if doubleClick {
			// Cleared so a third click starts a fresh pair rather than
			// activating again on every following click.
			tv.lastClickAt = time.Time{}
			tv.activateSelected()
		}
		return true
	case tcell.Button2: // tcell v3: Button2 is Secondary (right-click); Button3 is Middle.
		tv.sel = idx
		if tv.OnRightClick != nil {
			tv.OnRightClick(tv.nodes[idx].ID, mx, my)
		}
		return true
	case tcell.WheelUp:
		// Shift+wheel is the desktop convention for horizontal scroll, and some
		// terminals report it that way rather than as WheelLeft/WheelRight
		// below, so honour both — as DataGrid, Editor and PlanView do.
		if ev.Modifiers()&tcell.ModShift != 0 {
			tv.scrollLeft()
		} else if tv.scroll > 0 {
			tv.scroll--
		}
		return true
	case tcell.WheelDown:
		if ev.Modifiers()&tcell.ModShift != 0 {
			tv.scrollRight()
		} else if tv.scroll < len(tv.nodes)-inner.H {
			tv.scroll++
		}
		return true
	case tcell.WheelLeft:
		tv.scrollLeft()
		return true
	case tcell.WheelRight:
		tv.scrollRight()
		return true
	}
	return false
}

// nodeIndexAt returns the index into tv.nodes of the row drawn at screen
// position (mx, my), and whether that position is over a node row at all. The
// test is against the content area, not the whole rect, so the border columns
// and rows the scrollbars are drawn over never resolve to a node.
func (tv *TreeView) nodeIndexAt(mx, my int) (int, bool) {
	inner := tv.rect.Inner(1)
	if !inner.Contains(mx, my) {
		return 0, false
	}
	idx := tv.scroll + (my - inner.Y)
	if idx < 0 || idx >= len(tv.nodes) {
		return 0, false
	}
	return idx, true
}

// NodeIDAt returns the ID of the node drawn at screen position (mx, my), and
// whether there is one there — how a host finds out what a press landed on
// rather than inferring it from the selection afterwards. A press on the
// scrollbar, the border, or blank space below the last node reports no node.
// Object Explorer's drag-and-drop arms from this (via ObjectExplorer.NodeAt);
// without it a scrollbar drag arms as a node drag and the thumb stops following
// the mouse.
func (tv *TreeView) NodeIDAt(mx, my int) (int, bool) {
	idx, ok := tv.nodeIndexAt(mx, my)
	if !ok {
		return 0, false
	}
	return tv.nodes[idx].ID, true
}

// scrollLeft/scrollRight nudge the horizontal scroll offset by 4 columns,
// clamped to [0, contentW-inner.W] so the thumb never runs past either end of
// the track.
func (tv *TreeView) scrollLeft() {
	tv.scrollX = max(0, tv.scrollX-4)
}

func (tv *TreeView) scrollRight() {
	maxScroll := max(0, tv.contentW-tv.rect.Inner(1).W)
	tv.scrollX = min(maxScroll, tv.scrollX+4)
}

// activateSelected runs the selected node's default action, reporting whether
// anything handled it. A host with no OnActivate, or one that declines this
// node, gets false and the caller falls back to expand/collapse.
func (tv *TreeView) activateSelected() bool {
	n := tv.SelectedNode()
	if n == nil || tv.OnActivate == nil {
		return false
	}
	return tv.OnActivate(n.ID)
}

// toggleExpand flips the selected node's Expanded state and fires
// OnExpand/OnCollapse. OnExpand fires on every expand, loaded or not, so the
// caller can redisplay cached children; whether that means a fetch is the
// caller's decision. TreeNode.Loaded is caller-supplied display metadata, not
// something TreeView tracks or gates on.
func (tv *TreeView) toggleExpand() {
	n := tv.SelectedNode()
	if n == nil || !n.HasKids {
		return
	}
	n.Expanded = !n.Expanded
	if n.Expanded {
		if tv.OnExpand != nil {
			tv.OnExpand(n.ID)
		}
	} else if tv.OnCollapse != nil {
		tv.OnCollapse(n.ID)
	}
}

// collapseSelected collapses the selected node if it is expanded, firing
// OnCollapse. No-op if nothing is selected or it is already collapsed.
func (tv *TreeView) collapseSelected() {
	n := tv.SelectedNode()
	if n == nil || !n.Expanded {
		return
	}
	n.Expanded = false
	if tv.OnCollapse != nil {
		tv.OnCollapse(n.ID)
	}
}

func (tv *TreeView) ensureVisible(h int) {
	if tv.sel < tv.scroll {
		tv.scroll = tv.sel
	}
	if tv.sel >= tv.scroll+h {
		tv.scroll = tv.sel - h + 1
	}
}

func (tv *TreeView) fireSelect() {
	if tv.OnSelect != nil && tv.sel >= 0 && tv.sel < len(tv.nodes) {
		tv.OnSelect(tv.nodes[tv.sel].ID)
	}
}

// openContextMenuAtSelection fires OnRightClick for the selected node,
// positioned at its row — the keyboard equivalent of a right-click.
func (tv *TreeView) openContextMenuAtSelection() {
	x, y, ok := tv.SelectionAnchor()
	if !ok || tv.OnRightClick == nil {
		return
	}
	tv.OnRightClick(tv.SelectedNode().ID, x, y)
}

// SelectionAnchor returns the screen position a menu about the selected node
// should open at, and whether there is a selection. Exported so a host opening
// its own menu about the selection uses the same anchor the context menu does.
func (tv *TreeView) SelectionAnchor() (x, y int, ok bool) {
	n := tv.SelectedNode()
	if n == nil {
		return 0, 0, false
	}
	inner := tv.rect.Inner(1)
	// n.Depth*2 is a virtual column (see Draw's row-line layout), translated back
	// through tv.scrollX like the expander hit-test does and clamped, so a node
	// scrolled left of the panel still pops its menu on screen.
	return inner.X + max(0, n.Depth*2-tv.scrollX), inner.Y + (tv.sel - tv.scroll), true
}
