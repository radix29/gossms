package controls

import (
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
	Divider  bool        // renders as a ──── separator
	Action   func()      // called when the item is activated
	Enabled  func() bool // nil means always enabled

	// Sub, when non-empty, makes this item a cascade: it opens a submenu
	// instead of firing Action, and draws a ▸ marker where a shortcut would
	// otherwise go. See menuCascade.
	Sub []MenuItem

	// Note is shown in the shortcut column *while the item is disabled*, to
	// say why. A disabled item cannot be selected or activated, so there is no
	// keypress or click left to answer with a status message — without this,
	// an item withheld for lack of a permission is simply grey and silent.
	// Its own Shortcut is shown again once the item is enabled.
	Note string

	// NoteWhen narrows Note to the reason it actually describes. nil shows
	// Note whenever the item is disabled; a predicate shows it only when it
	// answers true.
	//
	// An item can be withheld for more than one reason at once, and Note
	// describes exactly one of them. "Fail Over to This Replica..." is
	// disabled on the primary because a replica cannot fail over to itself,
	// and it was labelled "needs ALTER ANY AVAILABILITY GROUP" — a right the
	// login may well hold, sending them to ask for a permission that would
	// change nothing.
	NoteWhen func() bool
}

// showsNote reports whether Note is the right thing to draw right now: the
// item is disabled, and NoteWhen (if any) agrees that Note is why.
func (it MenuItem) showsNote() bool {
	return it.Note != "" && !it.enabled() && (it.NoteWhen == nil || it.NoteWhen())
}

// enabled reports whether it can be selected or activated right now.
func (it MenuItem) enabled() bool {
	return it.Enabled == nil || it.Enabled()
}

// Menu is a top-level menu header with its items.
type Menu struct {
	Label string
	Items []MenuItem
}

// menuItemSkippable reports whether an item must be skipped by keyboard/
// mouse selection — a divider, or one whose Enabled predicate says no.
func menuItemSkippable(it MenuItem) bool {
	return it.Divider || !it.enabled()
}

// firstSelectableItem returns the index of the first selectable item, or
// -1 if the menu has none.
func firstSelectableItem(items []MenuItem) int {
	for i, it := range items {
		if !menuItemSkippable(it) {
			return i
		}
	}
	return -1
}

// stepSelectableItem returns the next selectable item index starting from
// `from`, moving by `dir` (+1 or -1) and wrapping around. Returns -1 if the
// menu has no selectable items at all.
func stepSelectableItem(items []MenuItem, from, dir int) int {
	n := len(items)
	if n == 0 {
		return -1
	}
	i := from
	for range n {
		i = (i + dir + n) % n
		if !menuItemSkippable(items[i]) {
			return i
		}
	}
	return -1
}

// menuRowSuffix returns the right-aligned text of an item: the ▸ cascade
// marker for a submenu, otherwise its shortcut. An item with a submenu has
// no shortcut of its own — activating it only opens the submenu.
func menuRowSuffix(it MenuItem) string {
	if len(it.Sub) > 0 {
		return "▸"
	}
	if it.showsNote() {
		return it.Note
	}
	return it.Shortcut
}

// menuContentWidth returns the width, in display columns, of a dropdown or
// context-menu box wide enough to fit every item's label and shortcut,
// floored at minW — shared by MenuBar.dropdownGeometry and
// ContextMenu.geometry so hit-testing always matches what was drawn.
func menuContentWidth(items []MenuItem, minW int) int {
	w := minW
	for _, item := range items {
		// +6: 2 columns of border/inset padding plus a guaranteed 2-column
		// gap between label and shortcut for whichever item ends up
		// defining w — without the extra margin, that widest item's own
		// label and shortcut land flush against each other with no gap.
		if n := core.DisplayWidth(item.Label) + core.DisplayWidth(menuRowSuffix(item)) + 6; n > w {
			w = n
		}
	}
	return w
}

// menuRowStyles returns the label and shortcut styles for one item —
// disabled (its Enabled predicate says no) beats selected/hovered, which
// beats the plain default — shared by MenuBar.drawDropdown and
// ContextMenu.Draw so both grey out a disabled item identically.
func menuRowStyles(item MenuItem, selected bool) (label, shortcut tcell.Style) {
	p := theme.Active()
	switch {
	case !item.enabled():
		st := theme.StyleDisabled()
		return st, st
	case selected:
		st := theme.StyleSelected()
		return st, st
	default:
		return tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text),
			tcell.StyleDefault.Background(p.MenuBar).Foreground(p.TextDim)
	}
}

// drawMenuRow renders one row of a dropdown/context-menu box at (x,y)
// spanning w columns: a divider line, or a label plus right-aligned
// shortcut styled per menuRowStyles — shared by MenuBar.drawDropdown and
// ContextMenu.Draw so both use identical glyphs, padding, and
// enabled/selected styling.
func drawMenuRow(s tcell.Screen, x, y, w int, item MenuItem, selected bool, borderStyle tcell.Style) {
	if item.Divider {
		for cx := x + 1; cx < x+w-1; cx++ {
			s.SetContent(cx, y, '─', nil, borderStyle)
		}
		s.SetContent(x, y, '├', nil, borderStyle)
		s.SetContent(x+w-1, y, '┤', nil, borderStyle)
		return
	}
	labelStyle, shortcutStyle := menuRowStyles(item, selected)
	core.FillRect(s, core.Rect{X: x + 1, Y: y, W: w - 2, H: 1}, ' ', labelStyle)
	core.DrawTextClipped(s, x+2, y, w-4, labelStyle, item.Label)
	if suffix := menuRowSuffix(item); suffix != "" {
		sx := x + w - 1 - core.DisplayWidth(suffix) - 1
		core.DrawText(s, sx, y, shortcutStyle, suffix)
	}
}
