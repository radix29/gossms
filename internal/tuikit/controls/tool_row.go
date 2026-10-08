package controls

import "github.com/radix29/gossms/internal/tuikit/core"

// ToolRowGap is the blank column between two ToolRow cells; a cell's label is
// drawn with one space of padding either side.
const ToolRowGap = 1

// ToolRowMoreLabel is the cell that stands in for the cells a row is too
// narrow to draw.
const ToolRowMoreLabel = "More ▾"

// ToolCell is one clickable cell of a ToolRow.
//
// Selected, Disabled and Reason are for the host's drawing and gating: the row
// neither draws nor enforces them. A host that dims a cell still refuses the
// click itself, since a dimmed control that acts on a click is the failure the
// context-gating rule exists to prevent. A host whose gate is a predicate
// asked per draw and per click (one that depends on a probe not yet run when
// the row was built) leaves them unset.
type ToolCell struct {
	Label    string
	Selected bool
	Disabled bool
	Action   func()

	// Reason is what to tell the user when they click the cell while it is
	// disabled — the rights they are missing, typically. Swallowing the click
	// silently is the thing the context-gating rule exists to prevent. Empty
	// for a cell whose greyed state speaks for itself (a Refresh already
	// running).
	Reason string

	// Rect is where Layout placed the cell; zero when it is hidden, which is
	// "neither drawn nor hit-tested". Truncating instead would put half a label
	// on the row and still accept clicks on it.
	Rect core.Rect
}

// ToolRow is the geometry of a panel's one-row text toolbar: layout,
// hit-testing and the "More ▾" overflow. It does not draw — what a cell does,
// when it is dimmed and how it looks stay with the host, because the hosts
// disagree on all of it (one dims per cell and draws the active rate
// selected, one dims the whole row while a read is in flight, one asks a
// predicate per cell). Toolbar is the other kind: App's icon strip.
type ToolRow struct {
	Cells []ToolCell

	// More is the stand-in cell for the hidden ones: zero Rect when everything
	// fitted or the row is too narrow even for it.
	More ToolCell

	// Hidden holds the indexes of the Cells Layout could not place — always a
	// suffix of the row, so the overflow menu holds the row's tail rather than
	// a scattered subset of it.
	Hidden []int

	// NoOverflow drops the cells that do not fit instead of collapsing them
	// behind More. Only for a row whose host sizes the labels to fit (Plan
	// Compare's two pickers): a dropped cell is unreachable by mouse.
	NoOverflow bool
}

// Layout places the cells left to right inside r, after prefix (a label the
// host draws, such as "Refresh rate:"), and returns the column past the last
// cell placed plus one gap — More included — which is where a host puts
// whatever shares the row (a filter field, a right-aligned status).
//
// Cells that do not fit are hidden right to left by the same policy Toolbar
// uses (hideToFit), with room for More taken out of the row first, or the
// cell that stands in for the overflow would overflow. More is placed even
// when it holds the whole row: a menu that is the entire toolbar still reaches
// every action. It also takes the prefix's place on a row too narrow for both
// — a label naming controls that are not on the row is worse than no label.
// Only a row too narrow for More itself goes without (see PrefixShown).
func (tr *ToolRow) Layout(r core.Rect, prefix string) int {
	tr.More, tr.Hidden = ToolCell{}, nil
	for i := range tr.Cells {
		tr.Cells[i].Rect = core.Rect{}
	}
	if r.H != 1 {
		return r.X
	}
	x0 := r.X + 1
	if prefix != "" {
		x0 += core.DisplayWidth(prefix) + 1
	}
	moreW := core.DisplayWidth(ToolRowMoreLabel) + 2

	shown := make([]bool, len(tr.Cells))
	order := make([]int, len(tr.Cells))
	for i := range shown {
		shown[i], order[i] = true, len(tr.Cells)-1-i
	}
	need := func(shown []bool) int {
		x, placed, hidden := x0, false, false
		for i, c := range tr.Cells {
			if !shown[i] {
				hidden = true
				continue
			}
			x += core.DisplayWidth(c.Label) + 2 + ToolRowGap
			placed = true
		}
		if hidden && !tr.NoOverflow {
			x += moreW + ToolRowGap
			placed = true
		}
		if placed {
			x -= ToolRowGap
		}
		return x - r.X
	}
	hideToFit(shown, order, need, r.W)

	x := x0
	for i := range tr.Cells {
		if !shown[i] {
			tr.Hidden = append(tr.Hidden, i)
			continue
		}
		w := core.DisplayWidth(tr.Cells[i].Label) + 2
		tr.Cells[i].Rect = core.Rect{X: x, Y: r.Y, W: w, H: 1}
		x += w + ToolRowGap
	}
	if len(tr.Hidden) == 0 || tr.NoOverflow {
		return x
	}
	if x+moreW > r.Right() && prefix != "" {
		x = r.X + 1
	}
	if x+moreW <= r.Right() {
		tr.More = ToolCell{Label: ToolRowMoreLabel, Rect: core.Rect{X: x, Y: r.Y, W: moreW, H: 1}}
		x += moreW + ToolRowGap
	}
	return x
}

// PrefixShown reports whether the prefix Layout was given still has its place
// — More takes it on a row too narrow for both. r is the rect Layout was given.
func (tr *ToolRow) PrefixShown(r core.Rect) bool {
	return tr.More.Rect.IsZero() || tr.More.Rect.X > r.X+1
}

// CellAt returns the index of the cell under (mx, my), or -1. A hidden cell is
// never hit; neither is More (test More.Rect).
func (tr *ToolRow) CellAt(mx, my int) int {
	for i := range tr.Cells {
		if !tr.Cells[i].Rect.IsZero() && tr.Cells[i].Rect.Contains(mx, my) {
			return i
		}
	}
	return -1
}

// OverflowItems builds the menu behind More: one entry per hidden cell, each
// carrying the gate its cell would have — and its reason as the item's Note,
// which a MenuItem shows precisely while it is disabled, so a withheld action
// still explains itself the way the dimmed cell does. A nil disabled or reason
// reads the cell's Disabled or Reason field. A selected cell keeps a bullet,
// since the point of drawing a rate selector selected is to say which is in
// force.
func (tr *ToolRow) OverflowItems(disabled func(int) bool, reason func(int) string, run func(int)) []MenuItem {
	if disabled == nil {
		disabled = func(i int) bool { return tr.Cells[i].Disabled }
	}
	if reason == nil {
		reason = func(i int) string { return tr.Cells[i].Reason }
	}
	items := make([]MenuItem, 0, len(tr.Hidden))
	for _, i := range tr.Hidden {
		label := tr.Cells[i].Label
		if tr.Cells[i].Selected {
			label = "• " + label
		}
		// Note is fixed when the menu is built, while Enabled is asked on every
		// draw. Ask reason only for a cell withheld right now: a reason function
		// is free to answer for a state the cell isn't in (Query Store's returns
		// the permission text whenever the login can't force a plan, including
		// for cells that don't need the permission), and an enabled item never
		// shows its note anyway. A menu is modal, so nothing underneath it
		// changes while it is open.
		note := ""
		if disabled(i) {
			note = reason(i)
		}
		items = append(items, MenuItem{
			Label:   label,
			Enabled: func() bool { return !disabled(i) },
			Note:    note,
			Action:  func() { run(i) },
		})
	}
	return items
}
