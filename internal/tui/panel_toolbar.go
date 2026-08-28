package tui

import (
	"github.com/radix29/gossms/internal/tuikit/core"
)

// panel_toolbar.go holds the one-row toolbar shared by the panels that have one
// — Activity Monitor, the Log File Viewer and Query Store (which has two rows
// of it). It is not App's own toolbar, the icon strip in the menu bar row,
// which is toolbar.go.
//
// Only the geometry is shared: what a button does, when it is dimmed and how it
// is drawn stay with the panel, because the three disagree on all of it
// (Activity Monitor dims per button and renders the active rate selected; the
// Log Viewer dims the whole row while a read is in flight; Query Store asks a
// predicate per cell and per row).

// toolGap is the blank column between two toolbar buttons; a button's own
// label is drawn with one space of padding either side.
const toolGap = 1

// toolButton is one clickable toolbar cell.
//
// selected and disabled are drawn, not enforced: a panel that dims a button
// still has to refuse the click itself, since a dimmed control that acts on a
// click is the failure both panels have already shipped once.
//
// Only Activity Monitor reads these two fields at all. The Log Viewer dims and
// gates on toolsEnabled(), a whole-row state; Query Store asks selDisabled/
// actDisabled per draw and per click, because its answers depend on a
// capability probe that has not necessarily run when the row is built. Setting
// disabled or reason on one of their buttons is neither drawn nor enforced — it
// does nothing.
type toolButton struct {
	label    string
	selected bool
	disabled bool
	action   func()
	rect     core.Rect

	// reason is what to tell the user when they click the button while it is
	// disabled — the rights they are missing, typically. A disabled button
	// swallows its click, and swallowing it silently is the thing the
	// context-gating rule exists to prevent. Empty for a button whose greyed
	// state speaks for itself (a Refresh already running).
	reason string
}

// layoutToolButtons places buttons left to right inside r, after prefix, and
// returns the column just past the last one — where a panel puts whatever
// shares the row (a filter field, a right-aligned status).
//
// A button that would run past the right edge gets a zero rect, which is both
// panels' "neither drawn nor hit-tested" marker. Truncating instead would put
// half a label on the row and still accept clicks on it.
func layoutToolButtons(tools []toolButton, r core.Rect, prefix string) int {
	x := r.X + 1
	if prefix != "" {
		x += core.DisplayWidth(prefix) + 1
	}
	for i := range tools {
		w := core.DisplayWidth(tools[i].label) + 2
		if x+w > r.Right() {
			tools[i].rect = core.Rect{}
			continue
		}
		tools[i].rect = core.Rect{X: x, Y: r.Y, W: w, H: 1}
		x += w + toolGap
	}
	return x
}

// overflowLabel is the cell that stands in for the buttons a row is too narrow
// to draw.
const overflowLabel = "More ▾"

// layoutToolButtonsOverflow places buttons like layoutToolButtons, but instead
// of silently dropping the ones that do not fit it collapses them behind a
// "More ▾" cell and returns their indexes for the caller to build a menu from.
// more is given that cell's rect, or a zero one when everything fitted.
//
// layoutToolButtons alone deletes a button outright: a zero rect is neither
// painted nor clickable, so an action with no key binding beside it simply
// cannot be reached. The Query Store panel's action row shipped that way —
// 119 columns of buttons in a pane that gets 70% of the terminal, so Compare
// Plans was gone below a 170-column terminal and Track Query below 132.
//
// The hidden set is always a suffix: layout stops at the first button that does
// not fit rather than skipping it and squeezing in a later, shorter one, so the
// menu holds the row's tail instead of a scattered subset of it.
func layoutToolButtonsOverflow(tools []toolButton, r core.Rect, more *toolButton) []int {
	more.rect = core.Rect{}
	if r.H != 1 || len(tools) == 0 {
		layoutToolButtons(tools, r, "")
		return nil
	}
	layoutToolButtons(tools, r, "")
	if !anyToolDropped(tools) {
		return nil
	}
	// Room for the More cell has to come out of the row before the rest is
	// laid out, or the cell that stands in for the overflow would overflow.
	moreW := core.DisplayWidth(overflowLabel) + 2
	right := r.Right() - moreW - toolGap

	x := r.X + 1
	var hidden []int
	for i := range tools {
		w := core.DisplayWidth(tools[i].label) + 2
		if len(hidden) > 0 || x+w > right {
			tools[i].rect = core.Rect{}
			hidden = append(hidden, i)
			continue
		}
		tools[i].rect = core.Rect{X: x, Y: r.Y, W: w, H: 1}
		x += w + toolGap
	}
	// The stand-in is placed even when it holds the whole row: a menu that is
	// the entire toolbar still reaches every action, and suppressing it because
	// nothing else fitted would leave the pane with no toolbar at all. Only a
	// row too narrow for the cell itself goes without.
	if x+moreW <= r.Right() {
		more.label = overflowLabel
		more.rect = core.Rect{X: x, Y: r.Y, W: moreW, H: 1}
	}
	return hidden
}

// anyToolDropped reports whether layoutToolButtons gave any button a zero rect.
func anyToolDropped(tools []toolButton) bool {
	for i := range tools {
		if tools[i].rect.IsZero() {
			return true
		}
	}
	return false
}

// toolButtonAt returns the index of the button under (mx, my), or -1. A
// zero-rect button — one that didn't fit — is never hit.
func toolButtonAt(tools []toolButton, mx, my int) int {
	for i := range tools {
		if !tools[i].rect.IsZero() && tools[i].rect.Contains(mx, my) {
			return i
		}
	}
	return -1
}
