package tui

import (
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/tuikit/core"
)

// applyHistory pre-fills the right pane from History row i.
func (d *ConnectDialog) applyHistory(i int) {
	if i < 0 || i >= len(d.historyConns) {
		return
	}
	d.PreFill(&d.historyConns[i])
}

// syncHistorySelection moves the History highlight onto the row the form is
// showing, matched on the saved-connection key. Without it a reopened dialog
// highlights a stale row (the list survives Hide and reloadHistory
// renumbers). A form matching nothing saved leaves the highlight.
func (d *ConnectDialog) syncHistorySelection() {
	name := d.currentOptions().GeneratedName()
	for i, c := range d.historyConns {
		if c.GeneratedName() == name {
			d.history.SetSelected(i)
			return
		}
	}
}

// deleteSelectedHistory removes the highlighted saved connection after a
// confirmation naming it; the entry carries the sealed password, so it is
// not recoverable by retyping.
//
// The confirmation is a nested dialog, leaving this one inert (see
// dialog_stack.go), so the selection cannot move under the callback.
func (d *ConnectDialog) deleteSelectedHistory() {
	i := d.history.Selected()
	if i < 0 || i >= len(d.historyConns) || d.app == nil || d.app.cfg == nil {
		return
	}
	name := d.historyConns[i].GeneratedName()
	d.app.confirmDialog().ShowConfirm("Delete Connection",
		"Remove "+name+" from the saved connections? Any password saved with it is deleted too.",
		func(confirmed bool) {
			if !confirmed || !d.app.cfg.RemoveConnection(name) {
				return
			}
			d.app.saveConfig(func(err error) {
				if err != nil {
					d.app.alertDialog().ShowAlert("Delete Connection",
						"Removed, but the configuration could not be saved: "+err.Error())
				}
			})
			d.reloadHistory()
			if len(d.historyConns) == 0 {
				d.resetForm()
				return
			}
			// Land on the row that took the deleted one's place, so the
			// highlight and the right pane still agree.
			next := min(i, len(d.historyConns)-1)
			d.history.SetSelected(next)
			d.applyHistory(next)
		})
}

// profileSelectedHistory opens the Profile dialog on the highlighted saved
// connection: the name the command line addresses it by, and its operation
// ceiling (config.Config.SetProfile). The highlight stays on the same entry,
// which reloadHistory may renumber.
func (d *ConnectDialog) profileSelectedHistory() {
	i := d.history.Selected()
	if i < 0 || i >= len(d.historyConns) || d.app == nil || d.app.cfg == nil {
		return
	}
	conn := d.historyConns[i]
	key := conn.GeneratedName()
	d.app.profileDialog().ShowProfile(conn, func(name string, allow config.AllowClass) error {
		if err := d.app.cfg.SetProfile(key, name, allow); err != nil {
			return err
		}
		d.app.saveConfig(func(err error) {
			if err != nil {
				d.app.alertDialog().ShowAlert("Connection Profile",
					"Named, but the configuration could not be saved: "+err.Error())
			}
		})
		d.reloadHistory()
		// A connected server's Object Explorer root shows its profile name
		// (ObjectExplorer.flatten); redraw it now, not on some later rebuild.
		d.app.explorer.rebuild()
		for j, c := range d.historyConns {
			if c.GeneratedName() == key {
				d.history.SetSelected(j)
				break
			}
		}
		return nil
	})
}

// reloadHistory refills the History pane from the saved connections, most
// recent first (MatchByServer with an empty prefix).
func (d *ConnectDialog) reloadHistory() {
	d.historyConns = nil
	if d.app != nil && d.app.cfg != nil {
		d.historyConns = d.app.cfg.MatchByServer("")
	}
	items := make([]string, len(d.historyConns))
	for i, c := range d.historyConns {
		items[i] = matchLabel(c)
	}
	d.history.SetItems(items)
}

// History row label budget. A row is the ListBox's selection marker ("▸ ")
// then the label, and once the list overflows the pane its scrollbar paints
// the last column; both are reserved, or the end of the label — the login,
// the part that tells two entries for one server apart — is what gets cut.
const (
	historyMarkerWidth    = 2 // controls.ListBox.Draw's "▸ " / "  "
	historyScrollbarWidth = 1
	historyLabelWidth     = connectHistoryPane - historyMarkerWidth - historyScrollbarWidth
)

// Floors a long server or database name is clipped no shorter than (ellipsis
// included), and the budget for a profile name.
const (
	matchServerFloor   = 6
	matchDatabaseFloor = 6
	matchProfileWidth  = 10
)

// matchLabel is a saved connection's History name: GeneratedName fitted to
// historyLabelWidth. The port, login and auth tag are never clipped; a long
// database gives up room first, down to matchDatabaseFloor, then the server,
// down to matchServerFloor (ellipsis on whatever is cut). A named connection
// leads with its profile name in brackets — the name is what the user chose —
// and the server and database share what is left. Display only: the stored
// Name is the dedup key and stays whole.
func matchLabel(c config.Connection) string {
	prefix := ""
	if c.Profile != "" {
		prefix = "[" + core.Truncate(c.Profile, matchProfileWidth) + "] "
	}
	// Everything but the server and database: the commas, port, login and tag.
	rest := c
	rest.Server, rest.Database = "", ""
	avail := historyLabelWidth - core.DisplayWidth(prefix) - core.DisplayWidth(rest.GeneratedName())

	sw, dw := core.DisplayWidth(c.Server), core.DisplayWidth(c.Database)
	if sw+dw > avail {
		dw = min(dw, max(matchDatabaseFloor, avail-sw))
		sw = min(sw, max(matchServerFloor, avail-dw))
		c.Server = core.Truncate(c.Server, sw)
		c.Database = core.Truncate(c.Database, dw)
	}
	return prefix + c.GeneratedName()
}
