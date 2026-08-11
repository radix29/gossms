package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ag_dashboard_draw.go holds the Always On dashboard's layout and drawing:
// a fixed header block, two labelled grids, and a key hint row.

// agDashHeaderRows is the header block's height: two fact lines plus one line
// that carries provenance or the last refresh error.
const agDashHeaderRows = 3

// agGridChrome is what a DataGrid spends on anything but data rows — column
// header, separator, status bar — so a grid asked to show n rows needs
// n+agGridChrome lines.
const agGridChrome = 3

// SetBounds splits the panel: header, replica section, database section, hint.
// The replica grid is sized to its own contents where there is room, since the
// replica count is small and fixed while the database rows are the ones that
// grow; whatever it does not need goes to the databases below it.
func (d *AGDashboard) SetBounds(x, y, w, h int) {
	d.rect = core.Rect{X: x, Y: y, W: w, H: h}

	body := h - agDashHeaderRows - 1 // less the hint row
	if body < 4 {
		// Too short to split. The replica grid alone is the more useful half
		// of the two — it is the one that says which instance is the primary.
		d.topRect = core.Rect{X: x, Y: y + agDashHeaderRows, W: w, H: max(0, body)}
		d.bottomRect = core.Rect{X: x, Y: y + h, W: w, H: 0}
		d.topGrid.SetBounds(d.topRect.X, d.topRect.Y, d.topRect.W, d.topRect.H)
		d.bottomGrid.SetBounds(d.bottomRect.X, d.bottomRect.Y, d.bottomRect.W, 0)
		return
	}

	// Each section spends one line on its own bar.
	want := len(d.topRows) + agGridChrome
	if want < agGridChrome+1 {
		want = agGridChrome + 1
	}
	replicaH := min(want, (body-2)/2)
	replicaH = max(replicaH, 4)

	replicaY := y + agDashHeaderRows + 1 // below the "Availability replicas" bar
	d.topRect = core.Rect{X: x, Y: replicaY, W: w, H: replicaH}
	dbY := replicaY + replicaH + 1 // below the "Availability databases" bar
	d.bottomRect = core.Rect{X: x, Y: dbY, W: w, H: max(0, y+h-1-dbY)}

	d.topGrid.SetBounds(d.topRect.X, d.topRect.Y, d.topRect.W, d.topRect.H)
	d.bottomGrid.SetBounds(d.bottomRect.X, d.bottomRect.Y, d.bottomRect.W, d.bottomRect.H)
}

func (d *AGDashboard) Draw(s tcell.Screen) {
	core.FillRect(s, d.rect, ' ', theme.StylePanel())
	d.drawHeader(s)

	topTitle, bottomTitle := "Availability replicas", "Availability databases"
	if d.allGroups() {
		topTitle, bottomTitle = "Availability groups", "Availability replicas"
	}
	if d.topRect.H > 0 {
		d.drawSectionBar(s, d.topRect.Y-1, topTitle)
		d.topGrid.Focus(d.active && !d.focusBottom)
		d.topGrid.Draw(s)
	}
	if d.bottomRect.H > 0 {
		d.drawSectionBar(s, d.bottomRect.Y-1, bottomTitle)
		d.bottomGrid.Focus(d.active && d.focusBottom)
		d.bottomGrid.Draw(s)
	}
	d.drawHint(s)
	// Both overlays after every grid: a grid's "Show Value" popup is drawn
	// last so it is not painted over by the section bar below it.
	d.topGrid.DrawOverlay(s)
	d.bottomGrid.DrawOverlay(s)
}

// drawSectionBar draws one section's title strip, matching the Activity
// Monitor's dashboards so the two panels read as one system.
func (d *AGDashboard) drawSectionBar(s tcell.Screen, y int, title string) {
	if y < d.rect.Y || y >= d.rect.Bottom() {
		return
	}
	pal := theme.Active()
	style := tcell.StyleDefault.Background(pal.ChartSectionBg).Foreground(pal.Text)
	core.FillRect(s, core.Rect{X: d.rect.X, Y: y, W: d.rect.W, H: 1}, ' ', style)
	core.DrawTextClipped(s, d.rect.X+1, y, d.rect.W-2, style.Bold(true), title)
}

func (d *AGDashboard) drawHeader(s tcell.Screen) {
	pal := theme.Active()
	base := theme.StylePanel()
	w := d.rect.W - 2
	if w <= 0 {
		return
	}
	left, top := d.rect.X+1, d.rect.Y

	if d.allGroups() {
		d.drawAllGroupsHeader(s, left, top, w, base, pal)
		return
	}

	group, state := d.agName, "Loading..."
	primary, cluster, backups := "—", "—", "—"
	if d.snap.ok() {
		g := d.snap.group
		state = orDefault(g.SynchronizationHealth, "(unknown)")
		primary = orDefault(g.PrimaryReplicaServerName, "(none visible)")
		cluster = orDefault(g.ClusterType, "WSFC (implied)")
		backups = orDefault(g.AutomatedBackupPreference, "—")
	}

	core.DrawTextClipped(s, left, top, w, base.Bold(true),
		fmt.Sprintf("Availability group %s — %s", group, state))
	core.DrawTextRight(s, left, top, w, base.Foreground(pal.TextDim), d.refreshState())

	core.DrawTextClipped(s, left, top+1, w, base.Foreground(pal.TextDim),
		fmt.Sprintf("Primary: %s     Cluster type: %s     Automated backups: %s     Replicas: %d     Databases: %d",
			primary, cluster, backups, len(d.snap.replicas), agDistinctDatabases(d.snap.dbs)))

	core.DrawTextClipped(s, left, top+2, w, d.noteStyle(base, pal), d.note())
}

// drawAllGroupsHeader is the header for the all-groups view, which has no one
// group to describe. It counts what is on screen and, most usefully, how many
// groups are not healthy — the number that says whether the rest is worth
// reading.
func (d *AGDashboard) drawAllGroupsHeader(s tcell.Screen, left, top, w int, base tcell.Style, pal *theme.Palette) {
	title := "Always On — " + d.conn.Opts.Server
	core.DrawTextClipped(s, left, top, w, base.Bold(true), title)
	core.DrawTextRight(s, left, top, w, base.Foreground(pal.TextDim), d.refreshState())

	summary := "Reading..."
	if d.snap.ok() {
		unhealthy := 0
		replicas, databases := 0, 0
		for _, g := range d.snap.groups {
			if g.issues() != "" {
				unhealthy++
			}
			replicas += len(g.replicas)
			databases += agDistinctDatabases(g.dbs)
		}
		health := "all healthy"
		if unhealthy > 0 {
			health = fmt.Sprintf("%d needing attention", unhealthy)
		}
		summary = fmt.Sprintf("Availability groups: %d (%s)     Replicas: %d     Databases: %d",
			len(d.snap.groups), health, replicas, databases)
	}
	core.DrawTextClipped(s, left, top+1, w, base.Foreground(pal.TextDim), summary)

	note := ""
	switch {
	case d.err != nil:
		note = d.err.Error()
		if d.snap.ok() {
			note = "Last refresh failed, showing the previous reading: " + d.err.Error()
		}
	case d.snap.ok() && len(d.snap.groups) == 0:
		note = "This instance has no availability groups."
	default:
		note = "Enter opens the selected group's own dashboard."
	}
	core.DrawTextClipped(s, left, top+2, w, d.noteStyle(base, pal), note)
}

// refreshState is the right-hand end of the header: when the numbers on
// screen were read, and whether polling is still running. A paused dashboard
// that does not say so is the one way this panel can actively mislead.
func (d *AGDashboard) refreshState() string {
	if !d.snap.ok() {
		if d.paused.Load() {
			return "PAUSED"
		}
		return "Reading..."
	}
	age := time.Since(d.snap.at)
	text := "Refreshed " + d.snap.at.Format("15:04:05")
	if d.paused.Load() {
		return text + "   PAUSED"
	}
	text += "   every " + agDashboardRateLabels[d.rateIdx.Load()]
	// Only once it is old enough to matter: at the cadence being polled at
	// every reading is a few seconds stale, and saying so every frame is noise.
	if age > 2*d.rate() {
		text += fmt.Sprintf("   (%ds old)", int(age.Seconds()))
	}
	return text
}

// note is the header's third line: the last refresh error if there was one,
// otherwise where the reading came from.
func (d *AGDashboard) note() string {
	if d.err != nil {
		if d.snap.ok() {
			return "Last refresh failed, showing the previous reading: " + d.err.Error()
		}
		return d.err.Error()
	}
	if d.snap.followed {
		return "Read from the primary replica " + d.snap.group.PrimaryReplicaServerName +
			" — send and redo queues for a secondary's databases are only reported there."
	}
	return ""
}

func (d *AGDashboard) noteStyle(base tcell.Style, pal *theme.Palette) tcell.Style {
	if d.err != nil {
		return base.Foreground(pal.Error)
	}
	return base.Foreground(pal.TextDim)
}

func (d *AGDashboard) drawHint(s tcell.Screen) {
	y := d.rect.Bottom() - 1
	if y <= d.rect.Y || d.rect.W <= 2 {
		return
	}
	pal := theme.Active()
	style := theme.StyleMenuBar()
	core.FillRect(s, core.Rect{X: d.rect.X, Y: y, W: d.rect.W, H: 1}, ' ', style)
	resume := "P Pause"
	if d.paused.Load() {
		resume = "P Resume"
	}
	items := []string{"F5 Refresh", resume, "+/- Rate", "Tab Switch grid", "Ctrl+C Copy"}
	if d.allGroups() {
		items = append([]string{"Enter Open group"}, items...)
	}
	hint := strings.Join(items, "   ")
	core.DrawTextClipped(s, d.rect.X+1, y, d.rect.W-2, style.Foreground(pal.TextDim), hint)
}

// agDistinctDatabases counts databases, not (database, replica) rows, for the
// header's summary line.
func agDistinctDatabases(dbs []agDatabaseMetrics) int {
	seen := map[string]bool{}
	for _, m := range dbs {
		seen[strings.ToLower(m.DB.DatabaseName)] = true
	}
	return len(seen)
}
