package tui

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func fabricStatisticResponse(fabric, byName bool) fakeResponse {
	row := []driver.Value{
		"ST_Selected", int64(7), false, true, false, "",
		time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC),
		int64(50), int64(100), int64(10), int64(150),
		false, false, int64(9),
	}
	if fabric {
		for _, i := range []int{7, 8, 9, 10, 13} {
			row[i] = nil
		}
	}
	r := fakeResponse{match: "FROM   sys.stats s", db: idxDatabase, cols: 14}
	if byName {
		r.arg = "ST_Selected"
		r.rows = [][]driver.Value{row}
	} else {
		first := append([]driver.Value(nil), row...)
		first[0], first[1] = "ST_First", int64(1)
		r.rows = [][]driver.Value{first, row}
	}
	return r
}

func TestFabricDWStatisticsFolderAndGeneral(t *testing.T) {
	for _, fabric := range []bool{true, false} {
		t.Run(fmt.Sprintf("fabric=%v", fabric), func(t *testing.T) {
			sc, inst := newFakeConn(t,
				dbByNameResp(idxDatabase, 5), idxTableResp(),
				fabricStatisticResponse(fabric, true), fabricStatisticResponse(fabric, false))
			sc.IsFabricDW, sc.Server.IsFabricDW = fabric, fabric
			folder := &explorerNode{data: nodeData{Type: NodeStatistics, DBName: idxDatabase,
				Schema: idxSchema, Name: idxTable, conn: sc}}
			children, err := loadStatisticsChildren(loaderCtx{ctx: t.Context(), sc: sc}, folder)
			if err != nil || len(children) != 2 {
				t.Fatalf("statistics folder = %v, %v; want two leaves", children, err)
			}
			n := children[1].data
			if n.Type != NodeStatistic || n.Name != "ST_Selected" || n.TableName != idxTable ||
				n.Schema != idxSchema || n.DBName != idxDatabase {
				t.Fatalf("selected statistic lost its identity: %+v", n)
			}
			form, _ := loadPage(t, pageStatisticGeneral(sc, n.DBName, n.Schema, n.TableName, n.Name), inst)
			if got := staticValue(t, form, "Statistics name"); got != "ST_Selected" {
				t.Errorf("General shows %q, want the selected statistic", got)
			}
			want := map[string]string{
				"Rows": "100", "Rows sampled": "50", "Sampling rate": "50.0 %",
				"Steps": "10", "Unfiltered rows": "150", "Modification counter": "9",
			}
			for label, value := range want {
				if fabric {
					value = "n/a"
				}
				if got := staticValue(t, form, label); got != value {
					t.Errorf("%s = %q, want %q", label, got, value)
				}
			}
			if got := staticValue(t, form, "Last updated"); !strings.Contains(got, "2026-10-22") {
				t.Errorf("Last updated = %q, want the available STATS_DATE value", got)
			}
			for _, q := range inst.Reads("FROM   sys.stats s") {
				if strings.Contains(q, "dm_db_stats_properties") == fabric {
					t.Errorf("fabric=%v: incorrect statistics query:\n%s", fabric, q)
				}
			}
		})
	}
}

func TestFabricDWStatisticErrorsAreNotHidden(t *testing.T) {
	want := errors.New("statistics access refused")
	sc, _ := newFakeConn(t, dbByNameResp(idxDatabase, 5), idxTableResp(),
		fakeResponse{match: "FROM   sys.stats s", db: idxDatabase, err: want})
	sc.IsFabricDW, sc.Server.IsFabricDW = true, true
	_, _, err := pageStatisticGeneral(sc, idxDatabase, idxSchema, idxTable, "ST_Selected").load(t.Context())
	if !errors.Is(err, want) {
		t.Fatalf("General error = %v, want the actual catalog failure", err)
	}
}

func fabricStatisticHeaderResponse(match string) fakeResponse {
	return fakeResponse{match: match, db: idxDatabase, cols: 10,
		colNames: []string{"Updated", "Rows", "Rows Sampled", "Steps", "Density",
			"Average key length", "String Index", "Filter Expression", "Unfiltered Rows", "Name"},
		rows: [][]driver.Value{{"Oct 22 2026 08:00AM", int64(100), int64(50), int64(10),
			float64(0.01), float64(4), "NO", nil, int64(150), "ST_Selected"}}}
}

func TestFabricDWStatisticSelectionShowsHeaderInDetails(t *testing.T) {
	for _, tc := range []struct {
		name, schema, table, statistic, want string
		onView                               bool
	}{
		{"table", "sales", "Orders", "ST_Selected",
			"DBCC SHOW_STATISTICS (N'[sales].[Orders]', N'ST_Selected') WITH STAT_HEADER", false},
		{"quoted names", "s]a'les", "Order's]", "st_']",
			"DBCC SHOW_STATISTICS (N'[s]]a''les].[Order''s]]]', N'st_'']') WITH STAT_HEADER", false},
		{"view", "sales", "vOrders", "ST_Selected",
			"DBCC SHOW_STATISTICS (N'[sales].[vOrders]', N'ST_Selected') WITH STAT_HEADER", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			a.detailBrowser = a.newDetailBrowser()
			a.panels.AddPanel(a.detailBrowser)
			original := NewQueryPanel(a, "Existing query")
			original.editor.SetText("SELECT 42;")
			a.panels.SetActive(a.panels.AddPanel(original))
			a.focusExplorer()
			sc, inst := newFakeConn(t, append(capabilityResponses(true, nil, nil, nil, nil),
				fabricStatisticHeaderResponse(tc.want))...)
			sc.IsFabricDW, sc.Server.IsFabricDW = true, true
			sc.DatabaseCapabilities(t.Context(), idxDatabase)
			n := &explorerNode{label: tc.statistic, data: nodeData{Type: NodeStatistic, DBName: idxDatabase,
				Schema: tc.schema, TableName: tc.table, Name: tc.statistic, OnView: tc.onView, conn: sc}}

			a.onNodeSelected(n)
			drainUntil(t, a, func() bool { _, ok := a.detailBrowser.cache[n]; return ok },
				"the selected statistic's header to appear in Explorer Details")
			result := a.detailBrowser.cache[n]
			if result.err != nil {
				t.Fatalf("header failed: %v", result.err)
			}
			want := map[string]string{
				"Name": tc.statistic, "Database": idxDatabase, "Schema": tc.schema,
				"Last updated": "Oct 22 2026 08:00AM", "Rows": "100", "Rows sampled": "50",
				"Sampling rate": "50.0 %", "Steps": "10", "Density": "0.01000000",
				"Average key length": "4.0", "String index": "NO", "Unfiltered rows": "150",
				"Filter expression": "n/a",
			}
			got := make(map[string]string)
			for i := range result.rows {
				row := a.detailBrowser.grid.Row(i)
				if len(row) == 2 {
					got[row[0]] = row[1]
				}
			}
			for label, value := range want {
				if got[label] != value {
					t.Errorf("%s = %q, want %q", label, got[label], value)
				}
			}
			reads := inst.Reads("DBCC SHOW_STATISTICS")
			if len(reads) != 1 || !strings.Contains(reads[0], tc.want) || strings.Contains(reads[0], "NO_INFOMSGS") {
				t.Errorf("header reads = %q, want the selected statistic and supported Fabric options", reads)
			}
			if a.panels.Count() != 2 || a.queryPanelCnt != 0 || a.panels.ActivePanel() != a.detailBrowser {
				t.Fatal("selection must show Explorer Details without adding a query tab")
			}
			if a.focus != focusOnExplorer || a.detailBrowser.active {
				t.Fatal("showing the header stole keyboard focus from Object Explorer")
			}
			if original.editor.Text() != "SELECT 42;" {
				t.Fatal("selection overwrote the existing query")
			}
			if len(inst.Reads("dm_db_stats_properties")) != 0 {
				t.Fatal("the header must not use the unsupported DMV")
			}
			a.onNodeSelected(n)
			if len(inst.Reads("DBCC SHOW_STATISTICS")) != 1 {
				t.Fatal("reselection bypassed the Details cache")
			}
			a.detailBrowser.RefreshCurrent(a)
			drainUntil(t, a, func() bool { _, ok := a.detailBrowser.cache[n]; return ok }, "the header refresh")
			if len(inst.Reads("DBCC SHOW_STATISTICS")) != 2 {
				t.Fatal("refresh did not reload the DBCC header")
			}
		})
	}
}

func TestStatisticReselectionAndSQLServerSelectionKeepActivePanel(t *testing.T) {
	for _, fabric := range []bool{true, false} {
		t.Run(fmt.Sprintf("fabric=%v", fabric), func(t *testing.T) {
			a := newTestApp()
			a.detailBrowser = a.newDetailBrowser()
			a.panels.AddPanel(a.detailBrowser)
			original := NewQueryPanel(a, "Existing query")
			a.panels.SetActive(a.panels.AddPanel(original))
			sc, inst := newFakeConn(t, append(capabilityResponses(true, nil, nil, nil, nil),
				fabricStatisticHeaderResponse("DBCC SHOW_STATISTICS"))...)
			sc.IsFabricDW, sc.Server.IsFabricDW = fabric, fabric
			sc.DatabaseCapabilities(t.Context(), idxDatabase)
			n := &explorerNode{label: "ST_Selected", data: nodeData{Type: NodeStatistic, DBName: idxDatabase,
				Schema: idxSchema, TableName: idxTable, Name: "ST_Selected", conn: sc}}
			if fabric {
				a.onNodeReselected(n, n)
			} else {
				a.onNodeSelected(n)
			}
			drainUntil(t, a, func() bool { _, ok := a.detailBrowser.cache[n]; return ok }, "the statistic's details")
			if a.panels.Count() != 2 || a.activeQueryPanel() != original || a.queryPanelCnt != 0 {
				t.Fatal("reselection or a non-Fabric statistic switched panels or created a query")
			}
			if !fabric {
				r := a.detailBrowser.cache[n]
				if !isFallback(r.cols, r.rows) || len(inst.Reads("DBCC SHOW_STATISTICS")) != 0 {
					t.Fatal("non-Fabric statistics must retain the existing identity-only details")
				}
			}
		})
	}
}

func TestFabricDWStatisticDetailsShowDBCCErrors(t *testing.T) {
	refused := errors.New("SHOW_STATISTICS access refused")
	for _, tc := range []struct {
		name string
		resp fakeResponse
		want error
	}{
		{"server error", fakeResponse{match: "DBCC SHOW_STATISTICS", db: idxDatabase, err: refused}, refused},
		{"missing header", fakeResponse{match: "DBCC SHOW_STATISTICS", db: idxDatabase, cols: 1,
			colNames: []string{"Rows"}}, sql.ErrNoRows},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			sc, _ := newFakeConn(t, append(capabilityResponses(true, nil, nil, nil, nil), tc.resp)...)
			sc.IsFabricDW, sc.Server.IsFabricDW = true, true
			sc.DatabaseCapabilities(t.Context(), idxDatabase)
			n := &explorerNode{label: "ST_Selected", data: nodeData{Type: NodeStatistic, DBName: idxDatabase,
				Schema: idxSchema, TableName: idxTable, Name: "ST_Selected", conn: sc}}

			a.onNodeSelected(n)
			drainUntil(t, a, func() bool { _, ok := a.detailBrowser.cache[n]; return ok }, "the header error")
			if err := a.detailBrowser.cache[n].err; !errors.Is(err, tc.want) {
				t.Errorf("header error = %v, want %v", err, tc.want)
			}
			if a.detailBrowser.grid.Status() != "Error" ||
				!strings.Contains(a.detailBrowser.grid.Row(0)[0], tc.want.Error()) {
				t.Fatal("the actual DBCC failure was not displayed in Explorer Details")
			}
			if a.panels.Count() != 1 || a.queryPanelCnt != 0 {
				t.Fatal("a failed header read must not open a query tab")
			}
		})
	}
}

func TestMovingOnCancelsFabricDWStatisticHeader(t *testing.T) {
	gate := make(chan struct{})
	resp := fabricStatisticHeaderResponse("DBCC SHOW_STATISTICS")
	resp.block = gate
	defer func() {
		if gate != nil {
			close(gate)
		}
	}()
	a := newTestApp()
	sc, inst := newFakeConn(t, append(capabilityResponses(true, nil, nil, nil, nil), resp)...)
	sc.IsFabricDW, sc.Server.IsFabricDW = true, true
	sc.DatabaseCapabilities(t.Context(), idxDatabase)
	n := &explorerNode{label: "ST_Selected", data: nodeData{Type: NodeStatistic, DBName: idxDatabase,
		Schema: idxSchema, TableName: idxTable, Name: "ST_Selected", conn: sc}}

	a.onNodeSelected(n)
	drainUntil(t, a, func() bool { _, ok := inst.ReadContext(resp.match); return ok }, "the header read")
	ctx, _ := inst.ReadContext(resp.match)
	other := &explorerNode{label: "Other column", data: nodeData{Type: NodeColumn, conn: sc}}
	a.onNodeSelected(other)
	if ctx.Err() == nil {
		t.Fatal("moving to another node did not cancel the DBCC header read")
	}
	drainUntil(t, a, func() bool { _, ok := a.detailBrowser.cache[other]; return ok }, "the next node's details")
	close(gate)
	gate = nil
	settleDetails(a)
	if _, ok := a.detailBrowser.cache[n]; ok {
		t.Fatal("the cancelled header result was cached")
	}
	if row := a.detailBrowser.grid.Row(0); len(row) != 2 || row[1] != other.label {
		t.Fatalf("the late header replaced the new selection: %v", row)
	}
}
