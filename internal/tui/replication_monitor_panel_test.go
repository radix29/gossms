package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
)

// Replication Monitor's wiring: what it does with the monitor procedures'
// answers — which publication each subscription and agent hangs on, what it
// says when there is nothing it may show, the row it keeps across a refresh,
// and the error detail it joins onto a session's actions. Statement text is
// gosmo's to test; acceptance is the live drive.

// rmInfo answers ReplicationInfo for a local distributor whose msdb tables
// this login cannot read (so no msdb read is scripted), with a distribution
// database and one published database.
func rmInfo() []fakeResponse {
	return []fakeResponse{
		{match: replDistributorRead, cols: 6, rows: [][]driver.Value{{"WIN10CLI", true, true, true, false, false}}},
		{match: replDatabasesRead, cols: 5, rows: [][]driver.Value{
			{"distribution", false, false, true, true},
			{"salesdb", true, false, false, true},
		}},
	}
}

// rmCanMonitor answers CanMonitor as sysadmin.
func rmCanMonitor() fakeResponse {
	return fakeResponse{match: "HAS_DBACCESS(@p1)", cols: 2, rows: [][]driver.Value{{true, true}}}
}

var rmPubCols = []string{"publisher", "publisher_db", "publication", "publication_type", "status", "warning",
	"worst_latency", "last_distsync", "subscriptioncount", "snapshot_agentname", "logreader_agentname"}

var rmSubCols = []string{"publisher", "publisher_db", "publication", "publication_type", "status", "warning",
	"subscriber", "subscriber_db", "subtype", "latency", "distribution_agentname", "mergeagentname"}

var rmAgentCols = []string{"agent_id", "name", "status", "publisher", "publisher_db", "publication",
	"subscriber", "subscriber_db", "start_time", "time", "comments", "error_id"}

// rmMonitorResponses scripts one distribution database: two transactional
// publications (sorted apart by name), a subscription to the second, and the
// agents, with the procedures naming them in a different case than
// sp_MSenum_* lists them — the fixture stores WIN10CLI and win10cli.
func rmMonitorResponses() []fakeResponse {
	at := time.Date(2026, 10, 7, 20, 0, 0, 0, time.UTC)
	return []fakeResponse{
		{match: "sp_replmonitorhelppublication", cols: len(rmPubCols), colNames: rmPubCols, rows: [][]driver.Value{
			{"WIN10CLI", "salesdb", "zeta_pub", int64(0), int64(2), int64(0), int64(4), at, int64(1), "SNAP-ZETA", "WIN10CLI-salesdb-1"},
			{"WIN10CLI", "salesdb", "alpha_pub", int64(0), int64(2), int64(0), nil, at, int64(0), "SNAP-ALPHA", "WIN10CLI-salesdb-1"},
		}},
		{match: "sp_replmonitorhelpsubscription", arg: "0", cols: len(rmSubCols), colNames: rmSubCols, rows: [][]driver.Value{
			{"win10cli", "SALESDB", "ZETA_PUB", int64(0), int64(6), int64(2), "SUBSRV", "subdb", int64(1), int64(12), "dist-zeta", ""},
		}},
		{match: "sp_replmonitorhelpsubscription", cols: len(rmSubCols), colNames: rmSubCols},
		{match: "sp_MSenum_snapshot", cols: len(rmAgentCols), colNames: rmAgentCols, rows: [][]driver.Value{
			{int64(1), "snap-zeta", int64(2), "win10cli", "salesdb", "zeta_pub", "", "", "20261007 19:55:00.000", "20261007 19:55:01.000", "snapshot ok", int64(0)},
		}},
		{match: "sp_MSenum_logreader", cols: len(rmAgentCols), colNames: rmAgentCols, rows: [][]driver.Value{
			{int64(1), "win10cli-salesdb-1", int64(4), "win10cli", "salesdb", "", "", "", "20261006 23:51:18.543", "20261007 20:42:17.560", "No replicated transactions are available.", int64(0)},
		}},
		{match: "sp_MSenum_distribution", cols: len(rmAgentCols), colNames: rmAgentCols, rows: [][]driver.Value{
			{int64(3), "DIST-ZETA", int64(2), "win10cli", "salesdb", "zeta_pub", "SUBSRV", "subdb", "20261007 20:45:56.920", "20261007 20:47:12.140", "Invalid object name 'dbo.OrderLine'.", int64(9)},
		}},
		{match: "sp_MSenum_merge", cols: len(rmAgentCols), colNames: rmAgentCols},
	}
}

func TestReplicationSnapshotHangsSubscriptionsAndAgentsOnTheirPublication(t *testing.T) {
	sc, _ := newFakeConn(t, slices.Concat(rmInfo(), []fakeResponse{rmCanMonitor()}, rmMonitorResponses())...)
	snap, err := readReplicationSnapshot(context.Background(), sc.Server)
	if err != nil {
		t.Fatal(err)
	}
	if snap.note != "" || len(snap.pubs) != 2 {
		t.Fatalf("snapshot = note %q, %d publications; want 2 and no note", snap.note, len(snap.pubs))
	}
	alpha, zeta := snap.pubs[0], snap.pubs[1]
	if alpha.mon.Publication != "alpha_pub" || zeta.mon.Publication != "zeta_pub" {
		t.Fatalf("publications in order %q, %q; want alpha_pub then zeta_pub", alpha.mon.Publication, zeta.mon.Publication)
	}
	if len(alpha.subs) != 0 {
		t.Errorf("alpha_pub has %d subscriptions, want none — the one there is zeta_pub's", len(alpha.subs))
	}
	if len(zeta.subs) != 1 || zeta.subs[0].agent == nil || zeta.subs[0].agent.Name != "DIST-ZETA" {
		t.Fatalf("zeta_pub's subscription and its agent = %+v", zeta.subs)
	}
	if zeta.snapshot == nil || zeta.snapshot.Name != "snap-zeta" || alpha.snapshot != nil {
		t.Errorf("Snapshot Agents: zeta %v, alpha %v; want snap-zeta on zeta only", zeta.snapshot, alpha.snapshot)
	}
	// One Log Reader serves both publications of the database.
	if zeta.logReader == nil || alpha.logReader != zeta.logReader {
		t.Errorf("Log Reader not shared by the database's publications: %v, %v", alpha.logReader, zeta.logReader)
	}

	// The agent grid: the subscription first, then the publication's agents;
	// the subscription's status is the monitor's (Failed), not the agent's
	// last history row (Succeeded).
	rows := agentRowsFor(zeta)
	var kinds []string
	for _, r := range rows {
		kinds = append(kinds, r.kind.String())
	}
	if want := []string{"Distribution Agent", "Snapshot Agent", "Log Reader Agent"}; !slices.Equal(kinds, want) {
		t.Fatalf("agent rows = %q, want %q", kinds, want)
	}
	c := rows[0].cells()
	if c[1] != "Failed" || c[2] != "[SUBSRV].[subdb] (Pull)" || c[3] != "Invalid object name 'dbo.OrderLine'." ||
		c[7] != "00:00:12" || !strings.Contains(c[9], "Latency") {
		t.Errorf("subscription row = %q", c)
	}
	// alpha_pub names a Snapshot Agent sp_MSenum_snapshot does not list.
	for _, r := range agentRowsFor(alpha) {
		if r.kind == gosmo.ReplSnapshotAgent && r.cells()[3] != "The distributor lists no such agent" {
			t.Errorf("a missing agent's row = %q", r.cells())
		}
	}
}

func TestReplicationSnapshotSaysWhyItIsEmpty(t *testing.T) {
	cases := []struct {
		name      string
		responses []fakeResponse
		want      string
	}{
		{"no distributor", replInfo(nil), replicationNotConfiguredLabel},
		{"remote distributor", replInfo("DISTSRV", []driver.Value{"salesdb", true, false, false, true}),
			"This instance is not a distributor: its publications are distributed by DISTSRV."},
		{"no right to monitor", slices.Concat(rmInfo(), []fakeResponse{
			{match: "HAS_DBACCESS(@p1)", cols: 2, rows: [][]driver.Value{{true, false}}},
			{match: "IS_MEMBER(N'replmonitor')", cols: 1, rows: [][]driver.Value{{false}}},
		}), "membership of db_owner or replmonitor in the distribution database (distribution)"},
		{"nothing published", slices.Concat(rmInfo(), []fakeResponse{rmCanMonitor(),
			{match: "sp_replmonitorhelppublication", cols: len(rmPubCols), colNames: rmPubCols},
			{match: "sp_replmonitorhelpsubscription", cols: len(rmSubCols), colNames: rmSubCols},
			{match: "sp_MSenum_", cols: len(rmAgentCols), colNames: rmAgentCols},
		}), rmNothingNote},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, inst := newFakeConn(t, tc.responses...)
			snap, err := readReplicationSnapshot(context.Background(), sc.Server)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(snap.note, tc.want) || len(snap.pubs) != 0 {
				t.Errorf("note = %q, %d publications; want %q", snap.note, len(snap.pubs), tc.want)
			}
			if tc.name == "no right to monitor" && len(inst.Reads("sp_replmonitor")) != 0 {
				t.Error("the monitor procedures were called for a login refused them")
			}
		})
	}
}

// A refresh keeps the user on the publication they had even when it moves —
// the procedures list worst first, and an agent failing reorders them.
func TestReplicationMonitorKeepsThePublicationAcrossARefresh(t *testing.T) {
	a := newTestApp()
	p := NewReplicationMonitorPanel(a, nil)
	p.SetBounds(0, 0, 160, 48)
	pub := func(name string) rmPub {
		return rmPub{mon: gosmo.MonitorPublication{Publisher: "S", PublisherDB: "db", Publication: name}}
	}
	p.applySnapshot(rmSnapshot{pubs: []rmPub{pub("a"), pub("b"), pub("c")}}, nil)
	p.pubsGrid.SetSelectedRow(1)
	p.applySnapshot(rmSnapshot{pubs: []rmPub{pub("x"), pub("b"), pub("a"), pub("c")}}, nil)
	// Re-sorted by key the order is a, b, c, x: b is row 1 again only by
	// accident, so move it.
	p.applySnapshot(rmSnapshot{pubs: []rmPub{pub("b2"), pub("c"), pub("b")}}, nil)
	if got := p.selectedPub(); got == nil || got.mon.Publication != "b" {
		t.Errorf("after the refresh the cursor is on %v, want b", got)
	}

	// The publication a Launch Replication Monitor named wins over the cursor,
	// once.
	p.wantPubDB, p.wantPub = "DB", "C"
	p.applySnapshot(rmSnapshot{pubs: []rmPub{pub("b2"), pub("c"), pub("b")}}, nil)
	if got := p.selectedPub(); got == nil || got.mon.Publication != "c" || p.wantPub != "" {
		t.Errorf("opened for c, the cursor is on %v (want still pending %q)", got, p.wantPub)
	}

	// A note replaces the rows and leaves nothing selected below.
	p.applySnapshot(rmSnapshot{note: rmNothingNote}, nil)
	if p.selectedPub() != nil || len(p.agentRows) != 0 {
		t.Error("a note left a publication or agent rows selected")
	}
}

// Each error id an action names is read once and its rows joined, oldest
// first, onto every action naming it.
func TestReplicationSessionActionsCarryTheirErrorDetail(t *testing.T) {
	actCols := []string{"runstatus", "time", "comments", "error_id"}
	sc, inst := newFakeConn(t, slices.Concat(rmMonitorResponses()[3:], []fakeResponse{
		{match: "sp_MSenum_distribution_sd", cols: len(actCols), colNames: actCols, rows: [][]driver.Value{
			{int64(6), "20261007 20:47:12.140", "Invalid object name 'dbo.OrderLine'.", int64(9)},
			{int64(5), "20261007 20:46:12.050", "Retrying", int64(9)},
			{int64(3), "20261007 20:45:57.000", "Initializing", int64(0)},
		}},
		{match: "sp_MSget_repl_error", arg: "9", cols: 9, rows: [][]driver.Value{
			{int64(1), "MSSQL_ENG", "14151", "agent failed.", "20261007 20:47:12.193", int64(1), false, nil, int64(0)},
			{int64(2), "MSSQL_ENG", "208", "Invalid object name 'dbo.OrderLine'.", "20261007 20:47:12.210", int64(2), true, []byte{0x3b, 0x01}, int64(1)},
		}},
	})...)
	// sp_MSenum_distribution_sd must answer before sp_MSenum_distribution:
	// move the agent lists behind it.
	inst.responses = append(append(inst.responses[:2:2], inst.responses[6:]...), inst.responses[2:6]...)

	agents, err := sc.Server.DistributionDatabaseRef("distribution").Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(agents, func(a *gosmo.ReplicationAgent) bool { return a.Kind == gosmo.ReplDistributionAgent })
	if i < 0 {
		t.Fatalf("no Distribution Agent among %v", agents)
	}
	rows, err := readActions(context.Background(), agents[i], gosmo.ReplAgentSession{Start: agents[i].LastStart})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d actions, want 3", len(rows))
	}
	want := "14151: agent failed. (MSSQL_ENG) | 208: Invalid object name 'dbo.OrderLine'. (MSSQL_ENG)"
	if rows[0].errText != want || rows[1].errText != want || rows[2].errText != "" {
		t.Errorf("error details = %q, %q, %q; want the error on the two actions naming it", rows[0].errText, rows[1].errText, rows[2].errText)
	}
	if n := len(inst.Reads("sp_MSget_repl_error")); n != 1 {
		t.Errorf("error 9 read %d times, want once", n)
	}
}

// The timer refreshes only while nothing would be lost by it: a refresh
// rebuilds the grids, which closes a value popup or cell menu — the error a
// user opened to read vanished at the next tick.
func TestReplicationMonitorTickWaitsForAnOpenPopup(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t, replInfo(nil)...)
	p := NewReplicationMonitorPanel(a, sc)
	p.SetBounds(0, 0, 160, 48)
	p.actions = []rmActionRow{{a: gosmo.ReplAgentAction{Message: "Invalid object name"}, errText: "208: Invalid object name"}}
	p.actionsGrid.SetData(rmActionColumns, [][]string{p.actions[0].cells()})
	r := p.actionsGrid.Bounds()
	p.actionsGrid.HandleMouse(tcell.NewEventMouse(r.X+3, r.Y+2, tcell.Button2, 0))
	if !p.actionsGrid.OverlayActive() {
		t.Fatal("setup: the right-click opened no cell menu")
	}

	_, tok := p.ticker.Begin(context.Background())
	p.onTick(tok)
	if p.busy {
		t.Fatal("a tick refreshed under an open cell menu")
	}
	p.actionsGrid.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", 0))
	if p.actionsGrid.OverlayActive() {
		t.Fatal("setup: Escape left the menu open")
	}
	p.onTick(tok - 1)
	if p.busy {
		t.Error("a superseded timer's tick refreshed")
	}
	p.onTick(tok)
	if !p.busy {
		t.Error("a tick with nothing open did not refresh")
	}
	drainUntil(t, a, func() bool { return !p.busy }, "the refresh")

	// Closing the panel stops the timer: a tick already posted does nothing.
	_, tok = p.ticker.Begin(context.Background())
	p.Close()
	p.onTick(tok)
	if p.busy {
		t.Error("a tick after Close refreshed a panel no longer hosted")
	}
}

// The note's column is widened to fit the sentence; publications arriving
// after it must not inherit that width in their Status column.
func TestReplicationMonitorNoteWidthDoesNotOutliveTheNote(t *testing.T) {
	p := NewReplicationMonitorPanel(newTestApp(), nil)
	p.SetBounds(0, 0, 160, 48)
	p.applySnapshot(rmSnapshot{note: fmtNoAccess("distribution")}, nil)
	if w := p.pubsGrid.ColumnWidth(0); w < len(fmtNoAccess("distribution")) {
		t.Fatalf("the note's column is %d wide, clipping it", w)
	}
	p.applySnapshot(rmSnapshot{pubs: []rmPub{{mon: gosmo.MonitorPublication{Publication: "p"}}}}, nil)
	if w := p.pubsGrid.ColumnWidth(0); w > 20 {
		t.Errorf("Status column is %d wide after the note went: it kept the note's width", w)
	}
}

func fmtNoAccess(db string) string { return strings.Replace(rmNoAccessNote, "%s", db, 1) }
