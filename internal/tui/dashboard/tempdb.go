package dashboard

import (
	"strconv"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// TempDB canvas size. Same width as the other two dashboards so the panel's
// horizontal scrolling behaves identically on every tab; taller because it
// carries five sections, the last of which is a grid rather than a chart.
const (
	TempDBCanvasW = 150
	TempDBCanvasH = 68
)

// TempDB section geometry.
const (
	tempdbBodyH     = 14
	tempdbFilesH    = 8
	tempdbSessionsH = 12
	tempdbHeaderH   = 1
)

// DrawTempDB renders the TempDB dashboard into r, top to bottom: space,
// activity, objects, files, session usage. The returned hits describe where
// each chart plotted, in r's coordinates — see DrawHistory.
func DrawTempDB(s tcell.Screen, r core.Rect, v TempDBView) []ChartHit {
	if r.W <= 0 || r.H <= 0 {
		return nil
	}
	drawHeader(s, core.Rect{X: r.X, Y: r.Y, W: r.W, H: tempdbHeaderH}, v.Header)

	hits := make([]ChartHit, 0, 6)
	y := r.Y + tempdbHeaderH
	y = tempdbSpace(s, r, y, v, &hits)
	y = tempdbActivity(s, r, y, v, &hits)
	y = tempdbObjects(s, r, y, v, &hits)
	y = tempdbFiles(s, r, y, v)
	tempdbSessions(s, r, y, v)
	return hits
}

func tempdbSpace(s tcell.Screen, r core.Rect, y int, v TempDBView, hits *[]ChartHit) int {
	body, next := section(s, r, y, tempdbBodyH, "TEMPDB SPACE", v.SpaceKPIs)
	// Stacked, and Free is one of the bands: the column's full height is the
	// size tempdb has grown to, so a file that is filling up and a file that
	// has already grown look different rather than both pinning the axis.
	drawStackedChart(s, body, "TEMPDB SPACE (MB)", charts.StackedHistoryChart{
		Series:     v.Space,
		TimeLabel:  v.Header.SampleTime,
		Interval:   v.Interval,
		LegendRows: 1,
	}, hits)
	return next
}

func tempdbActivity(s tcell.Screen, r core.Rect, y int, v TempDBView, hits *[]ChartHit) int {
	body, next := section(s, r, y, tempdbBodyH, "TEMPDB ACTIVITY", v.ActivityKPIs)
	cols := splitColumns(body, 3)

	drawChart(s, cols[0], "TEMP TABLES", charts.HistoryChart{
		Series:    v.TempTables,
		TimeLabel: v.Header.SampleTime,
		Interval:  v.Interval,
	}, hits)

	if len(cols) > 1 {
		drawChart(s, cols[1], "VERSION STORE TRANSACTIONS", charts.HistoryChart{
			Series:    v.VersionTx,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	if len(cols) > 2 {
		// Overlaid, not stacked: generation and cleanup are two readings of
		// the same flow, and the gap between them — cleanup falling behind
		// generation — is the whole point of the panel.
		drawChart(s, cols[2], "VERSION GENERATION / CLEANUP KB/s", charts.HistoryChart{
			Series:    v.VersionRates,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	return next
}

func tempdbObjects(s tcell.Screen, r core.Rect, y int, v TempDBView, hits *[]ChartHit) int {
	body, next := section(s, r, y, tempdbBodyH, "TEMPDB OBJECTS", v.ObjectKPIs)
	cols := splitColumns(body, 2)

	drawStackedChart(s, cols[0], "RESERVED SPACE BY OBJECT KIND (MB)", charts.StackedHistoryChart{
		Series:     v.ObjectSpace,
		TimeLabel:  v.Header.SampleTime,
		Interval:   v.Interval,
		LegendRows: 1,
	}, hits)

	if len(cols) > 1 {
		drawBarPanel(s, cols[1], "OBJECT COUNT", v.ObjectCounts)
	}
	return next
}

func tempdbFiles(s tcell.Screen, r core.Rect, y int, v TempDBView) int {
	body, next := section(s, r, y, tempdbFilesH, "TEMPDB FILES", v.FileKPIs)
	inner := drawPanelTitle(s, body, "SIZE AND USED SPACE BY FILE (MB)")

	// The advisory takes the last row when there is something to say. It is
	// the one thing on this tab that is a recommendation rather than a
	// reading, so it is worded as one and coloured as a warning.
	if v.FileNote != "" && inner.H > 1 {
		core.DrawTextClipped(s, inner.X, inner.Bottom()-1, inner.W,
			theme.StylePanel().Foreground(theme.Active().Warning), v.FileNote)
		inner.H--
	}
	charts.BarChart{Bars: v.Files.Bars, Scale: v.Files.Scale, ShowValues: true}.Draw(s, inner)
	return next
}

// sessionColumns are the grid's headings and how many columns each gets.
// Fixed widths rather than measured ones: the grid is redrawn every tick,
// and a column that resizes itself around the current widest login makes the
// whole table jump between readings.
var sessionColumns = []struct {
	title string
	width int
	right bool
}{
	{"Session", 9, true},
	{"Login", 20, false},
	{"Host", 20, false},
	{"Application", 34, false},
	{"User MB", 11, true},
	{"Internal MB", 13, true},
	{"Total MB", 11, true},
}

func tempdbSessions(s tcell.Screen, r core.Rect, y int, v TempDBView) int {
	body, next := section(s, r, y, tempdbSessionsH, "TEMPDB SESSION USAGE", []charts.KPI{
		kpi("Sessions", strconv.Itoa(len(v.Sessions))),
	})
	if body.W <= 0 || body.H <= 0 {
		return next
	}
	pal := theme.Active()
	core.FillRect(s, body, ' ', theme.StylePanel())

	head := core.Rect{X: body.X + 1, Y: body.Y, W: body.W - 2, H: 1}
	core.FillRect(s, head, ' ', theme.StyleGridHeader())
	drawSessionRow(s, head, theme.StyleGridHeader(), sessionHeadings())

	if len(v.Sessions) == 0 {
		core.DrawTextClipped(s, body.X+1, body.Y+1, body.W-2,
			theme.StylePanel().Foreground(pal.TextDim), "No session is holding tempdb space.")
		return next
	}
	for i, row := range v.Sessions {
		ry := body.Y + 1 + i
		if ry >= body.Bottom() {
			break
		}
		drawSessionRow(s, core.Rect{X: body.X + 1, Y: ry, W: body.W - 2, H: 1}, theme.StylePanel(),
			[]string{row.Session, row.Login, row.Host, row.Application, row.UserMB, row.InternalMB, row.TotalMB})
	}
	return next
}

func sessionHeadings() []string {
	out := make([]string, len(sessionColumns))
	for i, c := range sessionColumns {
		out[i] = c.title
	}
	return out
}

// drawSessionRow lays one row out across the fixed column grid, clipping
// each cell to its own column so a long application name can't run into the
// numbers beside it.
func drawSessionRow(s tcell.Screen, r core.Rect, style tcell.Style, cells []string) {
	x := r.X
	for i, col := range sessionColumns {
		if i >= len(cells) || x >= r.Right() {
			return
		}
		w := min(col.width, r.Right()-x)
		if col.right {
			core.DrawTextRight(s, x, r.Y, w-1, style, cells[i])
		} else {
			core.DrawTextClipped(s, x, r.Y, w-1, style, cells[i])
		}
		x += w
	}
}
