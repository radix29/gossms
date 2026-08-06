package dashboard

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

func testSeries(label string, values ...float64) []charts.Series {
	return []charts.Series{{Label: label, Color: tcell.NewRGBColor(9, 9, 9), Values: values}}
}

func fullHistory() HistoryView {
	return HistoryView{
		Header:      Header{Instance: "SQLDEMO01", SampleTime: "11:56:23", Resolution: "2 sec"},
		Activity:    testSeries("Batches", 1, 2, 3),
		Lookups:     testSeries("Key lookups", 10, 20),
		Backup:      testSeries("Backup MB/sec", 0, 1),
		CPU:         testSeries("SQL Server %", 20, 30),
		Waits:       testSeries("Network", 5, 6),
		Memory:      testSeries("Buffer", 100, 120),
		CacheRatios: testSeries("Buffer cache hit ratio", 90, 91),
		Pages:       testSeries("Read", 3, 4),
		DatabaseIO:  testSeries("ms/Read", 2, 3),
		LogFlushes:  testSeries("Log flushes", 40, 50),
		Checkpoints: testSeries("Checkpoint pages", 1, 0),
	}
}

func renderHistory(v HistoryView) *charts.Canvas {
	c := charts.NewCanvas(HistoryCanvasW, HistoryCanvasH)
	DrawHistory(c, c.Rect(), v)
	return c
}

func rowsContain(rows []string, want string) bool {
	for _, r := range rows {
		if strings.Contains(r, want) {
			return true
		}
	}
	return false
}

func TestDrawHistoryDrawsEverySectionAndPanel(t *testing.T) {
	rows := renderHistory(fullHistory()).Rows()

	for _, want := range []string{
		"SQL SERVER ACTIVITY", "Key lookups / Forwarded recs", "BACKUP THROUGHPUT",
		"CPU usage", "SQL SERVER WAITS",
		"SQL SERVER MEMORY", "CACHE HIT RATIOS / PLE", "PAGES READ / WRITE",
		"DATABASE IO", "LOG FLUSHES", "CHECKPOINTS / LAZY WRITES",
	} {
		if !rowsContain(rows, want) {
			t.Errorf("History dashboard is missing %q", want)
		}
	}
}

// The section bars have to land on fixed rows: the panel's scroll maths and
// its mouse hit-testing both assume this geometry.
func TestDrawHistorySectionGeometry(t *testing.T) {
	rows := renderHistory(fullHistory()).Rows()

	wantAt := map[int]string{
		0:  "SQLDEMO01",           // header
		1:  "SQL SERVER ACTIVITY", // first section bar
		16: "SQL SERVER WAITS",
		31: "SQL SERVER MEMORY",
		46: "DATABASE IO",
	}
	for row, want := range wantAt {
		if !strings.Contains(rows[row], want) {
			t.Errorf("row %d = %q, want it to carry %q", row, rows[row], want)
		}
	}
	// The last section's body has to fit inside the canvas.
	if got := 46 + historySectionH; got > HistoryCanvasH {
		t.Errorf("sections end at row %d, past the %d-row canvas", got, HistoryCanvasH)
	}
}

func TestDrawHistoryHeaderShowsSampleTimeAndResolution(t *testing.T) {
	rows := renderHistory(fullHistory()).Rows()
	if !strings.Contains(rows[0], "11:56:23") || !strings.Contains(rows[0], "2 sec") {
		t.Errorf("header = %q, want the sample time and resolution", rows[0])
	}
}

// A paused collector must say so next to the timestamp: a frozen dashboard
// showing a plausible time is the one way this display can mislead.
func TestDrawHistoryHeaderMarksPaused(t *testing.T) {
	v := fullHistory()
	v.Header.Paused = true
	rows := renderHistory(v).Rows()
	if !strings.Contains(rows[0], "PAUSED") {
		t.Errorf("header = %q, want it to mark the collector paused", rows[0])
	}
}

func TestDrawHistoryStatusIsShownWithoutReplacingData(t *testing.T) {
	v := fullHistory()
	v.Header.Status = "collection failed: timeout"
	rows := renderHistory(v).Rows()

	if !rowsContain(rows, "collection failed") {
		t.Error("collector status is missing from the header")
	}
	if !rowsContain(rows, "SQL SERVER WAITS") {
		t.Error("a status message replaced the dashboard instead of annotating it")
	}
}

// One unavailable metric blanks its own panel and nothing else.
func TestDrawHistoryEmptySeriesKeepOtherPanels(t *testing.T) {
	v := fullHistory()
	v.Waits = nil
	v.Backup = nil
	rows := renderHistory(v).Rows()

	for _, want := range []string{"SQL SERVER WAITS", "BACKUP THROUGHPUT", "SQL SERVER MEMORY", "LOG FLUSHES"} {
		if !rowsContain(rows, want) {
			t.Errorf("missing %q after emptying two series", want)
		}
	}
}

func TestDrawHistoryEmptyViewDrawsChrome(t *testing.T) {
	c := charts.NewCanvas(HistoryCanvasW, HistoryCanvasH)
	DrawHistory(c, c.Rect(), HistoryView{})

	if !rowsContain(c.Rows(), "SQL SERVER WAITS") {
		t.Error("an empty view drew no sections at all")
	}
}

// A dashboard drawn into a sub-rect must stay inside it — the panel draws
// into a canvas it shares with nothing, but the same rule keeps a future
// caller from having its surroundings overwritten.
func TestDrawHistoryStaysInsideItsRect(t *testing.T) {
	c := charts.NewCanvas(HistoryCanvasW, HistoryCanvasH)
	DrawHistory(c, core.Rect{X: 4, Y: 3, W: 100, H: 30}, fullHistory())

	rows := c.Rows()
	for y := 0; y < 3; y++ {
		if strings.TrimSpace(rows[y]) != "" {
			t.Errorf("row %d above the rect was written: %q", y, rows[y])
		}
	}
	for y := 33; y < len(rows); y++ {
		if strings.TrimSpace(rows[y]) != "" {
			t.Errorf("row %d below the rect was written: %q", y, rows[y])
		}
	}
	for y := 3; y < 33; y++ {
		if strings.TrimSpace(rows[y][:4]) != "" {
			t.Errorf("row %d wrote left of the rect: %q", y, rows[y][:8])
		}
	}
}

func TestDrawHistoryTinyRect(t *testing.T) {
	c := charts.NewCanvas(40, 6)
	DrawHistory(c, c.Rect(), fullHistory())

	if !rowsContain(c.Rows(), "SQL SERVER ACTIVITY") {
		t.Error("a short rect should still draw the sections that fit")
	}
}

func fullSample() SampleView {
	bars := []charts.Bar{{Label: "Batches", Short: "Btch", Value: 10, Color: tcell.NewRGBColor(9, 9, 9)}}
	return SampleView{
		Header:              Header{Instance: "SQLDEMO01", SampleTime: "11:56:23"},
		UserConnections:     "1519",
		BlockedProcesses:    "92",
		Activity:            BarPanel{Bars: bars},
		Lookups:             BarPanel{Bars: bars},
		Backup:              BarPanel{Bars: bars, Scale: charts.Scale{Min: 0, Max: 1.2}},
		CPUPctOfWaits:       "19",
		Waits:               BarPanel{Bars: bars},
		WaitLegend:          []charts.LegendItem{{Label: "Resource", Color: tcell.NewRGBColor(1, 1, 1)}},
		PageLifeExpectancy:  "5761",
		MemoryGrantsPending: "0",
		Memory:              testSeries("Buffer", 100),
		CacheRatios:         BarPanel{Bars: bars},
		Pages:               BarPanel{Bars: bars},
		LogFlushes:          "512",
		CheckpointPages:     "0",
		LazyWrites:          "0",
		DatabaseIO:          BarPanel{Bars: bars},
	}
}

// loadFactorBars is one bar per core, the way the panel is fed live.
func loadFactorBars(cores int) []charts.Bar {
	bars := make([]charts.Bar, 0, cores)
	for i := 0; i < cores; i++ {
		bars = append(bars, charts.Bar{Label: strconv.Itoa(i), Value: float64(i), Color: tcell.NewRGBColor(2, 2, 2)})
	}
	return bars
}

// The Load Factor panel is sized by the core count, not by the section: a
// four-core box must not give half the waits chart away, and a 64-core box
// must not push the waits chart off the canvas.
func TestLoadFactorWidthFollowsTheCoreCount(t *testing.T) {
	if got := loadFactorWidth(0, 148); got != 0 {
		t.Errorf("no cores = width %d, want 0 so waits keeps the whole section", got)
	}
	four := loadFactorWidth(4, 148)
	eight := loadFactorWidth(8, 148)
	if eight-four != 4*loadFactorSlotW {
		t.Errorf("4 cores = %d, 8 cores = %d: each core should add %d columns", four, eight, loadFactorSlotW)
	}
	if got := loadFactorWidth(64, 148); got != 74 {
		t.Errorf("64 cores = width %d, want it capped at half the 148-column body", got)
	}
}

// Both charts have to be on screen: the load factor panel on the left and
// the waits chart in what's left, neither drawn over the other.
func TestDrawSampleWaitsSectionCarriesLoadFactor(t *testing.T) {
	v := fullSample()
	v.LoadFactor = BarPanel{Bars: loadFactorBars(4)}
	c := charts.NewCanvas(SampleCanvasW, SampleCanvasH)
	DrawSample(c, c.Rect(), v)
	rows := c.Rows()

	if !rowsContain(rows, "Load Factor") {
		t.Error("the waits section is missing the Load Factor panel")
	}
	if !rowsContain(rows, "SQL SERVER WAITS") {
		t.Error("the Load Factor panel drew over the waits chart")
	}
}

// No cores collected — an instance that hasn't answered yet — leaves the
// section exactly as it was before the panel existed.
func TestDrawSampleWithoutCoresKeepsTheWaitsChart(t *testing.T) {
	c := charts.NewCanvas(SampleCanvasW, SampleCanvasH)
	DrawSample(c, c.Rect(), fullSample())

	if rowsContain(c.Rows(), "Load Factor") {
		t.Error("the Load Factor panel was drawn with no cores to show")
	}
}

func TestDrawSampleDrawsSectionsAndKPIs(t *testing.T) {
	c := charts.NewCanvas(SampleCanvasW, SampleCanvasH)
	DrawSample(c, c.Rect(), fullSample())
	rows := c.Rows()

	for _, want := range []string{
		"SQL SERVER ACTIVITY", "SQL SERVER WAITS", "SQL SERVER MEMORY", "DATABASE IO",
		"User Connections", "1519", "Blocked Processes", "92",
		"CPU % of Total Waits", "Page Life Expectancy", "5761", "Log Flushes", "512",
	} {
		if !rowsContain(rows, want) {
			t.Errorf("Sample dashboard is missing %q", want)
		}
	}
}

func TestDrawSampleSectionGeometry(t *testing.T) {
	c := charts.NewCanvas(SampleCanvasW, SampleCanvasH)
	DrawSample(c, c.Rect(), fullSample())
	rows := c.Rows()

	// Activity is two rows shorter than the other sections and Waits two
	// rows taller, so the four still add up to the canvas.
	wantAt := map[int]string{
		0:  "SQLDEMO01",
		2:  "SQL SERVER ACTIVITY",
		12: "SQL SERVER WAITS",
		26: "SQL SERVER MEMORY",
		38: "DATABASE IO",
	}
	for row, want := range wantAt {
		if !strings.Contains(rows[row], want) {
			t.Errorf("row %d = %q, want it to carry %q", row, rows[row], want)
		}
	}
	if got := 38 + sampleSectionH; got > SampleCanvasH {
		t.Errorf("sections end at row %d, past the %d-row canvas", got, SampleCanvasH)
	}
}

func TestDrawSampleEmptyView(t *testing.T) {
	c := charts.NewCanvas(SampleCanvasW, SampleCanvasH)
	DrawSample(c, c.Rect(), SampleView{})

	if !rowsContain(c.Rows(), "DATABASE IO") {
		t.Error("an empty Sample view drew no sections")
	}
	// An absent KPI reads as absent, not as zero.
	if !rowsContain(c.Rows(), "—") {
		t.Error("missing KPI values should render as a dash")
	}
}

func TestSplitColumnsDividesAndCollapses(t *testing.T) {
	r := core.Rect{X: 0, Y: 0, W: 150, H: 10}
	cols := splitColumns(r, 3)
	if len(cols) != 3 {
		t.Fatalf("splitColumns(150, 3) returned %d columns, want 3", len(cols))
	}
	if cols[0].X != 0 || cols[2].Right() != r.Right() {
		t.Errorf("columns %+v don't span the rect", cols)
	}
	for i := 1; i < len(cols); i++ {
		if gap := cols[i].X - cols[i-1].Right(); gap != panelGutter {
			t.Errorf("gap between columns %d and %d = %d, want %d", i-1, i, gap, panelGutter)
		}
	}

	// Too narrow for three readable panels: one full-width panel instead of
	// three unreadable slivers.
	narrow := splitColumns(core.Rect{X: 0, Y: 0, W: 40, H: 10}, 3)
	if len(narrow) != 1 || narrow[0].W != 40 {
		t.Errorf("narrow split = %+v, want a single full-width panel", narrow)
	}
}

// DrawHistory reports where it drew, so a click can be turned back into the
// sample under it. Every hit has to sit inside the canvas and carry series;
// a hit whose rect is stale points the reader at the wrong numbers.
func TestDrawHistoryReportsChartHits(t *testing.T) {
	c := charts.NewCanvas(HistoryCanvasW, HistoryCanvasH)
	hits := DrawHistory(c, c.Rect(), fullHistory())

	if len(hits) != 11 {
		t.Fatalf("DrawHistory reported %d hits, want one per chart panel (11)", len(hits))
	}
	for _, h := range hits {
		if len(h.Series) == 0 {
			t.Errorf("%s reported a hit with no series", h.Title)
		}
		if h.Plot.X < 0 || h.Plot.Y < 0 || h.Plot.Right() > HistoryCanvasW || h.Plot.Bottom() > HistoryCanvasH {
			t.Errorf("%s plot %+v falls outside the canvas", h.Title, h.Plot)
		}
	}
}

// The rightmost column of a plot is the newest bucket, and a column left of
// the data reports no bucket at all.
func TestChartHitBucketMapsColumnsToSamples(t *testing.T) {
	c := charts.NewCanvas(HistoryCanvasW, HistoryCanvasH)
	hits := DrawHistory(c, c.Rect(), fullHistory())
	h := hits[0]

	buckets := charts.BucketCount(h.Series)
	if got := h.Bucket(h.Plot.Right() - 1); got != buckets-1 {
		t.Errorf("rightmost column = bucket %d, want the newest (%d)", got, buckets-1)
	}
	if got := h.Bucket(h.Plot.X - 1); got != -1 {
		t.Errorf("a column outside the plot = bucket %d, want -1", got)
	}
	if buckets < h.Plot.W {
		if got := h.Bucket(h.Plot.X); got != -1 {
			t.Errorf("a column predating the data = bucket %d, want -1", got)
		}
	}
}

func fullTempDB() TempDBView {
	return TempDBView{
		Header:       Header{Instance: "SQLDEMO01", SampleTime: "20:05:00", Resolution: "30 sec"},
		Space:        testSeries("Version store MB", 1, 2, 3),
		TempTables:   testSeries("Active temp tables", 8, 9),
		VersionTx:    testSeries("Snapshot transactions", 0, 1),
		VersionRates: testSeries("Generation KB/sec", 10, 20),
		ObjectSpace:  testSeries("Local temp tables", 0.5, 0.6),
		ObjectCounts: BarPanel{Bars: []charts.Bar{{Label: "Local temp tables", Value: 8, Color: tcell.NewRGBColor(3, 3, 3)}}},
		Files: BarPanel{Bars: []charts.Bar{
			{Label: "tempdev", Parts: []charts.BarPart{{Value: 4, Color: tcell.NewRGBColor(4, 4, 4)}, {Value: 68, Color: tcell.NewRGBColor(5, 5, 5)}}},
		}},
		FileNote: "2 data files for 8 cores — one file per core up to 8 is the usual recommendation.",
		Sessions: []SessionRow{
			{Session: "57", Login: "sa", Host: "APP01", Application: "loadtest", UserMB: "3.00", InternalMB: "1.00", TotalMB: "4.00"},
		},
	}
}

func TestDrawTempDBDrawsEverySection(t *testing.T) {
	c := charts.NewCanvas(TempDBCanvasW, TempDBCanvasH)
	DrawTempDB(c, c.Rect(), fullTempDB())
	rows := c.Rows()

	for _, want := range []string{
		"TEMPDB SPACE", "TEMPDB ACTIVITY", "TEMP TABLES", "VERSION STORE TRANSACTIONS",
		"TEMPDB OBJECTS", "OBJECT COUNT", "TEMPDB FILES", "TEMPDB SESSION USAGE",
	} {
		if !rowsContain(rows, want) {
			t.Errorf("the TempDB dashboard is missing %q", want)
		}
	}
	// The advisory is a recommendation, and it has to survive the panel it
	// shares a body with rather than being overdrawn by the bars.
	if !rowsContain(rows, "one file per core") {
		t.Error("the file advisory was not drawn")
	}
}

// The session grid is the one place this dashboard reports rows rather than
// levels: the headings and the row have to line up in the same columns.
func TestDrawTempDBSessionGrid(t *testing.T) {
	c := charts.NewCanvas(TempDBCanvasW, TempDBCanvasH)
	DrawTempDB(c, c.Rect(), fullTempDB())
	rows := c.Rows()

	head, row := -1, -1
	for i, r := range rows {
		if strings.Contains(r, "Application") && strings.Contains(r, "Internal MB") {
			head = i
		}
		if strings.Contains(r, "loadtest") {
			row = i
		}
	}
	if head < 0 || row != head+1 {
		t.Fatalf("grid heading at row %d, session at row %d — want the session directly under the heading", head, row)
	}
	if strings.Index(rows[row], "APP01") != strings.Index(rows[head], "Host") {
		t.Errorf("the Host column and its heading are not in the same place:\n%q\n%q", rows[head], rows[row])
	}
}

// Nothing holding tempdb space says so, rather than leaving an empty box the
// reader can't tell from a grid that failed to load.
func TestDrawTempDBEmptySessionGrid(t *testing.T) {
	v := fullTempDB()
	v.Sessions = nil
	c := charts.NewCanvas(TempDBCanvasW, TempDBCanvasH)
	DrawTempDB(c, c.Rect(), v)

	if !rowsContain(c.Rows(), "No session is holding tempdb space.") {
		t.Error("an empty session grid drew no explanation")
	}
}

func TestDrawTempDBEmptyViewDrawsChrome(t *testing.T) {
	c := charts.NewCanvas(TempDBCanvasW, TempDBCanvasH)
	hits := DrawTempDB(c, c.Rect(), TempDBView{})

	if !rowsContain(c.Rows(), "TEMPDB SESSION USAGE") {
		t.Error("an empty TempDB view drew no sections")
	}
	if len(hits) != 0 {
		t.Errorf("an empty view reported %d chart hits, want none", len(hits))
	}
}
