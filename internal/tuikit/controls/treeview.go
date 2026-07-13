package controls

import (
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

	nodes  []TreeNode // flat, ordered visible list
	nodeID int        // auto-increment for IDs

	sel    int
	scroll int
	active bool

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
	tv.sel = core.Clamp(tv.sel, 0, core.Max(0, len(nodes)-1))
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

		col := inner.X
		// Indent
		for i := 0; i < node.Depth*2 && col < inner.Right(); i++ {
			s.SetContent(col, y, ' ', nil, style)
			col++
		}
		// Expander
		for _, r := range expander {
			if col >= inner.Right() {
				break
			}
			s.SetContent(col, y, r, nil, style)
			col++
		}
		// Icon — width-aware because some icon styles (emoji) render as
		// double-width glyphs; the second cell of a wide glyph is left
		// untouched for the terminal to fill in, matching core.DrawText's
		// putGrapheme convention.
		if node.Icon != 0 && col < inner.Right() {
			iconW := core.Max(1, core.DisplayWidth(string(node.Icon)))
			s.SetContent(col, y, node.Icon, nil, style)
			col += iconW
			if col < inner.Right() {
				s.SetContent(col, y, ' ', nil, style)
				col++
			}
		}
		// Label (width-aware: clips correctly on wide/CJK glyphs)
		if col < inner.Right() {
			core.DrawTextClipped(s, col, y, inner.Right()-col, style, node.Label)
		}
	}

	if len(tv.nodes) > inner.H {
		sbStyle := tcell.StyleDefault.Background(p.PanelBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, tv.rect.Right()-1, inner.Y, inner.H,
			len(tv.nodes), inner.H, tv.scroll, sbStyle, sbThumb)
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
		tv.sel = core.Max(0, tv.sel-inner.H)
		tv.ensureVisible(inner.H)
		tv.fireSelect()
		return true
	case tcell.KeyPgDn:
		tv.sel = core.Min(len(tv.nodes)-1, tv.sel+inner.H)
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
	if !tv.rect.Contains(mx, my) {
		return false
	}
	inner := tv.rect.Inner(1)
	row := my - inner.Y
	if row < 0 || row >= inner.H {
		return false
	}
	idx := tv.scroll + row
	if idx < 0 || idx >= len(tv.nodes) {
		return false
	}
	switch ev.Buttons() {
	case tcell.Button1:
		if tv.sel == idx {
			tv.toggleExpand()
		} else {
			tv.sel = idx
			tv.fireSelect()
		}
		return true
	case tcell.Button2: // tcell v3: Button2 is Secondary (right-click); Button3 is Middle.
		tv.sel = idx
		if tv.OnRightClick != nil {
			tv.OnRightClick(tv.nodes[idx].ID, mx, my)
		}
		return true
	case tcell.WheelUp:
		if tv.scroll > 0 {
			tv.scroll--
		}
		return true
	case tcell.WheelDown:
		if tv.scroll < len(tv.nodes)-inner.H {
			tv.scroll++
		}
		return true
	}
	return false
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
	x := inner.X + n.Depth*2
	y := inner.Y + (tv.sel - tv.scroll)
	tv.OnRightClick(n.ID, x, y)
}
