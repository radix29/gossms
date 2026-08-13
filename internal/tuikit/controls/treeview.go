package controls

import (
	"strings"

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

// TreeView is a collapsible/expandable tree control.
// The application populates it by calling SetNodes, and wires up
// OnExpand, OnSelect, and OnRightClick callbacks.
type TreeView struct {
	rect core.Rect

	nodes []TreeNode // flat, ordered visible list

	sel    int
	scroll int
	active bool

	// scrollX is the horizontal scroll offset, in display columns, applied
	// to every row's rendered content (indent + expander + icon + label).
	// contentW is the widest row across all of tv.nodes, recomputed by
	// SetNodes — the horizontal counterpart of scroll/len(tv.nodes) above.
	scrollX  int
	contentW int

	// mouseDragging distinguishes a fresh Button1 press from a continued
	// hold over the same row — mirrors MenuBar's/DataGrid's/Editor's field
	// of the same name and purpose. Without it, tcell's all-motion mouse
	// tracking resends Buttons()==Button1 on every cursor motion while the
	// button stays down, so a click that so much as twitches re-fires the
	// click handling on every resent event instead of once per press.
	mouseDragging bool

	// sbDragging is true while the user is dragging the scrollbar thumb —
	// see DataGrid's field of the same name and purpose for the rationale
	// on why this is a separate flag from mouseDragging.
	sbDragging  bool
	sbDraggingX bool // same, for the horizontal scrollbar thumb

	// Callbacks — set by the application layer
	OnExpand     func(nodeID TreeNodeID) // called when a node is expanded
	OnCollapse   func(nodeID TreeNodeID) // called when a node is collapsed
	OnSelect     func(nodeID TreeNodeID) // called when selection changes
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

// SetNodes replaces the entire visible node list.
// Callers typically rebuild this list in OnExpand after loading children.
func (tv *TreeView) SetNodes(nodes []TreeNode) {
	tv.nodes = nodes
	tv.sel = core.Clamp(tv.sel, 0, max(0, len(nodes)-1))
	// A collapse/refresh that shrinks the flat node list below the old
	// scroll offset would otherwise leave scroll pointing past the end of
	// nodes — Draw's render loop breaks on its first iteration in that
	// case, rendering nothing until the next arrow-key press recomputes
	// scroll via ensureVisible as a side effect.
	tv.ensureVisible(tv.rect.Inner(1).H)

	tv.contentW = 0
	for _, n := range nodes {
		if w := tv.lineWidth(n); w > tv.contentW {
			tv.contentW = w
		}
	}
	tv.scrollX = core.Clamp(tv.scrollX, 0, max(0, tv.contentW-tv.rect.Inner(1).W))
}

// lineWidth returns n's rendered row width in display columns: indent (2
// per depth level) + the 4-column expander field + icon (width-aware, plus
// its separating space) if present + the label.
func (tv *TreeView) lineWidth(n TreeNode) int {
	w := n.Depth*2 + 4
	if n.Icon != 0 {
		w += max(1, core.DisplayWidth(string(n.Icon))) + 1
	}
	w += core.DisplayWidth(n.Label)
	return w
}

// SelectID selects the node with the given ID, if present, and fires
// OnSelect — unlike SetNodes, whose tv.sel clamp is a pure bounds check with
// no notion of "this is the node the caller means." Use this when a node is
// added or replaced programmatically (e.g. a newly connected server's root)
// and should end up both visually selected and reported through OnSelect,
// the same as a manual click or arrow-key selection would.
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
		// logical line and drawn through DrawTextOffset so tv.scrollX shifts
		// it sideways uniformly; width-aware (wide/CJK glyphs, wide icons)
		// because DrawTextOffset measures by display column, not by rune.
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
	case tcell.KeyEnter, tcell.KeyRight:
		tv.toggleExpand()
		return true
	case tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		tv.collapseSelected()
		return true
	case tcell.KeyF10:
		// Shift+F10 is the cross-platform "open context menu" convention —
		// native to Windows and Linux, and also the binding most
		// cross-platform terminal/editor apps use on macOS, which has no
		// dedicated context-menu key of its own.
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
		// Ctrl+Space: a third, always-available keyboard equivalent for
		// opening the context menu, alongside Shift+F10/Menu above.
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

	// Scrollbar drag/click takes priority over row hit-testing below — the
	// vertical bar is drawn over the right border column (tv.rect.Right()-1)
	// spanning inner's rows, and the horizontal bar over the bottom border
	// row (tv.rect.Bottom()-1) spanning inner's columns (see Draw). The
	// latter sits outside the row range the row-based hit-testing below
	// checks for, so both scrollbar checks must run before that check, not
	// after it as a plain "does x/y already look like a node row" filter
	// would otherwise require.
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
	// A button press acts on one specific node, so it needs the stricter
	// content hit-test too: the row bound above doesn't constrain mx at all,
	// and the vertical scrollbar is drawn over the right border column (see
	// Draw). HandleScrollbarDrag has already claimed a press there whenever
	// the bar is actually showing, so this only changes the no-bar case —
	// but nodeIndexAt is also what app-level drag arming hit-tests with (see
	// NodeIDAt), and the two must agree on what "landed on a node" means.
	// The wheel cases below keep the looser row-only bound, so scrolling
	// with the pointer over the bar behaves exactly as before.
	if b := ev.Buttons(); b == tcell.Button1 || b == tcell.Button2 {
		if _, ok := tv.nodeIndexAt(mx, my); !ok {
			return false
		}
	}
	switch ev.Buttons() {
	case tcell.Button1:
		if tv.mouseDragging {
			// Still the same physical press (or the same drag toward the
			// query editor, see explorer_drag.go) — do not re-select or
			// re-toggle on every resent motion event.
			return true
		}
		tv.mouseDragging = true
		node := &tv.nodes[idx]
		// Only the "[+]"/"[-]" expander glyph toggles expand/collapse — a
		// click anywhere else on the row just (re)selects it. This is what
		// lets a node be click-dragged into the query editor without also
		// flipping its expand state: dragging always starts on the label,
		// never the expander glyph. mx is a screen column, but the expander's
		// own position is virtual (depth*2..depth*2+4, see Draw's row-line
		// layout) — it must be translated back through tv.scrollX before
		// comparing, or this only matches at scrollX==0.
		vcol := mx - inner.X + tv.scrollX
		onExpander := node.HasKids && vcol >= node.Depth*2 && vcol < node.Depth*2+4
		if tv.sel != idx {
			tv.sel = idx
			tv.fireSelect()
		}
		if onExpander {
			tv.toggleExpand()
		}
		return true
	case tcell.Button2: // tcell v3: Button2 is Secondary (right-click); Button3 is Middle.
		tv.sel = idx
		if tv.OnRightClick != nil {
			tv.OnRightClick(tv.nodes[idx].ID, mx, my)
		}
		return true
	case tcell.WheelUp:
		// Shift+wheel is the common desktop convention for horizontal
		// scroll; some terminals report it as WheelUp/WheelDown with a
		// Shift modifier rather than as WheelLeft/WheelRight below, so
		// honour both — matches DataGrid's/Editor's/PlanView's identical
		// convention.
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
// position (mx, my), and whether that position is over a node row at all.
// The test is against the content area (rect.Inner(1)), not the whole rect,
// so the border columns and rows the scrollbars are drawn over (see Draw)
// never resolve to a node.
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

// NodeIDAt returns the ID of the node drawn at screen position (mx, my),
// and whether there is one there. It's how a host finds out what a mouse
// press actually landed on, rather than inferring it from the selection
// afterward: a press on the scrollbar, on the border, or on blank space
// below the last node reports no node. App's Object Explorer drag-and-drop
// arms from this (via ObjectExplorer.NodeAt) — without it, a scrollbar drag
// was armed as a node drag and swallowed, so the thumb never followed the
// mouse.
func (tv *TreeView) NodeIDAt(mx, my int) (int, bool) {
	idx, ok := tv.nodeIndexAt(mx, my)
	if !ok {
		return 0, false
	}
	return tv.nodes[idx].ID, true
}

// scrollLeft/scrollRight nudge the horizontal scroll offset by 4 columns,
// clamped to [0, contentW-inner.W] so the scrollbar thumb never runs past
// either end of the track.
func (tv *TreeView) scrollLeft() {
	tv.scrollX = max(0, tv.scrollX-4)
}

func (tv *TreeView) scrollRight() {
	maxScroll := max(0, tv.contentW-tv.rect.Inner(1).W)
	tv.scrollX = min(maxScroll, tv.scrollX+4)
}

// toggleExpand flips the selected node's Expanded state and fires
// OnExpand/OnCollapse. OnExpand fires every time a node is expanded — even
// if it was already loaded before — so the caller can redisplay cached
// children. Deciding whether that means a real fetch or just redisplaying
// what's cached is the caller's job: TreeNode.Loaded is caller-supplied
// display metadata, not something TreeView tracks or gates on itself.
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

// collapseSelected collapses the currently selected node, if it's
// expanded, firing OnCollapse. No-op if nothing is selected or it's
// already collapsed.
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
	n := tv.SelectedNode()
	if n == nil || tv.OnRightClick == nil {
		return
	}
	inner := tv.rect.Inner(1)
	// n.Depth*2 is a virtual column (see Draw's row-line layout); translate
	// it back through tv.scrollX like the mouse-click expander hit-test
	// does, clamped so a node scrolled left of the panel still pops the
	// menu on-screen rather than off its left edge.
	x := inner.X + max(0, n.Depth*2-tv.scrollX)
	y := inner.Y + (tv.sel - tv.scroll)
	tv.OnRightClick(n.ID, x, y)
}
