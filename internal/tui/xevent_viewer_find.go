package tui

import (
	"fmt"
	"slices"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer_find.go is the Extended Events viewer's Find (Ctrl+F, F3,
// Shift+F3) and its bookmarks (Ctrl+F2 to toggle, F2 and Shift+F2 to move
// between them), SSMS's pair. Both walk the events in the order the grid
// shows them — grouped, group by group, collapsed groups included — and a hit
// inside a collapsed group opens it. Neither narrows anything: that is the
// filter's job.

// xeFindHelp is the Find prompt's explanation.
const xeFindHelp = "Selects the next event with any value — its name, a field or an action — " +
	"containing the text, ignoring case. F3 finds the next, Shift+F3 the previous."

// showFind opens the Find prompt on the text last looked for.
func (v *XEventViewer) showFind() {
	v.app.promptDialog.ShowPrompt("Find Event", xeFindHelp, "Find:", v.find, func(s string) {
		v.find = s
		v.findNext(1)
	})
}

// findNext selects the next (dir 1) or previous (dir -1) event containing the
// text Find last looked for, wrapping at the end, or opens Find when nothing
// has been looked for yet.
func (v *XEventViewer) findNext(dir int) {
	if v.find == "" {
		v.showFind()
		return
	}
	order := v.orderedEvents()
	i, ok := v.stepFrom(order, dir, func(e *xevent.Event) bool { return e.ContainsText(v.find) })
	if !ok {
		v.app.setStatus(fmt.Sprintf("No event contains %q", v.find))
		return
	}
	v.selectEvent(order[i])
	v.app.setStatus(fmt.Sprintf("Found %q — event %d of %d", v.find, i+1, len(order)))
}

// orderedEvents is every event the grid shows or holds in a group, in grid
// order.
func (v *XEventViewer) orderedEvents() []*xevent.Event {
	if !v.grouped() {
		return v.shown
	}
	out := make([]*xevent.Event, 0, len(v.shown))
	var walk func(gs []*xevent.Group)
	walk = func(gs []*xevent.Group) {
		for _, g := range gs {
			walk(g.Children)
			out = append(out, g.Events...)
		}
	}
	walk(v.groups)
	return out
}

// stepFrom finds the first event of order after (dir 1) or before (dir -1)
// the cursor matching match, wrapping around. The cursor on a group row
// stands just before the group's first event, so the next hit may be in it.
func (v *XEventViewer) stepFrom(order []*xevent.Event, dir int, match func(*xevent.Event) bool) (int, bool) {
	n := len(order)
	if n == 0 {
		return 0, false
	}
	start := v.orderPosition(order)
	// start is the cursor's event, or for a group row the index of its first
	// event minus a half: step 0 of a forward search is that event itself.
	pos := int(start)
	first := 1
	if start != float64(pos) {
		pos = int(start + 0.5)
		if dir > 0 {
			first = 0
		}
	}
	for k := first; k < n+first; k++ {
		i := ((pos+dir*k)%n + n) % n
		if match(order[i]) {
			return i, true
		}
	}
	return 0, false
}

// orderPosition is where the cursor stands in order: the index of its event,
// the index of a group row's first event less a half, or -0.5 with nothing
// selected: a forward search starts at the top, a backward one at the bottom.
func (v *XEventViewer) orderPosition(order []*xevent.Event) float64 {
	row := v.grid.SelectedRow()
	if e, ok := v.eventAt(row); ok {
		if i := slices.Index(order, e); i >= 0 {
			return float64(i)
		}
	}
	if g, ok := v.groupAt(row); ok {
		for g.Children != nil {
			g = g.Children[0]
		}
		if len(g.Events) > 0 {
			if i := slices.Index(order, g.Events[0]); i >= 0 {
				return float64(i) - 0.5
			}
		}
	}
	return -0.5
}

// selectEvent puts the cursor on e, opening the groups holding it.
func (v *XEventViewer) selectEvent(e *xevent.Event) {
	_, col := v.grid.SelectedCell()
	if !v.grouped() {
		if i := slices.Index(v.shown, e); i >= 0 {
			v.grid.SetSelectedCell(i, col)
		}
		v.detailScroll = 0
		return
	}
	opened := false
	for _, g := range v.groupPath(e.ID) {
		if !v.expanded[g.Key] {
			v.expanded[g.Key] = true
			opened = true
		}
	}
	if opened {
		v.rebuildDisplay()
	}
	v.restoreSelection(xeAnchor{event: e.ID})
	v.detailScroll = 0
}

// -- Bookmarks -------------------------------------------------------------------

// toggleBookmark bookmarks the selected event, or removes its bookmark.
func (v *XEventViewer) toggleBookmark() {
	e, ok := v.selectedEvent()
	if !ok {
		v.app.setStatus("Select an event to bookmark")
		return
	}
	if v.bookmarks[e.ID] {
		delete(v.bookmarks, e.ID)
	} else {
		v.bookmarks[e.ID] = true
	}
	v.updateStatus()
}

// nextBookmark selects the next (dir 1) or previous (dir -1) bookmarked event,
// wrapping around.
func (v *XEventViewer) nextBookmark(dir int) {
	if len(v.bookmarks) == 0 {
		v.app.setStatus("No bookmarks — Ctrl+F2 bookmarks the selected event")
		return
	}
	order := v.orderedEvents()
	i, ok := v.stepFrom(order, dir, func(e *xevent.Event) bool { return v.bookmarks[e.ID] })
	if !ok {
		v.app.setStatus("No bookmarked event passes the filter")
		return
	}
	v.selectEvent(order[i])
}

// clearBookmarks removes every bookmark.
func (v *XEventViewer) clearBookmarks() {
	v.bookmarks = map[uint64]bool{}
	v.updateStatus()
}

// showBookmarksMenu pops Find and the bookmark verbs with their keys.
func (v *XEventViewer) showBookmarksMenu() {
	has := func() bool { return len(v.bookmarks) > 0 }
	v.popMenu(xeToolBookmarks, []controls.MenuItem{
		{Label: "Toggle Bookmark", Shortcut: "Ctrl+F2", Enabled: func() bool { _, ok := v.selectedEvent(); return ok },
			Note: "no event selected", Action: v.toggleBookmark},
		{Label: "Next Bookmark", Shortcut: "F2", Enabled: has, Note: "no bookmarks", Action: func() { v.nextBookmark(1) }},
		{Label: "Previous Bookmark", Shortcut: "Shift+F2", Enabled: has, Note: "no bookmarks", Action: func() { v.nextBookmark(-1) }},
		{Label: "Clear All Bookmarks", Enabled: has, Note: "no bookmarks", Action: v.clearBookmarks},
	})
}
