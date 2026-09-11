package tui

import (
	"fmt"
	"strconv"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
)

// maxSessionRows is how many sessions the usage grid lists, sorted by space
// held.
const maxSessionRows = 10

// maxFileBars caps drawn tempdb files; more than this is a configuration
// problem the advisory line names.
const maxFileBars = 12

// tempdbObjectColors give each object kind a fixed colour, indexed by
// activity.TempDBObjectKind.
func tempdbObjectColors() []tcell.Color {
	cyan, green, yellow, blue, _, purple, _ := chartColors()
	return []tcell.Color{
		cyan,   // local temp tables
		purple, // global temp tables
		green,  // user tables
		yellow, // internal tables
		blue,   // system tables
	}
}

// buildTempDBView turns the tempdb store into the TempDB dashboard's series,
// oldest first.
func (am *ActivityMonitor) buildTempDBView() dashboard.TempDBView {
	cyan, green, yellow, blue, red, purple, neutral := chartColors()
	st := &am.tdStore

	series := func(label, short string, color tcell.Color, f func(activity.TempDBSample) float64) charts.Series {
		return charts.Series{Label: label, Short: short, Color: color, Values: st.Series(f)}
	}

	v := dashboard.TempDBView{
		Times: am.tempdbTimes(),
		// Free last so it stacks on top, leaving allocated bands comparable at
		// the baseline.
		Space: []charts.Series{
			series("Version store MB", "Version", red, func(s activity.TempDBSample) float64 { return s.Space.VersionStoreMB }),
			series("User objects MB", "User", cyan, func(s activity.TempDBSample) float64 { return s.Space.UserObjectMB }),
			series("Internal objects MB", "Internal", yellow, func(s activity.TempDBSample) float64 { return s.Space.InternalObjectMB }),
			series("Mixed extents MB", "Mixed", purple, func(s activity.TempDBSample) float64 { return s.Space.MixedExtentMB }),
			series("Free MB", "Free", neutral, func(s activity.TempDBSample) float64 { return s.Space.FreeMB }),
		},
		TempTables: []charts.Series{
			series("Active temp tables", "Active", cyan, func(s activity.TempDBSample) float64 { return s.ActiveTempTables }),
			series("Created/sec", "Created", yellow, func(s activity.TempDBSample) float64 { return s.TempTableCreateSec }),
		},
		VersionTx: []charts.Series{
			series("Snapshot transactions", "Snapshot", blue, func(s activity.TempDBSample) float64 { return s.SnapshotTx }),
			series("Non-snapshot version tx", "Non-snap", purple, func(s activity.TempDBSample) float64 { return s.NonSnapshotTx }),
		},
		VersionRates: []charts.Series{
			series("Generation KB/sec", "Gen", red, func(s activity.TempDBSample) float64 { return s.VersionGenKBSec }),
			series("Cleanup KB/sec", "Cleanup", green, func(s activity.TempDBSample) float64 { return s.VersionCleanupKBSec }),
		},
		ObjectSpace: am.tempdbObjectSeries(),
	}

	latest, ok := st.Latest()
	if !ok {
		return v
	}

	v.SpaceKPIs = []charts.KPI{
		{Label: "Total MB", Value: fmt.Sprintf("%.0f", latest.Space.TotalMB)},
		{Label: "Free MB", Value: fmt.Sprintf("%.0f", latest.Space.FreeMB)},
		{Label: "Used %", Value: fmt.Sprintf("%.0f", usedPercent(latest.Space))},
	}
	v.ActivityKPIs = []charts.KPI{
		{Label: "Version Store MB", Value: fmt.Sprintf("%.1f", latest.VersionStoreMB)},
		{Label: "Longest Tx (s)", Value: number(latest.LongestTxSec)},
	}
	v.ObjectKPIs = []charts.KPI{
		{Label: "Objects", Value: strconv.Itoa(objectTotal(latest))},
	}
	v.ObjectCounts = dashboard.BarPanel{Bars: tempdbObjectCountBars(latest)}
	v.Files = dashboard.BarPanel{Bars: tempdbFileBars(latest)}
	v.FileKPIs = []charts.KPI{
		{Label: "Data Files", Value: strconv.Itoa(len(latest.DataFiles()))},
		{Label: "Cores", Value: strconv.Itoa(latest.Cores)},
	}
	v.FileNote = tempdbFileAdvice(latest)
	v.Sessions = tempdbSessionRows(latest)
	return v
}

// tempdbTimes are the stored tempdb samples' clock times, oldest first, for
// tooltips.
func (am *ActivityMonitor) tempdbTimes() []string {
	samples := am.tdStore.Samples()
	out := make([]string, len(samples))
	for i, s := range samples {
		out[i] = s.At.Format("15:04:05")
	}
	return out
}

// tempdbObjectSeries is one stacked series per object kind, in declaration
// order.
func (am *ActivityMonitor) tempdbObjectSeries() []charts.Series {
	colors := tempdbObjectColors()
	out := make([]charts.Series, 0, len(activity.TempDBObjectKindNames))
	for i, name := range activity.TempDBObjectKindNames {
		out = append(out, charts.Series{
			Label:  name,
			Color:  colors[i],
			Values: am.tdStore.Series(func(s activity.TempDBSample) float64 { return s.Objects[i].ReservedMB }),
		})
	}
	return out
}

// usedPercent is the allocated share of tempdb's grown size; 0 for zero size.
func usedPercent(sp activity.TempDBSpace) float64 {
	if sp.TotalMB <= 0 {
		return 0
	}
	return (sp.TotalMB - sp.FreeMB) / sp.TotalMB * 100
}

func objectTotal(s activity.TempDBSample) int {
	total := 0
	for _, o := range s.Objects {
		total += o.Count
	}
	return total
}

// tempdbObjectCountBars is the current object count per kind.
func tempdbObjectCountBars(s activity.TempDBSample) []charts.Bar {
	colors := tempdbObjectColors()
	bars := make([]charts.Bar, 0, len(activity.TempDBObjectKindNames))
	for i, name := range activity.TempDBObjectKindNames {
		bars = append(bars, charts.Bar{Label: name, Value: float64(s.Objects[i].Count), Color: colors[i]})
	}
	return bars
}

// tempdbFileBars is one bar per file split used/free, so full and empty files
// of equal size look different.
func tempdbFileBars(s activity.TempDBSample) []charts.Bar {
	cyan, _, _, _, _, _, neutral := chartColors()
	files := s.Files
	if len(files) > maxFileBars {
		files = files[:maxFileBars]
	}
	bars := make([]charts.Bar, 0, len(files))
	for _, f := range files {
		free := f.SizeMB - f.UsedMB
		if free < 0 {
			free = 0
		}
		bars = append(bars, charts.Bar{
			Label: f.Name,
			Short: f.Name,
			Parts: []charts.BarPart{
				{Value: f.UsedMB, Color: cyan},
				{Value: free, Color: neutral},
			},
		})
	}
	return bars
}

// maxRecommendedFiles caps the one-file-per-core rule; beyond eight files the
// contention is already spread.
const maxRecommendedFiles = 8

// tempdbFileAdvice states the configuration problem this tab can see, or "".
// Standard guidance: one data file per core up to eight, all the same size (an
// oversized file takes more allocations, reintroducing the contention).
func tempdbFileAdvice(s activity.TempDBSample) string {
	data := s.DataFiles()
	if len(data) == 0 {
		return ""
	}
	want := min(s.Cores, maxRecommendedFiles)
	if s.Cores > 0 && len(data) < want {
		return fmt.Sprintf("%d data files for %d cores — one file per core up to %d is the usual recommendation.",
			len(data), s.Cores, maxRecommendedFiles)
	}
	first := data[0].SizeMB
	for _, f := range data[1:] {
		if f.SizeMB != first {
			return "Data files are not all the same size — the largest takes a disproportionate share of allocations."
		}
	}
	return ""
}

// tempdbSessionRows formats the busiest sessions for the usage grid.
func tempdbSessionRows(s activity.TempDBSample) []dashboard.SessionRow {
	sessions := s.Sessions
	if len(sessions) > maxSessionRows {
		sessions = sessions[:maxSessionRows]
	}
	out := make([]dashboard.SessionRow, 0, len(sessions))
	for _, ss := range sessions {
		out = append(out, dashboard.SessionRow{
			Session:     strconv.Itoa(ss.SessionID),
			Login:       ss.Login,
			Host:        ss.Host,
			Application: ss.Program,
			UserMB:      fmt.Sprintf("%.2f", ss.UserMB),
			InternalMB:  fmt.Sprintf("%.2f", ss.InternalMB),
			TotalMB:     fmt.Sprintf("%.2f", ss.TotalMB),
		})
	}
	return out
}
