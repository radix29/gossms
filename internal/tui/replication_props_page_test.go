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
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The read-only Publication and Subscription Properties, and the Details
// views beside them. Statement text is gosmo's; these pin which object the
// pages show and that the detail rows follow the selection.

const (
	replArticlesRead      = "FROM   dbo.sysarticles AS a"
	replSubscriptionsRead = "FROM   dbo.syssubscriptions AS s"
	replLocalSubsRead     = "FROM   dbo.MSreplication_subscriptions AS s"
)

// replPublicationResponses is SalesDB publishing pubA with two articles — the
// second filtered, with schema options — and two subscriptions.
func replPublicationResponses() []fakeResponse {
	return slices.Concat(
		[]fakeResponse{dbByNameResp("SalesDB", 5)},
		replTransactionalPub("SalesDB", "pubA"),
		[]fakeResponse{
			{match: replArticlesRead, db: "SalesDB", cols: 11, rows: [][]driver.Value{
				{"Customer", int64(1), false, "dbo", "Customer", "dbo", "Customer", "", int64(1), int64(0x01), ""},
				{"OrderLine", int64(1), false, "sales", "OrderLine", "dbo", "OrderLine_r", "Region = N'EU'", int64(3), int64(0x01 | 0x10), "lines"},
			}},
			{match: replSubscriptionsRead, db: "SalesDB", cols: 5, rows: [][]driver.Value{
				{"SUB1", "sub_a", int64(0), int64(2), int64(1)},
				{"SUB2", "sub_b", int64(1), int64(1), int64(2)},
			}},
		},
	)
}

func TestPublicationArticlesDetailFollowsTheSelection(t *testing.T) {
	sc, inst := newFakeConn(t, replPublicationResponses()...)
	pages := publicationPropPages(sc, "SalesDB", "pubA")
	form, _ := loadPage(t, pages[1], inst)

	var grids []*controls.DataGrid
	for _, r := range form.Rows() {
		if gr, ok := r.(*propsheet.GridRow); ok {
			grids = append(grids, gr.Grid)
		}
	}
	if len(grids) != 2 {
		t.Fatalf("want the articles grid and the schema-options grid, got %d grids", len(grids))
	}
	arts, opts := grids[0], grids[1]
	if got := staticValue(t, form, "Row filter"); got != "(none)" {
		t.Errorf("first article's filter = %q, want (none)", got)
	}

	// Move to OrderLine by key, as a user would, so OnSelectRow runs.
	gridKey(t, arts, tcell.KeyDown)
	if arts.Row(arts.SelectedRow())[0] != "OrderLine" {
		t.Fatalf("selected %v, want OrderLine", arts.Row(arts.SelectedRow()))
	}
	if got := staticValue(t, form, "Row filter"); got != "Region = N'EU'" {
		t.Errorf("Row filter = %q, want OrderLine's", got)
	}
	if got := staticValue(t, form, "Description"); got != "lines" {
		t.Errorf("Description = %q, want OrderLine's", got)
	}
	if got := staticValue(t, form, "Existing destination object"); got != "truncate" {
		t.Errorf("pre-creation = %q, want truncate", got)
	}
	want := gosmo.SchemaOption(0x11).Options()
	var got []string
	for i := 0; opts.Row(i) != nil; i++ {
		got = append(got, opts.Row(i)[0])
	}
	if !slices.Equal(got, want) {
		t.Errorf("schema options = %v, want %v", got, want)
	}
	if arts.Row(1)[2] != "sales.OrderLine" || arts.Row(1)[3] != "dbo.OrderLine_r" {
		t.Errorf("OrderLine row = %v, want source sales.OrderLine and destination dbo.OrderLine_r", arts.Row(1))
	}
}

func TestPublicationFilterRowsListsOnlyFilteredArticles(t *testing.T) {
	sc, inst := newFakeConn(t, replPublicationResponses()...)
	form, _ := loadPage(t, publicationPropPages(sc, "SalesDB", "pubA")[2], inst)
	g := plainGrid(t, form)
	if g.Row(0) == nil || g.Row(0)[0] != "OrderLine" || g.Row(1) != nil {
		t.Errorf("filter grid = %v, %v; want OrderLine alone", g.Row(0), g.Row(1))
	}
}

func TestPublicationSubscriptionsPage(t *testing.T) {
	sc, inst := newFakeConn(t, replPublicationResponses()...)
	form, _ := loadPage(t, publicationPropPages(sc, "SalesDB", "pubA")[5], inst)
	g := plainGrid(t, form)
	r := g.Row(gridRowIndex(t, g, 0, "SUB2"))
	if r[1] != "sub_b" || r[2] != "Pull" || r[3] != "subscribed" || r[4] != "none" {
		t.Errorf("SUB2 row = %v, want sub_b Pull subscribed none", r)
	}
}

func TestPublicationGeneralAndSubscriptionOptions(t *testing.T) {
	sc, inst := newFakeConn(t, replPublicationResponses()...)
	pages := publicationPropPages(sc, "SalesDB", "pubA")
	form, _ := loadPage(t, pages[0], inst)
	if got := staticValue(t, form, "Database"); got != "SalesDB" {
		t.Errorf("Database = %q", got)
	}
	if got := staticValue(t, form, "Type"); got != "Transactional" {
		t.Errorf("Type = %q", got)
	}
	if got := staticValue(t, form, "Subscriptions expire"); !strings.Contains(got, "336 hours") {
		t.Errorf("retention = %q, want 336 hours", got)
	}
	opts, _ := loadPage(t, pages[4], inst)
	if got := staticValue(t, opts, "Allow push subscriptions"); got != "True" {
		t.Errorf("Allow push = %q", got)
	}
	if got := staticValue(t, opts, "Allow anonymous subscriptions"); got != "False" {
		t.Errorf("Allow anonymous = %q", got)
	}
}

// replLocalSubsResponses is SubDB holding two subscriptions to publications
// named pubA, from two publishers.
func replLocalSubsResponses() []fakeResponse {
	when := time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC)
	return []fakeResponse{
		dbByNameResp("SubDB", 9),
		{match: replTablesRead, db: "SubDB", cols: 4, rows: [][]driver.Value{{true, true, false, false}}},
		{match: replLocalSubsRead, db: "SubDB", cols: 11, rows: [][]driver.Value{
			{"PUB1", "SalesDB", "pubA", int64(1), "", nil, "job1", true, int64(0), int64(0), "DIST1"},
			{"PUB2", "SalesDB", "pubA", int64(1), "second", when, "job2", false, int64(2), int64(1), "DIST2"},
		}},
	}
}

// TestSubscriptionPropertiesShowTheNamedSubscription acts on the second of two
// same-named subscriptions, in a case the server did not store: a finder
// ignoring the publisher, or comparing it exactly, shows the wrong one or
// none.
func TestSubscriptionPropertiesShowTheNamedSubscription(t *testing.T) {
	sc, inst := newFakeConn(t, replLocalSubsResponses()...)
	pages := localSubscriptionPropPages(sc, "SubDB", "pub2", "salesdb", "pubA")
	gen, _ := loadPage(t, pages[0], inst)
	if got := staticValue(t, gen, "Description"); got != "second" {
		t.Errorf("Description = %q, want PUB2's subscription", got)
	}
	if got := staticValue(t, gen, "Publication type"); got != "Snapshot" {
		t.Errorf("Publication type = %q", got)
	}
	agent, _ := loadPage(t, pages[1], inst)
	if got := staticValue(t, agent, "Agent job"); got != "job2" {
		t.Errorf("Agent job = %q", got)
	}
	if got := staticValue(t, agent, "Agent runs at"); !strings.HasPrefix(got, "Subscriber") {
		t.Errorf("Agent runs at = %q, want the subscriber for a pull subscription", got)
	}
	sync, _ := loadPage(t, pages[2], inst)
	if got := staticValue(t, sync, "Last synchronized"); got != "2026-10-07 09:30:00" {
		t.Errorf("Last synchronized = %q", got)
	}
	if got := staticValue(t, sync, "Update mode"); got != "Queued updating" {
		t.Errorf("Update mode = %q", got)
	}

	if _, err := findLocalSubscription(context.Background(), sc, "SubDB", "PUB3", "SalesDB", "pubA"); err == nil {
		t.Error("a publisher SubDB does not subscribe to was found")
	}
}

func TestPublicationDetailListsArticlesThenSubscriptions(t *testing.T) {
	sc, _ := newFakeConn(t, replPublicationResponses()...)
	node := &explorerNode{data: nodeData{Type: NodePublication, DBName: "SalesDB", Name: "pubA"}}
	_, rows, err := fetchNodeDetails(context.Background(), sc, node, new([]nodeData))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, r := range rows {
		kinds = append(kinds, r[0]+":"+r[1])
	}
	want := []string{"Article:Customer", "Article:OrderLine", "Subscription:SUB1.sub_a", "Subscription:SUB2.sub_b"}
	if !slices.Equal(kinds, want) {
		t.Errorf("rows = %v, want %v", kinds, want)
	}
}

func TestLocalSubscriptionDetailShowsTheNamedSubscription(t *testing.T) {
	sc, _ := newFakeConn(t, replLocalSubsResponses()...)
	node := &explorerNode{data: nodeData{Type: NodeLocalSubscription, DBName: "SubDB", Name: "pubA",
		ReplPublisher: "PUB2", ReplPublisherDB: "SalesDB"}}
	_, rows, err := fetchNodeDetails(context.Background(), sc, node, new([]nodeData))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r[0] == "Agent job" && r[1] != "job2" {
			t.Errorf("Agent job = %q, want PUB2's", r[1])
		}
	}
}

// TestLocalPublicationsDetailExplainsAnEmptyList mirrors the tree: nothing
// published and no distributor says "not configured"; an unreadable published
// database gets its own row.
func TestLocalPublicationsDetailExplainsAnEmptyList(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses []fakeResponse
		want      string
	}{
		{"not configured", slices.Concat([]fakeResponse{{match: replPublishedDBsRead, cols: 10}}, replInfo(nil)),
			replicationNotConfiguredLabel},
		{"not visible", slices.Concat([]fakeResponse{{match: replPublishedDBsRead, cols: 10}},
			replInfo("SRV", []driver.Value{"Hidden", true, false, false, false})),
			"Hidden:not visible (offline, or no access)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, _ := newFakeConn(t, tc.responses...)
			node := &explorerNode{data: nodeData{Type: NodeLocalPublications}}
			_, rows, err := fetchNodeDetails(context.Background(), sc, node, new([]nodeData))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range rows {
				if r[0] != "" {
					got = append(got, r[0])
				} else {
					got = append(got, r[1]+":"+r[3])
				}
			}
			if !slices.Equal(got, []string{tc.want}) {
				t.Errorf("rows = %v, want [%s]", got, tc.want)
			}
		})
	}
}
