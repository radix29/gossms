// Package controls provides higher-level, reusable TUI controls:
//
//   - MenuBar — application menu bar with drop-down menus
//   - ContextMenu — floating right-click popup menu
//   - TreeView — collapsible/expandable tree with generic node data
//   - DataGrid — scrollable, column-aligned tabular data display
//   - Editor — multi-line text editor with optional syntax highlighting
//
// Controls depend on core, theme, and widgets but not on any application
// types.  The application layer passes data in and reads state out;
// controls never call back into the application directly — instead they
// fire callbacks (func values) that the caller wires up.
package controls

import (
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// MenuItem / Menu — shared by MenuBar and ContextMenu
// ---------------------------------------------------------------------------

// MenuItem is a single entry in a menu.
type MenuItem struct {
	Label    string
	Shortcut string
	Divider  bool   // renders as a ──── separator
	Action   func() // called when the item is activated
}

// Menu is a top-level menu header with its items.
type Menu struct {
	Label string
	Items []MenuItem
}

// ---------------------------------------------------------------------------
// MenuBar
// ---------------------------------------------------------------------------

// MenuBar is the horizontal application menu bar with drop-down menus.
type MenuBar struct {
	rect      core.Rect
	menus     []Menu
	openMenu  int // -1 = closed
	hoverMenu int
}

// NewMenuBar creates a MenuBar.
func NewMenuBar() *MenuBar {
	return new(MenuBar{openMenu: -1, hoverMenu: -1})
}

// SetBounds positions the menu bar.
func (mb *MenuBar) SetBounds(x, y, w int) {
	mb.rect = core.Rect{X: x, Y: y, W: w, H: 1}
}

// SetMenus replaces all menus.
func (mb *MenuBar) SetMenus(menus []Menu) { mb.menus = menus }

// IsOpen reports whether a dropdown is currently open.
func (mb *MenuBar) IsOpen() bool { return mb.openMenu >= 0 }

// Close closes any open dropdown.
func (mb *MenuBar) Close() { mb.openMenu = -1 }

// Draw renders just the menu bar row. Call DrawOverlay afterward, once all
// other content has been drawn, to render any open dropdown on top — the
// dropdown extends below the bar into rows other panels also draw into, so
// it must be painted last or it gets overwritten before Show().
func (mb *MenuBar) Draw(s tcell.Screen) {
	p := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, mb.rect, ' ', barStyle)

	col := mb.rect.X + 1
	for i, m := range mb.menus {
		label := " " + m.Label + " "
		st := barStyle
		if i == mb.openMenu || i == mb.hoverMenu {
			st = tcell.StyleDefault.Background(p.MenuSelected).Foreground(tcell.ColorWhite)
		}
		core.DrawText(s, col, mb.rect.Y, st, label)
		col += core.DisplayWidth(label)
	}
}

// DrawOverlay renders the open dropdown, if any. Must be called after every
// other panel has drawn, so the dropdown isn't painted over.
func (mb *MenuBar) DrawOverlay(s tcell.Screen) {
	if mb.openMenu >= 0 && mb.openMenu < len(mb.menus) {
		mb.drawDropdown(s, mb.openMenu)
	}
}

// menuHeaderOffset returns the column where the dropdown for menu index idx
// should begin, measured by display width of the preceding menu headers.
func (mb *MenuBar) menuHeaderOffset(idx int) int {
	col := mb.rect.X + 1
	for i := 0; i < idx; i++ {
		col += core.DisplayWidth(" " + mb.menus[i].Label + " ")
	}
	return col
}

func (mb *MenuBar) drawDropdown(s tcell.Screen, idx int) {
	p := theme.Active()
	menu := mb.menus[idx]

	col, w, h := mb.dropdownGeometry(idx)

	ddStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	borderStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Border)
	r := core.Rect{X: col, Y: mb.rect.Y + 1, W: w, H: h}
	core.DrawBox(s, r, borderStyle)
	core.FillRect(s, r.Inner(1), ' ', ddStyle)

	for i, item := range menu.Items {
		y := mb.rect.Y + 2 + i
		if item.Divider {
			for x := col + 1; x < col+w-1; x++ {
				s.SetContent(x, y, '─', nil, borderStyle)
			}
			s.SetContent(col, y, '├', nil, borderStyle)
			s.SetContent(col+w-1, y, '┤', nil, borderStyle)
			continue
		}
		core.DrawTextClipped(s, col+2, y, w-4, ddStyle, item.Label)
		if item.Shortcut != "" {
			sx := col + w - 1 - core.DisplayWidth(item.Shortcut) - 1
			core.DrawText(s, sx, y,
				tcell.StyleDefault.Background(p.MenuBar).Foreground(p.TextDim),
				item.Shortcut)
		}
	}
}

// HandleKey processes keyboard when a dropdown is open.
func (mb *MenuBar) HandleKey(ev *tcell.EventKey) bool {
	if mb.openMenu < 0 {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		mb.openMenu = -1
	case tcell.KeyLeft:
		mb.openMenu = (mb.openMenu - 1 + len(mb.menus)) % len(mb.menus)
	case tcell.KeyRight:
		mb.openMenu = (mb.openMenu + 1) % len(mb.menus)
	case tcell.KeyEnter:
		if menu := mb.menus[mb.openMenu]; len(menu.Items) > 0 {
			for _, item := range menu.Items {
				if !item.Divider && item.Action != nil {
					item.Action()
					mb.openMenu = -1
					return true
				}
			}
		}
		mb.openMenu = -1
	}
	return true
}

// HandleMouse processes mouse events for the bar and any open dropdown.
func (mb *MenuBar) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()

	if mb.openMenu >= 0 && my != mb.rect.Y {
		if mb.dropdownContains(mx, my) {
			if ev.Buttons() == tcell.Button1 {
				mb.handleDropdownClick(mx, my)
				return true
			}
			return true
		}
		mb.openMenu = -1
		return false
	}

	if my == mb.rect.Y {
		col := mb.rect.X + 1
		mb.hoverMenu = -1
		for i, m := range mb.menus {
			label := " " + m.Label + " "
			labelW := core.DisplayWidth(label)
			if mx >= col && mx < col+labelW {
				mb.hoverMenu = i
				if ev.Buttons() == tcell.Button1 {
					if mb.openMenu == i {
						mb.openMenu = -1
					} else {
						mb.openMenu = i
					}
					return true
				}
				break
			}
			col += labelW
		}
	}
	return false
}

// dropdownGeometry returns the column, width, and height of the open
// dropdown for menu index idx, using the same width calculation as
// drawDropdown so hit-testing always matches what was actually drawn.
func (mb *MenuBar) dropdownGeometry(idx int) (col, w, h int) {
	menu := mb.menus[idx]
	col = mb.menuHeaderOffset(idx)
	w = 28
	for _, item := range menu.Items {
		if n := core.DisplayWidth(item.Label) + core.DisplayWidth(item.Shortcut) + 4; n > w {
			w = n
		}
	}
	if col+w > mb.rect.X+mb.rect.W {
		col = mb.rect.X + mb.rect.W - w
	}
	h = len(menu.Items) + 2
	return col, w, h
}

func (mb *MenuBar) dropdownContains(mx, my int) bool {
	if mb.openMenu < 0 {
		return false
	}
	col, w, h := mb.dropdownGeometry(mb.openMenu)
	return mx >= col && mx < col+w && my >= mb.rect.Y+1 && my < mb.rect.Y+1+h
}

func (mb *MenuBar) handleDropdownClick(mx, my int) {
	if mb.openMenu < 0 {
		return
	}
	itemIdx := my - (mb.rect.Y + 2)
	menu := mb.menus[mb.openMenu]
	mb.openMenu = -1
	if itemIdx >= 0 && itemIdx < len(menu.Items) {
		item := menu.Items[itemIdx]
		if !item.Divider && item.Action != nil {
			item.Action()
		}
	}
}

// ---------------------------------------------------------------------------
// ContextMenu
// ---------------------------------------------------------------------------

// ContextMenu is a floating popup menu shown on right-click.
type ContextMenu struct {
	x, y    int
	items   []MenuItem
	hover   int
	visible bool
}

// Show displays the menu at (x,y) with the given items.
func (cm *ContextMenu) Show(x, y int, items []MenuItem) {
	cm.x, cm.y = x, y
	cm.items = items
	cm.hover = -1
	cm.visible = true
}

// Hide dismisses the menu.
func (cm *ContextMenu) Hide() { cm.visible = false }

// Visible reports whether the menu is shown.
func (cm *ContextMenu) Visible() bool { return cm.visible }

func (cm *ContextMenu) width() int {
	w := 20
	for _, item := range cm.items {
		if n := core.DisplayWidth(item.Label) + 4; n > w {
			w = n
		}
	}
	return w
}

// Draw renders the context menu.
func (cm *ContextMenu) Draw(s tcell.Screen) {
	if !cm.visible {
		return
	}
	sw, sh := s.Size()
	w := cm.width()
	h := len(cm.items) + 2
	x, y := cm.x, cm.y
	if x+w > sw {
		x = sw - w
	}
	if y+h > sh {
		y = sh - h
	}
	p := theme.Active()
	itemStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	hoverStyle := theme.StyleSelected()
	borderStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Border)
	r := core.Rect{X: x, Y: y, W: w, H: h}
	core.FillRect(s, r, ' ', itemStyle)
	core.DrawBox(s, r, borderStyle)

	for i, item := range cm.items {
		iy := y + 1 + i
		if item.Divider {
			for cx := x + 1; cx < x+w-1; cx++ {
				s.SetContent(cx, iy, '─', nil, borderStyle)
			}
			s.SetContent(x, iy, '├', nil, borderStyle)
			s.SetContent(x+w-1, iy, '┤', nil, borderStyle)
			continue
		}
		st := itemStyle
		if i == cm.hover {
			st = hoverStyle
		}
		core.FillRect(s, core.Rect{X: x + 1, Y: iy, W: w - 2, H: 1}, ' ', st)
		core.DrawTextClipped(s, x+2, iy, w-4, st, item.Label)
	}
}

// HandleKey processes keyboard events.
func (cm *ContextMenu) HandleKey(ev *tcell.EventKey) bool {
	if !cm.visible {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		cm.Hide()
	case tcell.KeyUp:
		if cm.hover > 0 {
			cm.hover--
		}
	case tcell.KeyDown:
		if cm.hover < len(cm.items)-1 {
			cm.hover++
		}
	case tcell.KeyEnter:
		if cm.hover >= 0 && cm.hover < len(cm.items) {
			item := cm.items[cm.hover]
			cm.Hide()
			if !item.Divider && item.Action != nil {
				item.Action()
			}
		}
	}
	return true
}

// HandleMouse processes mouse events.
func (cm *ContextMenu) HandleMouse(ev *tcell.EventMouse) bool {
	if !cm.visible {
		return false
	}
	mx, my := ev.Position()
	w := cm.width()
	h := len(cm.items) + 2
	x, y := cm.x, cm.y

	if mx < x || mx >= x+w || my < y || my >= y+h {
		if ev.Buttons() == tcell.Button1 {
			cm.Hide()
		}
		return false
	}

	itemIdx := my - y - 1
	if itemIdx >= 0 && itemIdx < len(cm.items) {
		cm.hover = itemIdx
	}
	if ev.Buttons() == tcell.Button1 && itemIdx >= 0 && itemIdx < len(cm.items) {
		item := cm.items[itemIdx]
		cm.Hide()
		if !item.Divider && item.Action != nil {
			item.Action()
		}
		return true
	}
	return true
}

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
		// Icon
		if node.Icon != 0 {
			if col < inner.Right() {
				s.SetContent(col, y, node.Icon, nil, style)
				col++
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
	case tcell.KeyLeft:
		if n := tv.SelectedNode(); n != nil && n.Expanded {
			n.Expanded = false
			if tv.OnCollapse != nil {
				tv.OnCollapse(n.ID)
			}
		}
		return true
	}
	switch core.EvRune(ev) {
	case '+':
		tv.toggleExpand()
		return true
	case '-':
		if n := tv.SelectedNode(); n != nil && n.Expanded {
			n.Expanded = false
			if tv.OnCollapse != nil {
				tv.OnCollapse(n.ID)
			}
		}
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
	case tcell.Button3:
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

func (tv *TreeView) toggleExpand() {
	n := tv.SelectedNode()
	if n == nil || !n.HasKids {
		return
	}
	n.Expanded = !n.Expanded
	if n.Expanded {
		if !n.Loaded && tv.OnExpand != nil {
			tv.OnExpand(n.ID)
		}
	} else if tv.OnCollapse != nil {
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

// ---------------------------------------------------------------------------
// DataGrid
// ---------------------------------------------------------------------------

// DataGrid renders a scrollable, column-aligned tabular dataset.
type DataGrid struct {
	rect      core.Rect
	columns   []string
	rows      [][]string
	colWidths []int
	selRow    int
	scrollRow int
	scrollCol int
	status    string
	active    bool
}

// NewDataGrid creates a DataGrid.
func NewDataGrid() *DataGrid {
	return new(DataGrid{status: "Ready"})
}

// SetBounds positions the grid.
func (g *DataGrid) SetBounds(x, y, w, h int) { g.rect = core.Rect{X: x, Y: y, W: w, H: h} }

// SetData populates the grid.
func (g *DataGrid) SetData(columns []string, rows [][]string) {
	g.columns = columns
	g.rows = rows
	g.selRow, g.scrollRow, g.scrollCol = 0, 0, 0
	g.computeColWidths()
	g.status = core.Itoa(len(rows)) + " rows"
}

// SetError shows an error row.
func (g *DataGrid) SetError(err error) {
	g.columns = []string{"Error"}
	g.rows = [][]string{{err.Error()}}
	g.colWidths = []int{g.rect.W - 2}
	g.selRow, g.scrollRow = 0, 0
	g.status = "Error"
}

// SetStatus sets the status bar text.
func (g *DataGrid) SetStatus(msg string) { g.status = msg }

func (g *DataGrid) computeColWidths() {
	g.colWidths = make([]int, len(g.columns))
	for i, col := range g.columns {
		g.colWidths[i] = core.DisplayWidth(col) + 2
	}
	for _, row := range g.rows {
		for i, cell := range row {
			if i < len(g.colWidths) {
				if w := core.DisplayWidth(cell) + 2; w > g.colWidths[i] {
					g.colWidths[i] = w
				}
			}
		}
	}
	for i := range g.colWidths {
		g.colWidths[i] = core.Clamp(g.colWidths[i], 6, 40)
	}
}

// Draw renders the data grid.
func (g *DataGrid) Draw(s tcell.Screen) {
	core.FillRect(s, g.rect, ' ', theme.StylePanel())
	if g.rect.H < 3 {
		return
	}
	g.drawRow(s, g.rect.Y, g.columns, theme.StyleGridHeader())
	sep := tcell.StyleDefault.Background(theme.Active().GridHeader).Foreground(theme.Active().GridBorder)
	core.DrawHLine(s, g.rect.X, g.rect.Y+1, g.rect.W, sep)

	dataH := g.rect.H - 3
	for row := 0; row < dataH; row++ {
		dataIdx := g.scrollRow + row
		y := g.rect.Y + 2 + row
		if dataIdx >= len(g.rows) {
			core.FillRect(s, core.Rect{X: g.rect.X, Y: y, W: g.rect.W, H: 1}, ' ', theme.StylePanel())
			continue
		}
		style := theme.StyleGridRow()
		if dataIdx%2 == 1 {
			style = theme.StyleGridRowAlt()
		}
		if dataIdx == g.selRow {
			style = theme.StyleGridSelected()
		}
		g.drawRow(s, y, g.rows[dataIdx], style)
	}

	// Status bar
	p := theme.Active()
	statusStyle := tcell.StyleDefault.Background(p.GridHeader).Foreground(p.TextDim)
	core.FillRect(s, core.Rect{X: g.rect.X, Y: g.rect.Y + g.rect.H - 1, W: g.rect.W, H: 1}, ' ', statusStyle)
	core.DrawTextClipped(s, g.rect.X+1, g.rect.Y+g.rect.H-1, g.rect.W-2, statusStyle, g.status)

	// Scrollbar
	if len(g.rows) > dataH && dataH > 0 {
		sbStyle := tcell.StyleDefault.Background(p.GridHeader).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, g.rect.Right()-1, g.rect.Y+2, dataH,
			len(g.rows), dataH, g.scrollRow, sbStyle, sbThumb)
	}
}

func (g *DataGrid) drawRow(s tcell.Screen, y int, cells []string, style tcell.Style) {
	col := g.rect.X
	for i, cell := range cells {
		if i >= len(g.colWidths) {
			break
		}
		cw := g.colWidths[i]
		if col >= g.rect.Right() {
			break
		}
		avail := core.Min(cw, g.rect.Right()-col)
		core.FillRect(s, core.Rect{X: col, Y: y, W: avail, H: 1}, ' ', style)
		core.DrawTextClipped(s, col+1, y, avail-2, style, core.Truncate(cell, avail-2))
		if col+cw-1 < g.rect.Right() {
			s.SetContent(col+cw-1, y, '|', nil, style.Foreground(theme.Active().GridBorder))
		}
		col += cw
	}
}

// HandleKey handles keyboard navigation.
func (g *DataGrid) HandleKey(ev *tcell.EventKey) bool {
	dataH := g.rect.H - 3
	switch ev.Key() {
	case tcell.KeyUp:
		if g.selRow > 0 {
			g.selRow--
			g.ensureVisible(dataH)
		}
	case tcell.KeyDown:
		if g.selRow < len(g.rows)-1 {
			g.selRow++
			g.ensureVisible(dataH)
		}
	case tcell.KeyPgUp:
		g.selRow = core.Max(0, g.selRow-dataH)
		g.ensureVisible(dataH)
	case tcell.KeyPgDn:
		g.selRow = core.Min(len(g.rows)-1, g.selRow+dataH)
		g.ensureVisible(dataH)
	case tcell.KeyHome:
		g.selRow, g.scrollRow = 0, 0
	case tcell.KeyEnd:
		g.selRow = len(g.rows) - 1
		g.ensureVisible(dataH)
	case tcell.KeyLeft:
		g.scrollCol = core.Max(0, g.scrollCol-10)
	case tcell.KeyRight:
		g.scrollCol += 10
	default:
		return false
	}
	return true
}

// HandleMouse handles mouse events.
func (g *DataGrid) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	if !g.rect.Contains(mx, my) {
		return false
	}
	dataH := g.rect.H - 3
	switch ev.Buttons() {
	case tcell.Button1:
		if row := g.scrollRow + (my - g.rect.Y - 2); row >= 0 && row < len(g.rows) {
			g.selRow = row
		}
	case tcell.WheelUp:
		if g.scrollRow > 0 {
			g.scrollRow--
		}
	case tcell.WheelDown:
		if g.scrollRow < len(g.rows)-dataH {
			g.scrollRow++
		}
	}
	return true
}

func (g *DataGrid) ensureVisible(dataH int) {
	if g.selRow < g.scrollRow {
		g.scrollRow = g.selRow
	}
	if g.selRow >= g.scrollRow+dataH {
		g.scrollRow = g.selRow - dataH + 1
	}
}

// ---------------------------------------------------------------------------
// Editor
// ---------------------------------------------------------------------------

// Highlighter is a function that receives a line of runes and returns a
// slice of ColorRun segments.  Pass nil to disable syntax highlighting.
type Highlighter func(line []rune) []ColorRun

// ColorRun describes a coloured segment within an editor line.
type ColorRun struct {
	Start int
	Len   int
	Style tcell.Style
}

// editorState is an undo/redo snapshot.
type editorState struct {
	lines     [][]rune
	cursorRow int
	cursorCol int
}

// Editor is a multi-line text editor.
//
// Note: unlike the rest of tuikit (TreeView, DataGrid, MenuBar, dialogs —
// all of which use core.DisplayWidth for correct rendering of wide/CJK
// characters), Editor's cursor, selection, and rendering are rune-indexed:
// one rune occupies exactly one column. This is an intentional scope
// limit — full grapheme-aware cursor movement, click-to-position mapping,
// and line wrapping would require reworking the editing model throughout.
// SQL query text is overwhelmingly ASCII (keywords, identifiers,
// punctuation), so this only affects alignment inside wide-character
// string literals, which remain editable but may render with minor
// column drift.
type Editor struct {
	rect      core.Rect
	lines     [][]rune
	cursorRow int
	cursorCol int
	scrollRow int
	scrollCol int
	active    bool
	highlight Highlighter

	undoStack []editorState
	redoStack []editorState
}

// NewEditor creates an Editor. Pass a Highlighter or nil.
func NewEditor(h Highlighter) *Editor {
	return new(Editor{
		lines:     [][]rune{{}},
		highlight: h,
	})
}

// SetBounds positions the editor.
func (e *Editor) SetBounds(x, y, w, h int) { e.rect = core.Rect{X: x, Y: y, W: w, H: h} }

// SetActive sets focus state.
func (e *Editor) SetActive(v bool) { e.active = v }

// Text returns the editor content.
func (e *Editor) Text() string {
	var sb strings.Builder
	for i, line := range e.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(line))
	}
	return sb.String()
}

// SetText replaces content.
func (e *Editor) SetText(text string) {
	parts := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	e.lines = make([][]rune, len(parts))
	for i, p := range parts {
		e.lines[i] = []rune(p)
	}
	e.cursorRow, e.cursorCol, e.scrollRow, e.scrollCol = 0, 0, 0, 0
}

const gutterW = 5 // " NNN "

// Draw renders the editor.
func (e *Editor) Draw(s tcell.Screen) {
	p := theme.Active()
	bgStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.Text)
	gutterStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorLineNum)
	contentX := e.rect.X + gutterW
	contentW := e.rect.W - gutterW

	core.FillRect(s, e.rect, ' ', bgStyle)

	for row := 0; row < e.rect.H; row++ {
		lineIdx := e.scrollRow + row
		y := e.rect.Y + row

		// Gutter
		core.FillRect(s, core.Rect{X: e.rect.X, Y: y, W: gutterW, H: 1}, ' ', gutterStyle)
		if lineIdx < len(e.lines) {
			num := core.Itoa(lineIdx + 1)
			gx := e.rect.X + gutterW - 1 - len(num)
			core.DrawText(s, gx, y, gutterStyle, num)
		}

		if lineIdx >= len(e.lines) {
			continue
		}
		line := e.lines[lineIdx]

		if e.highlight != nil {
			runs := e.highlight(line)
			e.drawHighlighted(s, contentX, y, contentW, line, runs)
		} else {
			// Plain
			for col := 0; col < contentW; col++ {
				ch := ' '
				ci := e.scrollCol + col
				if ci < len(line) {
					ch = line[ci]
				}
				s.SetContent(contentX+col, y, ch, nil, bgStyle)
			}
		}
	}

	if e.active {
		curY := e.rect.Y + (e.cursorRow - e.scrollRow)
		curX := contentX + (e.cursorCol - e.scrollCol)
		if curY >= e.rect.Y && curY < e.rect.Y+e.rect.H &&
			curX >= contentX && curX < contentX+contentW {
			s.ShowCursor(curX, curY)
		}
	}
}

func (e *Editor) drawHighlighted(s tcell.Screen, x, y, w int, line []rune, runs []ColorRun) {
	p := theme.Active()
	bgStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.Text)

	// Build a per-column style map
	styles := make([]tcell.Style, len(line))
	for i := range styles {
		styles[i] = bgStyle
	}
	for _, run := range runs {
		for j := run.Start; j < run.Start+run.Len && j < len(styles); j++ {
			styles[j] = run.Style
		}
	}

	for col := 0; col < w; col++ {
		ci := e.scrollCol + col
		ch := ' '
		st := bgStyle
		if ci < len(line) {
			ch = line[ci]
			st = styles[ci]
		}
		s.SetContent(x+col, y, ch, nil, st)
	}
}

// HandleKey handles keyboard input.
func (e *Editor) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp:
		if e.cursorRow > 0 {
			e.cursorRow--
		}
	case tcell.KeyDown:
		if e.cursorRow < len(e.lines)-1 {
			e.cursorRow++
		}
	case tcell.KeyLeft:
		if e.cursorCol > 0 {
			e.cursorCol--
		} else if e.cursorRow > 0 {
			e.cursorRow--
			e.cursorCol = len(e.lines[e.cursorRow])
		}
	case tcell.KeyRight:
		if e.cursorRow < len(e.lines) && e.cursorCol < len(e.lines[e.cursorRow]) {
			e.cursorCol++
		} else if e.cursorRow < len(e.lines)-1 {
			e.cursorRow++
			e.cursorCol = 0
		}
	case tcell.KeyHome, tcell.KeyCtrlA:
		e.cursorCol = 0
	case tcell.KeyEnd:
		if e.cursorRow < len(e.lines) {
			e.cursorCol = len(e.lines[e.cursorRow])
		}
	case tcell.KeyPgUp:
		e.cursorRow = core.Max(0, e.cursorRow-e.rect.H)
	case tcell.KeyPgDn:
		e.cursorRow = core.Min(len(e.lines)-1, e.cursorRow+e.rect.H)
	case tcell.KeyEnter:
		e.pushUndo()
		e.insertNewline()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		e.pushUndo()
		e.backspace()
	case tcell.KeyDelete:
		e.pushUndo()
		e.deleteChar()
	case tcell.KeyTab:
		e.pushUndo()
		e.insertRune('\t')
	case tcell.KeyCtrlZ:
		e.undo()
	case tcell.KeyCtrlY:
		e.redo()
	default:
		r := core.EvRune(ev)
		if r != 0 && ev.Modifiers()&tcell.ModCtrl == 0 && ev.Modifiers()&tcell.ModAlt == 0 {
			e.pushUndo()
			e.insertRune(r)
		} else {
			return false
		}
	}
	e.clampCursor()
	e.ensureCursorVisible()
	return true
}

// HandleMouse handles mouse events.
func (e *Editor) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	contentX := e.rect.X + gutterW
	if mx < contentX || !e.rect.Contains(mx, my) {
		return false
	}
	if ev.Buttons() == tcell.Button1 {
		row := core.Clamp(e.scrollRow+(my-e.rect.Y), 0, len(e.lines)-1)
		col := core.Max(0, e.scrollCol+(mx-contentX))
		if row < len(e.lines) && col > len(e.lines[row]) {
			col = len(e.lines[row])
		}
		e.cursorRow, e.cursorCol = row, col
		return true
	}
	if ev.Buttons() == tcell.WheelUp && e.scrollRow > 0 {
		e.scrollRow--
		return true
	}
	if ev.Buttons() == tcell.WheelDown && e.scrollRow < len(e.lines)-1 {
		e.scrollRow++
		return true
	}
	return false
}

func (e *Editor) clampCursor() {
	e.cursorRow = core.Clamp(e.cursorRow, 0, len(e.lines)-1)
	if e.cursorRow < len(e.lines) {
		e.cursorCol = core.Clamp(e.cursorCol, 0, len(e.lines[e.cursorRow]))
	}
}

func (e *Editor) ensureCursorVisible() {
	if e.cursorRow < e.scrollRow {
		e.scrollRow = e.cursorRow
	}
	if e.cursorRow >= e.scrollRow+e.rect.H {
		e.scrollRow = e.cursorRow - e.rect.H + 1
	}
	contentW := e.rect.W - gutterW
	if e.cursorCol < e.scrollCol {
		e.scrollCol = e.cursorCol
	}
	if e.cursorCol >= e.scrollCol+contentW {
		e.scrollCol = e.cursorCol - contentW + 1
	}
}

func (e *Editor) insertRune(r rune) {
	if e.cursorRow >= len(e.lines) {
		e.lines = append(e.lines, []rune{})
	}
	line := e.lines[e.cursorRow]
	nl := make([]rune, len(line)+1)
	copy(nl, line[:e.cursorCol])
	nl[e.cursorCol] = r
	copy(nl[e.cursorCol+1:], line[e.cursorCol:])
	e.lines[e.cursorRow] = nl
	e.cursorCol++
}

func (e *Editor) insertNewline() {
	if e.cursorRow >= len(e.lines) {
		e.lines = append(e.lines, []rune{})
	}
	line := e.lines[e.cursorRow]
	before := make([]rune, e.cursorCol)
	copy(before, line[:e.cursorCol])
	after := make([]rune, len(line)-e.cursorCol)
	copy(after, line[e.cursorCol:])
	e.lines[e.cursorRow] = before
	nl := make([][]rune, len(e.lines)+1)
	copy(nl, e.lines[:e.cursorRow+1])
	nl[e.cursorRow+1] = after
	copy(nl[e.cursorRow+2:], e.lines[e.cursorRow+1:])
	e.lines = nl
	e.cursorRow++
	e.cursorCol = 0
}

func (e *Editor) backspace() {
	if e.cursorRow == 0 && e.cursorCol == 0 {
		return
	}
	if e.cursorCol > 0 {
		line := e.lines[e.cursorRow]
		e.lines[e.cursorRow] = append(line[:e.cursorCol-1], line[e.cursorCol:]...)
		e.cursorCol--
	} else {
		prev := e.lines[e.cursorRow-1]
		cur := e.lines[e.cursorRow]
		e.cursorCol = len(prev)
		e.lines[e.cursorRow-1] = append(prev, cur...)
		e.lines = append(e.lines[:e.cursorRow], e.lines[e.cursorRow+1:]...)
		e.cursorRow--
	}
}

func (e *Editor) deleteChar() {
	if e.cursorRow >= len(e.lines) {
		return
	}
	line := e.lines[e.cursorRow]
	if e.cursorCol < len(line) {
		e.lines[e.cursorRow] = append(line[:e.cursorCol], line[e.cursorCol+1:]...)
	} else if e.cursorRow < len(e.lines)-1 {
		e.lines[e.cursorRow] = append(line, e.lines[e.cursorRow+1]...)
		e.lines = append(e.lines[:e.cursorRow+1], e.lines[e.cursorRow+2:]...)
	}
}

func (e *Editor) pushUndo() {
	lines := make([][]rune, len(e.lines))
	for i, l := range e.lines {
		nl := make([]rune, len(l))
		copy(nl, l)
		lines[i] = nl
	}
	e.undoStack = append(e.undoStack, editorState{lines, e.cursorRow, e.cursorCol})
	e.redoStack = nil
}

func (e *Editor) snapshot() editorState {
	lines := make([][]rune, len(e.lines))
	for i, l := range e.lines {
		nl := make([]rune, len(l))
		copy(nl, l)
		lines[i] = nl
	}
	return editorState{lines, e.cursorRow, e.cursorCol}
}

func (e *Editor) undo() {
	if len(e.undoStack) == 0 {
		return
	}
	e.redoStack = append(e.redoStack, e.snapshot())
	st := e.undoStack[len(e.undoStack)-1]
	e.undoStack = e.undoStack[:len(e.undoStack)-1]
	e.lines, e.cursorRow, e.cursorCol = st.lines, st.cursorRow, st.cursorCol
}

func (e *Editor) redo() {
	if len(e.redoStack) == 0 {
		return
	}
	e.undoStack = append(e.undoStack, e.snapshot())
	st := e.redoStack[len(e.redoStack)-1]
	e.redoStack = e.redoStack[:len(e.redoStack)-1]
	e.lines, e.cursorRow, e.cursorCol = st.lines, st.cursorRow, st.cursorCol
}

// ---------------------------------------------------------------------------
// SQL syntax highlighter (can be used as a Highlighter for Editor)
// ---------------------------------------------------------------------------

var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "INSERT": true, "UPDATE": true,
	"DELETE": true, "CREATE": true, "DROP": true, "ALTER": true, "TABLE": true,
	"INDEX": true, "VIEW": true, "PROCEDURE": true, "FUNCTION": true, "TRIGGER": true,
	"DATABASE": true, "SCHEMA": true, "AND": true, "OR": true, "NOT": true,
	"IN": true, "IS": true, "NULL": true, "LIKE": true, "BETWEEN": true,
	"JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "FULL": true,
	"OUTER": true, "ON": true, "AS": true, "ORDER": true, "BY": true,
	"GROUP": true, "HAVING": true, "DISTINCT": true, "TOP": true, "LIMIT": true,
	"OFFSET": true, "UNION": true, "ALL": true, "EXISTS": true, "CASE": true,
	"WHEN": true, "THEN": true, "ELSE": true, "END": true, "IF": true,
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "TRANSACTION": true,
	"EXEC": true, "EXECUTE": true, "SET": true, "USE": true, "GO": true,
	"WITH": true, "DECLARE": true, "PRINT": true, "RETURN": true,
	"INT": true, "BIGINT": true, "VARCHAR": true, "NVARCHAR": true, "CHAR": true,
	"NCHAR": true, "TEXT": true, "NTEXT": true, "DATETIME": true, "DATE": true,
	"TIME": true, "BIT": true, "FLOAT": true, "DECIMAL": true, "NUMERIC": true,
	"MONEY": true, "UNIQUEIDENTIFIER": true, "VARBINARY": true,
	"PRIMARY": true, "KEY": true, "FOREIGN": true, "REFERENCES": true,
	"CONSTRAINT": true, "DEFAULT": true, "IDENTITY": true, "UNIQUE": true,
	"CHECK": true, "CASCADE": true,
}

// SQLHighlighter is the built-in SQL syntax highlighter for Editor.
func SQLHighlighter(p *theme.Palette) Highlighter {
	kwStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorKeyword).Bold(true)
	strStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorString)
	cmtStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorComment)
	numStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorNumber)

	return func(line []rune) []ColorRun {
		runs := make([]ColorRun, 0, 8)
		i := 0
		for i < len(line) {
			// Line comment
			if i+1 < len(line) && line[i] == '-' && line[i+1] == '-' {
				runs = append(runs, ColorRun{i, len(line) - i, cmtStyle})
				break
			}
			// String literal
			if line[i] == '\'' {
				j := i + 1
				for j < len(line) && line[j] != '\'' {
					j++
				}
				if j < len(line) {
					j++
				}
				runs = append(runs, ColorRun{i, j - i, strStyle})
				i = j
				continue
			}
			// Number
			if unicode.IsDigit(line[i]) {
				j := i
				for j < len(line) && (unicode.IsDigit(line[j]) || line[j] == '.') {
					j++
				}
				runs = append(runs, ColorRun{i, j - i, numStyle})
				i = j
				continue
			}
			// Word
			if unicode.IsLetter(line[i]) || line[i] == '_' || line[i] == '@' || line[i] == '#' {
				j := i
				for j < len(line) && (unicode.IsLetter(line[j]) || unicode.IsDigit(line[j]) || line[j] == '_') {
					j++
				}
				if sqlKeywords[strings.ToUpper(string(line[i:j]))] {
					runs = append(runs, ColorRun{i, j - i, kwStyle})
				}
				i = j
				continue
			}
			i++
		}
		return runs
	}
}
