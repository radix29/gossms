package controls

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
