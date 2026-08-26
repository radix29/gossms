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
